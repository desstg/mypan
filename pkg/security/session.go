package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrBadSessionSignature = errors.New("bad session signature")
	ErrSessionExpired      = errors.New("session expired")
)

type TimedSerializer struct {
	secret []byte
}

func NewTimedSerializer(secret []byte) *TimedSerializer {
	return &TimedSerializer{secret: secret}
}

func (s *TimedSerializer) Dumps(payload string) (string, error) {
	wrapped := map[string]any{
		"d": payload,
		"t": time.Now().Unix(),
	}
	raw, err := json.Marshal(wrapped)
	if err != nil {
		return "", err
	}
	encodedPayload := b64Encode(raw)
	sig := hmac.New(sha256.New, s.secret)
	_, _ = sig.Write([]byte(encodedPayload))
	return encodedPayload + "." + b64Encode(sig.Sum(nil)), nil
}

func (s *TimedSerializer) Loads(token string, maxAge int) (string, error) {
	if token == "" {
		return "", ErrBadSessionSignature
	}
	i := len(token) - 1
	for i >= 0 && token[i] != '.' {
		i--
	}
	if i <= 0 || i >= len(token)-1 {
		return "", ErrBadSessionSignature
	}
	encodedPayload := token[:i]
	encodedSig := token[i+1:]

	expectedSig := hmac.New(sha256.New, s.secret)
	_, _ = expectedSig.Write([]byte(encodedPayload))
	expected := b64Encode(expectedSig.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(encodedSig), []byte(expected)) != 1 {
		return "", ErrBadSessionSignature
	}

	raw, err := b64Decode(encodedPayload)
	if err != nil {
		return "", ErrBadSessionSignature
	}
	var wrapped struct {
		D string `json:"d"`
		T int64  `json:"t"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return "", ErrBadSessionSignature
	}
	if maxAge > 0 && time.Now().Unix()-wrapped.T > int64(maxAge) {
		return "", ErrSessionExpired
	}
	return wrapped.D, nil
}

func b64Encode(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

func b64Decode(data string) ([]byte, error) {
	pad := (4 - len(data)%4) % 4
	if pad > 0 {
		data += strings.Repeat("=", pad)
	}
	return base64.URLEncoding.DecodeString(data)
}
