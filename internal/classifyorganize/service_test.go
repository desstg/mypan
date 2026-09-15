package classifyorganize

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"litepan/internal/mediaorganize/classification"
	"litepan/internal/settings"
)

type configRepo struct {
	mu     sync.Mutex
	values map[string]string
}

func (r *configRepo) Get(_ context.Context, key string) (string, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.values[key]
	return value, ok, nil
}

func (r *configRepo) Set(_ context.Context, key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values[key] = value
	return nil
}

func (r *configRepo) All(context.Context) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]string, len(r.values))
	for key, value := range r.values {
		out[key] = value
	}
	return out, nil
}

func newService(t *testing.T, enabled bool) *Service {
	t.Helper()
	return newServiceWithValues(t, map[string]string{
		settings.KeyMOClassificationEnabled: boolString(enabled),
	})
}

func newServiceWithValues(t *testing.T, values map[string]string) *Service {
	t.Helper()
	settingsSvc, err := settings.New(context.Background(), &configRepo{values: values})
	if err != nil {
		t.Fatal(err)
	}
	return New(settingsSvc)
}

type detailLoader struct {
	calls int
	raw   map[string]any
	err   error
}

func (l *detailLoader) Lookup(context.Context, string, string) (json.RawMessage, error) {
	l.calls++
	if l.err != nil {
		return nil, l.err
	}
	return json.Marshal(l.raw)
}

func TestDefaultConfigMatchesFourTemplates(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Version != ConfigVersion || cfg.SelectedTemplate != TemplateMedia || len(cfg.Templates) != 4 {
		t.Fatalf("默认配置异常: %+v", cfg)
	}
	if got := cfg.Templates[0].Rules[0].Condition; got != "type=movie" {
		t.Fatalf("影视分类匹配条件异常: %s", got)
	}
	if got := cfg.Templates[1].Rules[0].Children[2]; got.Name != "欧美" || got.Condition != "origin_country=US;GB;FR;DE;IT;ES" {
		t.Fatalf("地区模板顺序异常: %+v", got)
	}
	if got := cfg.Templates[2].Rules[1].Children[0]; got.Name != "综艺" || got.Condition != "genres=脱口秀;真人秀" {
		t.Fatalf("类型模板顺序异常: %+v", got)
	}
	if got := cfg.Templates[3].Rules[0]; got.Name != "综艺" || got.Condition != "type=tv，genres=脱口秀;真人秀" {
		t.Fatalf("自定义模板一级示例异常: %+v", got)
	}
	if got := cfg.Templates[3].Rules[2]; got.FallbackDir != "" || got.Children[0].Condition != "origin_country=JP，genres=动画" {
		t.Fatalf("自定义模板混合层级示例异常: %+v", got)
	}
}

