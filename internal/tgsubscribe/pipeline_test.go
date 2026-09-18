package tgsubscribe

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/offlinedownload"
	"litepan/internal/settings"
	"litepan/internal/store"
	"litepan/internal/tgsubscribe/telegram"
)

// 这一组测试走**真实链路**：真实 Telegram 页面片段 → 真实抽取器 → 真实匹配 →
// 真实落库 → 投递器收到什么 URL。
//
// 它替代不了的只剩最后一步（把 URL 交给 115 网盘 API），那一步需要真账号；
// 但「我们到底会把这个链接原样发出去吗」这件事，在这里就能钉死。

// 实测自 t.me/s/QukanMovie 第 11140 条帖子：ed2k 写在正文的 <code> 块里
// （「资源链接 (点击复制)：」下面），preview 包会把它摊平成纯文本。
const realChannelED2KPost = `🎬 迪迦奥特曼 剧场版：最终圣战 (2000) 1080p｜REMUX｜SDR｜24fps｜H.264｜LPCM 2.0｜简中 PGS｜Primus｜已精简音轨及字幕
🧲 资源链接 (点击复制)：
ed2k://|file|%e8%bf%aa%e8%bf%a6%e5%a5%a5%e7%89%b9%e6%9b%bc%ef%bc%9a%e6%9c%80%e7%bb%88%e5%9c%a3%e6%88%98%20%282000%29%20-%201080p.REMUX.SDR.H.264.8-bit.23.976fps.LPCM%202.0-Primus.mkv|17023113474|4d517deece354c11fe7e497999956663|h=7cqi3wirkq7zqlkr75vqs22h6nksm6xo|/`

// 重建后应当长这样：名字里的 `｜` 与空格被严格 percent 编码，AICH 参数保留。
const wantRebuiltED2K = "ed2k://|file|%E8%BF%AA%E8%BF%A6%E5%A5%A5%E7%89%B9%E6%9B%BC" +
	"%EF%BC%9A%E6%9C%80%E7%BB%88%E5%9C%A3%E6%88%98%20%282000%29%20-%201080p.REMUX.SDR.H.264.8-bit." +
	"23.976fps.LPCM%202.0-Primus.mkv|17023113474|4d517deece354c11fe7e497999956663|h=7cqi3wirkq7zqlkr75vqs22h6nksm6xo|/"

// recordingDeliverer 记下投递器收到的请求 —— 这是「最终发出去的到底是什么」的观测点。
type recordingDeliverer struct {
	kinds []string

	mu   sync.Mutex
	reqs []DeliverRequest
}

func (d *recordingDeliverer) Supports(kind string) bool {
	for _, k := range d.kinds {
		if k == kind {
			return true
		}
	}
	return false
}

func (d *recordingDeliverer) Deliver(_ context.Context, req DeliverRequest) (DeliverResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reqs = append(d.reqs, req)
	return DeliverResult{TaskID: "task-1", ProviderKind: req.ProviderKind}, nil
}

func (d *recordingDeliverer) last() (DeliverRequest, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.reqs) == 0 {
		return DeliverRequest{}, false
	}
	return d.reqs[len(d.reqs)-1], true
}

// pipelineHarness 是一套真实依赖的服务：真库、真设置、真抽取器，
// 只有「网盘能力探测」与「投递器」是假的。
type pipelineHarness struct {
	svc      *Service
	deliver  *recordingDeliverer
	settings *settings.Service
}

func newPipelineHarness(t *testing.T, kinds ...string) *pipelineHarness {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, store.Options{Memory: true})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st := store.New(db)

	settingsSvc, err := settings.New(ctx, st.Configs)
	if err != nil {
		t.Fatalf("settings: %v", err)
	}

	svc := New(Options{
		Channels: st.TGChannels,
		Quality:  st.TGQualityProfiles,
		Subs:     st.TGSubscriptions,
		Episodes: st.TGSubscriptionEpisodes,
		Records:  st.TGMatchRecords,
		Settings: settingsSvc,
		Log:      slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{Level: slog.LevelError})),
	})
	// 115 的能力矩阵：ed2k 走原生离线。
	svc.prober = proberWith("magnet", "ed2k", "http", "https")
	deliver := &recordingDeliverer{kinds: kinds}
	svc.deliverers = []Deliverer{deliver}

	return &pipelineHarness{svc: svc, deliver: deliver, settings: settingsSvc}
}

