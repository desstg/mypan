package logx

type Module string

const (
	ModuleSystem       Module = "system"
	ModuleDriver       Module = "driver"
	ModuleAPI          Module = "api"
	ModuleWeb          Module = "web"
	ModuleDriverSystem Module = "driver_system"
	ModuleCache        Module = "cache"
	ModuleDatabase     Module = "database"
	ModuleAuth         Module = "auth"
	ModuleFileOp       Module = "file_op"
	ModuleConfig       Module = "config"
	ModuleWebDAV       Module = "webdav"
)

func (m Module) String() string {
	if m == "" {
		return string(ModuleSystem)
	}
	return string(m)
}

func (m Module) Group() (id, name, color string) {
	switch m {
	case ModuleDriver, ModuleDriverSystem, ModuleAuth:
		return "driver", "驱动", "#4CAF50"
	case ModuleFileOp, ModuleWebDAV:
		return "file", "文件", "#009688"
	case ModuleCache:
		return "cache", "缓存", "#795548"
	case ModuleAPI, ModuleWeb:
		return "interface", "接口", "#FF9800"
	case ModuleSystem, ModuleConfig, ModuleDatabase:
		return "system", "系统", "#2196F3"
	default:
		return string(m), string(m), "#64748B"
	}
}

// ModulesInGroup 返回分组下的原始 module 值列表。
func ModulesInGroup(group string) []string {
	switch group {
	case "driver":
		return []string{string(ModuleDriver), string(ModuleDriverSystem), string(ModuleAuth)}
	case "file":
		return []string{string(ModuleFileOp), string(ModuleWebDAV)}
	case "cache":
		return []string{string(ModuleCache)}
	case "interface":
		return []string{string(ModuleAPI), string(ModuleWeb)}
	case "system":
		return []string{string(ModuleSystem), string(ModuleConfig), string(ModuleDatabase)}
	default:
		return []string{group}
	}
}
