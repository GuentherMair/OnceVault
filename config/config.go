// Package config loads and validates the OnceVault configuration file
// per the frozen schema in docs/PLAN_STEP_0.md §0.5.
package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	defaultListen         = "127.0.0.1:8420"
	defaultMaxSecretBytes = 16384
	// fallbackDefaultLimit replaces rate_limit.default=0, which would disable limiting.
	fallbackDefaultLimit = 6000
)

var validDrivers = map[string]bool{"sqlite": true, "redis": true, "mysql": true, "postgres": true}

// DB selects the storage backend.
type DB struct {
	Driver string `json:"driver" yaml:"driver"`
	DSN    string `json:"dsn" yaml:"dsn"`
}

// RateLimit holds the raw rate-limiting section; Config.Rules is its parsed form.
type RateLimit struct {
	Default int               `json:"default" yaml:"default"`
	Rules   map[string]string `json:"rules" yaml:"rules"`
}

// Rule is one parsed CIDR rate-limit rule. Exactly one of Blocked, Unlimited,
// or a positive Limit applies.
type Rule struct {
	Net       netip.Prefix
	Limit     int
	Blocked   bool
	Unlimited bool
}

// Config is the validated OnceVault configuration.
type Config struct {
	Listen         string    `json:"listen" yaml:"listen"`
	DB             DB        `json:"db" yaml:"db"`
	MaxSecretBytes int       `json:"max_secret_bytes" yaml:"max_secret_bytes"`
	TrustedProxies []string  `json:"trusted_proxies" yaml:"trusted_proxies"`
	RateLimit      RateLimit `json:"rate_limit" yaml:"rate_limit"`

	// TrustedProxyNets is TrustedProxies parsed; bare IPs normalized to /32 or /128.
	TrustedProxyNets []netip.Prefix `json:"-" yaml:"-"`
	// Rules is RateLimit.Rules parsed and sorted by descending prefix length,
	// so the first Contains match during iteration is the longest-prefix match.
	Rules []Rule `json:"-" yaml:"-"`
}

// Load reads, parses, and validates the config file at path. The format is
// chosen by extension: .json → JSON, .yaml/.yml → YAML; anything else errors.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := &Config{}
	switch ext := strings.ToLower(filepath.Ext(path)); ext {
	case ".json":
		if err := json.Unmarshal(raw, cfg); err != nil {
			return nil, fmt.Errorf("parse JSON config %s: %w", path, err)
		}
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(raw, cfg); err != nil {
			return nil, fmt.Errorf("parse YAML config %s: %w", path, err)
		}
	default:
		return nil, fmt.Errorf("config %s: unsupported extension %q (want .json, .yaml, or .yml)", path, ext)
	}

	if err := cfg.finalize(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// finalize applies defaults and validates per contract 0.5.
func (c *Config) finalize() error {
	if c.Listen == "" {
		c.Listen = defaultListen
	}
	if !validDrivers[c.DB.Driver] {
		return fmt.Errorf("db.driver %q invalid: must be one of sqlite, redis, mysql, postgres", c.DB.Driver)
	}
	if c.DB.DSN == "" {
		return fmt.Errorf("db.dsn must not be empty")
	}
	if c.MaxSecretBytes == 0 {
		c.MaxSecretBytes = defaultMaxSecretBytes
	}
	if c.MaxSecretBytes < 1 {
		return fmt.Errorf("max_secret_bytes must be >= 1, got %d", c.MaxSecretBytes)
	}

	if c.RateLimit.Default < 0 {
		return fmt.Errorf("rate_limit.default must be >= 0, got %d", c.RateLimit.Default)
	}
	if c.RateLimit.Default == 0 {
		slog.Warn("rate_limit.default=0 is not allowed, reverting to 6000/min")
		c.RateLimit.Default = fallbackDefaultLimit
	}

	c.TrustedProxyNets = make([]netip.Prefix, 0, len(c.TrustedProxies))
	for _, s := range c.TrustedProxies {
		p, err := parsePrefix(s)
		if err != nil {
			return fmt.Errorf("trusted_proxies entry %q: %w", s, err)
		}
		c.TrustedProxyNets = append(c.TrustedProxyNets, p)
	}

	c.Rules = make([]Rule, 0, len(c.RateLimit.Rules))
	for cidr, val := range c.RateLimit.Rules {
		p, err := parsePrefix(cidr)
		if err != nil {
			return fmt.Errorf("rate_limit.rules key %q: %w", cidr, err)
		}
		r := Rule{Net: p}
		switch val {
		case "-":
			r.Blocked = true
		case "0":
			r.Unlimited = true
		default:
			n, err := strconv.Atoi(val)
			if err != nil || n <= 0 {
				return fmt.Errorf("rate_limit.rules[%q] value %q invalid: must be \"0\", \"-\", or a positive integer", cidr, val)
			}
			r.Limit = n
		}
		c.Rules = append(c.Rules, r)
	}
	// Longer prefixes first; ties ordered by address for determinism.
	sort.Slice(c.Rules, func(i, j int) bool {
		if c.Rules[i].Net.Bits() != c.Rules[j].Net.Bits() {
			return c.Rules[i].Net.Bits() > c.Rules[j].Net.Bits()
		}
		return c.Rules[i].Net.Addr().Less(c.Rules[j].Net.Addr())
	})
	return nil
}

// parsePrefix parses a CIDR; a bare IP is normalized to /32 (IPv4) or /128 (IPv6).
func parsePrefix(s string) (netip.Prefix, error) {
	if !strings.Contains(s, "/") {
		addr, err := netip.ParseAddr(s)
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("not a valid IP or CIDR: %w", err)
		}
		addr = addr.Unmap()
		return netip.PrefixFrom(addr, addr.BitLen()), nil
	}
	p, err := netip.ParsePrefix(s)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("not a valid CIDR: %w", err)
	}
	return p.Masked(), nil
}

// LimitFor resolves the POST rate limit for ip via longest-prefix match over
// Rules, falling back to rate_limit.default when nothing matches.
// Conventions: blocked == true means all routes are denied (limit is 0 and
// meaningless); limit == 0 with blocked == false means unlimited.
func (c *Config) LimitFor(ip netip.Addr) (limit int, blocked bool) {
	ip = ip.Unmap()
	for _, r := range c.Rules { // sorted longest-prefix first
		if !r.Net.Contains(ip) {
			continue
		}
		if r.Blocked {
			return 0, true
		}
		if r.Unlimited {
			return 0, false
		}
		return r.Limit, false
	}
	return c.RateLimit.Default, false
}
