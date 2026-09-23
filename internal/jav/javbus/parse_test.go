package javbus

import (
	"strings"
	"testing"
)

// ajaxFixture 是 JAVBUS 磁链接口的响应形状：一张只有 <tr> 的表，
// 每行三格（名称 / 大小 / 日期），名称格里可能混着角标 <a>。
const ajaxFixture = `
<table class="table table-hover">
<tr>
    <td>
        <a class="btn btn-mini" href="#" onclick="window.open('https://example.test/ad')">高清</a>
        <a href="magnet:?xt=urn:btih:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&amp;dn=SSIS-001-1080p&tr=http%3A%2F%2Ft1.test%2Fa">SSIS-001 1080p 中文字幕</a>
    </td>
    <td><a class="btn btn-mini" href="#">4.70GB</a></td>
    <td>2024-03-15</td>
</tr>
<tr>
    <td><a href="magnet:?xt=urn:btih:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB&dn=SSIS-001-4K">SSIS-001 2160p 超清</a></td>
    <td>12.5 GB</td>
    <td>2024-04-02</td>
</tr>
<tr>
    <td><a href="magnet:?xt=urn:btih:CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC">SSIS-001 U-无码流出</a></td>
    <td>2,048 MB</td>
    <td>2024-05-01</td>
</tr>
<tr>
    <td><a href="magnet:?xt=urn:btih:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&dn=dup">重复的同一颗</a></td>
    <td>4.70GB</td>
    <td>2024-03-15</td>
</tr>
<tr>
    <td>没有磁链的空行</td>
    <td>1GB</td>
    <td>2024-01-01</td>
</tr>
</table>`

func TestParseMagnets(t *testing.T) {
	got := ParseMagnets(ajaxFixture)

	// 5 行里有 4 行带磁链，其中 AAAA 出现两次要合并成一条。
	if len(got) != 3 {
		t.Fatalf("应当解析出 3 颗去重后的磁链，got %d: %+v", len(got), got)
	}

	first := got[0]
	if first.Btih != strings.Repeat("a", 40) {
		t.Errorf("btih = %q", first.Btih)
	}
	if first.Size != "4.70GB" {
		t.Errorf("size = %q, want 4.70GB", first.Size)
	}
	if first.Date != "2024-03-15" {
		t.Errorf("date = %q, want 2024-03-15", first.Date)
	}
	if !first.HasHD || !first.HasSub {
		t.Errorf("角标应当识别出高清与字幕：hd=%v sub=%v", first.HasHD, first.HasSub)
	}
	// 角标文本要从名称里剥掉，否则卡片上会出现「1080p 中文字幕 高清 字幕」。
	if strings.Contains(first.Name, "高清") || strings.Contains(first.Name, "字幕") {
		t.Errorf("名称里的角标没剥干净: %q", first.Name)
	}
	if !strings.Contains(first.Name, "SSIS-001 1080p") {
		t.Errorf("名称丢内容了: %q", first.Name)
	}

	// &amp; 必须反转义 —— 不反转义的话提交给网盘的链接里会带字面量 "&amp;"，
	// 整条链接作废，而它看起来「几乎是对的」。
	if strings.Contains(first.Magnet, "&amp;") {
		t.Errorf("磁链没有反转义 HTML 实体: %q", first.Magnet)
	}
	if !strings.Contains(first.Magnet, "&tr=") {
		t.Errorf("磁链丢了 tracker 参数: %q", first.Magnet)
	}
}

func TestParseMagnetsSizeWithComma(t *testing.T) {
	got := ParseMagnets(ajaxFixture)
	var found bool
	for _, m := range got {
		if m.Btih == strings.Repeat("c", 40) {
			found = true
			// 逗号在解析前被去掉，所以这里是 "2048 MB"。
			// 要紧的是**没被截断**：直接拿原始文本去匹配会得到 "048 MB"。
			if m.Size != "2048 MB" {
				t.Errorf("带千分位的体积没解析对: %q", m.Size)
			}
		}
	}
	if !found {
		t.Fatal("没找到第三颗磁链")
	}
}

func TestParseMagnetsEmptyOrGarbage(t *testing.T) {
	for _, body := range []string{"", "<table></table>", "<html><body>没有表</body></html>", "magnet:?xt=urn:btih:xxx"} {
		if got := ParseMagnets(body); len(got) != 0 {
			t.Errorf("ParseMagnets(%q) 应当返回空，got %+v", body, got)
		}
	}
}

func TestParseMagnetsWindowOpenForm(t *testing.T) {
	// JAVBUS 有些版本用 onclick="window.open('magnet:...')" 而不是 href。
	const body = `<tr><td><a onclick="window.open('magnet:?xt=urn:btih:DDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD')">SSIS-002</a></td><td>1GB</td><td>2024-01-01</td></tr>`
	got := ParseMagnets(body)
	if len(got) != 1 {
		t.Fatalf("window.open 形态没解析出来，got %+v", got)
	}
	if got[0].Btih != strings.Repeat("d", 40) {
		t.Errorf("btih = %q", got[0].Btih)
	}
}

func TestParsePageParams(t *testing.T) {
	const page = `<html><head><script>
        var gid = 123456;
        var uc = 0;
        var img = 'aBcDeF123';
    </script></head><body></body></html>`

	p, ok := ParsePageParams(page)
	if !ok {
		t.Fatal("应当解析出 gid")
	}
	if p.Gid != "123456" || p.UC != "0" || p.Img != "aBcDeF123" {
		t.Fatalf("params = %+v", p)
	}

	// 没有 gid 时必须报失败，而不是给一个空参数集去请求 ajax ——
	// 那会拿到空表，与「这部片确实没有磁链」无法区分。
	if _, ok := ParsePageParams(`<html>无</html>`); ok {
		t.Error("没有 gid 时应当返回 false")
	}

	// 单引号、无引号两种写法都要认。
	if p, ok := ParsePageParams(`var gid='777';`); !ok || p.Gid != "777" {
		t.Errorf("单引号形态没认出来: %+v ok=%v", p, ok)
	}
	if p, ok := ParsePageParams(`var gid=888;`); !ok || p.Gid != "888" {
		t.Errorf("无引号形态没认出来: %+v ok=%v", p, ok)
	}
}
