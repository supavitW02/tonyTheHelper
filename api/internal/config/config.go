// Package config reads the API's settings from the environment and fails loudly
// when something required is missing, rather than at the first request.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// minSecretLen is the shortest session secret we accept. A short HMAC key is the
// kind of mistake that looks fine until someone forges a session.
const minSecretLen = 32

type Config struct {
	DatabaseURL   string
	LineChannelID string // audience the LINE ID token must be issued for
	LiffID        string // public; served to the frontend at runtime
	SessionSecret []byte
	Port          string
}

// Load reads the environment. It reports every problem at once: fixing one missing
// variable only to be told about the next is a miserable way to configure a service.
func Load() (Config, error) {
	cfg := Config{
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		LineChannelID: os.Getenv("LINE_CHANNEL_ID"),
		LiffID:        os.Getenv("LINE_LIFF_ID"),
		SessionSecret: []byte(os.Getenv("SESSION_SECRET")),
		Port:          envOr("PORT", "8080"),
	}

	var problems []string
	if cfg.DatabaseURL == "" {
		problems = append(problems, "DATABASE_URL is not set")
	}
	if cfg.LineChannelID == "" {
		problems = append(problems, "LINE_CHANNEL_ID is not set")
	}
	if cfg.LiffID == "" {
		problems = append(problems, "LINE_LIFF_ID is not set")
	}
	switch {
	case len(cfg.SessionSecret) == 0:
		problems = append(problems, "SESSION_SECRET is not set")
	case len(cfg.SessionSecret) < minSecretLen:
		problems = append(problems, fmt.Sprintf("SESSION_SECRET is %d bytes, need at least %d", len(cfg.SessionSecret), minSecretLen))
	}

	if len(problems) > 0 {
		return Config{}, errors.New("configuration: " + strings.Join(problems, "; "))
	}
	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
