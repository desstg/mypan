package strm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFillNFOPlotOnRealSample 用**用户库里那份真实的坏样本**跑一遍。
//
// # 为什么值得单独一条
//
// 上面几条用例的 nfo 都是 `emby.BuildNFO` 现造的 —— 形状对了，但那是「我以为
// 生成器会写出来的样子」。这个 bug 恰恰是**我以为的形状与实际不符**造成的：
// 我以为 outline 那行是 `  <outline>` 结尾换行，实际是
// `  <outline><![CDATA[发行日期: …]]></outline>`，于是带换行的匹配串永远打不中。
//
// testdata 里这两份是 2026-10-07 从 `strm/116/国产/RS034/` 原样拷出来的：
// 侧车里躺着 128 字简介，nfo 里连 `<plot>` 都没有。拿真样本跑，才不会再被
// 「我以为」骗一次。
func TestFillNFOPlotOnRealSample(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("testdata", "RS034.json"))
	if err != nil {
		t.Skip("缺少 testdata/RS034.json")
	}
	nfoSrc, err := os.ReadFile(filepath.Join("testdata", "RS034.nfo"))
	if err != nil {
		t.Skip("缺少 testdata/RS034.nfo")
	}
	if strings.Contains(string(nfoSrc), "<plot>") {
		t.Fatal("样本 nfo 不该有 <plot>（它就是这个 bug 的现场）")
	}
	var doc map[string]any
	if err := json.Unmarshal(src, &doc); err != nil {
		t.Fatal(err)
	}
	summary, _ := doc["summary"].(string)
	if strings.TrimSpace(summary) == "" {
		t.Fatal("样本侧车里该有简介")
	}

	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "RS034.json")
	nfoPath := filepath.Join(dir, "RS034.nfo")
	if err := os.WriteFile(jsonPath, src, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nfoPath, nfoSrc, 0o644); err != nil {
		t.Fatal(err)
	}

	// 走真正的回写入口（字段值与侧车一致 → json 不该被改，nfo 该被修）。
	if _, err := writeFieldsIntoFile(jsonPath, map[string]any{"summary": summary}); err != nil {
		t.Fatal(err)
	}

	after, _ := os.ReadFile(nfoPath)
	text := string(after)
	if !strings.Contains(text, "<plot><![CDATA[") {
		t.Fatalf("真样本的简介仍然没补进 nfo：\n%s", text)
	}
	if !strings.Contains(text, strings.TrimSpace(summary)[:24]) {
		t.Fatalf("补进去的不是那份简介：\n%s", text)
	}
	// 顺序：<plot> 在 <outline> 之前（与生成器一致）。
	if strings.Index(text, "<plot>") > strings.Index(text, "<outline") {
		t.Fatalf("<plot> 该排在 <outline> 前面：\n%s", text)
	}
	// json 那边一个字节都不该动。
	raw2, _ := os.ReadFile(jsonPath)
	if string(raw2) != string(src) {
		t.Error("json 被白改了（值没变就不该写盘）")
	}
}
