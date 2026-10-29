package quality

import "testing"

// TestBuildAndParseJavFileNameRoundTrip 钉住「自己拼的名字自己拆得回来」。
//
// 这一条是整套做法成立的前提：写侧车时（internal/jav）用 Build 拼出文件名，
// 目录整理时（internal/mediaorganize/javplanner）用 Parse 从**文件名**把番号与
// 标记取回去 —— 中间没有别的信道。有一组拼不回去，那一类资源就再也认不出番号。
func TestBuildAndParseJavFileNameRoundTrip(t *testing.T) {
	cases := []struct {
		name   string
		number string
		marks  Marks
		ext    string
	}{
		{"什么标记都没有", "SSIS-444", Marks{}, ".mp4"},
		{"中字", "SSIS-444", Marks{Subtitle: true}, ".mp4"},
		{"破解", "SSIS-444", Marks{Uncensored: true}, ".mkv"},
		{"破解 + 中字", "SSIS-444", Marks{Uncensored: true, Subtitle: true}, ".mp4"},
		{"只有 4K", "SSIS-444", Marks{FourK: true}, ".mp4"},
		{"中字 + 4K", "SSIS-444", Marks{Subtitle: true, FourK: true}, ".mp4"},
		{"破解 + 4K", "SSIS-444", Marks{Uncensored: true, FourK: true}, ".mp4"},
		{"三样都占", "SSIS-444", Marks{Uncensored: true, Subtitle: true, FourK: true}, ".mp4"},
		{"FC2 那种多段番号", "FC2-PPV-1234567", Marks{Uncensored: true}, ".mp4"},
		{"日期序号番号", "123456-789", Marks{Subtitle: true}, ".ts"},
		{"小写番号也原样保留", "dvaj-725", Marks{FourK: true}, ".mp4"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			full := BuildJavFileName(c.number, c.marks, 0, c.ext)
			if full == "" {
				t.Fatal("BuildJavFileName 返回空串")
			}
			// 解析吃的是主名（扩展名由调用方剥掉），这里按调用方的做法来。
			stem := full[:len(full)-len(c.ext)]
			number, marks, ok := ParseJavFileName(stem)
			if !ok {
				t.Fatalf("拆不回来：%q", full)
			}
			if number != c.number {
				t.Errorf("番号 = %q，want %q", number, c.number)
			}
			if marks != c.marks {
				t.Errorf("标记 = %+v，want %+v", marks, c.marks)
			}
		})
	}
}

// TestBuildJavFileNameShape 逐字钉住文件名形状 —— 这是给外部看的东西（网盘上的
// 真实文件名、媒体库里的排序），改一个字都算改契约。
func TestBuildJavFileNameShape(t *testing.T) {
	cases := []struct {
		name  string
		marks Marks
		cd    int
		want  string
	}{
		{"无标记", Marks{}, 0, "MOIL-001.mp4"},
		{"只中字", Marks{Subtitle: true}, 0, "MOIL-001-C.mp4"},
		{"只破解", Marks{Uncensored: true}, 0, "MOIL-001-U.mp4"},
		{"破解+中字（U 在前）", Marks{Uncensored: true, Subtitle: true}, 0, "MOIL-001-UC.mp4"},
		{"只 4K", Marks{FourK: true}, 0, "MOIL-001-4K.mp4"},
		{"破解+4K", Marks{Uncensored: true, FourK: true}, 0, "MOIL-001-U-4K.mp4"},
		{"三样都占", Marks{Uncensored: true, Subtitle: true, FourK: true}, 0, "MOIL-001-UC-4K.mp4"},
		{"第一个分片不编号", Marks{}, 1, "MOIL-001-cd1.mp4"},
		{"分片编号排在 4K 之后", Marks{Uncensored: true, FourK: true}, 2, "MOIL-001-U-4K-cd2.mp4"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := BuildJavFileName("MOIL-001", c.marks, c.cd, ".mp4"); got != c.want {
				t.Errorf("= %q，want %q", got, c.want)
			}
		})
	}

	if got := BuildJavFileName("   ", Marks{}, 0, ".mp4"); got != "" {
		t.Errorf("空番号应当返回空串（调用方据此跳过），got %q", got)
	}
}

