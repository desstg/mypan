//go:build !fuse

package fuse

import (
	"context"

	"litepan/internal/domain"
)

type stubManager struct{}

func NewManager(Deps) Manager { return stubManager{} }

func (stubManager) Mount(context.Context, *domain.FuseMount) error {
	return domain.Errorf(domain.CodeNotImplement, "当前程序未编译 FUSE 支持，请使用 -tags fuse 构建")
}

func (stubManager) Unmount(context.Context, int64) error { return nil }
