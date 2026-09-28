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

// TestMapStrmSettingAliasesKeepsJavWatermarkKeys 钉住水印那四个键的**自指别名**陷阱。
//
// 那四个键（`jav_watermark_*`）的别名**与 settings key 逐字相同**（它们本来就是
// jav 模块的键，只是借 STRM 设置页给个入口）。别名表里保留这几行是**故意的**，
// 但循环里必须加 `k != v` 的守卫 —— 否则 `delete(in, k)` 会在 `in[v] = raw`
// 之后把刚写进去的那一项删掉。
//
// 踩过的表现：水印开关点开、保存、界面又跳回关（后端其实收到了值，只是被自己删了，
// 于是这 4 个键根本进不了 Update，库里一行都没写）。而且**日志也看不出来** ——
// 「系统设置已更新」只列真正写进去的那 14 个键。
func TestMapStrmSettingAliasesKeepsJavWatermarkKeys(t *testing.T) {
	in := map[string]string{
		"jav_watermark_enabled": "true",
		"jav_watermark_scale":   "18",
		"jav_watermark_margin":  "2",
		"jav_watermark_dir":     "",
	}
	mapStrmSettingAliases(in)

	for key, want := range map[string]string{
		settings.KeyJavWatermarkEnabled: "true",
		settings.KeyJavWatermarkScale:   "18",
		settings.KeyJavWatermarkMargin:  "2",
		settings.KeyJavWatermarkDir:     "",
	} {
		got, ok := in[key]
		if !ok {
			t.Errorf("%q 被自指别名删掉了 —— 保存后界面会跳回原值", key)
			continue
		}
		if got != want {
			t.Errorf("%q = %q，期望 %q", key, got, want)
		}
	}
	if len(in) != 4 {
		t.Errorf("应当只剩这 4 个键，got %v", in)
	}
}
