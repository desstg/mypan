package httpx

import "encoding/json"

type DataEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func ParseDataEnvelope(raw []byte, out any) (DataEnvelope, error) {
	var env DataEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return env, err
	}
	if out != nil && len(env.Data) > 0 && string(env.Data) != "null" {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return env, err
		}
	}
	return env, nil
}
