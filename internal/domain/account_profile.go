package domain

import "time"

// AccountProfile 是驱动返回并由服务端缓存的账号公开资料。
// UserID 仅供服务端做账号关联，不会下发给浏览器。
type AccountProfile struct {
	AccountID   int64
	UserID      string
	Nickname    string
	Membership  string
	UsedBytes   int64
	TotalBytes  int64
	AttemptDate string
	RefreshedAt time.Time
}
