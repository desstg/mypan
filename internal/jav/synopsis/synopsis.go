// Package synopsis 按番号去别的站补「剧情简介」。
//
// 起因：JAVDB 的 `summary` 在用户的库里**大面积是空的**（8574 部里只有 435 部有，5%），
// 而这批片子的 nfo 里「剧情简介」就永远空着。核实过不是被谁冲掉的 —— 侧车、
// `jav_movies` 列、`raw_json` 三处一致，是上游本来就没给。所以要像磁链那样多家互补。
//
// 实测（2026-09-27，本机）能连上、且真的带剧情简介的只有两家：
//
//	jav321       有码       日文简介在 col-md-12 的正文段里
//	caribbeancom 无码 / 素人  片站自己的页面，日文简介在 <p> 里
//
// 其余都试过并否掉：DMM 撞年龄确认墙、JAVBUS 与 avbase 根本没有简介、
// JavLibrary 与 mgstage 直接 403、avmoo/avsox 跳到一个连不上的域名。
//
// **拿到的都是日文** —— 与 JAVDB 那 435 部的原文一致，不是偏差。要中文得另外接翻译，
// 不在这一版的范围内（`Source` 接口留了口子）。
package synopsis

import (
	"context"
	"strings"
)

// Source 是一个「按番号拿剧情简介」的来源。
type Source interface {
	Name() string
	// Fetch 按番号取简介。
	//
	// **取不到不是错误**：这站没有这部、页面结构变了、搜索出来的是别的片 ——
	// 都返回空串与 nil，让调用方安静地试下一家。只有真正的传输失败（超时、HTTP 5xx）
	// 才返回 error，好让调用方决定要不要退避。
	Fetch(ctx context.Context, number string) (text string, err error)
}

// Result 是一次查找的结果。
type Result struct {
	Text   string
	Source string
}

// Lookup 按顺序试一组来源，返回第一个非空的。
//
// 「按顺序」是有意的：有码那批先问 jav321（快、覆盖好），无码那批 jav321 必然空、
// 下一个才轮到 caribbeancom。不并行：这些站都有反爬，同一部片连打两家既慢又容易被盯上，
// 而顺序试的代价只是「第一家常空」多花 0.5 秒。
//
// 某一家报错不中断（记在 lastErr 里），与磁链那边「一边挂了不该把另一边一起丢掉」同一条规矩。
func Lookup(ctx context.Context, number string, sources ...Source) (Result, error) {
	number = strings.TrimSpace(number)
	if number == "" {
		return Result{}, nil
	}
	var lastErr error
	for _, src := range sources {
		if src == nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		text, err := src.Fetch(ctx, number)
		if err != nil {
			lastErr = err
			continue
		}
		if text = strings.TrimSpace(text); text != "" {
			return Result{Text: text, Source: src.Name()}, nil
		}
	}
	return Result{}, lastErr
}

// ————————————————————— 补字段（不止简介）—————————————————————

// FieldPatch 是「从别的站补来的字段」。**只放 JAVDB 缺的** —— 调用方负责判断。
type FieldPatch struct {
	Summary string
	// TitleZH 是**中文标题**（missav / airav 这类站给的是中文）。
	//
	// 与其它字段不同，它**不是「补缺」而是「另存」**：JAVDB 给的 title 大面积是日文，
	// 而用户要的是「生成 nfo 时能直接取中文标题」—— 所以两行并存，谁也不是谁的替补。
	// 调用方据此写进 `title_zh`，**不覆盖 `title`**。
	TitleZH     string
	ReleaseDate string
	DurationMin int
	Director    string
	Maker       string
	Tags        []string
	Actors      []string
	CoverURL    string
	// Source 是这些字段来自哪家（jav321 / caribbeancom / javbus）。
	Source string
	// Filled 列出这次实际填了哪些字段（给日志与记账用）。
	Filled []string
}

// Empty 报告这份补丁什么都没带。
func (p FieldPatch) Empty() bool { return len(p.Filled) == 0 }

// Missing 描述「这部片缺什么」——调用方据此决定要不要去补。
//
// 时长与导演只有 javbus 有；简介两家有。缺什么都不影响这一次查找：
// 能补到几项就补几项。
type Missing struct {
	Summary bool
	// TitleZH 与其它项不同：它不是「缺了才要」，而是「没有中文标题就要」。
	// 判断在调用方（库里 title_zh 为空，且现有 title 不是中文）。
	TitleZH     bool
	ReleaseDate bool
	Duration    bool
	Director    bool
	Maker       bool
	Actors      bool
	Tags        bool
	Cover       bool
}

