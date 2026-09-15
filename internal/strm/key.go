package strm

import (
	"encoding/base64"
)

func EncodeFileKey(fileID string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fileID))
}

func DecodeFileKey(fileKey string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(fileKey)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
