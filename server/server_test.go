//go:build driver_sqlite || driver_all

package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"oncevault/config"
	"oncevault/store"
)

const testIndex = "<!doctype html><title>test index</title>"

var testFavicon = []byte{0, 0, 1, 0} // any bytes; served verbatim

// --- helpers ---------------------------------------------------------------

// loadCfg round-trips a config through config.Load so tests exercise the real
// parsed/validated form (Rules sorting, TrustedProxyNets, defaults).
func loadCfg(t *testing.T, m map[string]any) *config.Config {
	t.Helper()
	if _, ok := m["listen"]; !ok {
		m["listen"] = "127.0.0.1:0"
	}
	if _, ok := m["db"]; !ok {
		m["db"] = map[string]any{"driver": "sqlite", "dsn": ":memory:"}
	}
	if _, ok := m["max_secret_bytes"]; !ok {
		m["max_secret_bytes"] = 1024
	}
	if _, ok := m["rate_limit"]; !ok {
		m["rate_limit"] = map[string]any{"default": 60}
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	path := filepath.Join(t.TempDir(), "cfg.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return cfg
}

func memStore(t *testing.T) *store.SQLite {
	t.Helper()
	st, err := store.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func do(t *testing.T, h http.Handler, method, target, remoteAddr, body string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, rd)
	if remoteAddr != "" {
		req.RemoteAddr = remoteAddr
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// errField decodes {"error":...} and returns the exact string.
func errField(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var v struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &v); err != nil {
		t.Fatalf("response %q is not error JSON: %v", rr.Body.String(), err)
	}
	return v.Error
}

func wantError(t *testing.T, rr *httptest.ResponseRecorder, status int, msg string) {
	t.Helper()
	if rr.Code != status {
		t.Fatalf("status = %d, want %d (body %q)", rr.Code, status, rr.Body.String())
	}
	if got := errField(t, rr); got != msg {
		t.Fatalf("error = %q, want %q", got, msg)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	if cc := rr.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", cc)
	}
}

var (
	validSecret = base64.StdEncoding.EncodeToString([]byte("attack at dawn"))
	validIV     = base64.StdEncoding.EncodeToString([]byte("0123456789ab")) // 12 bytes
)

func postBody(secret, iv string, duration int) string {
	b, _ := json.Marshal(map[string]any{"secret": secret, "iv": iv, "duration": duration})
	return string(b)
}

// fakeStore lets tests force failures, panics, and count calls.
type fakeStore struct {
	mu         sync.Mutex
	putErrs    []error // consumed one per Put; nil entry / exhausted → success
	putGuids   []string
	takeFn     func(guid string) (string, string, error)
	takeCalls  atomic.Int64
	purgeCalls atomic.Int64
	purgeErr   error
}

func (f *fakeStore) Put(_ context.Context, guid, _, _ string, _ int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.putGuids = append(f.putGuids, guid)
	if len(f.putErrs) > 0 {
		err := f.putErrs[0]
		f.putErrs = f.putErrs[1:]
		return err
	}
	return nil
}

func (f *fakeStore) TakeOnce(_ context.Context, guid string) (string, string, error) {
	f.takeCalls.Add(1)
	if f.takeFn != nil {
		return f.takeFn(guid)
	}
	return "", "", store.ErrNotFound
}

func (f *fakeStore) PurgeExpired(context.Context) (int64, error) {
	f.purgeCalls.Add(1)
	return 0, f.purgeErr
}

func (f *fakeStore) Vacuum(context.Context) error { return nil }
func (f *fakeStore) Close() error                 { return nil }

// --- POST validation matrix ------------------------------------------------

func TestPostValidationMatrix(t *testing.T) {
	cfg := loadCfg(t, map[string]any{"max_secret_bytes": 16})
	srv := New(cfg, memStore(t), []byte(testIndex), testFavicon)

	big := base64.StdEncoding.EncodeToString(make([]byte, 17))
	shortIV := base64.StdEncoding.EncodeToString([]byte("short"))

	cases := []struct {
		name, body, wantMsg string
	}{
		{"syntax error", `{"secret":`, msgInvalidJSON},
		{"not an object", `["secret"]`, msgInvalidJSON},
		{"null body", `null`, msgInvalidJSON},
		{"empty body", ``, msgInvalidJSON},
		{"trailing garbage", postBody(validSecret, validIV, 24) + "x", msgInvalidJSON},
		{"wrong field type", `{"secret":1,"iv":"` + validIV + `","duration":24}`, msgInvalidJSON},
		{"body exceeds cap", `{"pad":"` + strings.Repeat("A", 4096) + `"}`, msgInvalidJSON},
		{"secret missing", `{"iv":"` + validIV + `","duration":24}`, msgSecretEmpty},
		{"secret empty", postBody("", validIV, 24), msgSecretEmpty},
		{"secret bad base64", postBody("not$$base64", validIV, 24), msgSecretEmpty},
		{"secret too large", postBody(big, validIV, 24), msgSecretSize},
		{"iv missing", `{"secret":"` + validSecret + `","duration":24}`, msgBadIV},
		{"iv bad base64", postBody(validSecret, "%%%", 24), msgBadIV},
		{"iv wrong length", postBody(validSecret, shortIV, 24), msgBadIV},
		{"duration missing", `{"secret":"` + validSecret + `","iv":"` + validIV + `"}`, msgBadDuration},
		{"duration invalid", postBody(validSecret, validIV, 3), msgBadDuration},
		{"duration negative", postBody(validSecret, validIV, -24), msgBadDuration},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := do(t, srv, "POST", "/api/secrets", "192.0.2.10:4711", tc.body, nil)
			wantError(t, rr, http.StatusBadRequest, tc.wantMsg)
		})
	}
}

// TestPostValidationOrder: a body failing several checks must report the
// first one in contract order (secret before iv before duration).
func TestPostValidationOrder(t *testing.T) {
	cfg := loadCfg(t, map[string]any{})
	srv := New(cfg, memStore(t), []byte(testIndex), testFavicon)

	rr := do(t, srv, "POST", "/api/secrets", "192.0.2.10:4711", `{"secret":"","iv":"x","duration":9}`, nil)
	wantError(t, rr, http.StatusBadRequest, msgSecretEmpty)

	rr = do(t, srv, "POST", "/api/secrets", "192.0.2.10:4711", postBody(validSecret, "x", 9), nil)
	wantError(t, rr, http.StatusBadRequest, msgBadIV)
}

func TestPostSuccess(t *testing.T) {
	cfg := loadCfg(t, map[string]any{})
	st := memStore(t)
	srv := New(cfg, st, []byte(testIndex), testFavicon)

	rr := do(t, srv, "POST", "/api/secrets", "192.0.2.10:4711", postBody(validSecret, validIV, 24), nil)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %q)", rr.Code, rr.Body.String())
	}
	if cc := rr.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", cc)
	}
	var v struct {
		GUID string `json:"guid"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &v); err != nil {
		t.Fatalf("bad 201 body %q: %v", rr.Body.String(), err)
	}
	if !guidRe.MatchString(v.GUID) {
		t.Fatalf("guid %q does not match UUID regex", v.GUID)
	}
	if v.GUID[14] != '4' {
		t.Fatalf("guid %q: version nibble = %c, want 4", v.GUID, v.GUID[14])
	}
	if !strings.ContainsRune("89ab", rune(v.GUID[19])) {
		t.Fatalf("guid %q: variant nibble = %c, want one of 89ab", v.GUID, v.GUID[19])
	}

	// Stored verbatim: retrieval returns the identical base64 strings.
	secret, iv, err := st.TakeOnce(context.Background(), v.GUID)
	if err != nil {
		t.Fatalf("stored secret not retrievable: %v", err)
	}
	if secret != validSecret || iv != validIV {
		t.Fatalf("stored (%q,%q), want (%q,%q)", secret, iv, validSecret, validIV)
	}
}

func TestPostDuplicateRetry(t *testing.T) {
	cfg := loadCfg(t, map[string]any{})

	t.Run("one collision retries with fresh guid", func(t *testing.T) {
		fs := &fakeStore{putErrs: []error{store.ErrDuplicate}}
		srv := New(cfg, fs, []byte(testIndex), testFavicon)
		rr := do(t, srv, "POST", "/api/secrets", "192.0.2.10:1", postBody(validSecret, validIV, 24), nil)
		if rr.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201 (body %q)", rr.Code, rr.Body.String())
		}
		if len(fs.putGuids) != 2 || fs.putGuids[0] == fs.putGuids[1] {
			t.Fatalf("put guids = %v, want two distinct attempts", fs.putGuids)
		}
	})

	t.Run("two collisions fail with 500", func(t *testing.T) {
		fs := &fakeStore{putErrs: []error{store.ErrDuplicate, store.ErrDuplicate}}
		srv := New(cfg, fs, []byte(testIndex), testFavicon)
		rr := do(t, srv, "POST", "/api/secrets", "192.0.2.10:1", postBody(validSecret, validIV, 24), nil)
		wantError(t, rr, http.StatusInternalServerError, msgBackend)
	})
}

func TestPostStoreFailure(t *testing.T) {
	cfg := loadCfg(t, map[string]any{})
	fs := &fakeStore{putErrs: []error{errors.New("disk on fire")}}
	srv := New(cfg, fs, []byte(testIndex), testFavicon)
	rr := do(t, srv, "POST", "/api/secrets", "192.0.2.10:1", postBody(validSecret, validIV, 24), nil)
	wantError(t, rr, http.StatusInternalServerError, msgBackend)
}

// --- GET matrix ------------------------------------------------------------

func TestGetMatrix(t *testing.T) {
	const unknownGUID = "01234567-89ab-4cde-8f01-23456789abcd"

	t.Run("200 and row gone after", func(t *testing.T) {
		cfg := loadCfg(t, map[string]any{})
		srv := New(cfg, memStore(t), []byte(testIndex), testFavicon)
		rr := do(t, srv, "POST", "/api/secrets", "192.0.2.10:1", postBody(validSecret, validIV, 1), nil)
		var v struct {
			GUID string `json:"guid"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &v); err != nil || rr.Code != http.StatusCreated {
			t.Fatalf("POST failed: %d %q", rr.Code, rr.Body.String())
		}

		rr = do(t, srv, "GET", "/api/secrets/"+v.GUID, "192.0.2.10:1", "", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", rr.Code, rr.Body.String())
		}
		var got struct {
			Secret string `json:"secret"`
			IV     string `json:"iv"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatalf("bad 200 body %q: %v", rr.Body.String(), err)
		}
		if got.Secret != validSecret || got.IV != validIV {
			t.Fatalf("got (%q,%q), want (%q,%q)", got.Secret, got.IV, validSecret, validIV)
		}

		rr = do(t, srv, "GET", "/api/secrets/"+v.GUID, "192.0.2.10:1", "", nil)
		wantError(t, rr, http.StatusNotFound, msgBurned)
	})

	t.Run("410 expired fixture", func(t *testing.T) {
		cfg := loadCfg(t, map[string]any{})
		st := memStore(t)
		srv := New(cfg, st, []byte(testIndex), testFavicon)
		const guid = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
		if err := st.Put(context.Background(), guid, validSecret, validIV, time.Now().Unix()-10); err != nil {
			t.Fatalf("fixture put: %v", err)
		}
		rr := do(t, srv, "GET", "/api/secrets/"+guid, "192.0.2.10:1", "", nil)
		wantError(t, rr, http.StatusGone, msgExpired)
		// Row was deleted with the expired take.
		rr = do(t, srv, "GET", "/api/secrets/"+guid, "192.0.2.10:1", "", nil)
		wantError(t, rr, http.StatusNotFound, msgBurned)
	})

	t.Run("404 unknown guid", func(t *testing.T) {
		cfg := loadCfg(t, map[string]any{})
		srv := New(cfg, memStore(t), []byte(testIndex), testFavicon)
		rr := do(t, srv, "GET", "/api/secrets/"+unknownGUID, "192.0.2.10:1", "", nil)
		wantError(t, rr, http.StatusNotFound, msgBurned)
	})

	t.Run("404 malformed guid short-circuits without store hit", func(t *testing.T) {
		cfg := loadCfg(t, map[string]any{})
		fs := &fakeStore{}
		srv := New(cfg, fs, []byte(testIndex), testFavicon)
		for _, g := range []string{"nope", "01234567-89ab-4cde-8f01-23456789abcg", "0123456789ab4cde8f0123456789abcd"} {
			rr := do(t, srv, "GET", "/api/secrets/"+g, "192.0.2.10:1", "", nil)
			wantError(t, rr, http.StatusNotFound, msgBurned)
		}
		if n := fs.takeCalls.Load(); n != 0 {
			t.Fatalf("TakeOnce called %d times for malformed guids, want 0", n)
		}
		if n := fs.purgeCalls.Load(); n != 0 {
			t.Fatalf("purge triggered %d times for malformed guids, want 0", n)
		}
	})

	t.Run("500 failing store", func(t *testing.T) {
		cfg := loadCfg(t, map[string]any{})
		fs := &fakeStore{takeFn: func(string) (string, string, error) {
			return "", "", errors.New("backend melted")
		}}
		srv := New(cfg, fs, []byte(testIndex), testFavicon)
		rr := do(t, srv, "GET", "/api/secrets/"+unknownGUID, "192.0.2.10:1", "", nil)
		wantError(t, rr, http.StatusInternalServerError, msgBackend)
	})
}

func TestReadOnceOverHTTP(t *testing.T) {
	cfg := loadCfg(t, map[string]any{})
	srv := New(cfg, memStore(t), []byte(testIndex), testFavicon)

	rr := do(t, srv, "POST", "/api/secrets", "192.0.2.10:1", postBody(validSecret, validIV, 168), nil)
	var v struct {
		GUID string `json:"guid"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &v); err != nil || rr.Code != http.StatusCreated {
		t.Fatalf("POST failed: %d %q", rr.Code, rr.Body.String())
	}

	first := do(t, srv, "GET", "/api/secrets/"+v.GUID, "192.0.2.10:1", "", nil)
	if first.Code != http.StatusOK {
		t.Fatalf("first GET = %d, want 200", first.Code)
	}
	second := do(t, srv, "GET", "/api/secrets/"+v.GUID, "192.0.2.10:1", "", nil)
	wantError(t, second, http.StatusNotFound, msgBurned)
}

