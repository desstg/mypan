package rules

import (
	"encoding/json"
	"strconv"
	"strings"
	"unicode"
)

func SettingBool(value any, defaultVal bool) bool {
	if value == nil {
		return defaultVal
	}
	switch v := value.(type) {
	case bool:
		return v
	case int:
		return v != 0
	case int64:
		return v != 0
	case float64:
		return v != 0
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on", "开启":
			return true
		default:
			return false
		}
	default:
		return defaultVal
	}
}

func AsFirstInt(value any) *int {
	switch v := value.(type) {
	case []int:
		if len(v) == 0 {
			return nil
		}
		n := v[0]
		return &n
	case []any:
		if len(v) == 0 {
			return nil
		}
		return AsFirstInt(v[0])
	case *int:
		return v
	case int:
		n := v
		return &n
	case int64:
		n := int(v)
		return &n
	case float64:
		n := int(v)
		return &n
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return nil
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			return nil
		}
		return &n
	default:
		return nil
	}
}

func asFirstInt(value any) *int {
	return AsFirstInt(value)
}

func FileExtension(name string) string {
	if name == "" || !strings.Contains(name, ".") {
		return ""
	}
	return strings.ToLower(name[strings.LastIndex(name, ".")+1:])
}

func ParseExtensionSet(text string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, part := range strings.Split(text, ";") {
		part = strings.TrimSpace(strings.ToLower(part))
		part = strings.TrimPrefix(part, "*.")
		part = strings.TrimLeft(part, ".")
		if part != "" {
			out[part] = struct{}{}
		}
	}
	return out
}

func ChineseNumberToInt(value string) *int {
	text := strings.TrimSpace(value)
	if text == "" {
		return nil
	}
	if n, err := strconv.Atoi(text); err == nil {
		return &n
	}
	digitMap := map[rune]int{
		'零': 0, '〇': 0, '一': 1, '二': 2, '两': 2, '三': 3, '四': 4,
		'五': 5, '六': 6, '七': 7, '八': 8, '九': 9,
	}
	if n, ok := digitMap[[]rune(text)[0]]; ok && len([]rune(text)) == 1 {
		return &n
	}
	if idx := strings.Index(text, "百"); idx >= 0 {
		left := text[:idx]
		right := text[idx+len("百"):]
		hundreds := 1
		if left != "" {
			if v, ok := digitMap[[]rune(left)[0]]; ok {
				hundreds = v
			} else {
				return nil
			}
		}
		tail := 0
		if right != "" {
			if t := ChineseNumberToInt(right); t != nil {
				tail = *t
			}
		}
		n := hundreds*100 + tail
		return &n
	}
	if idx := strings.Index(text, "十"); idx >= 0 {
		left := text[:idx]
		right := text[idx+len("十"):]
		tens := 1
		if left != "" {
			if v, ok := digitMap[[]rune(left)[0]]; ok {
				tens = v
			} else {
				return nil
			}
		}
		ones := 0
		if right != "" {
			if v, ok := digitMap[[]rune(right)[0]]; ok {
				ones = v
			} else {
				return nil
			}
		}
		n := tens*10 + ones
		return &n
	}
	return nil
}

func ParseEpisodeNumber(value any) *int {
	if value == nil {
		return nil
	}
	raw := strings.TrimSpace(toString(value))
	if raw == "" {
		return nil
	}
	if n, err := strconv.Atoi(raw); err == nil {
		return &n
	}
	return ChineseNumberToInt(raw)
}

func toString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case json.Number:
		return t.String()
	default:
		return ""
	}
}

func toLowerASCII(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

func trimChars(s string, cutset string) string {
	return strings.Trim(s, cutset+" ")
}

func containsHan(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

func splitStemExt(name string) (string, string) {
	if name == "" || !strings.Contains(name, ".") {
		return name, ""
	}
	i := strings.LastIndex(name, ".")
	return name[:i], name[i+1:]
}