func TestCustomTemplateSupportsCompoundConditionsAndMixedDepth(t *testing.T) {
	svc := newService(t, true)
	cfg := svc.Config()
	cfg.SelectedTemplate = TemplateCustom
	if _, err := svc.Update(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		mediaType string
		raw       map[string]any
		want      []string
	}{
		{name: "国产综艺在同等条件数时由 genres 自动优先", mediaType: "tv", raw: map[string]any{"origin_country": []string{"CN"}, "genres": []string{"真人秀"}}, want: []string{"综艺"}},
		{name: "日本动漫使用更具体的三条件路径", mediaType: "tv", raw: map[string]any{"origin_country": []string{"JP"}, "genres": []string{"动画"}}, want: []string{"电视剧", "日本动漫"}},
		{name: "普通剧集使用父级兜底", mediaType: "tv", raw: map[string]any{"origin_country": []string{"US"}, "genres": []string{"剧情"}}, want: []string{"电视剧"}},
		{name: "普通电影使用父级兜底", mediaType: "movie", raw: map[string]any{"origin_country": []string{"US"}}, want: []string{"电影"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision, err := svc.Classify(context.Background(), classification.Request{MediaType: test.mediaType, Raw: test.raw})
			if err != nil {
				t.Fatal(err)
			}
			if !decision.Matched || strings.Join(decision.RelativeSegments, "/") != strings.Join(test.want, "/") {
				t.Fatalf("自定义分类异常: %+v", decision)
			}
		})
	}

	// 根节点数组顺序不是匹配优先级，将综艺移到最后仍应得到相同结果。
	cfg = svc.Config()
	rules := cfg.Templates[3].Rules
	cfg.Templates[3].Rules = []Rule{rules[1], rules[2], rules[0]}
	if _, err := svc.Update(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	decision, err := svc.Classify(context.Background(), classification.Request{
		MediaType: "tv",
		Raw:       map[string]any{"origin_country": []string{"CN"}, "genres": []string{"真人秀"}},
	})
	if err != nil || !decision.Matched || strings.Join(decision.RelativeSegments, "/") != "综艺" {
		t.Fatalf("自定义模板不应依赖目录顺序: decision=%+v err=%v", decision, err)
	}
}

func TestCustomTemplateNormalizesEnglishCommaAndRejectsInheritedGenreConflict(t *testing.T) {
	svc := newService(t, false)
	cfg := svc.Config()
	cfg.Templates[3].Rules[0].Condition = "type=tv, genres=脱口秀;真人秀"
	updated, err := svc.Update(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := updated.Templates[3].Rules[0].Condition; got != "type=tv，genres=脱口秀;真人秀" {
		t.Fatalf("英文逗号未归一化为中文逗号: %s", got)
	}

	cfg = svc.Config()
	cfg.Templates[3].Rules = []Rule{
		{Name: "电视剧", Condition: "type=tv", Children: []Rule{
			{Name: "综艺", Condition: "genres=脱口秀;真人秀"},
		}},
		{Name: "综艺", Condition: "type=tv，genres=脱口秀;真人秀"},
	}
	if _, err := svc.Update(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "重复匹配 genres=") {
		t.Fatalf("应拒绝继承后 type 相同且 genres 重复的路径: %v", err)
	}

	cfg = svc.Config()
	cfg.Templates[3].Rules = []Rule{
		{Name: "电影动画", Condition: "type=movie，genres=动画"},
		{Name: "电视动画", Condition: "type=tv，genres=动画"},
	}
	if _, err := svc.Update(context.Background(), cfg); err != nil {
		t.Fatalf("不同 type 范围应允许使用相同 genres 值: %v", err)
	}

	cfg = svc.Config()
	cfg.Templates[3].Rules[0].Condition = "type=movie，type=tv"
	if _, err := svc.Update(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "不能重复配置字段 type") {
		t.Fatalf("应拒绝同一条规则重复字段: %v", err)
	}
}

func TestClassifyMediaUsesDirectTypeIndexWithoutTMDB(t *testing.T) {
	svc := newService(t, true)
	for mediaType, want := range map[string]string{"movie": "电影", "tv": "电视剧"} {
		decision, err := svc.Classify(context.Background(), classification.Request{MediaType: mediaType})
		if err != nil {
			t.Fatal(err)
		}
		if !decision.Applied || !decision.Matched || len(decision.RelativeSegments) != 1 || decision.RelativeSegments[0] != want {
			t.Fatalf("%s 影视分类结果异常: %+v", mediaType, decision)
		}
	}
}

func TestClassifyRegionAndGenreUseTMDBValueOrder(t *testing.T) {
	svc := newService(t, true)
	cfg := svc.Config()
	cfg.SelectedTemplate = TemplateRegion
	if _, err := svc.Update(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	region, err := svc.Classify(context.Background(), classification.Request{
		MediaType: "movie",
		Raw:       map[string]any{"origin_country": []string{"US", "GB"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !region.Matched || region.Category != "欧美" {
		t.Fatalf("地区分类结果异常: %+v", region)
	}

	cfg = svc.Config()
	cfg.SelectedTemplate = TemplateGenre
	if _, err := svc.Update(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	genre, err := svc.Classify(context.Background(), classification.Request{
		MediaType: "tv",
		Raw: map[string]any{"genres": []map[string]any{
			{"name": "喜剧"}, {"name": "真人秀"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !genre.Matched || genre.Category != "喜剧" {
		t.Fatalf("应按 TMDB 首个可匹配值优先命中喜剧: %+v", genre)
	}

	movieGenre, err := svc.Classify(context.Background(), classification.Request{
		MediaType: "movie",
		Raw: map[string]any{"genres": []map[string]any{
			{"name": "科幻"}, {"name": "动作"},
		}},
	})
	if err != nil || !movieGenre.Matched || movieGenre.Category != "科幻奇幻" {
		t.Fatalf("应按 TMDB 返回顺序优先命中科幻: decision=%+v err=%v", movieGenre, err)
	}
}

func TestBuiltInChildFallbackSupportsRootAndNamedDirectory(t *testing.T) {
	svc := newService(t, true)
	cfg := svc.Config()
	cfg.SelectedTemplate = TemplateRegion
	if _, err := svc.Update(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	req := classification.Request{
		MediaType: "movie",
		Raw:       map[string]any{"origin_country": []string{"BR"}},
	}
	decision, err := svc.Classify(context.Background(), req)
	if err != nil || !decision.Matched || strings.Join(decision.RelativeSegments, "/") != "电影" {
		t.Fatalf("二级未命中时应默认放入一级目录: decision=%+v err=%v", decision, err)
	}

	cfg = svc.Config()
	cfg.Templates[1].Rules[0].FallbackMode = "directory"
	cfg.Templates[1].Rules[0].FallbackDir = "其他"
	if _, err := svc.Update(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	decision, err = svc.Classify(context.Background(), req)
	if err != nil || !decision.Matched || decision.Category != "其他" || strings.Join(decision.RelativeSegments, "/") != "电影/其他" {
		t.Fatalf("二级未命中时应放入指定目录: decision=%+v err=%v", decision, err)
	}
}

func TestUnrecognizedMediaTypeStillUsesMoveRoot(t *testing.T) {
	svc := newService(t, true)
	cfg := svc.Config()
	cfg.SelectedTemplate = TemplateRegion
	cfg.Templates[1].Rules[0].FallbackMode = "directory"
	cfg.Templates[1].Rules[0].FallbackDir = "其他"
	if _, err := svc.Update(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	decision, err := svc.Classify(context.Background(), classification.Request{MediaType: "unknown"})
	if err != nil || decision.Matched || len(decision.RelativeSegments) != 0 {
		t.Fatalf("一级无法识别时应保持任务目标根: decision=%+v err=%v", decision, err)
	}
}

func TestCustomChildFallbackSupportsNamedDirectory(t *testing.T) {
	svc := newService(t, true)
	cfg := svc.Config()
	cfg.SelectedTemplate = TemplateCustom
	cfg.Templates[3].Rules[2].FallbackMode = "directory"
	cfg.Templates[3].Rules[2].FallbackDir = "其他剧集"
	if _, err := svc.Update(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	decision, err := svc.Classify(context.Background(), classification.Request{
		MediaType: "tv",
		Raw:       map[string]any{"origin_country": []string{"US"}, "genres": []string{"剧情"}},
	})
	if err != nil || !decision.Matched || strings.Join(decision.RelativeSegments, "/") != "电视剧/其他剧集" {
		t.Fatalf("自定义模板应使用指定未命中目录: decision=%+v err=%v", decision, err)
	}
}

func TestClassifySupportsUserDefinedFieldCondition(t *testing.T) {
	svc := newService(t, true)
	cfg := svc.Config()
	cfg.SelectedTemplate = TemplateCustom
	cfg.Templates[3].Rules = []Rule{
		{Name: "中文片", Condition: "original_language=zh"},
		{Name: "英文片", Condition: "original_language=en"},
	}
	if _, err := svc.Update(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	decision, err := svc.Classify(context.Background(), classification.Request{
		MediaType: "movie",
		Raw:       map[string]any{"original_language": "zh"},
	})
	if err != nil || !decision.Matched || decision.Category != "中文片" {
		t.Fatalf("自定义字段分类异常: decision=%+v err=%v", decision, err)
	}
}

func TestClassifyLoadsAndCachesTMDBDetail(t *testing.T) {
	svc := newService(t, true)
	cfg := svc.Config()
	cfg.SelectedTemplate = TemplateGenre
	if _, err := svc.Update(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	loader := &detailLoader{raw: map[string]any{
		"id":     231620,
		"genres": []any{map[string]any{"name": "真人秀"}},
	}}
	req := classification.Request{MediaType: "tv", TMDBID: "231620", Raw: map[string]any{"id": 231620}, Loader: loader}
	for range 2 {
		decision, err := svc.Classify(context.Background(), req)
		if err != nil || !decision.Matched || decision.Category != "综艺" {
			t.Fatalf("detail 分类异常: decision=%+v err=%v", decision, err)
		}
	}
	if loader.calls != 1 {
		t.Fatalf("detail 缓存未命中，请求次数=%d", loader.calls)
	}
}

func TestClassifyDetailFailureFallsBackToMatchedRoot(t *testing.T) {
	svc := newService(t, true)
	cfg := svc.Config()
	cfg.SelectedTemplate = TemplateRegion
	if _, err := svc.Update(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	decision, err := svc.Classify(context.Background(), classification.Request{
		MediaType: "movie",
		TMDBID:    "19995",
		Raw:       map[string]any{"id": 19995},
		Loader:    &detailLoader{err: errors.New("网络错误")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Applied || !decision.Matched || strings.Join(decision.RelativeSegments, "/") != "电影" {
		t.Fatalf("失败降级异常: %+v", decision)
	}
}

func TestClassifyMissingDetailFallsBackToMatchedRoot(t *testing.T) {
	svc := newService(t, true)
	cfg := svc.Config()
	cfg.SelectedTemplate = TemplateRegion
	if _, err := svc.Update(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	decision, err := svc.Classify(context.Background(), classification.Request{MediaType: "tv"})
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Matched || strings.Join(decision.RelativeSegments, "/") != "电视剧" {
		t.Fatalf("缺少 TMDB 详情时应落已识别的一级目录: %+v", decision)
	}
}

func TestBuiltInTemplateWithoutChildrenUsesMatchedRoot(t *testing.T) {
	svc := newService(t, true)
	cfg := svc.Config()
	cfg.SelectedTemplate = TemplateRegion
	cfg.Templates[1].Rules[1].Children = nil
	if _, err := svc.Update(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	decision, err := svc.Classify(context.Background(), classification.Request{
		MediaType: "tv",
		Raw:       map[string]any{"origin_country": []string{"CN"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Applied || !decision.Matched || strings.Join(decision.RelativeSegments, "/") != "电视剧" {
		t.Fatalf("内置模板二没有二级分类时应落已识别的一级目录: %+v", decision)
	}
}

func TestBuiltInTemplatesKeepFixedRootsAndFields(t *testing.T) {
	svc := newService(t, false)
	cfg := svc.Config()
	cfg.Templates[0].Rules[0].Name = "影片"
	cfg.Templates[1].Rules[1].Name = "剧集"
	if _, err := svc.Update(context.Background(), cfg); err != nil {
		t.Fatalf("内置模板应允许修改一级目录名称: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "删除一级分类", mutate: func(cfg *Config) {
			cfg.Templates[0].Rules = cfg.Templates[0].Rules[:1]
		}},
		{name: "添加一级分类", mutate: func(cfg *Config) {
			cfg.Templates[1].Rules = append(cfg.Templates[1].Rules, Rule{Name: "其它", Condition: "type=other"})
		}},
		{name: "修改一级条件", mutate: func(cfg *Config) {
			cfg.Templates[2].Rules[0].Condition = "genres=动作"
		}},
		{name: "地区模板使用类型字段", mutate: func(cfg *Config) {
			cfg.Templates[1].Rules[0].Children[0].Condition = "genres=剧情"
		}},
		{name: "类型模板使用地区字段", mutate: func(cfg *Config) {
			cfg.Templates[2].Rules[0].Children[0].Condition = "origin_country=CN"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := svc.Config()
			test.mutate(&cfg)
			if _, err := svc.Update(context.Background(), cfg); err == nil {
				t.Fatal("应拒绝破坏内置模板的固定结构或字段")
			}
		})
	}
}

func TestUpdateRejectsUnsafeDirectoryNameAndCondition(t *testing.T) {
	svc := newService(t, false)
	cfg := svc.Config()
	cfg.Templates[0].Rules[0].Name = "../电影"
	if _, err := svc.Update(context.Background(), cfg); err == nil {
		t.Fatal("应拒绝路径穿越目录名")
	}

	cfg = svc.Config()
	cfg.Templates[0].Rules[0].Condition = "type"
	if _, err := svc.Update(context.Background(), cfg); err == nil {
		t.Fatal("应拒绝无效匹配条件")
	}

	cfg = svc.Config()
	cfg.Templates[0].Rules[0].Children = []Rule{{Name: "非法二级", Condition: "genres=剧情"}}
	if _, err := svc.Update(context.Background(), cfg); err == nil {
		t.Fatal("影视分类应拒绝二级目录")
	}

	cfg = svc.Config()
	cfg.Templates[2].Rules[0].Children[1].Condition = "genres=动作;动画"
	if _, err := svc.Update(context.Background(), cfg); err == nil {
		t.Fatal("应拒绝同级重复匹配值")
	}
}