// --- routing ---------------------------------------------------------------

func TestRouting(t *testing.T) {
	cfg := loadCfg(t, map[string]any{})
	srv := New(cfg, memStore(t), []byte(testIndex), testFavicon)

	t.Run("index served on exact root", func(t *testing.T) {
		rr := do(t, srv, "GET", "/", "192.0.2.10:1", "", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("GET / = %d, want 200", rr.Code)
		}
		if ct := rr.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Fatalf("Content-Type = %q, want text/html; charset=utf-8", ct)
		}
		if cc := rr.Header().Get("Cache-Control"); cc != "no-store" {
			t.Fatalf("Cache-Control = %q, want no-store", cc)
		}
		if rr.Body.String() != testIndex {
			t.Fatalf("body = %q, want index bytes", rr.Body.String())
		}
	})

	t.Run("favicon served", func(t *testing.T) {
		rr := do(t, srv, "GET", "/favicon.ico", "192.0.2.10:1", "", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("GET /favicon.ico = %d, want 200", rr.Code)
		}
		if ct := rr.Header().Get("Content-Type"); ct != "image/x-icon" {
			t.Fatalf("Content-Type = %q, want image/x-icon", ct)
		}
		if rr.Body.String() != string(testFavicon) {
			t.Fatalf("body = %q, want favicon bytes", rr.Body.String())
		}
	})

	notFound := []struct{ method, path string }{
		{"GET", "/x"},
		{"GET", "/api"},
		{"GET", "/api/secrets"},
		{"GET", "/api/secrets/x/y"},
		{"PUT", "/api/secrets"},
		{"DELETE", "/api/secrets/01234567-89ab-4cde-8f01-23456789abcd"},
		{"POST", "/"},
		{"GET", "/index.html"},
	}
	for _, tc := range notFound {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rr := do(t, srv, tc.method, tc.path, "192.0.2.10:1", "", nil)
			wantError(t, rr, http.StatusNotFound, msgNotFound)
		})
	}
}

