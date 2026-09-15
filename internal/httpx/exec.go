package httpx

import "net/http"

func Execute(client *http.Client, req *http.Request, readLimit int64) (*http.Response, []byte, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	data, err := ReadLimited(resp.Body, readLimit)
	resp.Body.Close()
	if err != nil {
		return resp, nil, err
	}
	return resp, data, nil
}
