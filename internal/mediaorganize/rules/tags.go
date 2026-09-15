package rules

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

func NormalizeMediaTagValue(key string, value any) any {
	if value == nil || value == "" {
		return nil
	}
	switch key {
	case "frame_rate":
		return normalizeFrameRate(value)
	case "video_codec":
		return normalizeVideoCodec(fmt.Sprint(value))
	case "audio_codec":
		switch v := value.(type) {
		case []string:
			if len(v) > 0 {
				return normalizeAudioCodec(v[0])
			}
		case []any:
			if len(v) > 0 {
				return normalizeAudioCodec(fmt.Sprint(v[0]))
			}
		}
		return normalizeAudioCodec(fmt.Sprint(value))
	case "audio_channels":
		return fmt.Sprint(value)
	default:
		return fmt.Sprint(value)
	}
}

func normalizeFrameRate(value any) string {
	raw := strings.TrimSpace(fmt.Sprint(value))
	if raw == "" {
		return ""
	}
	if strings.HasSuffix(strings.ToLower(raw), "fps") {
		return raw
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return raw
	}
	rounded := int(f + 0.5)
	if absFloat(f-float64(rounded)) < 0.05 {
		return fmt.Sprintf("%dfps", rounded)
	}
	return fmt.Sprintf("%.2ffps", f)
}

func normalizeVideoCodec(v string) string {
	mapping := map[string]string{
		"h.265": "H.265",
		"h265":  "H.265",
		"x265":  "H.265",
		"hevc":  "H.265",
		"h.264": "H.264",
		"h264":  "H.264",
		"x264":  "H.264",
		"avc":   "H.264",
		"av1":   "AV1",
		"vp9":   "VP9",
	}
	raw := strings.ToLower(strings.TrimSpace(v))
	if mapped, ok := mapping[raw]; ok {
		return mapped
	}
	if v != "" && isAlphaToken(v) {
		return strings.ToUpper(v)
	}
	return strings.TrimSpace(v)
}

func normalizeAudioCodec(v string) string {
	v = regexp.MustCompile(`(?i)\d+\.\d+$`).ReplaceAllString(strings.TrimSpace(v), "")
	v = strings.ToLower(strings.TrimSpace(v))
	mapping := map[string]string{
		"dolby digital plus":     "DDP",
		"dolby digital":          "DD",
		"dolby truehd":           "TrueHD",
		"dts-hd master audio":    "DTS-HD MA",
		"dts-hd high resolution": "DTS-HD HRA",
		"dts-hd ma":              "DTS-HD MA",
		"dts-hd hra":             "DTS-HD HRA",
		"dts-hd":                 "DTS-HD",
		"dts-x":                  "DTS:X",
		"dts:x":                  "DTS:X",
		"dts":                    "DTS",
		"aac":                    "AAC",
		"mp3":                    "MP3",
		"flac":                   "FLAC",
		"pcm":                    "PCM",
		"opus":                   "Opus",
		"vorbis":                 "Vorbis",
		"wma":                    "WMA",
		"eac3":                   "DDP",
		"ac3":                    "DD",
		"truehd":                 "TrueHD",
		"ddp":                    "DDP",
		"dd+":                    "DDP",
		"dd":                     "DD",
		"dolby atmos":            "",
		"atmos":                  "",
	}
	if mapped, ok := mapping[v]; ok {
		return mapped
	}
	if v == "" {
		return ""
	}
	return strings.ToUpper(v)
}

func isAlphaToken(v string) bool {
	for _, r := range v {
		if (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
			return false
		}
	}
	return v != ""
}

func absFloat(n float64) float64 {
	if n < 0 {
		return -n
	}
	return n
}

func MergeAlignedMediaTags(parsed ParsedMedia, defaults map[string]any) ParsedMedia {
	m := parsed.ToMap()
	for key, value := range defaults {
		found := false
		for _, field := range MediaTagFields {
			if field == key {
				found = true
				break
			}
		}
		if !found {
			continue
		}
		current := NormalizeMediaTagValue(key, m[key])
		if current != nil && current != "" {
			continue
		}
		if value != nil && value != "" {
			m[key] = value
		}
	}
	return parsedFromMap(m)
}

func BuildMediaInfoTags(parsed ParsedMedia, tagOrder []string) string {
	if len(tagOrder) == 0 {
		return ""
	}
	m := parsed.ToMap()
	parts := make([]string, 0, len(tagOrder))
	for _, key := range tagOrder {
		v := NormalizeMediaTagValue(key, m[key])
		if v == nil || v == "" {
			continue
		}
		s := fmt.Sprint(v)
		if s == "" {
			continue
		}
		if key == "frame_rate" {
			if !containsStr(parts, s) {
				parts = append(parts, s)
			}
			continue
		}
		if !containsStr(parts, s) {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func containsStr(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}
