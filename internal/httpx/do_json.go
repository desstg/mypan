package httpx

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

func DoJSON(ctx context.Context, client *http.Client, method, rawURL string, query url.Values, body any, headers map[string]string, readLimit int64) (*http.Response, []byte, error) {
	if client == nil {
		return nil, nil, fmt.Errorf("httpx: nil client")
	}
	req, err := NewJSONRequest(ctx, method, rawURL, query, body)
	if err != nil {
		return nil, nil, err
	}
	if len(headers) > 0 {
		SetHeaders(req, headers)
	}
	return Execute(client, req, readLimit)
}
