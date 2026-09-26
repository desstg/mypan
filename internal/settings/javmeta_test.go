package settings

import "testing"

// TestJavMetaItemsCodec 六个开关的存取。
//
// 重点在两件容易做错的事：
//  1. **全不勾要存得住** —— 这正是它不能写成「;」分隔列表的原因（空串在本项目里
//     表示「没存过、回落默认值」，用户取消全选保存后界面又会全勾上）。
//  2. **解析失败回落全开** —— 静默关掉全部会让用户面对一个「什么都没生成」的
//     任务却找不到原因。
func TestJavMetaItemsCodec(t *testing.T) {
	const allOn = `{"subtitle":true,"preview":true,"thumb":true,"poster":true,"fanart":true,"nfo":true}`
	const allOff = `{"subtitle":false,"preview":false,"thumb":false,"poster":false,"fanart":false,"nfo":false}`

	if got := DefaultJavMetaItems().Encode(); got != allOn {
		t.Errorf("默认应当全开，got %s", got)
	}
	// 键顺序必须与声明顺序一致：前端拿前后两个字符串比「有没有改动」，
	// 顺序一变就会一直误报未保存。
	if got := normalizeJavMetaItems(JavMetaItems{}.Encode()); got != allOff {
		t.Errorf("全不勾应当原样存住，got %s", got)
	}
	if parsed := ParseJavMetaItems(allOff); parsed.NFO || parsed.Poster || parsed.Thumb {
		t.Errorf("全不勾读回来应当是关的：%+v", parsed)
	}

	// 缺项只关掉缺的那一个（先灌默认值再 Unmarshal）
	if got := ParseJavMetaItems(`{"nfo":false}`); got.NFO || !got.Poster || !got.Preview {
		t.Errorf("缺项应当保持默认（开），只有显式 false 的才关：%+v", got)
	}
	// 空串 / 坏 JSON → 全开
	for _, raw := range []string{"", "   ", "{", "null", "not json"} {
		if got := ParseJavMetaItems(raw); !got.Subtitle || !got.Preview || !got.Thumb || !got.Poster || !got.Fanart || !got.NFO {
			t.Errorf("%q 应当回落全开：%+v", raw, got)
		}
	}
	// 未知键被忽略，不影响已知项
	if got := ParseJavMetaItems(`{"nfo":false,"future_flag":true}`); got.NFO || !got.Poster {
		t.Errorf("未知键不该影响解析：%+v", got)
	}
}