// TestBuildJavFileNameOnlyCDWhenMultiple 只有一个视频时**不编号**。
//
// 调用方传 cd<=0 表示不编号；这条钉的是「约定本身」：一份没分片的资源叫
// `MOIL-001.mp4`，而不是 `MOIL-001-cd1.mp4` —— 后者是噪声，而且会让
// 「补一个分片」与「换一版」的文件名无法对齐。
func TestBuildJavFileNameOnlyCDWhenMultiple(t *testing.T) {
	if got := BuildJavFileName("MOIL-001", Marks{}, 0, ".mp4"); got != "MOIL-001.mp4" {
		t.Errorf("cd=0 不该带编号，got %q", got)
	}
}

// TestParseJavFileNameOnlyRejectsEmpty 只做机械拆分：**只有拆完什么都不剩**才认不出。
//
// 「拆出来的到底是不是番号」不在这里判 —— 那由调用方拿 javrules.HasCode 兜一道。
// 这条边界是有意的：两套判据迟早分家，而分家的表现是「同一份文件在设置页试跑里
// 认得出、整理时不认」。所以这里连 `4K` 都照拆不误（它确实是「没有标记的主名」）。
func TestParseJavFileNameOnlyRejectsEmpty(t *testing.T) {
	for _, in := range []string{"", "   ", "-U"} {
		if _, _, ok := ParseJavFileName(in); ok {
			t.Errorf("ParseJavFileName(%q) 拆完什么都不剩，应当认不出", in)
		}
	}
	// 这些拆得开（有没有数字不归这里管）—— 断言它们**不 panic 且返回非空番号**。
	for _, in := range []string{"4K", "notes", "config", "readme-4K"} {
		if _, _, ok := ParseJavFileName(in); !ok {
			t.Errorf("ParseJavFileName(%q) 机械上拆得开，不该在这里被拒", in)
		}
	}
}

// TestParseJavFileNameAcceptsExistingSidecars 向后兼容：**已经写出去的旧名字**。
//
// 侧车刚上线时叫 `<番号>.json`（没有后缀段），今天真实库里就有一份
// `MOIL-001.json` 躺在 115 上。那种名字拆出来就是「没有标记」——
// 不是错误，是正常情况，不能因为它是旧格式就认不出番号。
func TestParseJavFileNameAcceptsExistingSidecars(t *testing.T) {
	number, marks, ok := ParseJavFileName("MOIL-001")
	if !ok {
		t.Fatal("旧的 <番号>.json 名字必须仍然认得出来")
	}
	if number != "MOIL-001" {
		t.Errorf("番号 = %q", number)
	}
	if marks != (Marks{}) {
		t.Errorf("旧名字没有后缀段，标记应当全假，got %+v", marks)
	}
}

// TestParseJavFileNameTolerance 容错：大小写、分片编号。
//
// 大小写：名字经过各种工具的手，`-uc` / `-4k` 都出现过，而番号本体的大小写
// 我们不动（那是调用方的事）。分片编号：视频名带 `-cdN`，用同一套拆法，
// 免得再写一个几乎一样的「拆视频名」函数。
func TestParseJavFileNameTolerance(t *testing.T) {
	cases := []struct {
		in     string
		number string
		marks  Marks
	}{
		{"SSIS-444-uc-4k", "SSIS-444", Marks{Uncensored: true, Subtitle: true, FourK: true}},
		{"SSIS-444-C", "SSIS-444", Marks{Subtitle: true}},
		{"SSIS-444-UC-4K-cd2", "SSIS-444", Marks{Uncensored: true, Subtitle: true, FourK: true}},
		{"MOIL-001-cd10", "MOIL-001", Marks{}},
		{"FC2-PPV-1234567-U-4K-cd1", "FC2-PPV-1234567", Marks{Uncensored: true, FourK: true}},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			number, marks, ok := ParseJavFileName(c.in)
			if !ok {
				t.Fatalf("应当认得出来")
			}
			if number != c.number {
				t.Errorf("番号 = %q，want %q", number, c.number)
			}
			if marks != c.marks {
				t.Errorf("标记 = %+v，want %+v", marks, c.marks)
			}
		})
	}
}

