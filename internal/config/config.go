// Package config loads minimal, zero-dependency runtime configuration.
// Every field has a sane default so the service runs without any config file.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

// Config holds the small set of tunable options for the service.
type Config struct {
	Listen      string // e.g. ":8290"
	DataDir     string // SQLite db, per-user space and cache all live here
	ShareRoot   string // host-mounted shared/public disk
	Secret      string // JWT + share signing secret (auto-generated if empty)
	Log         Log
	MaxUploadMB int64
	CacheMB     int64
}

// Log configures backend logging. File is optional; when empty logs go to stdout.
type Log struct {
	Level string
	File  string
}

// FromEnv builds a Config from environment variables, applying defaults.
func FromEnv() *Config {
	cfg := &Config{
		Listen:      getenv("LISTEN", ":8290"),
		DataDir:     getenv("DATA_DIR", "/app/data"),
		ShareRoot:   getenv("SHARE_ROOT", "/share"),
		Log:         Log{Level: getenv("LOG_LEVEL", "info"), File: getenv("LOG_FILE", "")},
		MaxUploadMB: 10240, // 10 GiB default
		CacheMB:     2048,  // 2 GiB cache budget default
	}
	cfg.Secret = getenv("SECRET", "")
	if cfg.Secret == "" {
		// Generate and persist a secret so restarts don't invalidate tokens/shares.
		cfg.Secret = loadOrCreateSecret(filepath.Join(cfg.DataDir, "secret"))
	}
	return cfg
}

func getenv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func loadOrCreateSecret(path string) string {
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
		return string(b)
	}
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	secret := hex.EncodeToString(buf)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
		_ = os.WriteFile(path, []byte(secret), 0o600)
	}
	return secret
}
