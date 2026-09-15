package logx

import (
	"log/slog"
	"strings"
)

const (
	LevelDebug = 10
	LevelInfo  = 20
	LevelWarn  = 30
	LevelError = 40
)

// ParseLevel 解析配置字符串为 slog 级别。
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func LevelToInt(l slog.Level) int {
	switch {
	case l >= slog.LevelError:
		return LevelError
	case l >= slog.LevelWarn:
		return LevelWarn
	case l >= slog.LevelInfo:
		return LevelInfo
	default:
		return LevelDebug
	}
}

// LevelName 返回大写级别名。
func LevelName(l int) string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelWarn:
		return "WARNING"
	case LevelError:
		return "ERROR"
	default:
		return "INFO"
	}
}

// LevelEmoji 返回级别 emoji（前端展示用）。
func LevelEmoji(l int) string {
	switch l {
	case LevelDebug:
		return "🔍"
	case LevelWarn:
		return "⚠️"
	case LevelError:
		return "❌"
	default:
		return "ℹ️"
	}
}
