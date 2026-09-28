package emby

import "testing"

// TestSubtitleName 钉住字幕文件的命名形态。
//
// 这不是「随便起个名」—— Emby 认外部字幕的规则是**与影片同名、换扩展名**，
// 语言段用 ISO 639-2 三字母或全名，中文是文档里唯一点名的例外（zh-CN / zh-TW）。
// 名字写错的后果是静默的：Emby 只是「这条字幕不存在」，不报错。
func TestSubtitleName(t *testing.T) {
	cases := []struct {
		stem, lang, ext, want string
	}{
		{"MIAA-001", "zh-CN", "srt", "MIAA-001.zh-CN.srt"},
		{"MIAA-001", "zh-TW", "ass", "MIAA-001.zh-TW.ass"},
		{"MIAA-001", "eng", "srt", "MIAA-001.eng.srt"},
		// 语言判不出时省略那一段：Emby 仍会把它当成一条没有语言名的字幕轨加载，
		// 这比编一个语言码写错要好。
		{"MIAA-001", "", "srt", "MIAA-001.srt"},
		// 大小写与点都归一。
		{"MIAA-001", "ZH-CN", ".SRT", "MIAA-001.ZH-CN.srt"},
		// 主干必须**原样保留**（含大小写）：Emby 是按视频主干逐字配对的，
		// Linux 上大小写敏感，小写化会变成「nfo/字幕明明在那儿却认不出来」。
		{"SSIS-001-UC-4K", "zh-CN", "srt", "SSIS-001-UC-4K.zh-CN.srt"},
	}
	for _, c := range cases {
		if got := SubtitleName(c.stem, c.lang, c.ext); got != c.want {
			t.Errorf("SubtitleName(%q, %q, %q) = %q，期望 %q", c.stem, c.lang, c.ext, got, c.want)
		}
	}
}

func TestIsSubtitleExtension(t *testing.T) {
	for _, ok := range []string{"srt", ".SRT", "ass", "ssa", "vtt", "sub"} {
		if !IsSubtitleExtension(ok) {
			t.Errorf("IsSubtitleExtension(%q) 该为 true", ok)
		}
	}
	// idx 必须**不在**名单里：它要与同名 .sub 成对出现，单写一个没有意义。
	for _, bad := range []string{"", ".idx", "txt", "jpg", "mp4", "nfo"} {
		if IsSubtitleExtension(bad) {
			t.Errorf("IsSubtitleExtension(%q) 该为 false", bad)
		}
	}
}
