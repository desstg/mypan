package fusemount

import (
	"os"
	"strings"
)

const (
	KeyMountRoot         = "fuse_mount_root"
	KeyEnabled           = "fuse_enabled"
	DefaultMountRoot     = "/app/mounts"
	DefaultEntryTimeoutS = 30
	DefaultAttrTimeoutS  = 3
)

// MountRoot 是 FUSE 挂载根目录。
// 取值优先级：界面设置（ApplyConfiguredMountRoot 启动时注入）> LITEPAN_MOUNT_ROOT > 默认值。
// 修改后需重启程序生效。
var MountRoot = resolveMountRootFromEnv()

func resolveMountRootFromEnv() string {
	if v := strings.TrimSpace(os.Getenv("LITEPAN_MOUNT_ROOT")); v != "" {
		return v
	}
	return DefaultMountRoot
}
