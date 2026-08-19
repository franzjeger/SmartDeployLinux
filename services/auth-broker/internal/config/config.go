// Config loaded from env. No defaults that would fail closed in surprising
// ways: anything required is required, and we error early with a clear
// message.

package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Listen     string
	LogLevel   slog.Level

	PgHost     string
	PgPort     string
	PgUser     string
	PgPass     string
	PgDB       string

	DefaultCodeTTL  time.Duration
	TSAuthkeyTTL    time.Duration

	RateRedeemPerIPHour int
	RateRedeemPerCode   int
	RateIssuePerOpHour  int // issue-code cap per operator per hour

	// Exactly one of HeadscaleURL or TailscaleAPIKey must be set.
	HeadscaleURL    string
	HeadscaleAPIKey string
	HeadscaleUser   string

	TailscaleAPIKey string
	TailscaleTailnet string

	DeployFQDN     string
	DeployFQDNTailnet string
	APIInternalURL string

	// IssueSharedSecret gates POST /issue-code. When set, callers must
	// present it in X-Internal-Auth; when empty the broker logs a loud
	// warning and accepts any caller on the internal network (dev-mode
	// escape hatch, matching the API's OIDC fallback).
	IssueSharedSecret string

	// AuditFile, if set, receives one JSON line per audit event in
	// addition to stdout. Append-only; intended to live on a volume the
	// operator backs up separately from Postgres so a DB compromise
	// can't rewrite history. SECURITY.md §4 #4.
	AuditFile string
}

func FromEnv() (*Config, error) {
	c := &Config{
		Listen:           getenv("AUTH_BROKER_LISTEN", ":8081"),
		LogLevel:         parseLogLevel(getenv("LOG_LEVEL", "info")),
		PgHost:           getenv("POSTGRES_HOST", "postgres"),
		PgPort:           getenv("POSTGRES_PORT", "5432"),
		PgUser:           os.Getenv("POSTGRES_USER"),
		PgPass:           os.Getenv("POSTGRES_PASSWORD"),
		PgDB:             os.Getenv("POSTGRES_DB"),
		HeadscaleURL:     os.Getenv("HEADSCALE_URL"),
		HeadscaleAPIKey:  os.Getenv("HEADSCALE_API_KEY"),
		HeadscaleUser:    getenv("HEADSCALE_USER", "deploy"),
		TailscaleAPIKey:  os.Getenv("TAILSCALE_API_KEY"),
		TailscaleTailnet: os.Getenv("TAILSCALE_TAILNET"),
		DeployFQDN:       os.Getenv("DEPLOY_FQDN"),
		DeployFQDNTailnet: os.Getenv("DEPLOY_FQDN_TAILNET"),
		APIInternalURL:   getenv("API_INTERNAL_URL", "http://api:8080"),
		AuditFile:         os.Getenv("AUDIT_FILE"),
		IssueSharedSecret: os.Getenv("AUTH_BROKER_ISSUE_SHARED_SECRET"),
	}

	var err error
	if c.DefaultCodeTTL, err = parseDur("AUTH_BROKER_DEFAULT_CODE_TTL", "24h"); err != nil {
		return nil, err
	}
	if c.TSAuthkeyTTL, err = parseDur("AUTH_BROKER_TS_AUTHKEY_TTL", "1h"); err != nil {
		return nil, err
	}
	if c.RateRedeemPerIPHour, err = parseInt("AUTH_BROKER_RATE_LIMIT_REDEEM_PER_IP_PER_HOUR", "30"); err != nil {
		return nil, err
	}
	if c.RateRedeemPerCode, err = parseInt("AUTH_BROKER_RATE_LIMIT_REDEEM_PER_CODE", "5"); err != nil {
		return nil, err
	}
	if c.RateIssuePerOpHour, err = parseInt("AUTH_BROKER_RATE_LIMIT_ISSUE_PER_OPERATOR_PER_HOUR", "100"); err != nil {
		return nil, err
	}

	if c.PgUser == "" || c.PgPass == "" || c.PgDB == "" {
		return nil, errors.New("POSTGRES_USER/POSTGRES_PASSWORD/POSTGRES_DB required")
	}
	if c.DeployFQDN == "" {
		return nil, errors.New("DEPLOY_FQDN required")
	}
	if c.HeadscaleURL == "" && c.TailscaleAPIKey == "" {
		return nil, errors.New("either HEADSCALE_URL+HEADSCALE_API_KEY or TAILSCALE_API_KEY+TAILSCALE_TAILNET must be set")
	}
	if c.HeadscaleURL != "" {
		if _, err := url.Parse(c.HeadscaleURL); err != nil {
			return nil, fmt.Errorf("HEADSCALE_URL invalid: %w", err)
		}
		if c.HeadscaleAPIKey == "" {
			return nil, errors.New("HEADSCALE_API_KEY required when HEADSCALE_URL set")
		}
	}
	if c.TailscaleAPIKey != "" && c.TailscaleTailnet == "" {
		return nil, errors.New("TAILSCALE_TAILNET required when TAILSCALE_API_KEY set")
	}

	return c, nil
}

func (c *Config) PostgresDSN() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		c.PgUser, c.PgPass, c.PgHost, c.PgPort, c.PgDB)
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func parseDur(k, def string) (time.Duration, error) {
	v := getenv(k, def)
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", k, err)
	}
	return d, nil
}

func parseInt(k, def string) (int, error) {
	v := getenv(k, def)
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", k, err)
	}
	return n, nil
}

func parseLogLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
