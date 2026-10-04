package actormap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultTableLoads(t *testing.T) {
	tbl := Default()
	if tbl == nil {
		t.Fatal("内置索引表没解析出来 —— 打包或 XML 格式出了问题")
	}
	if tbl.Len() < 8000 {
		t.Errorf("条目数 = %d，期望 8000+（8213 条）", tbl.Len())
	}
}

func TestResolveKnownAliases(t *testing.T) {
	tbl := Default()
	cases := []struct{ in, want string }{
		// 多别名条目：任意别名都归到统一名
		{"優木あいか", "AIKA"},
		{"本田愛華", "AIKA"},
		{"AIKA", "AIKA"},
		// 繁简归一
		{"樂奈子", "乐奈子"},
		{"壞壞", "坏坏"},
		// 两个不同名字并成一个
		{"黄依娜", "沈娜娜"},
	}
	for _, c := range cases {
		if got := tbl.Resolve(c.in); got != c.want {
			t.Errorf("Resolve(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}

// TestResolveUnknownReturnsAsIs 索引表里没有的演员**原样返回**。
//
// 用户明确要求：表里没有的演员，程序不能出错，回到现在的行为。
// 这条同时钉住「缺条目」「空串」两种输入。
func TestResolveUnknownReturnsAsIs(t *testing.T) {
	tbl := Default()
	for _, name := range []string{"彩月七緒", "デカ吉", "完全不存在的演员XYZ", "  ", ""} {
		if got := tbl.Resolve(name); got != name {
			t.Errorf("Resolve(%q) = %q，表里没有的应当原样返回", name, got)
		}
	}
}

// TestResolveNilTableIsSafe nil 表（内置表解析失败时的降级形态）不能 panic。
func TestResolveNilTableIsSafe(t *testing.T) {
	var tbl *Table
	if got := tbl.Resolve("彩月七緒"); got != "彩月七緒" {
		t.Errorf("nil 表应当原样返回，got %q", got)
	}
	if tbl.Len() != 0 {
		t.Error("nil 表的 Len 应当是 0")
	}
}

// TestParseConflictRule 冲突裁决：**原文相等优先，其次先出现**。
//
// 表里有 207 个别名指向多个条目（`ASUKA` 同时是 `ASUKA` 与 `Asuka` 的别名）。
// 规则写死是刻意的 —— 同一份 nfo 在不同机器上写成不同名字最难查。
func TestParseConflictRule(t *testing.T) {
	const raw = `<actor>
  <a zh_cn="Asuka" keyword=",ASUKA,Asuka,"/>
  <a zh_cn="ASUKA" keyword=",ASUKA,ASUKA2,"/>
</actor>`
	tbl, err := Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	// `ASUKA` 在第一条里不是原文相等（统一名是 Asuka），第二条里是 → 第二条赢
	if got := tbl.Resolve("ASUKA"); got != "ASUKA" {
		t.Errorf("原文相等的条目应当胜出，got %q", got)
	}
	// `Asuka` 只在第一条里出现 → 归到 Asuka
	if got := tbl.Resolve("Asuka"); got != "Asuka" {
		t.Errorf("Resolve(Asuka) = %q", got)
	}
	// 两条都不相等时保持先出现的：换个顺序，`ASUKA2` 不受影响
	if got := tbl.Resolve("ASUKA2"); got != "ASUKA" {
		t.Errorf("Resolve(ASUKA2) = %q", got)
	}
}

func TestParseFirstSeenWinsWhenNeitherExact(t *testing.T) {
	const raw = `<actor>
  <a zh_cn="甲" keyword=",X,"/>
  <a zh_cn="乙" keyword=",X,"/>
</actor>`
	tbl, err := Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got := tbl.Resolve("X"); got != "甲" {
		t.Errorf("两条都不是原文相等时应当先出现的赢，got %q", got)
	}
}

// TestParseOutputNameFallback zh_cn 空时用 zh_tw；两个都空则整条跳过。
func TestParseOutputNameFallback(t *testing.T) {
	const raw = `<actor>
  <a zh_cn="" zh_tw="繁体名" keyword=",别名A,"/>
  <a zh_cn="" zh_tw="" keyword=",别名B,"/>
</actor>`
	tbl, err := Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got := tbl.Resolve("别名A"); got != "繁体名" {
		t.Errorf("zh_cn 空时应当回落到 zh_tw，got %q", got)
	}
	if got := tbl.Resolve("别名B"); got != "别名B" {
		t.Errorf("两个语言字段都空的条目整条跳过，got %q", got)
	}
	if tbl.Len() != 1 {
		t.Errorf("条目数 = %d，期望 1（空的那条被跳过）", tbl.Len())
	}
}

// TestParseExactMatchOnly 匹配是精确的 —— 不做子串、不做大小写折叠。
func TestParseExactMatchOnly(t *testing.T) {
	const raw = `<actor><a zh_cn="甲" keyword=",AIKA,"/></actor>`
	tbl, _ := Parse([]byte(raw))
	for _, n := range []string{"AIKAX", "XAIKA", "aika", "AIK"} {
		if got := tbl.Resolve(n); got != n {
			t.Errorf("Resolve(%q) = %q，不该做子串/大小写匹配", n, got)
		}
	}
	if got := tbl.Resolve("AIKA"); got != "甲" {
		t.Errorf("精确命中应当生效，got %q", got)
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, err := Parse([]byte("not xml at all")); err == nil {
		t.Error("非 XML 应当报错")
	}
}

// TestBuiltinTableHasNoEmptyUnifiedName 内置表里不该有条目输出空统一名。
func TestBuiltinTableHasNoEmptyUnifiedName(t *testing.T) {
	tbl := Default()
	if tbl == nil {
		t.Skip("内置表没加载")
	}
	n := 0
	for alias, unified := range tbl.byAlias {
		if strings.TrimSpace(unified) == "" {
			n++
			if n <= 3 {
				t.Errorf("别名 %q 的统一名是空的", alias)
			}
		}
	}
}

// TestLoadOverride 覆盖表：存在就用它，不存在保持内置，坏文件退回内置并报错。
func TestLoadOverride(t *testing.T) {
	dir := t.TempDir()

	// ① 文件不存在 → 保持内置，不算错误
	ResetToBuiltin()
	if err := LoadOverride(filepath.Join(dir, "不存在.xml")); err != nil {
		t.Errorf("文件不存在不该报错，got %v", err)
	}
	if Default() == nil || Default().Len() < 8000 {
		t.Error("文件不存在时应当保持内置表")
	}

	// ② 存在且合法 → 换成它
	p := filepath.Join(dir, "mapping_actor.xml")
	if err := os.WriteFile(p, []byte(`<actor><a zh_cn="统一名" keyword=",别名,"/></actor>`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := LoadOverride(p); err != nil {
		t.Fatalf("合法覆盖表应当加载成功：%v", err)
	}
	if got := Default().Resolve("别名"); got != "统一名" {
		t.Errorf("覆盖表没生效：Resolve(别名) = %q", got)
	}
	if Default().Len() != 1 {
		t.Errorf("覆盖后条目数 = %d，期望 1", Default().Len())
	}

	// ③ 坏文件 → 报错，但**保持上一次那份**（不把表清空）
	bad := filepath.Join(dir, "bad.xml")
	_ = os.WriteFile(bad, []byte("<actor><a"), 0o600)
	if err := LoadOverride(bad); err == nil {
		t.Error("坏文件应当报错（静默退回会让用户以为改的那份生效了）")
	}
	if got := Default().Resolve("别名"); got != "统一名" {
		t.Errorf("坏文件不该把表清空，Resolve(别名) = %q", got)
	}

	// ④ 空路径 → 什么都不做
	if err := LoadOverride(""); err != nil {
		t.Errorf("空路径不该报错：%v", err)
	}

	ResetToBuiltin()
	if Default().Len() < 8000 {
		t.Error("ResetToBuiltin 之后应当回到内置表")
	}
}
