package tgsubscribe

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/store"
)

// webSearchStub 是 webSearchClient 的桩，按关键词返回预设结果。
type webSearchStub struct {
	hits map[string][]webHit
	err  error
	// calls 记录每次实际搜的关键词，用来验「一轮搜索打了几次请求」。
	calls *[]string
}

func (s *webSearchStub) searchHits(_ context.Context, keyword string) ([]webHit, error) {
	if s.calls != nil {
		*s.calls = append(*s.calls, keyword)
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.hits[keyword], nil
}

// webHitFor 造一条磁力搜索结果。seed 派生 infohash —— 每个用例的每条结果都必须
// 有不同的指纹，否则会被 idx_tg_rec_sub_magnet 判成重复，那是去重的既有设计。
func webHitFor(title string, seed int64) webHit {
	return webHit{
		URL:    "magnet:?xt=urn:btih:" + fmt.Sprintf("%040x", seed),
		Title:  title,
		Source: "plugin:test",
	}
}

// newWebSearchServiceForTest 建一个带真库的服务，并注入搜索桩。
func newWebSearchServiceForTest(t *testing.T, client webSearchClient) (*Service, *store.Store) {
	t.Helper()
	s, st := newSearchServiceForTest(t, nil)
	s.webSearch = client
	return s, st
}

func listWebRecords(t *testing.T, st *store.Store, subID int64) []*domain.TGMatchRecord {
	t.Helper()
	rows, _, err := st.TGMatchRecords.List(context.Background(),
		domain.TGMatchRecordFilter{SubscriptionID: subID, Limit: 50})
	if err != nil {
		t.Fatalf("list records: %v", err)
	}
	return rows
}

// **手动**「搜网盘」命中的记录必须落成 ambiguous（待确认）且 channel_id=0（非 TG 来源）。
//
// 手动路径不能落 pending：那会进聚合窗口被自动推出去，而用户点这个按钮的意图
// 是「搜出来给我看看」，不是「搜出来直接下」。外部搜来的结果质量参差，正是不该
// 因为一次点击就自动推的那一类。
//
// 自动搜索是另一条路（ingestWebHitAuto）：它**会**落 pending 并 arm 窗口，
// 由 dispatchLoop 按画质选优后推送 —— 那是用户在设置页明确打开的，
// 且仍然受「自动推送」总开关与每小时上限约束。别把两者混起来。
func TestSearchWebLandsAsAmbiguous(t *testing.T) {
	stub := &webSearchStub{hits: map[string][]webHit{
		"生逢其时": {webHitFor("生逢其时 (2026) S01E05 2160p WEB-DL", 5001)},
	}}
	s, st := newWebSearchServiceForTest(t, stub)
	sub := seedSearchSub(t, st, "生逢其时", nil)

	res, err := s.SearchWeb(context.Background(), sub.ID)
	if err != nil {
		t.Fatalf("SearchWeb: %v", err)
	}
	if res.HitRecords != 1 {
		t.Fatalf("应落库 1 条，实际 %d（%s）", res.HitRecords, res.Message)
	}
	if res.HitsScanned != 1 {
		t.Errorf("HitsScanned = %d, want 1", res.HitsScanned)
	}

	rows := listWebRecords(t, st, sub.ID)
	if len(rows) != 1 {
		t.Fatalf("库里应有 1 条记录，实际 %d", len(rows))
	}
	rec := rows[0]
	if rec.Status != domain.TGRecordAmbiguous {
		t.Errorf("状态 = %q，网盘搜索必须落 ambiguous（否则会被自动推送）", rec.Status)
	}
	if rec.ChannelID != 0 {
		t.Errorf("ChannelID = %d，非 TG 来源必须是 0", rec.ChannelID)
	}
	if rec.MessageID != 0 {
		t.Errorf("MessageID = %d，搜索结果没有消息号", rec.MessageID)
	}
	if !strings.Contains(rec.ChatTitle, "网盘搜索") {
		t.Errorf("ChatTitle = %q，应当标出来源", rec.ChatTitle)
	}
	if !strings.Contains(rec.ChatTitle, "plugin:test") {
		t.Errorf("ChatTitle = %q，应当带上服务端标注的上游来源", rec.ChatTitle)
	}
	if !strings.Contains(rec.Reason, "网盘搜索") {
		t.Errorf("原因没说清来自网盘搜索: %q", rec.Reason)
	}
	// 片名解析要真的走通 —— 落库的字段是匹配历史里展示给人看的。
	if rec.RawName != "生逢其时 (2026) S01E05 2160p WEB-DL" {
		t.Errorf("RawName = %q，应当是搜索结果的标题", rec.RawName)
	}
	if rec.ParsedTitle != "生逢其时" || rec.Season != 1 || rec.Episode != 5 {
		t.Errorf("解析结果不对: title=%q season=%d episode=%d",
			rec.ParsedTitle, rec.Season, rec.Episode)
	}
	if rec.MatchScore < matchAcceptThreshold {
		t.Errorf("MatchScore = %.1f，这条应当稳稳命中", rec.MatchScore)
	}
	// 搜索结果自带标题字段，不该吃「名字取自正文」那 5 分惩罚。
	if rec.NameSource != "dn" {
		t.Errorf("NameSource = %q, want \"dn\"（note 是专门的标题字段，不是正文巧合）", rec.NameSource)
	}
}

// 指纹必须与 TG 来源**完全同一种格式**：40 位小写 hex、不带 magnet: 前缀。
//
// 这是去重唯一索引的契约。指纹一旦不一致（大写、带前缀、base32 没归一），
// 索引就失效 —— 同一个种子会被反复推送。
func TestSearchWebFingerprintMatchesTGRules(t *testing.T) {
	const upper = "946120C2D9DD252E3FCAAF3110D313A4F02E5D10"
	stub := &webSearchStub{hits: map[string][]webHit{
		"生逢其时": {{
			URL:   "magnet:?xt=urn:btih:" + upper,
			Title: "生逢其时 (2026) S01E05 2160p WEB-DL",
		}},
	}}
	s, st := newWebSearchServiceForTest(t, stub)
	sub := seedSearchSub(t, st, "生逢其时", nil)

	if _, err := s.SearchWeb(context.Background(), sub.ID); err != nil {
		t.Fatalf("SearchWeb: %v", err)
	}
	rows := listWebRecords(t, st, sub.ID)
	if len(rows) != 1 {
		t.Fatalf("库里应有 1 条记录，实际 %d", len(rows))
	}
	want := strings.ToLower(upper)
	if rows[0].MagnetHash != want {
		t.Fatalf("MagnetHash = %q, want %q（必须是小写裸 hex）", rows[0].MagnetHash, want)
	}
	if strings.Contains(rows[0].MagnetHash, ":") {
		t.Error("指纹带了前缀 —— 会让去重索引对不上升级前的历史行")
	}
}

// 搜到但没匹配上这条订阅的，一条都不该落库 —— 否则匹配历史会被无关结果刷满。
//
// 这里用的是真实场景：外部搜索引擎对陌生片名会回一堆只是名字沾边的结果。
func TestSearchWebSkipsUnrelatedHits(t *testing.T) {
	stub := &webSearchStub{hits: map[string][]webHit{
		"生逢其时": {webHitFor("完全无关的片子 (1999) 1080p BluRay", 6001)},
	}}
	s, st := newWebSearchServiceForTest(t, stub)
	sub := seedSearchSub(t, st, "生逢其时", nil)

	res, err := s.SearchWeb(context.Background(), sub.ID)
	if err != nil {
		t.Fatalf("SearchWeb: %v", err)
	}
	if res.HitRecords != 0 {
		t.Errorf("不该落库，实际落了 %d 条", res.HitRecords)
	}
	if res.HitsScanned != 1 {
		t.Errorf("HitsScanned = %d，搜到了还是要如实报数", res.HitsScanned)
	}
	if !strings.Contains(res.Message, "没有一条匹配上") {
		t.Errorf("提示没说清是「搜到了但没匹配上」: %q", res.Message)
	}
	if rows := listWebRecords(t, st, sub.ID); len(rows) != 0 {
		t.Errorf("库里不该有记录，实际 %d 条", len(rows))
	}
}

// 同一个磁力搜到两次只落一条 —— 第二次是唯一索引挡下来的，不是靠内存去重。
func TestSearchWebDeduplicatesAcrossRuns(t *testing.T) {
	stub := &webSearchStub{hits: map[string][]webHit{
		"生逢其时": {webHitFor("生逢其时 (2026) S01E05 2160p WEB-DL", 7001)},
	}}
	s, st := newWebSearchServiceForTest(t, stub)
	sub := seedSearchSub(t, st, "生逢其时", nil)
	ctx := context.Background()

	first, err := s.SearchWeb(ctx, sub.ID)
	if err != nil {
		t.Fatalf("第一次 SearchWeb: %v", err)
	}
	if first.HitRecords != 1 {
		t.Fatalf("第一次应落库 1 条，实际 %d", first.HitRecords)
	}

	second, err := s.SearchWeb(ctx, sub.ID)
	if err != nil {
		t.Fatalf("第二次 SearchWeb: %v", err)
	}
	if second.HitRecords != 0 {
		t.Errorf("第二次不该再落库，实际落了 %d 条", second.HitRecords)
	}
	if rows := listWebRecords(t, st, sub.ID); len(rows) != 1 {
		t.Errorf("库里应始终只有 1 条，实际 %d 条", len(rows))
	}
}

// 搜索失败只影响这一轮：如实报失败次数与**原因**，且一条垃圾都不落库。
//
// 原因必须透出来：超时/被限流要「等一会儿再点」，连不上站点则要查网络或代理，
// 用户能采取的动作完全不同。全糊成一句「搜索失败」等于什么都没说。
func TestSearchWebReportsFailuresWithoutStoringJunk(t *testing.T) {
	var calls []string
	stub := &webSearchStub{
		err:   &searchError{Reason: "站点返回 HTTP 403", Detail: "HTTP 403: blocked"},
		calls: &calls,
	}
	s, st := newWebSearchServiceForTest(t, stub)
	sub := seedSearchSub(t, st, "生逢其时", []string{"别名一"})

	res, err := s.SearchWeb(context.Background(), sub.ID)
	if err != nil {
		t.Fatalf("单个关键词失败不该让整轮报错: %v", err)
	}
	if res.FailedRequests != res.RequestCount {
		t.Errorf("FailedRequests = %d, want %d", res.FailedRequests, res.RequestCount)
	}
	if res.HitRecords != 0 {
		t.Errorf("失败时不该落库，实际 %d 条", res.HitRecords)
	}
	if len(calls) != res.RequestCount {
		t.Errorf("实际发起 %d 次请求，计划 %d 次", len(calls), res.RequestCount)
	}
	if !strings.Contains(res.Message, "都没成功") {
		t.Errorf("提示没说清是失败而不是没搜到: %q", res.Message)
	}
	if !strings.Contains(res.Message, "站点返回 HTTP 403") {
		t.Errorf("提示必须带上具体原因，否则用户不知道该怎么办: %q", res.Message)
	}
	if len(res.FailureReasons) != 1 || res.FailureReasons[0] != "站点返回 HTTP 403" {
		t.Errorf("FailureReasons = %v", res.FailureReasons)
	}
}

// 没带原因的普通错误要让位给兜底文案，而不是把 raw error 摊给用户。
func TestSearchWebUnknownFailureReason(t *testing.T) {
	stub := &webSearchStub{err: errors.New("something odd")}
	s, st := newWebSearchServiceForTest(t, stub)
	sub := seedSearchSub(t, st, "生逢其时", nil)

	res, err := s.SearchWeb(context.Background(), sub.ID)
	if err != nil {
		t.Fatalf("SearchWeb: %v", err)
	}
	if len(res.FailureReasons) != 1 || res.FailureReasons[0] != "未知错误" {
		t.Errorf("FailureReasons = %v, want [未知错误]", res.FailureReasons)
	}
	if strings.Contains(res.Message, "something odd") {
		t.Errorf("不该把原始错误摊进给用户看的消息: %q", res.Message)
	}
}

// 失败原因按出现次数排序、最多 3 条 —— 用户一眼看到最主要的问题。
func TestTopFailureReasonsOrderAndCap(t *testing.T) {
	got := topFailureReasons(map[string]int{
		"请求超时": 3, "站点返回 HTTP 400": 2, "连不上站点": 2,
		"返回的不是 JSON": 1, "站点报错": 1,
	}, 3)
	want := []string{"请求超时", "站点返回 HTTP 400", "连不上站点"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v（同次数要按字典序稳定排序）", got, want)
		}
	}
}

