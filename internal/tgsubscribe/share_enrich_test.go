package tgsubscribe

import (
	"context"
	"strings"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

// 补全的判据：只在**新的更像发布名**时才换名字，大小则照单全收。
//
// 这条保守规则的来历：现在记录里的名字来自正文首行（NameSource=text），
// 对很多频道来说那本来就是发布名，而且往往比分享标题更完整
// （分享标题常是「XX合集」「更新至第N集」这类文件夹名）。
func TestApplyPeekedMeta(t *testing.T) {
	cases := []struct {
		name         string
		raw          string
		size         int64
		meta         driver.SharePeekResult
		wantRaw      string
		wantSize     int64
		wantNameFrom string
	}{
		{
			name: "现有名字不像发布名 → 换成分享里的真名",
			raw:  "《某某》更新啦",
			meta: driver.SharePeekResult{
				FileName:  "测试剧 (2026) S01E03 2160p WEB-DL",
				TotalSize: 1234567,
			},
			wantRaw:      "测试剧 (2026) S01E03 2160p WEB-DL",
			wantSize:     1234567,
			wantNameFrom: "dn",
		},
		{
			name: "现有名字已经是好的发布名 → 保留，只补大小",
			raw:  "测试剧 (2026) S01E03 2160p WEB-DL DDP 5.1",
			meta: driver.SharePeekResult{
				FileName:  "测试剧.S01E03.2160p.mkv",
				TotalSize: 999,
			},
			wantRaw:      "测试剧 (2026) S01E03 2160p WEB-DL DDP 5.1",
			wantSize:     999,
			wantNameFrom: "text",
		},
		{
			name:         "分享标题是文件夹名、文件名为空 → 用标题，但它不像发布名时不换",
			raw:          "《某某》更新啦",
			meta:         driver.SharePeekResult{Title: "某某合集"},
			wantRaw:      "《某某》更新啦",
			wantSize:     0,
			wantNameFrom: "text",
		},
		{
			name:         "没有文件名但有大小 → 只补大小",
			raw:          "《某某》更新啦",
			meta:         driver.SharePeekResult{TotalSize: 500},
			wantRaw:      "《某某》更新啦",
			wantSize:     500,
			wantNameFrom: "text",
		},
		{
			name: "已经有大小就不覆盖",
			raw:  "《某某》更新啦",
			size: 111,
			meta: driver.SharePeekResult{FileName: "测试剧 (2026) S01E03 1080p", TotalSize: 222},
			// 名字仍会被换（现有名字不像发布名），但大小保持原值。
			wantRaw:      "测试剧 (2026) S01E03 1080p",
			wantSize:     111,
			wantNameFrom: "dn",
		},
	}

	for _, tc := range cases {
		rec := &domain.TGMatchRecord{
			RawName: tc.raw, NameSource: "text", SizeBytes: tc.size,
			Season: -1, Episode: -1, EpisodeEnd: -1,
		}
		applyPeekedMeta(rec, tc.meta)
		if rec.RawName != tc.wantRaw {
			t.Errorf("%s: RawName = %q, want %q", tc.name, rec.RawName, tc.wantRaw)
		}
		if rec.SizeBytes != tc.wantSize {
			t.Errorf("%s: SizeBytes = %d, want %d", tc.name, rec.SizeBytes, tc.wantSize)
		}
		if rec.NameSource != tc.wantNameFrom {
			t.Errorf("%s: NameSource = %q, want %q", tc.name, rec.NameSource, tc.wantNameFrom)
		}
	}
}

// 换名字时必须把解析结果一起重算 —— 否则记录会显示新片名、却带着旧名字解析出的季集，
// 剧集去重会跟着错。
func TestApplyPeekedMetaReparsesFields(t *testing.T) {
	rec := &domain.TGMatchRecord{
		RawName: "《某某》更新啦", NameSource: "text",
		Season: -1, Episode: -1, EpisodeEnd: -1,
	}
	applyPeekedMeta(rec, driver.SharePeekResult{
		FileName: "测试剧 (2026) S02E07 2160p WEB-DL H265",
	})
	if rec.ParsedTitle != "测试剧" {
		t.Errorf("ParsedTitle = %q", rec.ParsedTitle)
	}
	if rec.ParsedYear != 2026 {
		t.Errorf("ParsedYear = %d", rec.ParsedYear)
	}
	if rec.Season != 2 || rec.Episode != 7 {
		t.Errorf("季集 = S%dE%d，应当随新名字重算", rec.Season, rec.Episode)
	}
	if rec.Resolution != "2160p" {
		t.Errorf("Resolution = %q", rec.Resolution)
	}
}

// 偷看只针对 115 分享，且有条数上限 —— 它是真实接口调用，不封顶就是风控姿势。
func TestEnrichTargetsAreBoundedAndShareOnly(t *testing.T) {
	recs := make([]*domain.TGMatchRecord, 0, 20)
	for i := 0; i < 20; i++ {
		recs = append(recs, &domain.TGMatchRecord{
			ResourceKind: KindShare115,
			MagnetHash:   "115:code" + strings.Repeat("x", i%3),
			Magnet:       "https://115.com/s/abcdef123",
			Season:       -1, Episode: -1, EpisodeEnd: -1,
		})
	}
	// 一条磁力：不该被偷看（magnet 的名字本来就来自链接自带）。
	recs = append(recs, &domain.TGMatchRecord{
		ResourceKind: KindMagnet, MagnetHash: strings.Repeat("a", 40),
		Season: -1, Episode: -1, EpisodeEnd: -1,
	})

	// 用一个计数器桩统计实际偷看次数。
	peeker := &countingPeeker{}
	s := newShareServiceForTest(t, peeker, nil)
	s.enrichShareRecords(context.Background(), 1, recs)

	if peeker.calls > maxPeekPerFlush {
		t.Errorf("偷看 %d 次，超过上限 %d —— 那是真实的 115 接口调用", peeker.calls, maxPeekPerFlush)
	}
	if peeker.calls == 0 {
		t.Error("应当至少偷看一条")
	}
}

// 没配执行器 / 账号非法时整个跳过，不报错也不 panic。
func TestEnrichSkipsWithoutExecutor(t *testing.T) {
	s := &Service{}
	recs := []*domain.TGMatchRecord{{
		ResourceKind: KindShare115, MagnetHash: "115:abc",
		Season: -1, Episode: -1, EpisodeEnd: -1,
	}}
	s.enrichShareRecords(context.Background(), 1, recs) // 不 panic 即可
}

// countingPeeker 实现 Driver + ShareMetaPeeker，只数调用次数。
type countingPeeker struct{ calls int }

func (p *countingPeeker) Config() driver.Config      { return driver.Config{Name: "peek"} }
func (p *countingPeeker) GetAddition() any           { return nil }
func (p *countingPeeker) Init(context.Context) error { return nil }
func (p *countingPeeker) Drop(context.Context) error { return nil }
func (p *countingPeeker) Ping(context.Context) error { return nil }
func (p *countingPeeker) ListFiles(context.Context, string) ([]domain.FileItem, error) {
	return nil, nil
}
func (p *countingPeeker) ShareReceiveCapabilities() driver.ShareReceiveCapabilities {
	return driver.ShareReceiveCapabilities{Ready: true}
}
func (p *countingPeeker) ReceiveShare(context.Context, driver.ShareReceiveRequest) (driver.ShareReceiveResult, error) {
	return driver.ShareReceiveResult{}, nil
}
func (p *countingPeeker) PeekShare(context.Context, driver.SharePeekRequest) (driver.SharePeekResult, error) {
	p.calls++
	return driver.SharePeekResult{FileName: "测试剧 (2026) S01E01 2160p WEB-DL", TotalSize: 42}, nil
}
