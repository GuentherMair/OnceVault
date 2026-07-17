//go:build driver_sqlite || driver_all

package store

// Canonical Store-behavior tests, run against the in-memory sqlite backend.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *SQLite {
	t.Helper()
	s, err := NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite(:memory:): %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func future() int64 { return time.Now().Unix() + 3600 }
func past() int64   { return time.Now().Unix() - 3600 }

func TestPutTakeOnceRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	const guid = "11111111-2222-4333-8444-555555555555"

	if err := s.Put(ctx, guid, "c2VjcmV0", "aXZpdml2aXZpdg==", future()); err != nil {
		t.Fatalf("Put: %v", err)
	}
	secret, iv, err := s.TakeOnce(ctx, guid)
	if err != nil {
		t.Fatalf("TakeOnce: %v", err)
	}
	if secret != "c2VjcmV0" || iv != "aXZpdml2aXZpdg==" {
		t.Fatalf("TakeOnce = (%q, %q), want stored values", secret, iv)
	}
	if _, _, err := s.TakeOnce(ctx, guid); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second TakeOnce = %v, want ErrNotFound", err)
	}
}

func TestTakeOnceExpired(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	const guid = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"

	if err := s.Put(ctx, guid, "cw==", "aXY=", past()); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, _, err := s.TakeOnce(ctx, guid); !errors.Is(err, ErrExpired) {
		t.Fatalf("TakeOnce on expired row = %v, want ErrExpired", err)
	}
	// The expired row must have been deleted by the take.
	if _, _, err := s.TakeOnce(ctx, guid); !errors.Is(err, ErrNotFound) {
		t.Fatalf("TakeOnce after expired take = %v, want ErrNotFound", err)
	}
}

func TestPutDuplicateGUID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	const guid = "99999999-8888-4777-8666-555555555555"

	if err := s.Put(ctx, guid, "YQ==", "aXY=", future()); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Put(ctx, guid, "Yg==", "aXY=", future()); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate Put = %v, want ErrDuplicate", err)
	}
}

func TestPurgeExpired(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	expired := []string{
		"00000000-0000-4000-8000-000000000001",
		"00000000-0000-4000-8000-000000000002",
	}
	const live = "00000000-0000-4000-8000-000000000003"
	for _, g := range expired {
		if err := s.Put(ctx, g, "cw==", "aXY=", past()); err != nil {
			t.Fatalf("Put(%s): %v", g, err)
		}
	}
	if err := s.Put(ctx, live, "bGl2ZQ==", "aXY=", future()); err != nil {
		t.Fatalf("Put(live): %v", err)
	}

	n, err := s.PurgeExpired(ctx)
	if err != nil {
		t.Fatalf("PurgeExpired: %v", err)
	}
	if n != 2 {
		t.Fatalf("PurgeExpired = %d, want 2", n)
	}
	for _, g := range expired {
		if _, _, err := s.TakeOnce(ctx, g); !errors.Is(err, ErrNotFound) {
			t.Fatalf("TakeOnce(%s) after purge = %v, want ErrNotFound", g, err)
		}
	}
	if secret, _, err := s.TakeOnce(ctx, live); err != nil || secret != "bGl2ZQ==" {
		t.Fatalf("TakeOnce(live) = (%q, %v), want live row untouched by purge", secret, err)
	}
}

func TestTakeOnceConcurrent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	const guid = "deadbeef-dead-4eef-8eef-deadbeefdead"

	if err := s.Put(ctx, guid, "cmFjZQ==", "aXY=", future()); err != nil {
		t.Fatalf("Put: %v", err)
	}

	const workers = 20
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := s.TakeOnce(ctx, guid)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	var wins, notFound int
	for err := range errs {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, ErrNotFound):
			notFound++
		default:
			t.Fatalf("unexpected TakeOnce error: %v", err)
		}
	}
	if wins != 1 || notFound != workers-1 {
		t.Fatalf("concurrent TakeOnce: %d successes, %d ErrNotFound; want 1 and %d", wins, notFound, workers-1)
	}
}

func TestVacuum(t *testing.T) {
	s := newTestStore(t)
	if err := s.Vacuum(context.Background()); err != nil {
		t.Fatalf("Vacuum: %v", err)
	}
}
