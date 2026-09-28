package settings

import (
	"context"
	"strings"
	"testing"

	"litepan/internal/announcement"
)

type memoryConfigRepo struct {
	values map[string]string
}

func (r *memoryConfigRepo) Get(_ context.Context, key string) (string, bool, error) {
	v, ok := r.values[key]
	return v, ok, nil
}

func (r *memoryConfigRepo) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}

func (r *memoryConfigRepo) All(context.Context) (map[string]string, error) {
	out := make(map[string]string, len(r.values))
	for key, value := range r.values {
		out[key] = value
	}
	return out, nil
}

func TestStringAllowEmptyDistinguishesUnsetAndExplicitEmpty(t *testing.T) {
	repo := &memoryConfigRepo{values: map[string]string{}}
	svc, err := New(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if got := svc.StringAllowEmpty(KeyQuarkTVProxyClients); got != "vidhub" {
		t.Fatalf("未设置时=%q，期望默认值 vidhub", got)
	}
	if err := svc.Update(context.Background(), map[string]string{KeyQuarkTVProxyClients: ""}); err != nil {
		t.Fatal(err)
	}
	if got := svc.StringAllowEmpty(KeyQuarkTVProxyClients); got != "" {
		t.Fatalf("显式清空后=%q，期望保留空值", got)
	}
	if got := svc.String(KeyQuarkTVProxyClients); got != "vidhub" {
		t.Fatalf("原 String 语义不应改变，实际=%q", got)
	}
	if got := svc.StringAllowEmpty(KeyFnosDirectSTRMClients); got != "Infuse" {
		t.Fatalf("飞牛直读客户端未设置时=%q，期望默认值 Infuse", got)
	}
	if err := svc.Update(context.Background(), map[string]string{KeyFnosDirectSTRMClients: ""}); err != nil {
		t.Fatal(err)
	}
	if got := svc.StringAllowEmpty(KeyFnosDirectSTRMClients); got != "" {
		t.Fatalf("飞牛直读客户端显式清空后=%q，期望保留空值", got)
	}
}

// 番号方案新增的设置项必须同时登记 Spec —— 否则 svc.Update 会以
// 「未知设置项」拒绝写入（读也会静默返回空串）。这里把这几个键钉住。
func TestJavOrganizeSettingKeysAreRegistered(t *testing.T) {
	repo := &memoryConfigRepo{values: map[string]string{}}
	svc, err := New(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{
		KeyMOJavRules,
		KeyMOJavDeleteSmall,
		KeyMOJavSmallFileMB,
		KeyMOJavCleanEmpty,
		KeyMOJavMaxDirs,
		KeyMOJavDeleteTypes,
		KeyMOJavDeleteExclude,
	}
	registered := map[string]struct{}{}
	for _, sp := range defaultSpecs() {
		registered[sp.Key] = struct{}{}
	}
	for _, key := range keys {
		if _, ok := registered[key]; !ok {
			t.Errorf("设置项 %q 未在 registry 登记 Spec，读写都会失败", key)
		}
	}

	// 写一遍确认不会被当成未知设置项
	if err := svc.Update(context.Background(), map[string]string{
		KeyMOJavDeleteTypes:   "txt;html",
		KeyMOJavDeleteExclude: "",
	}); err != nil {
		t.Fatalf("写入番号设置项失败：%v", err)
	}
	if got := svc.StringAllowEmpty(KeyMOJavDeleteTypes); got != "txt;html" {
		t.Errorf("可删除类型 = %q，期望 txt;html", got)
	}
	// 显式清空要保留空值（「都不填 = 满足大小就删」依赖这个语义）
	if got := svc.StringAllowEmpty(KeyMOJavDeleteExclude); got != "" {
		t.Errorf("排除表显式清空后 = %q，期望空串", got)
	}
}

// 番号墙的隐藏名单走 settings.Update 的**真实路径**（不只是注册表登记）——
// 这里的重点与别处不同：它要能存住 `[]`，而这正是「不能用空串表示空列表」那件事。
// 空串在本项目里是「没存过」（读侧据此回落分类规则的兜底目录名），
// 一旦 `[]` 被写成空串，用户取消全选后「未匹配」会自己冒回来。
func TestJavWallHiddenDirsRoundTripThroughService(t *testing.T) {
	repo := &memoryConfigRepo{values: map[string]string{}}
	svc, err := New(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	// 没存过：StringAllowEmpty 给空串（Spec.Default 是空串）
	if got := svc.StringAllowEmpty(KeyStrmJavWallHiddenDirs); got != "" {
		t.Fatalf("没存过时=%q，期望空串（读侧据此回落兜底目录名）", got)
	}
	// 存一份名单（前端照规范串回传）
	if err := svc.Update(context.Background(), map[string]string{
		KeyStrmJavWallHiddenDirs: `["未匹配"]`,
	}); err != nil {
		t.Fatalf("写入隐藏名单失败：%v", err)
	}
	if got := svc.StringAllowEmpty(KeyStrmJavWallHiddenDirs); got != `["未匹配"]` {
		t.Errorf("读回=%q，期望 [\"未匹配\"]", got)
	}
	// **`[]` 必须存得住**（= 一个都不隐藏）
	if err := svc.Update(context.Background(), map[string]string{
		KeyStrmJavWallHiddenDirs: "[]",
	}); err != nil {
		t.Fatalf("写入空名单失败：%v", err)
	}
	if got := svc.StringAllowEmpty(KeyStrmJavWallHiddenDirs); got != "[]" {
		t.Fatalf("空名单读回=%q，期望 []（存不下去的话用户取消全选后「未匹配」会自己冒回来）", got)
	}
	if dirs, ok := ParseJavWallHiddenDirs(svc.StringAllowEmpty(KeyStrmJavWallHiddenDirs)); !ok || len(dirs) != 0 {
		t.Errorf("空名单解析 = %v ok=%v，期望「存过、且一个都不藏」", dirs, ok)
	}
	// 乱序 + 重复要归一（前端拿前后两个串比「有没有改动」）
	if err := svc.Update(context.Background(), map[string]string{
		KeyStrmJavWallHiddenDirs: `["b","a","b"]`,
	}); err != nil {
		t.Fatal(err)
	}
	if got := svc.StringAllowEmpty(KeyStrmJavWallHiddenDirs); got != `["a","b"]` {
		t.Errorf("归一后=%q，期望 [\"a\",\"b\"]", got)
	}
}

// 公告地址必须是一个「可见、可写、默认回落内置地址」的普通设置项。
//
// 这里锁的是一个静默失效点：如果哪天给它加上 Hidden，它会从 /admin/settings 的返回里消失，
// 设置页上的「后台公告」卡片跟着没了，而且不报任何错；category 同理 —— 前端按它分卡片渲染，
// 改了一边另一边就默默不显示。
func TestAnnouncementURLSettingIsVisibleAndWritable(t *testing.T) {
	specs := defaultSpecs()
	var spec *Spec
	for i := range specs {
		if specs[i].Key == KeyAnnouncementURL {
			spec = &specs[i]
			break
		}
	}
	if spec == nil {
		t.Fatalf("设置项 %q 未在 registry 登记 Spec，读写都会失败", KeyAnnouncementURL)
	}
	if spec.Hidden {
		t.Errorf("%q 被标记为 Hidden，设置页上将不再出现该输入框", KeyAnnouncementURL)
	}
	if spec.Category != "announcement" {
		t.Errorf("category = %q，期望 announcement（SystemSettings.vue 按此分组渲染卡片）", spec.Category)
	}
	if spec.Default != announcement.DefaultURL {
		t.Errorf("默认值 = %q，期望内置默认地址 %q", spec.Default, announcement.DefaultURL)
	}

	repo := &memoryConfigRepo{values: map[string]string{}}
	svc, err := New(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}

	// 从没设置过 → 回落内置默认地址，出厂行为保持不变
	if got := svc.String(KeyAnnouncementURL); got != announcement.DefaultURL {
		t.Errorf("未设置时 = %q，期望回落 %q", got, announcement.DefaultURL)
	}

	// 能写入自定义地址（会被当成未知设置项拒绝的话，设置页保存就会失败）
	custom := "http://127.0.0.1:18080/a.json"
	if err := svc.Update(context.Background(), map[string]string{KeyAnnouncementURL: custom}); err != nil {
		t.Fatalf("写入公告地址失败：%v", err)
	}
	if got := svc.String(KeyAnnouncementURL); got != custom {
		t.Errorf("写入后 = %q，期望 %q", got, custom)
	}
}

// 未显式设置时，「不删除的文件类型」的默认值应当保护元数据扩展名。
func TestJavDeleteExcludeDefaultProtectsMetadata(t *testing.T) {
	repo := &memoryConfigRepo{values: map[string]string{}}
	svc, err := New(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	got := svc.StringAllowEmpty(KeyMOJavDeleteExclude)
	for _, ext := range []string{"nfo", "srt", "jpg"} {
		if !strings.Contains(got, ext) {
			t.Errorf("默认排除表应包含 %s，实际 %q", ext, got)
		}
	}
}