// TestParseJavFileNameKeepsNumberTail 番号里本来就带连字符或字母的，不能被吃掉。
//
// `FC2-PPV-1234567` 这种多段番号最容易被后缀解析误伤：`-PPV` 长得像字母段，
// 但字母段只可能是 U / C 两种，且**必须带数字**这条闸在后面兜着。
func TestParseJavFileNameKeepsNumberTail(t *testing.T) {
	number, marks, ok := ParseJavFileName("1PON-123456-U")
	if !ok {
		t.Fatal("应当认得出来")
	}
	if number != "1PON-123456" || !marks.Uncensored || marks.Subtitle || marks.FourK {
		t.Errorf("拆成了 %q %+v", number, marks)
	}

	// 番号尾巴的 `-PPV` 不能被当成字母段。
	number, marks, ok = ParseJavFileName("FC2-PPV-1234567")
	if !ok {
		t.Fatal("应当认得出来")
	}
	if number != "FC2-PPV-1234567" || marks != (Marks{}) {
		t.Errorf("拆成了 %q %+v", number, marks)
	}
}

// TestStripLTXMark 钉住「剥掉分享者贴的 LTX 标记」这条判据。
//
// 它只服务于**拿主名反查番号**那一步（见 strmscrape.javScrapeNumber 第 ③ 步），
// 所以 `ParseJavFileName` 一个字都没改 —— 这条测试也顺手盯住那件事：
// 剥完之后剩下的 `-U` 之类仍要由 `ParseJavFileName` 吃。
func TestStripLTXMark(t *testing.T) {
	cases := []struct {
		in   string
		want string
		hit  bool
	}{
		{"IPX-680_LTX", "IPX-680", true},
		{"IPX-680_LTX-U", "IPX-680-U", true},
		{"IPX-680_U_LTX", "IPX-680_U", true},
		{"PPPD-451LTX", "PPPD-451", true},
		{"MISM-305_LTX_restored", "MISM-305-restored", true},
		{"www.98T.la@MISM-305_LTX", "www.98T.la@MISM-305", true},
		// 没有这个标记：原样返回、且告诉调用方「没动过」——调用方据此只试一轮。
		{"IPX-680", "IPX-680", false},
		{"XLTX-001", "XLTX-001", false}, // 连写的 xltx 不是独立 token
		// 整串就是标记：不能剥成空串（那会让调用方以为「拆完了什么都不剩」）。
		{"LTX", "LTX", false},
	}
	for _, c := range cases {
		got, hit := StripLTXMark(c.in)
		if got != c.want || hit != c.hit {
			t.Errorf("StripLTXMark(%q) = (%q, %v)，期望 (%q, %v)", c.in, got, hit, c.want, c.hit)
		}
	}
	// 剥完之后仍能被 ParseJavFileName 接上（两段契约的接缝）。
	if number, marks, ok := ParseJavFileName(mustStrip(t, "IPX-680_LTX-U")); !ok ||
		number != "IPX-680" || !marks.Uncensored {
		t.Errorf("剥完再拆 = (%q, %+v, %v)，期望 (IPX-680, {Uncensored:true}, true)", number, marks, ok)
	}
}

func mustStrip(t *testing.T, stem string) string {
	t.Helper()
	out, hit := StripLTXMark(stem)
	if !hit {
		t.Fatalf("%q 里应当有 LTX 标记", stem)
	}
	return out
}
