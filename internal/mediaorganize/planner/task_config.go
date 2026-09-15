package planner

import (
	"fmt"
	"strings"

	"litepan/internal/mediaorganize/rules"
)

func TaskConfigFromMap(cfg map[string]any) TaskConfig {
	if cfg == nil {
		return TaskConfig{}
	}
	return TaskConfig{
		TargetDirectoryID:    strings.TrimSpace(strMap(cfg, "target_directory_id")),
		TargetRootID:         strings.TrimSpace(strMap(cfg, "target_root_id")),
		ActionType:           strings.TrimSpace(strMap(cfg, "action_type")),
		MediaType:            strings.TrimSpace(strMap(cfg, "media_type")),
		RenameMarker:         strings.TrimSpace(strMap(cfg, "rename_marker")),
		UseTMDB:              rules.SettingBool(cfg["use_tmdb"], false),
		OverwriteExisting:    rules.SettingBool(cfg["overwrite_existing"], false),
		Recursive:            rules.SettingBool(cfg["recursive"], false),
		SeasonFolderTemplate: strings.TrimSpace(strMap(cfg, "season_folder_template")),
		FileExtensions:       strings.TrimSpace(strMap(cfg, "file_extensions")),
		MetadataExtensions:   strings.TrimSpace(strMap(cfg, "metadata_extensions")),
	}
}

func strMap(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}
