package telegram

import (
	"strings"
	"testing"
)

// 频道里的链接脏得很（漏分隔符、HTML 转义、名字里带空格/竖线…）。
// 番号模块那边是一路修过来的（截断、还原转义、解析体积），这一组用例把同一批
// 形状喂给**电影/剧集这条链路的抽取器**，钉住「它本来就是干净的」——
// 它是解析参数后**重建**链接，不是截断，所以这些脏东西结构上就进不去。
func TestExtractorsNormalizeDirtyChannelLinks(t *testing.T) {
	cases := []struct {
		name string
		text string
		// 期望：Raw 里不该出现这些
		notInRaw []string
		// 期望：Raw 里该出现这些
		inRaw []string
	}{
		{
			// 漏了 & 分隔符，说明文字粘在 hash 后面。
			name:     "hash 后粘杂字",
			text:     "magnet:?xt=urn:btih:0A7BC56F48ADE6F9A489204D8F758E3B2B9C2C73.无码",
			notInRaw: []string{"无码"},
		},
		{
			// 从网页复制出来的，& 被转义。
			name:     "HTML 转义",
			text:     "无码破解magnet:?xt=urn:btih:" + hexHash + "&amp;dn=DASS-092&amp;xl=6444544844&amp;tr=udp%3A%2F%2Ft.test",
			notInRaw: []string{"&amp;"},
			inRaw:    []string{"&dn=DASS-092", "&xl=6444544844"},
		},
		{
			// 尾部粘了正文。
			name:     "ed2k 尾巴粘正文",
			text:     "ed2k://|file|a.mp4|1234|0123456789ABCDEF0123456789ABCDEF|/这个不错，推荐",
			notInRaw: []string{"这个不错"},
		},
	}

	reg := NewRegistry(MagnetExtractor{}, ED2KExtractor{})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			refs := reg.Extract(&Message{Text: tc.text})
			if len(refs) == 0 {
				t.Fatalf("应当抽出至少一条资源，got 0")
			}
			raw := refs[0].Raw
			for _, bad := range tc.notInRaw {
				if strings.Contains(raw, bad) {
					t.Errorf("Raw 里不该出现 %q\ngot %q", bad, raw)
				}
			}
			for _, want := range tc.inRaw {
				if !strings.Contains(raw, want) {
					t.Errorf("Raw 里应当保留 %q\ngot %q", want, raw)
				}
			}
			// 抽出来的每条都必须能解出身份 —— 重建后一定带 hash。
			if refs[0].InfoHash == "" {
				t.Errorf("重建后应当带 info hash，got %+v", refs[0])
			}
		})
	}
}

// `xl=` 是迅雷记的文件字节数，频道里很常见，要解成 SizeBytes。
func TestExtractorParsesXLSize(t *testing.T) {
	refs := NewRegistry(MagnetExtractor{}).Extract(&Message{
		Text: "magnet:?xt=urn:btih:" + hexHash + "&dn=x&xl=6444544844",
	})
	if len(refs) != 1 {
		t.Fatalf("应当抽出 1 条，got %d", len(refs))
	}
	if refs[0].SizeBytes != 6444544844 {
		t.Errorf("xl= 应当解成体积，got %d", refs[0].SizeBytes)
	}
}