// 地址留空必须回落内置默认，**而且展示值与实际发请求的值必须是同一个**。
//
// 这条是有来历的：ConfigView 里写了回落、webSearcher 里漏了，结果界面上显示着
// 默认地址、客户端却拿着空串发请求 —— 用户看到的是「没有配置网盘搜索服务地址」，
// 而设置页面上明明有地址。两个读取点各写一份，就会这样漂移。
func TestWebSearchFallsBackToDefaultBaseURL(t *testing.T) {
	s, _ := newWebSearchServiceForTest(t, nil)
	ctx := context.Background()

	// 复现线上那份配置：开关打开、地址是空串。
	if err := s.UpdateConfig(ctx, ConfigInput{
		WebSearchEnabled:    true,
		WebSearchBaseURL:    "",
		WebSearchCloudTypes: "",
	}); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	baseURL, cloudTypes, _ := s.webSearchSettings()
	if baseURL != defaultWebBaseURL {
		t.Errorf("生效地址 = %q，留空应当回落 %q", baseURL, defaultWebBaseURL)
	}
	if cloudTypes != defaultWebCloudTypes {
		t.Errorf("生效类型 = %q，留空应当回落 %q", cloudTypes, defaultWebCloudTypes)
	}

	// 展示值必须与生效值一致 —— 否则用户照着界面看不出问题在哪。
	view := s.ConfigView(ctx)
	if view.WebSearchBaseURL != baseURL {
		t.Errorf("界面显示 %q 而实际用的是 %q：两处回落逻辑又漂移了",
			view.WebSearchBaseURL, baseURL)
	}
	if view.WebSearchCloudTypes != cloudTypes {
		t.Errorf("界面显示 %q 而实际用的是 %q", view.WebSearchCloudTypes, cloudTypes)
	}

	// 真正构造出来的客户端也必须拿着那个地址。
	client, ok := s.webSearcher().(*pansouClient)
	if !ok {
		t.Fatalf("应当构造出 pansouClient，实际 %T", s.webSearcher())
	}
	if client.baseURL != defaultWebBaseURL {
		t.Errorf("客户端 baseURL = %q，want %q", client.baseURL, defaultWebBaseURL)
	}
}

