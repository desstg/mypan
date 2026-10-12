package pansou

import "testing"

// 驱动类型 → 盘搜类型的映射。
//
// 判错的后果很具体：选了 123 账号却搜出 115 的结果，用户一屏按钮全是灰的。
// 而盘搜的类型名与驱动类型**不是同一套词表**（移动叫 mobile、天翼叫 tianyi），
// 所以这张表必须逐条钉住。
func TestDriverCloudType(t *testing.T) {
	cases := map[string]string{
		"115_open":   "115",
		"123_open":   "123",
		"139_cloud":  "mobile",
		"189_cloud":  "tianyi",
		"baidu_open": "baidu",
		"guangya":    "guangya",
		"quark":      "quark",
		// 大小写与空格也要容错：驱动类型是从库里读的，手工改过库会有脏值。
		"  QUARK  ": "quark",
		// 没有对应盘搜类型的驱动返回空串。
		"onedrive": "",
		"webdav":   "",
		"localfs":  "",
		"openlist": "",
		"":         "",
	}
	for driverType, want := range cases {
		if got := DriverCloudType(driverType); got != want {
			t.Errorf("DriverCloudType(%q) = %q，期望 %q", driverType, got, want)
		}
	}
}

// 「按用户选的目标网盘决定搜什么」：永远是「磁力 + 该网盘那一类」。
func TestCloudTypesForDriver(t *testing.T) {
	cases := map[string]string{
		"115_open":   "magnet,115",
		"123_open":   "magnet,123",
		"quark":      "magnet,quark",
		"guangya":    "magnet,guangya",
		"189_cloud":  "magnet,tianyi",
		"139_cloud":  "magnet,mobile",
		"baidu_open": "magnet,baidu",
		// 没有对应类型的驱动只搜磁力。
		"onedrive": "magnet",
		"webdav":   "magnet",
	}
	for driverType, want := range cases {
		if got := CloudTypesForDriver(driverType); got != want {
			t.Errorf("CloudTypesForDriver(%q) = %q，期望 %q", driverType, got, want)
		}
	}
}

// 合并是**追加**不是覆盖：基础的「磁力 + 目标网盘那一类」永远要在。
//
// 覆盖掉的话，搜出来的东西没有对应的存法 —— 表现是匹配历史堆满「暂不支持投递」。
func TestMergeCloudTypesKeepsBaseAndDedupes(t *testing.T) {
	cases := []struct {
		base, extra, want string
	}{
		{"magnet,quark", "", "magnet,quark"},
		{"magnet,quark", "baidu", "magnet,quark,baidu"},
		// 重复的（含大小写不同）只留一个。
		{"magnet,quark", "magnet,QUARK,115", "magnet,quark,115"},
		// 两边都空时回落内置默认。
		{"", "", DefaultCloudTypes},
		// 基础为空、用户填了：只用用户填的（不额外塞磁力 —— 那是调用方的判断）。
		{"", "baidu", "baidu"},
	}
	for _, tc := range cases {
		if got := MergeCloudTypes(tc.base, tc.extra); got != tc.want {
			t.Errorf("MergeCloudTypes(%q, %q) = %q，期望 %q", tc.base, tc.extra, got, tc.want)
		}
	}
}
