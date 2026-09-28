package strmscrape

import (
	"errors"

	"litepan/internal/domain"
)

var errRootMetaIncomplete = errors.New("根目录缺少 nfo 或海报，无法设为完结")

// errJavWallSettingsUnavailable 表示服务没接设置（只在测试里出现）。
var errJavWallSettingsUnavailable = domain.Errorf(domain.CodeInternal, "设置服务不可用，无法保存番号墙隐藏目录")
