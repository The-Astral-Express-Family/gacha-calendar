// Package config loads runtime settings from the environment with sane
// defaults for local development.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	// HTTP listen address. Default 127.0.0.1:8787 (nginx proxies to it).
	Addr string
	// SQLite database file. Default ./data/gacha.db.
	DBPath string
	// Directory where collected cover images are written. Default ./data/covers.
	CoversDir string
	// Interval between full collection cycles. Default 6h.
	RefreshInterval time.Duration
	// Delay between collection cycles at startup (let migrations settle).
	StartupDelay time.Duration
	// Bearer token protecting /api/admin/*. Empty disables admin routes.
	AdminToken string
	// Public base URL used in API self-references (optional).
	PublicURL string
	// Collect at startup if the last cycle is older than RefreshInterval/2.
	CollectOnStart bool
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return parsed, nil
}

func envBool(key string, fallback bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

// Load builds a Config from the environment.
func Load() (Config, error) {
	refreshInterval, err := envDuration("REFRESH_INTERVAL", 6*time.Hour)
	if err != nil {
		return Config{}, err
	}
	startupDelay, err := envDuration("STARTUP_DELAY", 5*time.Second)
	if err != nil {
		return Config{}, err
	}
	dataDir := env("DATA_DIR", "data")
	return Config{
		Addr:            env("ADDR", "127.0.0.1:8787"),
		DBPath:          env("DB_PATH", dataDir+"/gacha.db"),
		CoversDir:       env("COVERS_DIR", dataDir+"/covers"),
		RefreshInterval: refreshInterval,
		StartupDelay:    startupDelay,
		AdminToken:      os.Getenv("ADMIN_TOKEN"),
		PublicURL:       os.Getenv("PUBLIC_URL"),
		CollectOnStart:  envBool("COLLECT_ON_START", true),
	}, nil
}
