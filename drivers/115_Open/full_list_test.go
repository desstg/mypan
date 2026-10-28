package pan115open

import "testing"

func TestBuildDirPathSkipsAboveRootAndKeepsRootRelative(t *testing.T) {
	cases := []struct {
		name   string
		paths  []dirPathEntry
		self   string
		rootID string
		want   string
		wantOK bool
	}{
		{
			name:   "挂载根之上的段被丢弃",
			paths:  []dirPathEntry{{FileID: "1", FileName: "根之上"}, {FileID: "900", FileName: "我的云下载"}, {FileID: "901", FileName: "电影"}},
			self:   "沙丘",
			rootID: "900",
			want:   "电影/沙丘",
			wantOK: true,
		},
		{
			name:   "账号根就是 115 根时整条链都算",
			paths:  []dirPathEntry{{FileID: "0", FileName: "根"}, {FileID: "901", FileName: "电影"}},
			self:   "沙丘",
			rootID: "0",
			want:   "电影/沙丘",
			wantOK: true,
		},
		{
			name:   "父链里没有挂载根就不能瞎猜",
			paths:  []dirPathEntry{{FileID: "901", FileName: "电影"}},
			self:   "沙丘",
			rootID: "900",
			want:   "",
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := buildDirPath(tc.paths, tc.self, tc.rootID)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if got != tc.want {
				t.Fatalf("path = %q, want %q", got, tc.want)
			}
		})
	}
}

// 115 允许目录名里带斜杠；直接拼路径会让一个目录被读成好几层，
// STRM 写进并不存在的层级、随后又把正确位置的那份当重复删掉。
func TestBuildDirPathNeutralizesSlashInNames(t *testing.T) {
	paths := []dirPathEntry{{FileID: "901", FileName: "A/B"}}
	got, ok := buildDirPath(paths, `C\D`, "0")
	if !ok {
		t.Fatal("expected ok")
	}
	if got != "A_B/C_D" {
		t.Fatalf("path = %q, want %q", got, "A_B/C_D")
	}
	if segs := len(splitPath(got)); segs != 2 {
		t.Fatalf("段数 = %d, want 2 (%q)", segs, got)
	}
}

func splitPath(p string) []string {
	var out []string
	cur := ""
	for _, r := range p {
		if r == '/' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func TestPathSegmentNameTrimsAndBlanks(t *testing.T) {
	if got := pathSegmentName("  "); got != "" {
		t.Fatalf("空白目录名应归一成空串，得到 %q", got)
	}
	if got := pathSegmentName(" 电影 "); got != "电影" {
		t.Fatalf("未去空白：%q", got)
	}
}
