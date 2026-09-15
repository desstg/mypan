package domain

import (
	"errors"
	"fmt"
)

// ErrorCode 是跨层稳定的结构化错误码，HTTP 中间件据此统一映射状态码与文案。
type ErrorCode string

const (
	CodeAuthExpired       ErrorCode = "AUTH_EXPIRED"
	CodeAdminAuthRequired ErrorCode = "ADMIN_AUTH_REQUIRED"
	CodeRateLimited       ErrorCode = "RATE_LIMITED"
	CodeNotFound          ErrorCode = "NOT_FOUND"
	CodeDriverError       ErrorCode = "DRIVER_ERROR"
	CodeValidation        ErrorCode = "VALIDATION"
	CodeNotImplement      ErrorCode = "NOT_IMPLEMENT"
	CodePermissionDenied  ErrorCode = "PERMISSION_DENIED"
	CodeInternal          ErrorCode = "INTERNAL"
)

type codeMeta struct {
	http int
	msg  string
}

var codeTable = map[ErrorCode]codeMeta{
	CodeAuthExpired:       {401, "账号认证已失效，请重新授权"},
	CodeAdminAuthRequired: {401, "需要管理员权限"},
	CodeRateLimited:       {429, "请求过于频繁，请稍后重试"},
	CodeNotFound:          {404, "文件不存在"},
	CodeDriverError:       {502, "网盘服务异常"},
	CodeValidation:        {400, "参数错误"},
	CodeNotImplement:      {501, "该操作不支持"},
	CodePermissionDenied:  {403, "权限不足"},
	CodeInternal:          {500, "服务内部错误"},
}

// AppError 是带错误码的应用错误，可被 errors.As / errors.AsType 提取。
type AppError struct {
	Code    ErrorCode
	Message string
	Details map[string]any
	Err     error
}

// WithDetails 附加结构化细节（用于 API 响应 details 字段），返回自身便于链式调用。
func (e *AppError) WithDetails(d map[string]any) *AppError {
	e.Details = d
	return e
}

func (e *AppError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *AppError) Unwrap() error { return e.Err }

// HTTPStatus 返回错误码对应的 HTTP 状态码。
func (e *AppError) HTTPStatus() int {
	if m, ok := codeTable[e.Code]; ok {
		return m.http
	}
	return 500
}

func newAppErr(code ErrorCode, msg string, err error) *AppError {
	if msg == "" {
		if m, ok := codeTable[code]; ok {
			msg = m.msg
		}
	}
	return &AppError{Code: code, Message: msg, Err: err}
}

// Errf 构造带默认中文文案的错误。
func Errf(code ErrorCode) *AppError { return newAppErr(code, "", nil) }

// Errorf 构造自定义文案的错误。
func Errorf(code ErrorCode, format string, a ...any) *AppError {
	return newAppErr(code, fmt.Sprintf(format, a...), nil)
}

// Wrap 用错误码包裹底层错误，保留可 Unwrap 的链路。
func Wrap(code ErrorCode, err error) *AppError { return newAppErr(code, "", err) }

// AsAppError 从错误链提取 AppError（Go 1.26 亦可用 errors.AsType[*AppError]）。
func AsAppError(err error) (*AppError, bool) {
	var ae *AppError
	if errors.As(err, &ae) {
		return ae, true
	}
	return nil, false
}
