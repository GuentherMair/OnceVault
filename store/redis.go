// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Günther Mair

//go:build driver_redis || driver_all

package store

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
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

var redisVersionRe = regexp.MustCompile(`redis_version:(\d+)\.(\d+)`)

// NewRedis connects to the Redis at dsn (redis://[:password@]host:port/db) and
// verifies the connection with a ping. TakeOnce relies on GETDEL (Redis >= 6.2),
// so a server that reports an older version is rejected here rather than
// failing on every retrieval; when INFO is unavailable (some managed
// offerings restrict it) the check is skipped.
func NewRedis(dsn string) (*Redis, error) {
	opts, err := redis.ParseURL(dsn)
	if err != nil {
		return nil, fmt.Errorf("redis: parse dsn: %w", err)
	}
	client := redis.NewClient(opts)
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("redis: ping: %w", err)
	}
	if info, err := client.Info(ctx, "server").Result(); err == nil {
		if m := redisVersionRe.FindStringSubmatch(info); m != nil {
			major, _ := strconv.Atoi(m[1])
			minor, _ := strconv.Atoi(m[2])
			if major < 6 || (major == 6 && minor < 2) {
				client.Close()
				return nil, fmt.Errorf("redis: server version %s.%s too old — retrieval uses GETDEL, which requires Redis >= 6.2", m[1], m[2])
			}
		}
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
