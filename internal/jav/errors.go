package jav

import (
	"errors"

	"litepan/internal/domain"
)

// upstreamErr 把上游客户端（JAVDB / JAVBUS / 媒体服务器）的错误翻译成领域错误。
//
// 不翻译的后果实测过：一条请求在上游 404（比如影片 id 不存在）时，错误原样
// 冒到 API 层，`writeErr` 认不出它是领域错误，于是统一按内部错误处理 ——
// 用户看到的是「服务内部错误」500。
//
// 那句话的问题不是不准确，是**不可行动**：它把「上游说这个 id 不存在」
// 和「数据库炸了」显示成同一句话，用户既不知道是自己填错了还是系统坏了，
// 也不知道该等一等还是去改配置。
//
// 已经是领域错误的原样返回，避免把「没有可用的推送目标」这类本来就准确的
// 文案包成「驱动错误：没有可用的推送目标」。
func upstreamErr(err error) error {
	if err == nil {
		return nil
	}
	var ae *domain.AppError
	if errors.As(err, &ae) {
		return err
	}
	return domain.Errorf(domain.CodeDriverError, "%s", err.Error())
}
