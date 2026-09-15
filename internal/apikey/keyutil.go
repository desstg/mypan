package apikey

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

const (
	PrefixAPI  = "lpk_api_"
	PrefixStrm = "lpk_strm_"
)

func Hash(raw string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(raw)))
	return hex.EncodeToString(sum[:])
}

func NewRawKey(prefix string) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

func KeyPreview(prefix, suffix string) string {
	return fmt.Sprintf("%s••••••••••%s", prefix, suffix)
}

func StrmKeyPreview(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return "-"
	}
	prefix, suffix := PrefixSuffix(token)
	return KeyPreview(prefix, suffix)
}

func PrefixSuffix(raw string) (prefix, suffix string) {
	raw = strings.TrimSpace(raw)
	if len(raw) <= 12 {
		return raw, raw
	}
	if len(raw) <= 6 {
		return raw, raw
	}
	return raw[:12], raw[len(raw)-6:]
}

func ParseExpiryDays(days *int) (time.Time, error) {
	if days == nil || *days <= 0 {
		return time.Time{}, nil
	}
	if *days > 3650 {
		return time.Time{}, fmt.Errorf("有效期不能超过3650天")
	}
	return time.Now().UTC().Add(time.Duration(*days) * 24 * time.Hour), nil
}