// --- panic recovery --------------------------------------------------------

func TestPanicRecovery(t *testing.T) {
	cfg := loadCfg(t, map[string]any{})
	fs := &fakeStore{takeFn: func(string) (string, string, error) { panic("handler exploded") }}
	srv := New(cfg, fs, []byte(testIndex), testFavicon)
	rr := do(t, srv, "GET", "/api/secrets/01234567-89ab-4cde-8f01-23456789abcd", "192.0.2.10:1", "", nil)
	wantError(t, rr, http.StatusInternalServerError, msgBackend)
}

// --- purge throttle --------------------------------------------------------

func TestPurgeThrottle(t *testing.T) {
	cfg := loadCfg(t, map[string]any{})
	fs := &fakeStore{}
	srv := New(cfg, fs, []byte(testIndex), testFavicon)

	const guid = "/api/secrets/01234567-89ab-4cde-8f01-23456789abcd"
	do(t, srv, "GET", guid, "192.0.2.10:1", "", nil)
	do(t, srv, "GET", guid, "192.0.2.10:1", "", nil)

	// The purge runs async; wait for the single winner, then confirm no second run.
	deadline := time.Now().Add(2 * time.Second)
	for fs.purgeCalls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if n := fs.purgeCalls.Load(); n != 1 {
		t.Fatalf("purge ran %d times for two immediate GETs, want exactly 1", n)
	}
}

// TestPurgeFailureNotSurfaced: a failing purge must not affect the response.
func TestPurgeFailureNotSurfaced(t *testing.T) {
	cfg := loadCfg(t, map[string]any{})
	fs := &fakeStore{purgeErr: errors.New("purge broke")}
	srv := New(cfg, fs, []byte(testIndex), testFavicon)
	rr := do(t, srv, "GET", "/api/secrets/01234567-89ab-4cde-8f01-23456789abcd", "192.0.2.10:1", "", nil)
	wantError(t, rr, http.StatusNotFound, msgBurned)
}

// --- guid generator --------------------------------------------------------

func TestNewGUID(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 256; i++ {
		g, err := newGUID()
		if err != nil {
			t.Fatalf("newGUID: %v", err)
		}
		if !guidRe.MatchString(g) {
			t.Fatalf("guid %q does not match UUID regex", g)
		}
		if g[14] != '4' {
			t.Fatalf("guid %q: version nibble = %c, want 4", g, g[14])
		}
		if !strings.ContainsRune("89ab", rune(g[19])) {
			t.Fatalf("guid %q: variant nibble = %c", g, g[19])
		}
		if seen[g] {
			t.Fatalf("guid %q repeated", g)
		}
		seen[g] = true
	}
}
