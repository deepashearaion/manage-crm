// Package config loads and validates all runtime settings from the
// environment (and .env, when present). Load fails fast, reporting every
// problem at once, so a misconfigured server never starts.
package config

import (
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Environments accepted in APP_ENV.
const (
	EnvDevelopment = "development"
	EnvTest        = "test"
	EnvStaging     = "staging"
	EnvProduction  = "production"
)

const minJWTSecretLen = 32

type Config struct {
	Env  string // APP_ENV; defaults to production so dev-only switches stay off unless asked for
	Port string // PORT

	DatabaseURL string // built from DB_HOST, DB_PORT, DB_USER, DB_PASSWORD, DB_NAME

	JWTSecret []byte // JWT_SECRET, at least 32 bytes

	AccessTokenTTL  time.Duration // ACCESS_TOKEN_TTL, default 15m, 1m to 24h
	RefreshTokenTTL time.Duration // REFRESH_TOKEN_TTL, default 720h (30 days), 1h to 2160h (90 days)

	// RefreshCookieSecure marks the refresh-token cookie Secure (HTTPS only).
	// Off in development and test, where the API is served over plain HTTP.
	RefreshCookieSecure bool

	CORSAllowedOrigins []string // CORS_ALLOWED_ORIGINS, comma-separated
	FrontendURL        string   // FRONTEND_URL, used to build links in emails

	SMTP SMTP

	// ReturnResetTokenInResponse keeps the legacy forgot-password response
	// (token in the JSON body) for the current frontend. Development only.
	ReturnResetTokenInResponse bool

	LogLevel  string // LOG_LEVEL: debug, info, warn, error
	LogFormat string // LOG_FORMAT: text or json
}

type SMTP struct {
	Host string
	Port int
	User string
	Pass string
	From string
}

// Enabled reports whether an SMTP server is configured.
func (s SMTP) Enabled() bool { return s.Host != "" }

func (c *Config) IsDevelopment() bool { return c.Env == EnvDevelopment }

// Load reads .env (if present) and the environment, then validates the result.
func Load() (*Config, error) {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("load .env: %w", err)
	}
	return FromEnv(os.Getenv)
}

// LoadDatabaseURL reads only the database settings, for tools such as
// cmd/migrate that do not need the rest of the configuration.
func LoadDatabaseURL() (string, error) {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("load .env: %w", err)
	}
	return databaseURL(os.Getenv)
}

func databaseURL(getenv func(string) string) (string, error) {
	host, port := strings.TrimSpace(getenv("DB_HOST")), strings.TrimSpace(getenv("DB_PORT"))
	user, name := strings.TrimSpace(getenv("DB_USER")), strings.TrimSpace(getenv("DB_NAME"))
	if port == "" {
		port = "5432"
	}
	if host == "" || user == "" || name == "" {
		return "", errors.New("DB_HOST, DB_USER and DB_NAME are required")
	}
	return (&url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(user, getenv("DB_PASSWORD")),
		Host:   host + ":" + port,
		Path:   "/" + name,
	}).String(), nil
}