func (h *pipelineHarness) seedChannel(t *testing.T) *domain.TGChannel {
	t.Helper()
	ch := &domain.TGChannel{
		ChatID: "-1002245898899", Username: "QukanMovie", Title: "115影视资源分享频道",
		Level: 10, Enabled: true, Status: domain.TGChannelStatusOK,
	}
	if _, err := h.svc.channels.Create(context.Background(), ch); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	got, err := h.svc.channels.GetByChatID(context.Background(), ch.ChatID)
	if err != nil {
		t.Fatalf("get channel: %v", err)
	}
	return got
}

func (h *pipelineHarness) seedSub(t *testing.T, title string, year int, mediaType string) *domain.TGSubscription {
	t.Helper()
	ctx := context.Background()
	sub := &domain.TGSubscription{
		TMDBID: "12345", MediaType: mediaType,
		Title: title, OriginalTitle: title, Year: year,
		Status: domain.TGSubStatusActive,
		// 推送目标直接写在订阅上，省掉对全局默认设置的依赖。
		TargetAccountID: 7, TargetDisplayPath: "/电影",
		PushProvider:     domain.TGPushProviderAuto,
		CollectWindowMin: 5, UpgradeEnabled: true,
	}
	// Create 只返回自增 id，不回填 sub.ID —— 必须用返回值去取。
	id, err := h.svc.subs.Create(ctx, sub)
	if err != nil {
		t.Fatalf("create sub: %v", err)
	}
	got, err := h.svc.subs.Get(ctx, id)
	if err != nil {
		t.Fatalf("get sub: %v", err)
	}
	return got
}

// processRealPost 把真实帖文喂给真实抽取器，再逐条走完整的匹配落库流程。
// 返回落库后的记录（按指纹索引）。
func (h *pipelineHarness) processRealPost(
	t *testing.T, ch *domain.TGChannel, text string, subs []*domain.TGSubscription,
) map[string]*domain.TGMatchRecord {
	t.Helper()
	ctx := context.Background()
	msg := &telegram.Message{MessageID: 11140, Text: text, Chat: telegram.Chat{ID: ch.ID}}

	refs := h.svc.registry.Extract(msg)
	if len(refs) == 0 {
		t.Fatal("真实帖文里一条资源都没抽到")
	}
	out := map[string]*domain.TGMatchRecord{}
	for _, ref := range refs {
		h.svc.processResource(ctx, ch, msg, resourceFromRef(ref, msg), subs)
		rows, _, err := h.svc.records.List(ctx, domain.TGMatchRecordFilter{ChannelID: ch.ID, Limit: 50})
		if err != nil {
			t.Fatalf("list records: %v", err)
		}
		for _, rec := range rows {
			out[rec.MagnetHash] = rec
		}
	}
	return out
}

