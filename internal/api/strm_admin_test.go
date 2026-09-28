package api

import (
	"testing"

	"litepan/internal/settings"
)

// TestMapStrmSettingAliasesCoversJavWallHiddenDirs 别名表漏一行的表现是**静默的**：
// settings.Update 会以「未知设置项」拒绝，界面上看起来只是「保存没生效」
// （这正是本项目反复踩过的那一类）。这里把这几个借 STRM 设置页读写的键都钉住。
func TestMapStrmSettingAliasesCoversJavWallHiddenDirs(t *testing.T) {
	in := map[string]string{
		"jav_wall_hidden_dirs": `["未匹配"]`,
		"jav_metadata_items":   `{"nfo":true}`,
	}
	mapStrmSettingAliases(in)

	for alias, key := range map[string]string{
		"jav_wall_hidden_dirs": settings.KeyStrmJavWallHiddenDirs,
		"jav_metadata_items":   settings.KeyStrmJavMetaItems,
	} {
		if _, still := in[alias]; still {
			t.Errorf("别名 %q 没有被换掉（前端传的是别名，服务端只认 settings key）", alias)
		}
		if _, ok := in[key]; !ok {
			t.Errorf("%q 没有映射到 %q —— Update 会报「未知设置项」，界面上只是「保存没生效」", alias, key)
		}
	}
	// 没传的键不该被凭空塞进来（否则一次局部保存会把这几个设置清空）。
	extra := map[string]string{}
	mapStrmSettingAliases(extra)
	if len(extra) != 0 {
		t.Errorf("没传任何别名时不该改动 map：%v", extra)
	}
}
