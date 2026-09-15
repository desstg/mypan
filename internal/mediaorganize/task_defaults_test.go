package mediaorganize

import "testing"

// use_jav 必须出现在 defaults 里，否则 NormalizeTaskConfig 会把它静默丢掉 ——
// API 收下了、存进库就没了。这个测试就是钉住这一点。
func TestNormalizeTaskConfigKeepsUseJav(t *testing.T) {
	cfg := NormalizeTaskConfig(map[string]any{"task_name": "t", "use_jav": true})
	if v, ok := cfg["use_jav"]; !ok || v != true {
		t.Fatalf("use_jav 被丢掉了：%+v", cfg)
	}
}

func TestNormalizeTaskConfigDropsUnknownKeys(t *testing.T) {
	cfg := NormalizeTaskConfig(map[string]any{"不存在的键": "x"})
	if _, ok := cfg["不存在的键"]; ok {
		t.Error("未知键不该被保留（这正是 use_jav 必须加进 defaults 的原因）")
	}
}

// 两个匹配方案互斥，冲突时以番号为准。
func TestNormalizeTaskConfigMatchModeExclusion(t *testing.T) {
	cases := []struct {
		name      string
		in        map[string]any
		wantJav   bool
		wantTmdb  bool
		wantMedia string
	}{
		{
			name:      "默认走 TMDB",
			in:        map[string]any{},
			wantJav:   false,
			wantTmdb:  true,
			wantMedia: "auto",
		},
		{
			name:      "显式开番号：关掉 TMDB 并把 media_type 归一为 jav",
			in:        map[string]any{"use_jav": true},
			wantJav:   true,
			wantTmdb:  false,
			wantMedia: "jav",
		},
		{
			name:      "两个都开：番号优先",
			in:        map[string]any{"use_jav": true, "use_tmdb": true},
			wantJav:   true,
			wantTmdb:  false,
			wantMedia: "jav",
		},
		{
			name:      "媒体类型选番号：等价于开番号",
			in:        map[string]any{"media_type": "jav"},
			wantJav:   true,
			wantTmdb:  false,
			wantMedia: "jav",
		},
		{
			name:      "媒体类型大小写不敏感",
			in:        map[string]any{"media_type": "JAV"},
			wantJav:   true,
			wantTmdb:  false,
			wantMedia: "jav",
		},
		{
			name:      "只关 TMDB：不影响 media_type",
			in:        map[string]any{"use_tmdb": false, "media_type": "tv"},
			wantJav:   false,
			wantTmdb:  false,
			wantMedia: "tv",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := NormalizeTaskConfig(c.in)
			if got := cfg["use_jav"]; got != c.wantJav {
				t.Errorf("use_jav = %v, 期望 %v", got, c.wantJav)
			}
			if got := cfg["use_tmdb"]; got != c.wantTmdb {
				t.Errorf("use_tmdb = %v, 期望 %v", got, c.wantTmdb)
			}
			if got := cfg["media_type"]; got != c.wantMedia {
				t.Errorf("media_type = %v, 期望 %v", got, c.wantMedia)
			}
		})
	}
}

// CfgUseJAV 是 planner 分发的依据，两个入口都要认。
func TestCfgUseJAV(t *testing.T) {
	cases := []struct {
		in   map[string]any
		want bool
	}{
		{map[string]any{"use_jav": true}, true},
		{map[string]any{"use_jav": "true"}, true},
		{map[string]any{"media_type": "jav"}, true},
		{map[string]any{"media_type": "JAV"}, true},
		{map[string]any{"use_jav": false, "media_type": "movie"}, false},
		{map[string]any{"use_tmdb": true}, false},
		{map[string]any{}, false},
	}
	for _, c := range cases {
		if got := CfgUseJAV(c.in); got != c.want {
			t.Errorf("CfgUseJAV(%v) = %v, 期望 %v", c.in, got, c.want)
		}
	}
}
