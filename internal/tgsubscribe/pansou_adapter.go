package tgsubscribe

import (
	"context"
	"strings"

	"litepan/internal/pansou"
)

// ⚠️ 盘搜客户端本身住在 internal/pansou —— 后台「搜索转存」页与订阅侧共用一份实现。
// 本文件只保留订阅侧的适配层。
//
// 为什么要这一层而不是让 pansou.Client 直接实现 webSearchClient：
//
//	webSearchClient.searchHits 是**私有方法**（接口与实现都在本包），这是给测试留的
//	注入口 —— 桩只需实现一个方法。pansou.Client 在别的包，不可能实现一个私有方法，
//	所以这里做一次 thin 转发。同时把 pansou.Hit 转成订阅侧一直用的 webHit，
//	下游（prepareWebRecord 等）不用改。
type pansouAdapter struct{ client *pansou.Client }

func newPansouAdapter(client *pansou.Client) *pansouAdapter {
	return &pansouAdapter{client: client}
}

func (a *pansouAdapter) searchHits(ctx context.Context, keyword string) ([]webHit, error) {
	hits, err := a.client.Search(ctx, keyword)
	if err != nil {
		return nil, err
	}
	return toWebHits(hits), nil
}

// toWebHits 丢掉 URL 为空的脏条目 —— URL 是下游唯一的去重键与提交内容。
func toWebHits(hits []pansou.Hit) []webHit {
	out := make([]webHit, 0, len(hits))
	for _, h := range hits {
		if url := strings.TrimSpace(h.URL); url != "" {
			out = append(out, webHit{
				URL:    url,
				Title:  strings.TrimSpace(h.Note),
				Source: strings.TrimSpace(h.Source),
			})
		}
	}
	return out
}
