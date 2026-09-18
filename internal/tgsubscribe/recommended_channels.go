package tgsubscribe

import (
	"context"
	"strings"
)

// 推荐频道。
//
// 这份清单不是拍脑袋来的：2026-09-17 用真实抓取实测了 pansou（另一个网盘搜索项目）
// 内置的 110 个频道里最像的 28 个，**用 LitePan 自己的抽取器**跑每频道两页（40 条帖子），
// 只留下「抽得出来、而且投得出去」的那几个。
//
// 判据是**投递能力**，不是「频道好不好」—— 这是关键。实测结论：
//
//	夸克分享  300 条 ← 绝大多数频道发的是这个，但 LitePan 只识别不投递
//	115 分享  125 条 ← 能自动转存（需账号配网页 Cookie）
//	磁力        5 条 ← 能投，但只有 vip115hot 一个频道有
//	ed2k        1 条 ← 只有 QukanMovie 有
//
// 所以像 Quark_Movies / baicaoZY / PanjClub / Aliyun_4K_Movies 这些频道虽然活跃、
// 链接也多，**一条都推不出去** —— 收录进来只会让匹配历史里堆满「暂不支持投递」。
//
// 另外两个实测坑，写在下面 Summary 里提醒用户，别让他们以为是 LitePan 坏了：
//   - Lsp115：正文只有简介，真正的链接藏在 telegra.ph 中转页后面
//   - oneonefivewpfx：正文链接全指向 re0.me 中转站
type RecommendedChannel struct {
	// Username 是裸用户名，不带 @（与 TGChannel.Username 同口径）。
	Username string
	// Remark 是建议的备注名 —— 直接填进「备注」字段，用户想改就改。
	Remark string
	// Summary 说明这个频道发什么、以及它需要什么前提，展示在选择列表里。
	Summary string
}

// recommendedChannels 是内置推荐清单，顺序即展示顺序（按实测的可用度排）。
var recommendedChannels = []RecommendedChannel{
	{
		Username: "Channel_Shares_115",
		Remark:   "115 分享合集",
		Summary:  "几乎全是 115 分享链（实测两页 40 条帖子 / 40 条链接，零噪声）。需要 115 账号配网页 Cookie 才能自动转存。",
	},
	{
		Username: "gimy115iso",
		Remark:   "115 影视 ISO",
		Summary:  "同样是纯净的 115 分享链（实测 40/40），以原盘 ISO 为主。需要网页 Cookie。",
	},
	{
		Username: "QukanMovie",
		Remark:   "115 影视资源",
		Summary:  "115 分享为主，偶尔有 ed2k（实测 40 条帖子里 33 条 115 分享 + 1 条 ed2k）。115 分享需网页 Cookie；ed2k 走 115 原生离线。",
	},
	{
		Username: "vip115hot",
		Remark:   "115 + 磁力混合",
		Summary:  "实测是这批里唯一带磁力的频道（40 条帖子里 5 条磁力、9 条 115 分享，另有 32 条夸克分享——夸克目前只能识别、不能投递）。",
	},
}

// RecommendedChannels 返回内置推荐频道清单（不含「是否已添加」这类用户态信息）。
//
// ⚠️ 这份清单**不是**「给用户挑选的候选」—— 它们首次启动就已经自动入库成了普通频道
// （见 SeedRecommendedChannels）。这个导出函数只服务于测试与播种逻辑本身。
func RecommendedChannels() []RecommendedChannel {
	out := make([]RecommendedChannel, len(recommendedChannels))
	copy(out, recommendedChannels)
	return out
}

// RecommendedChannelView 是「播种时没加成功、仍可手动补加」的推荐频道。
type RecommendedChannelView struct {
	Username string `json:"username"`
	Remark   string `json:"remark"`
	Summary  string `json:"summary"`
	// Added 报告当前用户的频道列表里是不是已经有这个用户名。
	// 有了就不该出现在补加清单里（补加后前端会重新拉一次，这一条随即消失）。
	Added bool `json:"added"`
}

// ListRecommendedChannels 返回**还能补加**的推荐频道。
//
// 正常情况下这个列表是空的：内置推荐频道首次启动就已经自动入库了
// （见 SeedRecommendedChannels），它们就是频道列表里的普通行。
//
// 唯一还有内容的情况是那次播种没成功（新装实例还没配代理）—— 这时
// tg_recommended_pending 里记着是哪几条没加上，这里按它筛。
//
// **不能反过来用「推荐清单里没有它」来筛**：用户自己删掉的默认频道同样不在
// 频道列表里，那样筛会把删掉的东西立刻变成一行「未添加 · 补加」，
// 看起来像是删除没生效。
func (s *Service) ListRecommendedChannels(ctx context.Context) ([]RecommendedChannelView, error) {
	pending := s.pendingRecommendedUsernames()
	out := make([]RecommendedChannelView, 0, len(pending))
	if len(pending) == 0 {
		return out, nil
	}

	// 只看用户名判重（不按 chat_id）：推荐清单里写的就是用户名，
	// 而用户可能用 `https://t.me/xxx` 这种带前缀的形式添加过 ——
	// 落到库里的 Username 都是裸名，所以比对是准的。
	existing := map[string]struct{}{}
	if s != nil && s.channels != nil {
		rows, err := s.channels.List(ctx, false)
		if err != nil {
			return nil, err
		}
		for _, ch := range rows {
			if name := strings.ToLower(strings.TrimSpace(ch.Username)); name != "" {
				existing[name] = struct{}{}
			}
		}
	}

	for _, rec := range recommendedChannels {
		name := strings.ToLower(rec.Username)
		if _, ok := pending[name]; !ok {
			continue
		}
		_, added := existing[name]
		out = append(out, RecommendedChannelView{
			Username: rec.Username,
			Remark:   rec.Remark,
			Summary:  rec.Summary,
			Added:    added,
		})
	}
	return out, nil
}
