package config

import (
	"bytes"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fullYAML = `listen: "0.0.0.0:9999"
db:
  driver: "sqlite"
  dsn: "/var/lib/oncevault/oncevault.db"
max_secret_bytes: 65536
trusted_proxies: ["10.0.0.1", "fd00::/8"]
rate_limit:
  default: 60
  rules:
    "10.0.0.0/8": "0"
    "192.0.2.0/24": "-"
    "203.0.113.7": "12"
`

const fullJSON = `{
  "listen": "0.0.0.0:9999",
  "db": {"driver": "sqlite", "dsn": "/var/lib/oncevault/oncevault.db"},
  "max_secret_bytes": 65536,
  "trusted_proxies": ["10.0.0.1", "fd00::/8"],
  "rate_limit": {
    "default": 60,
    "rules": {
      "10.0.0.0/8": "0",
      "192.0.2.0/24": "-",
      "203.0.113.7": "12"
    }
  }
}`

func writeConfig(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustLoad(t *testing.T, name, content string) *Config {
	t.Helper()
	cfg, err := Load(writeConfig(t, name, content))
	if err != nil {
		t.Fatalf("Load(%s): %v", name, err)
	}
	return cfg
}

func mustFail(t *testing.T, name, content, wantSubstr string) {
	t.Helper()
	_, err := Load(writeConfig(t, name, content))
	if err == nil {
		t.Fatalf("Load(%s): expected error containing %q, got nil", name, wantSubstr)
	}
	if !strings.Contains(err.Error(), wantSubstr) {
		t.Fatalf("Load(%s): error %q does not contain %q", name, err, wantSubstr)
	}
}

func checkFullConfig(t *testing.T, cfg *Config) {
	t.Helper()
	if cfg.Listen != "0.0.0.0:9999" {
		t.Errorf("Listen = %q", cfg.Listen)
	}
	if cfg.DB.Driver != "sqlite" || cfg.DB.DSN != "/var/lib/oncevault/oncevault.db" {
		t.Errorf("DB = %+v", cfg.DB)
	}
	if cfg.MaxSecretBytes != 65536 {
		t.Errorf("MaxSecretBytes = %d", cfg.MaxSecretBytes)
	}
	wantProxies := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.1/32"),
		netip.MustParsePrefix("fd00::/8"),
	}
	if len(cfg.TrustedProxyNets) != len(wantProxies) {
		t.Fatalf("TrustedProxyNets = %v", cfg.TrustedProxyNets)
	}
	for i, want := range wantProxies {
		if cfg.TrustedProxyNets[i] != want {
			t.Errorf("TrustedProxyNets[%d] = %v, want %v", i, cfg.TrustedProxyNets[i], want)
		}
	}
	if cfg.RateLimit.Default != 60 {
		t.Errorf("RateLimit.Default = %d", cfg.RateLimit.Default)
	}
	// Sorted longest-prefix first: /32, /24, /8.
	want := []Rule{
		{Net: netip.MustParsePrefix("203.0.113.7/32"), Limit: 12},
		{Net: netip.MustParsePrefix("192.0.2.0/24"), Blocked: true},
		{Net: netip.MustParsePrefix("10.0.0.0/8"), Unlimited: true},
	}
	if len(cfg.Rules) != len(want) {
		t.Fatalf("Rules = %+v", cfg.Rules)
	}
	for i, w := range want {
		if cfg.Rules[i] != w {
			t.Errorf("Rules[%d] = %+v, want %+v", i, cfg.Rules[i], w)
		}
	}
}

func TestLoadYAML(t *testing.T) {
	checkFullConfig(t, mustLoad(t, "c.yaml", fullYAML))
	checkFullConfig(t, mustLoad(t, "c.yml", fullYAML))
}

func TestLoadJSON(t *testing.T) {
	checkFullConfig(t, mustLoad(t, "c.json", fullJSON))
}

func TestLoadDefaults(t *testing.T) {
	cfg := mustLoad(t, "c.yaml", "db: {driver: redis, dsn: \"redis://localhost:6379/0\"}\nrate_limit: {default: 60}\n")
	if cfg.Listen != "127.0.0.1:8420" {
		t.Errorf("default Listen = %q", cfg.Listen)
	}
	if cfg.MaxSecretBytes != 16384 {
		t.Errorf("default MaxSecretBytes = %d", cfg.MaxSecretBytes)
	}
	if len(cfg.Rules) != 0 || len(cfg.TrustedProxyNets) != 0 {
		t.Errorf("expected no rules/proxies, got %+v / %+v", cfg.Rules, cfg.TrustedProxyNets)
	}
}

func TestLoadErrors(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Error("missing file: expected error")
	}
	mustFail(t, "c.toml", "listen = \"x\"\n", "unsupported extension")
	mustFail(t, "c.yaml", "db: {driver: mongodb, dsn: x}\n", "db.driver")
	mustFail(t, "c.yaml", "db: {driver: sqlite, dsn: \"\"}\n", "db.dsn")
	mustFail(t, "c.yaml", "db: {driver: sqlite}\n", "db.dsn")
	mustFail(t, "c.yaml", "db: {driver: sqlite, dsn: x}\nmax_secret_bytes: -1\n", "max_secret_bytes")
	mustFail(t, "c.yaml", "db: {driver: sqlite, dsn: x}\nrate_limit: {default: -5}\n", "rate_limit.default")
	mustFail(t, "c.yaml", "not: [valid: yaml\n", "parse YAML")
	mustFail(t, "c.json", "{not json", "parse JSON")
}

