package aiorganize

import "errors"

// ErrAIDisabled 表示「AI 没开或没配」。
//
// 它是**安静跳过**的信号，不是故障：调用方（番号刮削的翻译）据此什么也不做，
// 不要把「用户没配 AI」当成错误弹给用户。
var ErrAIDisabled = errors.New("AI 未启用或未配置")