// 端到端主链路：真实 ed2k 帖 → 抽链 → 匹配 → 落库 → 推送到投递器。
//
// 断言的重点是**发给网盘的 URL 是什么**：必须是重建过的链（名字严格 percent 编码、
// AICH 参数保留），而不是页面里的原串 —— 原串的编码质量参差，网盘接口会拒。
func TestPipelineRealED2KPostReachesDeliverer(t *testing.T) {
	h := newPipelineHarness(t, KindMagnet, KindED2K, KindHTTP)
	ch := h.seedChannel(t)
	sub := h.seedSub(t, "迪迦奥特曼：最终圣战", 2000, domain.TGMediaTypeMovie)

	records := h.processRealPost(t, ch, realChannelED2KPost, []*domain.TGSubscription{sub})
	rec, ok := records["ed2k:4d517deece354c11fe7e497999956663"]
	if !ok {
		t.Fatalf("没落库 ed2k 记录，实际拿到 %d 条：%v", len(records), records)
	}

	if rec.ResourceKind != KindED2K {
		t.Errorf("ResourceKind = %q, want %q", rec.ResourceKind, KindED2K)
	}
	if rec.Status != domain.TGRecordPending {
		t.Fatalf("状态 = %q（原因 %q），want pending", rec.Status, rec.Reason)
	}
	if rec.Magnet != wantRebuiltED2K {
		t.Errorf("落库的链接不是重建后的形态：\n got %q\nwant %q", rec.Magnet, wantRebuiltED2K)
	}
	if rec.SizeBytes != 17023113474 {
		t.Errorf("SizeBytes = %d, want 17023113474", rec.SizeBytes)
	}
	// 名字取自链接本身，标 dn → 不会被 matcher 扣 5 分。
	if rec.NameSource != "dn" {
		t.Errorf("NameSource = %q, want dn", rec.NameSource)
	}
	if rec.SubscriptionID != sub.ID {
		t.Errorf("没匹配上订阅：SubscriptionID = %d, want %d", rec.SubscriptionID, sub.ID)
	}
	if !strings.Contains(rec.RawName, "迪迦奥特曼") {
		t.Errorf("发布名不对：%q", rec.RawName)
	}

	// 走到投递。用 pushRecord 而不是 flushWindow —— 后者要先把 auto_push 打开、
	// 还要过每小时限流，那是另一条测试的事。
	if err := h.svc.pushRecord(context.Background(), sub, rec); err != nil {
		t.Fatalf("pushRecord: %v", err)
	}

	req, ok := h.deliver.last()
	if !ok {
		t.Fatal("投递器没有收到任何请求")
	}
	if req.Resource.Kind != KindED2K {
		t.Errorf("投递的类型 = %q, want %q", req.Resource.Kind, KindED2K)
	}
	if req.Resource.Raw != wantRebuiltED2K {
		t.Errorf("发给网盘的链接不对：\n got %q\nwant %q", req.Resource.Raw, wantRebuiltED2K)
	}
	// 115 声明支持 ed2k，所以走原生离线，不该降级到内置下载器。
	if req.ProviderKind != offlinedownload.ProviderNative {
		t.Errorf("通道 = %q, want %q", req.ProviderKind, offlinedownload.ProviderNative)
	}
	if req.AccountID != 7 || req.TargetDisplayPath != "/电影" {
		t.Errorf("投递目标不对：account=%d path=%q", req.AccountID, req.TargetDisplayPath)
	}
	// 目录名用订阅的「片名 (年份)」，保证洗版前后落到同一个子目录。
	if req.FileName != "迪迦奥特曼：最终圣战 (2000)" {
		t.Errorf("离线任务名 = %q", req.FileName)
	}
}

// 同一条帖子里，投不了的分享链要落 unsupported 且**不进聚合窗口**；
// 能投的 ed2k 正常进窗口。这是实测 QukanMovie 的真实混合形态。
func TestPipelineRealMixedPostSeparatesDeliverable(t *testing.T) {
	h := newPipelineHarness(t, KindMagnet, KindED2K, KindHTTP) // 没有分享转存投递器
	ch := h.seedChannel(t)
	sub := h.seedSub(t, "迪迦奥特曼：最终圣战", 2000, domain.TGMediaTypeMovie)

	// 实测形态：正文里既有 115 分享链（text_link 实体），也有 ed2k（code 块）。
	text := "🎬 迪迦奥特曼 剧场版：最终圣战 (2000) 1080p\n" +
		"🔗 链接： 点击跳转\n" +
		"🧲 资源链接 (点击复制)：\n" +
		strings.TrimPrefix(realChannelED2KPost, "🎬 迪迦奥特曼 剧场版：最终圣战 (2000) 1080p｜REMUX｜SDR｜24fps｜H.264｜LPCM 2.0｜简中 PGS｜Primus｜已精简音轨及字幕\n🧲 资源链接 (点击复制)：\n")
	msg := &telegram.Message{
		MessageID: 11141,
		Text:      text,
		Chat:      telegram.Chat{ID: ch.ID},
		Entities: []telegram.MessageEntity{{
			Type: "text_link", Offset: 0, Length: 4,
			URL: "https://115cdn.com/s/swsa2t23zrk?password=t58d",
		}},
	}

	ctx := context.Background()
	refs := h.svc.registry.Extract(msg)
	if len(refs) != 2 {
		t.Fatalf("应抽到 2 条资源（分享 + ed2k），实际 %d：%+v", len(refs), refs)
	}
	for _, ref := range refs {
		h.svc.processResource(ctx, ch, msg, resourceFromRef(ref, msg), []*domain.TGSubscription{sub})
	}

	byKind := map[string]*domain.TGMatchRecord{}
	rows, _, err := h.svc.records.List(ctx, domain.TGMatchRecordFilter{ChannelID: ch.ID, Limit: 50})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, rec := range rows {
		byKind[rec.ResourceKind] = rec
	}

	share := byKind[KindShare115]
	if share == nil {
		t.Fatal("分享链没落库")
	}
	if share.Status != domain.TGRecordUnsupported {
		t.Errorf("分享链状态 = %q（原因 %q），want unsupported", share.Status, share.Reason)
	}
	if !strings.Contains(share.Reason, "115 分享") {
		t.Errorf("原因没说清类型：%q", share.Reason)
	}
	// 被识别但没有投递器的记录不该被拉进窗口 —— 否则订阅会被反复唤醒。
	if !share.NextRetryAt.IsZero() {
		t.Errorf("unsupported 不该有重试时间：%v", share.NextRetryAt)
	}

	ed2k := byKind[KindED2K]
	if ed2k == nil {
		t.Fatal("ed2k 没落库")
	}
	if ed2k.Status != domain.TGRecordPending {
		t.Errorf("ed2k 状态 = %q（原因 %q），want pending", ed2k.Status, ed2k.Reason)
	}
}

