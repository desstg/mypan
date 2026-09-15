package strm

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
)

func SignPath(path string, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(path))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func VerifyPath(path, signature string, secret []byte) bool {
	if signature == "" {
		return false
	}
	expected := SignPath(path, secret)
	return subtle.ConstantTimeCompare([]byte(expected), []byte(signature)) == 1
}
