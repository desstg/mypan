package api

import (
	"runtime"
	"testing"
)

// 2026-10-31：目录选择器的入口校验原来写死「必须 `/` 开头」，而
// `browseDefaultPath()` 在 Windows 上返回的是 `E:\...` —— 自己拒自己，
// 于是 Windows 版（含发出去的 windows-amd64.exe）一点开弹窗就报
// 「请使用绝对路径（以 / 开头）」，列表永远是空的。
//
// 这里钉住「两边都收、相对路径仍然拒」。
func TestIsBrowsableAbsPath(t *testing.T) {
	// 容器里的写法：跑在 Linux 容器里才有意义，但前端那排常用位置是写死的，
	// 所以不论宿主是什么都该放行。
	for _, p := range []string{"/", "/app", "/app/strm", "/mnt/watermark"} {
		if !isBrowsableAbsPath(p) {
			t.Errorf("isBrowsableAbsPath(%q) = false，容器内的绝对路径该放行", p)
		}
	}

	// 相对路径什么时候都不该放行（`filepath.Abs` 会把它们悄悄挂到当前目录上）。
	for _, p := range []string{"", "app", "./app", "../app", "E:"} {
		if isBrowsableAbsPath(p) {
			t.Errorf("isBrowsableAbsPath(%q) = true，相对路径不该放行", p)
		}
	}

	// Windows 卷名写法只在 Windows 上是绝对路径 —— 在 Linux 上 `E:\icons`
	// 就是个文件名，放行它等于允许浏览「当前目录下叫 E:\icons 的东西」。
	winPath := `E:\claude code\LitePan-main\data\icons`
	if got, want := isBrowsableAbsPath(winPath), runtime.GOOS == "windows"; got != want {
		t.Errorf("isBrowsableAbsPath(%q) = %v，在 %s 上期望 %v", winPath, got, runtime.GOOS, want)
	}
}
