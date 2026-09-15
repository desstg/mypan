// Package jsonvalue 提供不依赖业务模块的 JSON 基础值类型。
package jsonvalue

import (
	"encoding/json"
	"strings"
)

// FlexibleString 兼容 JSON 字符串、数字和 null，并统一以字符串保存。
type FlexibleString string

func (f *FlexibleString) UnmarshalJSON(raw []byte) error {
	if string(raw) == "null" {
		*f = ""
		return nil
	}
	if len(raw) > 0 && raw[0] == '"' {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		*f = FlexibleString(value)
		return nil
	}
	var value json.Number
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	*f = FlexibleString(value.String())
	return nil
}

func (f FlexibleString) String() string {
	return strings.TrimSpace(string(f))
}