func TestDefaultZeroRevertsTo6000(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	cfg := mustLoad(t, "c.yaml", "db: {driver: sqlite, dsn: x}\n")
	if cfg.RateLimit.Default != 6000 {
		t.Errorf("Default = %d, want 6000", cfg.RateLimit.Default)
	}
	if !strings.Contains(buf.String(), "reverting to 6000/min") {
		t.Errorf("expected warning log, got %q", buf.String())
	}
}

func TestRuleParsing(t *testing.T) {
	cfg := mustLoad(t, "c.yaml", `db: {driver: sqlite, dsn: x}
rate_limit:
  default: 60
  rules:
    "192.0.2.1": "5"
    "2001:db8::1": "7"
`)
	got := map[string]Rule{}
	for _, r := range cfg.Rules {
		got[r.Net.String()] = r
	}
	if r, ok := got["192.0.2.1/32"]; !ok || r.Limit != 5 {
		t.Errorf("bare IPv4 not normalized to /32: %+v", cfg.Rules)
	}
	if r, ok := got["2001:db8::1/128"]; !ok || r.Limit != 7 {
		t.Errorf("bare IPv6 not normalized to /128: %+v", cfg.Rules)
	}

	base := "db: {driver: sqlite, dsn: x}\nrate_limit:\n  default: 60\n  rules:\n"
	mustFail(t, "c.yaml", base+"    \"not-a-cidr\": \"5\"\n", "rate_limit.rules")
	mustFail(t, "c.yaml", base+"    \"10.0.0.0/33\": \"5\"\n", "rate_limit.rules")
	mustFail(t, "c.yaml", base+"    \"10.0.0.0/8\": \"fast\"\n", "positive integer")
	mustFail(t, "c.yaml", base+"    \"10.0.0.0/8\": \"-3\"\n", "positive integer")
}

func TestLimitFor(t *testing.T) {
	cfg := mustLoad(t, "c.yaml", `db: {driver: sqlite, dsn: x}
rate_limit:
  default: 60
  rules:
    "10.0.0.0/8": "100"
    "10.1.0.0/16": "7"
    "192.0.2.0/24": "-"
    "198.51.100.0/24": "0"
`)
	cases := []struct {
		ip      string
		limit   int
		blocked bool
	}{
		{"10.1.2.3", 7, false},        // /16 beats /8
		{"10.2.2.3", 100, false},      // only /8 matches
		{"192.0.2.55", 0, true},       // "-" → blocked
		{"198.51.100.9", 0, false},    // "0" → unlimited
		{"203.0.113.1", 60, false},    // unmatched → default
		{"2001:db8::1", 60, false},    // unmatched IPv6 → default
		{"::ffff:10.1.2.3", 7, false}, // 4-in-6 unmapped before matching
	}
	for _, c := range cases {
		limit, blocked := cfg.LimitFor(netip.MustParseAddr(c.ip))
		if limit != c.limit || blocked != c.blocked {
			t.Errorf("LimitFor(%s) = (%d, %v), want (%d, %v)", c.ip, limit, blocked, c.limit, c.blocked)
		}
	}
}