// 用户显式填了地址就以用户的为准（回落只在留空时发生）。
func TestWebSearchKeepsExplicitBaseURL(t *testing.T) {
	s, _ := newWebSearchServiceForTest(t, nil)
	if err := s.UpdateConfig(context.Background(), ConfigInput{
		WebSearchEnabled: true,
		WebSearchBaseURL: "https://pansou.example.com/",
	}); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}
	baseURL, _, _ := s.webSearchSettings()
	if baseURL != "https://pansou.example.com/" {
		t.Errorf("生效地址 = %q，显式配置不该被默认值盖掉", baseURL)
	}
}

// 默认类型必须同时含 magnet 与 115，**不能退回只搜磁力**。
//
// 这条是拿实测数据钉死的：那个站的国产内容里磁力占比只有 0%~4%
// （搜「生逢其时」204 条结果里磁力 0 条；搜「侠探杰克」148 条里 6 条）。
// 只搜 magnet 等于把 96%~100% 的结果扔掉，界面上表现就是「几乎永远搜不到」——
// 而这看起来像站点的问题，不像配置的问题，排查起来很费劲。所以直接钉住。
func TestDefaultCloudTypesIncludeNetdisk(t *testing.T) {
	types := strings.Split(defaultWebCloudTypes, ",")
	want := map[string]bool{"magnet": false, "115": false}
	for _, ty := range types {
		if _, ok := want[strings.TrimSpace(ty)]; ok {
			want[strings.TrimSpace(ty)] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("默认类型 %q 里缺 %q —— 只搜磁力会丢掉 96%%~100%% 的结果",
				defaultWebCloudTypes, name)
		}
	}

	// 而且必须只有这两类：其余类型没有投递器，收进来只会堆一屏「暂不支持投递」。
	if len(types) != len(want) {
		t.Errorf("默认类型 = %q，应当恰好是 magnet 与 115 两类", defaultWebCloudTypes)
	}
}

// 没打开开关时必须明确报错，而不是静默返回「没搜到」—— 后者会让人以为片源稀缺。
func TestSearchWebRequiresEnabled(t *testing.T) {
	s, st := newWebSearchServiceForTest(t, nil) // 不注入桩 = 走真实配置路径（默认关）
	sub := seedSearchSub(t, st, "生逢其时", nil)

	_, err := s.SearchWeb(context.Background(), sub.ID)
	if err == nil {
		t.Fatal("未启用时应当报错")
	}
	if !strings.Contains(err.Error(), "未启用") {
		t.Errorf("报错该指向开关: %v", err)
	}
}

// 订阅不存在时报 404 而不是空结果。
func TestSearchWebRejectsMissingSubscription(t *testing.T) {
	s, _ := newWebSearchServiceForTest(t, &webSearchStub{})
	if _, err := s.SearchWeb(context.Background(), 99999); err == nil {
		t.Fatal("订阅不存在时应当报错")
	}
}
