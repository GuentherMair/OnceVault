//go:build driver_redis || driver_all

package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

func init() {
	registry["redis"] = func(dsn string) (Store, error) { return NewRedis(dsn) }
}

const redisKeyPrefix = "oncevault:"

// redisValue is the JSON payload stored under oncevault:{guid} (contract 0.4).
type redisValue struct {
	Secret string `json:"secret"`
	IV     string `json:"iv"`
}

// Redis is the Store backed by a Redis server; expiry is enforced entirely by
// native key TTLs, so expired secrets are simply absent.
type Redis struct {
	client *redis.Client
}

// NewRedis connects to the Redis at dsn (redis://[:password@]host:port/db) and
// verifies the connection with a ping.
func NewRedis(dsn string) (*Redis, error) {
	opts, err := redis.ParseURL(dsn)
	if err != nil {
		return nil, fmt.Errorf("redis: parse dsn: %w", err)
	}
	client := redis.NewClient(opts)
	if err := client.Ping(context.Background()).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("redis: ping: %w", err)
	}
	return &Redis{client: client}, nil
}

// Put stores the secret with SET…EX…NX; NX enforces guid uniqueness → ErrDuplicate.
func (s *Redis) Put(ctx context.Context, guid, secret, iv string, expires int64) error {
	// TTL <= 0: the secret is born dead — skip the write, GET will report not found.
	ttl := expires - time.Now().Unix()
	if ttl <= 0 {
		return nil
	}
	val, err := json.Marshal(redisValue{Secret: secret, IV: iv})
	if err != nil {
		return fmt.Errorf("redis: marshal value: %w", err)
	}
	ok, err := s.client.SetNX(ctx, redisKeyPrefix+guid, val, time.Duration(ttl)*time.Second).Result()
	if err != nil {
		return fmt.Errorf("redis: put: %w", err)
	}
	if !ok {
		return ErrDuplicate
	}
	return nil
}

// TakeOnce fetches and deletes atomically via GETDEL (Redis >= 6.2). Never
// returns ErrExpired: native TTL means an expired key is absent → ErrNotFound.
func (s *Redis) TakeOnce(ctx context.Context, guid string) (string, string, error) {
	raw, err := s.client.GetDel(ctx, redisKeyPrefix+guid).Result()
	if err == redis.Nil {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", fmt.Errorf("redis: take: %w", err)
	}
	var val redisValue
	if err := json.Unmarshal([]byte(raw), &val); err != nil {
		return "", "", fmt.Errorf("redis: unmarshal value: %w", err)
	}
	return val.Secret, val.IV, nil
}

// PurgeExpired is a no-op: Redis evicts expired keys itself via native TTL.
func (s *Redis) PurgeExpired(ctx context.Context) (int64, error) { return 0, nil }

// Vacuum is a no-op: Redis reclaims expired-key memory itself.
func (s *Redis) Vacuum(ctx context.Context) error { return nil }

// Close releases the underlying connection pool.
func (s *Redis) Close() error { return s.client.Close() }