// FromEnv builds a Config from getenv without touching .env; tests use it
// directly.
func FromEnv(getenv func(string) string) (*Config, error) {
	var errs []error
	get := func(key, fallback string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return fallback
	}
	boolean := func(key string) bool {
		raw := get(key, "false")
		v, err := strconv.ParseBool(raw)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %q is not a boolean", key, raw))
		}
		return v
	}

	c := &Config{
		Env:         get("APP_ENV", EnvProduction),
		Port:        get("PORT", "8080"),
		JWTSecret:   []byte(getenv("JWT_SECRET")),
		FrontendURL: strings.TrimRight(get("FRONTEND_URL", ""), "/"),
		LogLevel:    get("LOG_LEVEL", "info"),
		SMTP: SMTP{
			Host: get("SMTP_HOST", ""),
			User: get("SMTP_USER", ""),
			Pass: getenv("SMTP_PASS"),
			From: get("SMTP_FROM", ""),
		},
		ReturnResetTokenInResponse: boolean("RETURN_RESET_TOKEN_IN_RESPONSE"),
	}

	switch c.Env {
	case EnvDevelopment, EnvTest, EnvStaging, EnvProduction:
	default:
		errs = append(errs, fmt.Errorf("APP_ENV: %q is not one of development, test, staging, production", c.Env))
	}

	if c.IsDevelopment() {
		c.LogFormat = get("LOG_FORMAT", "text")
	} else {
		c.LogFormat = get("LOG_FORMAT", "json")
	}
	if c.LogFormat != "text" && c.LogFormat != "json" {
		errs = append(errs, fmt.Errorf("LOG_FORMAT: %q is not text or json", c.LogFormat))
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("LOG_LEVEL: %q is not debug, info, warn or error", c.LogLevel))
	}

	if p, err := strconv.Atoi(c.Port); err != nil || p < 1 || p > 65535 {
		errs = append(errs, fmt.Errorf("PORT: %q is not a valid port", c.Port))
	}

	dbURL, err := databaseURL(getenv)
	if err != nil {
		errs = append(errs, err)
	}
	c.DatabaseURL = dbURL

	if len(c.JWTSecret) < minJWTSecretLen {
		errs = append(errs, fmt.Errorf("JWT_SECRET: must be at least %d bytes (generate one with `openssl rand -base64 48`)", minJWTSecretLen))
	}

	for _, origin := range strings.Split(get("CORS_ALLOWED_ORIGINS", "http://localhost:5173"), ",") {
		if origin = strings.TrimSpace(origin); origin != "" {
			c.CORSAllowedOrigins = append(c.CORSAllowedOrigins, origin)
		}
	}

	if c.FrontendURL == "" {
		if c.IsDevelopment() || c.Env == EnvTest {
			c.FrontendURL = "http://localhost:5173"
		} else {
			errs = append(errs, errors.New("FRONTEND_URL is required outside development"))
		}
	} else if u, err := url.Parse(c.FrontendURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		errs = append(errs, fmt.Errorf("FRONTEND_URL: %q is not an absolute http(s) URL", c.FrontendURL))
	}

	smtpPort := get("SMTP_PORT", "587")
	if p, err := strconv.Atoi(smtpPort); err != nil || p < 1 || p > 65535 {
		errs = append(errs, fmt.Errorf("SMTP_PORT: %q is not a valid port", smtpPort))
	} else {
		c.SMTP.Port = p
	}
	if c.SMTP.Enabled() {
		if _, err := mail.ParseAddress(c.SMTP.From); err != nil {
			errs = append(errs, fmt.Errorf("SMTP_FROM: %q is not a valid address", c.SMTP.From))
		}
	} else if !c.IsDevelopment() && c.Env != EnvTest {
		// Without SMTP, password-reset emails could not be delivered.
		errs = append(errs, errors.New("SMTP_HOST is required outside development"))
	}

	duration := func(key string, fallback, minimum, maximum time.Duration) time.Duration {
		raw := get(key, "")
		if raw == "" {
			return fallback
		}
		d, err := time.ParseDuration(raw)
		if err != nil || d < minimum || d > maximum {
			errs = append(errs, fmt.Errorf("%s: %q must be a duration from %s to %s (e.g. 15m, 720h)", key, raw, minimum, maximum))
			return fallback
		}
		return d
	}
	c.AccessTokenTTL = duration("ACCESS_TOKEN_TTL", 15*time.Minute, time.Minute, 24*time.Hour)
	c.RefreshTokenTTL = duration("REFRESH_TOKEN_TTL", 30*24*time.Hour, time.Hour, 90*24*time.Hour)
	c.RefreshCookieSecure = !c.IsDevelopment() && c.Env != EnvTest

	if c.ReturnResetTokenInResponse && !c.IsDevelopment() {
		errs = append(errs, fmt.Errorf("RETURN_RESET_TOKEN_IN_RESPONSE=true is only allowed when APP_ENV=development (APP_ENV is %q)", c.Env))
	}

	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("invalid configuration:\n%w", err)
	}
	return c, nil
}
