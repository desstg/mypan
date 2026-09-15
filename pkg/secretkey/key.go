package secretkey

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

func LoadOrCreate(dataDir string) ([]byte, error) {
	if v := os.Getenv("LITEPAN_SECRET_KEY"); v != "" {
		return []byte(v), nil
	}
	path := filepath.Join(dataDir, "secret.key")
	if raw, err := os.ReadFile(path); err == nil {
		key := []byte(string(raw))
		if len(key) >= 16 {
			return key, nil
		}
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("generate secret key: %w", err)
	}
	encoded := []byte(hex.EncodeToString(buf))
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		return nil, fmt.Errorf("persist secret key: %w", err)
	}
	return encoded, nil
}
