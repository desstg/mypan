package strm

import (
	"fmt"
	"net/url"
	"strconv"
)

func BuildPlayPath(accountID int64, fileID, fileName, token string, signEnabled bool, secret []byte) string {
	account := strconv.FormatInt(accountID, 10)
	escapedName := url.PathEscape(fileName)
	path := fmt.Sprintf("/api/strm/play/%s/%s/t/%s/n/%s", account, EncodeFileKey(fileID), token, escapedName)
	if signEnabled {
		path += "/s/" + SignPath(path, secret)
	}
	return path
}

func BuildPlayURL(baseURL string, accountID int64, fileID, fileName, token string, signEnabled bool, secret []byte) string {
	path := BuildPlayPath(accountID, fileID, fileName, token, signEnabled, secret)
	base := NormalizeBaseURL(baseURL)
	if base == "" {
		return path
	}
	return base + path
}