// 磁力链路不能被这轮改动碰坏：同一套真实流程跑一条实测过的磁力帖。
//
// 用剧集订阅而不是电影：实拍频道的 SxxEyy 发布名遇到电影订阅会被 typeCompatible
// 直接判死，那是既有行为，不是这轮要验的东西。
func TestPipelineRealMagnetPostUnchanged(t *testing.T) {
	h := newPipelineHarness(t, KindMagnet, KindED2K, KindHTTP)
	ch := h.seedChannel(t)
	sub := h.seedSub(t, "生逢其时", 2026, domain.TGMediaTypeTV)

	const hash = "ce5ef90c7cf08c9a1902a5e2a73362da32ae3418"
	text := "📺 生逢其时 (2026) S01E15 ✨4K WEB-DL DDP 5 1\n" +
		"magnet:?xt=urn:btih:" + hash + "&dn=生逢其时.2026.S01E15.2160p.WEB-DL"

	records := h.processRealPost(t, ch, text, []*domain.TGSubscription{sub})
	rec, ok := records[hash]
	if !ok {
		t.Fatalf("磁力记录没落库：%v", records)
	}
	if rec.ResourceKind != KindMagnet {
		t.Errorf("ResourceKind = %q, want %q", rec.ResourceKind, KindMagnet)
	}
	// 指纹必须保持裸 hex —— 加了前缀会让升级前的历史行与新值不等，去重失效。
	if rec.MagnetHash != hash {
		t.Errorf("磁力指纹被改了：%q", rec.MagnetHash)
	}
	if rec.Status != domain.TGRecordPending {
		t.Fatalf("状态 = %q（原因 %q），want pending", rec.Status, rec.Reason)
	}
	// 磁力的 dn= 仍然算「链接自带的名字」，标 dn、不被扣分。
	if rec.NameSource != "dn" {
		t.Errorf("NameSource = %q, want dn", rec.NameSource)
	}

	if err := h.svc.pushRecord(context.Background(), sub, rec); err != nil {
		t.Fatalf("pushRecord: %v", err)
	}
	req, ok := h.deliver.last()
	if !ok {
		t.Fatal("投递器没收到请求")
	}
	if req.Resource.Kind != KindMagnet || req.ProviderKind != offlinedownload.ProviderNative {
		t.Errorf("磁力应走原生离线：kind=%q provider=%q", req.Resource.Kind, req.ProviderKind)
	}
	if !strings.Contains(req.Resource.Raw, hash) {
		t.Errorf("磁力链接不对：%q", req.Resource.Raw)
	}
}
