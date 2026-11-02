package config

import (
	"strings"
	"testing"
	"time"
)

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func validEnv() map[string]string {
	return map[string]string{
		"APP_ENV":      "production",
		"DB_HOST":      "localhost",
		"DB_USER":      "postgres",
		"DB_PASSWORD":  "p@ss/word:1",
		"DB_NAME":      "crm",
		"JWT_SECRET":   strings.Repeat("x", 32),
		"FRONTEND_URL": "https://app.example.com/",
		"SMTP_HOST":    "smtp.example.com",
		"SMTP_FROM":    "CRM <no-reply@example.com>",
	}
}

func TestFromEnvValid(t *testing.T) {
	c, err := FromEnv(envFrom(validEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != "8080" || c.SMTP.Port != 587 || c.LogFormat != "json" {
		t.Errorf("defaults: port=%s smtp=%d format=%s", c.Port, c.SMTP.Port, c.LogFormat)
	}
	if c.FrontendURL != "https://app.example.com" {
		t.Errorf("FrontendURL = %q, want trailing slash trimmed", c.FrontendURL)
	}
	if want := "postgres://postgres:p%40ss%2Fword%3A1@localhost:5432/crm"; c.DatabaseURL != want {
		t.Errorf("DatabaseURL = %q, want %q", c.DatabaseURL, want)
	}
	if len(c.CORSAllowedOrigins) != 1 || c.CORSAllowedOrigins[0] != "http://localhost:5173" {
		t.Errorf("CORS default = %v", c.CORSAllowedOrigins)
	}
}

func TestFromEnvDefaultsToProduction(t *testing.T) {
	env := validEnv()
	delete(env, "APP_ENV")
	c, err := FromEnv(envFrom(env))
	if err != nil {
		t.Fatal(err)
	}
	if c.Env != EnvProduction {
		t.Errorf("Env = %q, want production", c.Env)
	}
}

func TestFromEnvRejects(t *testing.T) {
	cases := map[string]struct {
		change  map[string]string
		wantErr string
	}{
		"short jwt secret":          {map[string]string{"JWT_SECRET": "short"}, "JWT_SECRET"},
		"missing db":                {map[string]string{"DB_HOST": ""}, "DB_HOST"},
		"bad env":                   {map[string]string{"APP_ENV": "prod"}, "APP_ENV"},
		"bad port":                  {map[string]string{"PORT": "http"}, "PORT"},
		"no smtp in production":     {map[string]string{"SMTP_HOST": ""}, "SMTP_HOST is required"},
		"bad smtp from":             {map[string]string{"SMTP_FROM": "nope"}, "SMTP_FROM"},
		"no frontend in production": {map[string]string{"FRONTEND_URL": ""}, "FRONTEND_URL is required"},
		"relative frontend url":     {map[string]string{"FRONTEND_URL": "app.example.com"}, "FRONTEND_URL"},
		"reset token flag in prod":  {map[string]string{"RETURN_RESET_TOKEN_IN_RESPONSE": "true"}, "RETURN_RESET_TOKEN_IN_RESPONSE"},
		"reset token flag in staging": {map[string]string{
			"APP_ENV": "staging", "RETURN_RESET_TOKEN_IN_RESPONSE": "true",
		}, "only allowed when APP_ENV=development"},
		"bad boolean": {map[string]string{"RETURN_RESET_TOKEN_IN_RESPONSE": "yes please"}, "not a boolean"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			env := validEnv()
			for k, v := range tc.change {
				env[k] = v
			}
			_, err := FromEnv(envFrom(env))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestFromEnvDevelopment(t *testing.T) {
	env := validEnv()
	env["APP_ENV"] = "development"
	env["RETURN_RESET_TOKEN_IN_RESPONSE"] = "true"
	delete(env, "SMTP_HOST")
	delete(env, "FRONTEND_URL")
	c, err := FromEnv(envFrom(env))
	if err != nil {
		t.Fatal(err)
	}
	if !c.ReturnResetTokenInResponse || c.SMTP.Enabled() || c.FrontendURL != "http://localhost:5173" || c.LogFormat != "text" {
		t.Errorf("development config = %+v", c)
	}
}

func TestTokenLifetimes(t *testing.T) {
	c, err := FromEnv(envFrom(validEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if c.AccessTokenTTL != 15*time.Minute || c.RefreshTokenTTL != 720*time.Hour || !c.RefreshCookieSecure {
		t.Errorf("defaults: access=%s refresh=%s secure=%v", c.AccessTokenTTL, c.RefreshTokenTTL, c.RefreshCookieSecure)
	}

	env := validEnv()
	env["APP_ENV"] = "development"
	env["ACCESS_TOKEN_TTL"] = "24h"
	env["REFRESH_TOKEN_TTL"] = "168h"
	if c, err = FromEnv(envFrom(env)); err != nil {
		t.Fatal(err)
	}
	if c.AccessTokenTTL != 24*time.Hour || c.RefreshTokenTTL != 168*time.Hour || c.RefreshCookieSecure {
		t.Errorf("development: access=%s refresh=%s secure=%v", c.AccessTokenTTL, c.RefreshTokenTTL, c.RefreshCookieSecure)
	}

	for key, value := range map[string]string{
		"ACCESS_TOKEN_TTL":  "25h",
		"REFRESH_TOKEN_TTL": "30m",
	} {
		env := validEnv()
		env[key] = value
		if _, err := FromEnv(envFrom(env)); err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("%s=%s: err = %v, want rejection", key, value, err)
		}
	}
}