// Any 报告有没有任何一项值得去补。
func (m Missing) Any() bool {
	return m.Summary || m.TitleZH || m.ReleaseDate || m.Duration || m.Director || m.Maker ||
		m.Actors || m.Tags || m.Cover
}

// Enricher 是能补字段的来源。
type Enricher interface {
	Name() string
	// Enrich 按番号补字段。**只返回取到的**，不管调用方缺什么 ——
	// 「要不要用这一项」由调用方按 Missing 决定（同名字段以 JAVDB 为准，绝不反向覆盖）。
	Enrich(ctx context.Context, number string) (FieldPatch, error)
}

// Enrich 依次问每一家，把**缺的那几项**填上；已有的字段一个都不动。
//
// 顺序即优先级：先问 jav321（有码覆盖面最好），它没有的再问 caribbeancom（无码/素人），
// 最后 javbus（补导演/时长/类别这些前两家没有的）。
//
// 某一家报错不中断（与磁链那条并集同一个规矩）。**不做字段级的"两家合并"**：
// 只在某一项**当前为空**时才填，否则会把上游已有的好数据换成更差的。
func Enrich(ctx context.Context, number string, missing Missing, sources ...Enricher) (FieldPatch, error) {
	number = strings.TrimSpace(number)
	if number == "" || !missing.Any() {
		return FieldPatch{}, nil
	}
	var patch FieldPatch
	var lastErr error
	seen := map[string]bool{}
	fill := func(name, value string) {
		if value == "" || seen[name] {
			return
		}
		seen[name] = true
		patch.Filled = append(patch.Filled, name)
	}
	for _, src := range sources {
		if src == nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			return patch, err
		}
		got, err := src.Enrich(ctx, number)
		if err != nil {
			lastErr = err
			continue
		}
		if patch.Source == "" && !got.Empty() {
			patch.Source = src.Name()
		}
		if missing.Summary && patch.Summary == "" && got.Summary != "" {
			patch.Summary = got.Summary
			fill("summary", got.Summary)
		}
		// 中文标题与「补缺」的字段不同：它是**另存**（现有 title 不动），
		// 所以判据只是「还没有，且这家给了」。
		if missing.TitleZH && patch.TitleZH == "" && got.TitleZH != "" {
			patch.TitleZH = got.TitleZH
			fill("title_zh", got.TitleZH)
		}
		if missing.ReleaseDate && patch.ReleaseDate == "" && got.ReleaseDate != "" {
			patch.ReleaseDate = got.ReleaseDate
			fill("release_date", got.ReleaseDate)
		}
		if missing.Duration && patch.DurationMin == 0 && got.DurationMin > 0 {
			patch.DurationMin = got.DurationMin
			fill("duration", "duration")
		}
		if missing.Director && patch.Director == "" && got.Director != "" {
			patch.Director = got.Director
			fill("director", got.Director)
		}
		if missing.Maker && patch.Maker == "" && got.Maker != "" {
			patch.Maker = got.Maker
			fill("maker", got.Maker)
		}
		if missing.Tags && len(patch.Tags) == 0 && len(got.Tags) > 0 {
			patch.Tags = got.Tags
			fill("tags", "tags")
		}
		if missing.Actors && len(patch.Actors) == 0 && len(got.Actors) > 0 {
			patch.Actors = got.Actors
			fill("actors", "actors")
		}
		if missing.Cover && patch.CoverURL == "" && got.CoverURL != "" {
			patch.CoverURL = got.CoverURL
			fill("cover", got.CoverURL)
		}
		if !patch.AnyMissing(missing) {
			break // 缺的都补上了，不必再打扰后面的站
		}
	}
	return patch, lastErr
}

// AnyMissing 报告按 missing 看还有没有没补上的项。
func (p FieldPatch) AnyMissing(m Missing) bool {
	return (m.Summary && p.Summary == "") ||
		(m.TitleZH && p.TitleZH == "") ||
		(m.ReleaseDate && p.ReleaseDate == "") ||
		(m.Duration && p.DurationMin == 0) ||
		(m.Director && p.Director == "") ||
		(m.Maker && p.Maker == "") ||
		(m.Tags && len(p.Tags) == 0) ||
		(m.Actors && len(p.Actors) == 0) ||
		(m.Cover && p.CoverURL == "")
}
