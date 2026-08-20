package config

import (
	"encoding/hex"
	"fmt"
	"os"
)

// Config holds the backend's runtime configuration, loaded entirely from
// environment variables (no config file — keeps deployment/systemd wiring simple).
type Config struct {
	DatabaseURL string
	ListenAddr  string
	EncKey      []byte // 32-byte AES-256 key
}

func Load() (*Config, error) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}

	listen := os.Getenv("LISTEN_ADDR")
	if listen == "" {
		listen = "127.0.0.1:8090"
	}

	keyHex := os.Getenv("SERVERGO_ENC_KEY")
	if keyHex == "" {
		return nil, fmt.Errorf("SERVERGO_ENC_KEY is required (32-byte key, hex-encoded — e.g. `openssl rand -hex 32`)")
	}
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return nil, fmt.Errorf("SERVERGO_ENC_KEY must be hex-encoded: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("SERVERGO_ENC_KEY must decode to 32 bytes, got %d", len(key))
	}

	return &Config{DatabaseURL: dbURL, ListenAddr: listen, EncKey: key}, nil
}
