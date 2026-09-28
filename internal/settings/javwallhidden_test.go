package settings

import "testing"

// TestJavWallHiddenDirsCodec 番号墙隐藏名单的存取。
//
// 重点在两件容易做错的事：
//  1. **「一个都不藏」要存得住** —— `[]` 是有效值，而**空串必须只能是「没存过」**
//     （读侧要靠这个区别决定「动态回落兜底目录名」还是「就按用户勾的来」）。
//     写成「;」分隔列表的话这两件事分不开，用户取消全选后「未匹配」会自己冒回来。
//  2. **顺序无关** —— 前端拿前后两个字符串比「有没有改动」，顺序一变就会一直误报未保存。
func TestJavWallHiddenDirsCodec(t *testing.T) {
	// 编码：排序 + 去重 + 去空白
	if got := EncodeJavWallHiddenDirs([]string{" 未匹配 ", "有码", "未匹配", ""}); got != `["有码","未匹配"]` {
		t.Errorf("编码应当去空、去重、排序，got %s", got)
	}
	// 空列表编码成 `[]`（合法值），**绝不是空串**
	if got := EncodeJavWallHiddenDirs(nil); got != "[]" {
		t.Errorf("空名单应当编码成 []，got %q", got)
	}

	// 空串 / 坏 JSON / 空白 = 没存过
	for _, raw := range []string{"", "   ", "{", "not json", `{"a":1}`} {
		if _, ok := ParseJavWallHiddenDirs(raw); ok {
			t.Errorf("%q 应当算「没存过」", raw)
		}
	}
	// `[]` 算存过（= 一个都不藏），这是本测试存在的理由
	dirs, ok := ParseJavWallHiddenDirs("[]")
	if !ok {
		t.Fatal("`[]` 必须算「存过」—— 否则用户取消全选后兜底目录会自己冒回来")
	}
	if len(dirs) != 0 {
		t.Errorf("`[]` 应当是空名单，got %v", dirs)
	}
	// 正常值
	dirs, ok = ParseJavWallHiddenDirs(`["未匹配"," 有码 ","","未匹配"]`)
	if !ok || len(dirs) != 2 || dirs[0] != "有码" || dirs[1] != "未匹配" {
		t.Errorf("解析不干净：%v ok=%v", dirs, ok)
	}

	// 写入路径的收口：坏值一律留空串（= 继续走动态回落），**不能**填默认值 ——
	// 一填就把兜底目录名固化成一个死名字了（用户之后改名会跟不上）。
	for _, raw := range []string{"", "{", "null"} {
		if got := normalizeJavWallHiddenDirsValue(raw); got != "" {
			t.Errorf("%q 收口后应当留空串，got %q", raw, got)
		}
	}
	if got := normalizeJavWallHiddenDirsValue(`["b","a"]`); got != `["a","b"]` {
		t.Errorf("收口后应当是规范串，got %s", got)
	}
	if got := normalizeJavWallHiddenDirsValue("[]"); got != "[]" {
		t.Errorf("空名单收口后必须还是 []（不能塌成空串），got %q", got)
	}
}

// TestJavWallHiddenDirsSettingIsRegistered 新键必须在注册表里 —— 否则
// settings.Update 会以「未知设置项」拒绝写入，而界面上看起来只是「保存没生效」。
func TestJavWallHiddenDirsSettingIsRegistered(t *testing.T) {
	var spec *Spec
	for i := range defaultSpecs() {
		if defaultSpecs()[i].Key == KeyStrmJavWallHiddenDirs {
			spec = &defaultSpecs()[i]
			break
		}
	}
	if spec == nil {
		t.Fatalf("%s 没有登记进 defaultSpecs()", KeyStrmJavWallHiddenDirs)
	}
	if !spec.Hidden {
		t.Error("它由番号墙与 STRM 设置页读写，不该在通用设置表单里露出一个裸 JSON 框")
	}
	// Default 必须是空串：非空会被当成「用户存过」，读侧就不再跟随分类规则的改名了。
	if spec.Default != "" {
		t.Errorf("Default 必须是空串（= 没存过），got %q", spec.Default)
	}
}
