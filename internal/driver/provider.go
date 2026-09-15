package driver

import "context"

// Provider 是解析账号驱动实例的最小能力，由 Manager 实现。
type Provider interface {
	Get(ctx context.Context, accountID int64) (Driver, error)
}

// TransportResetter 可选能力：网络故障后关闭 idle 连接，不丢弃实例、不影响认证调度。
type TransportResetter interface {
	ResetTransport(ctx context.Context, accountID int64)
}
