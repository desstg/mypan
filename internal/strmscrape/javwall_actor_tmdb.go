package strmscrape

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/jav/emby/actormap"
)

// 演员墙详情页的**补充资料**：TMDB 那边的人档案。
//
// # 为什么可以去问 TMDB
//
// 番号墙的演员信息来自侧车 / nfo —— 只有 id、名字、性别、头像四样。而 TMDB 的
// `/person` 有简介、生日、出生地、别名。番号演员（尤其是欧美那批）很多在 TMDB
// 里也有档案，能补一段简介与生平，详情页才不像一张只有名字的卡片。
//
// # 但它是**补充**，不是数据源
//
// 演员**有没有这个人、演过哪些片**仍然只看墙上的影片（用户明确要求）。这里只做
// 「点开某位演员的详情时，顺手看看 TMDB 有没有同名的档案」，取不到就整块不显示 ——
// 绝不因为 TMDB 有一部片、墙上一部都没有，就往详情页里塞片子。
//
// # 怎么对上人
//
// 番号演员是 JAVDB 的 id，TMDB 是它自己的 id，**没有可靠的映射**。只能按名字查：
// 先按统一名（索引表里的中文名）查，再按别名（日文原名常常在别名里）查。
// 每一条结果都要**核对名字**（`name` / `original_name` / `also_known_as` 任一命中
// 才算），否则「同名不同人」会把别人的生平贴上来 —— 那比没有简介糟得多。
const (
	javActorTMDBTTL = 24 * time.Hour
	// 一个人最多试几次搜索：统一名 + 别名，再多就是拿 TMDB 当搜索引擎刷了。
	javActorTMDBMaxQueries = 3
	// javActorTMDBBudget 是一次详情查询在 TMDB 上最多能花多久。
	//
	// ⚠️ 必须有这个上限：代理不通时 TMDB 客户端单次就是 20s、还带 2 次重试 ——
	// 3 个名字能把这条请求拖到三分钟。这块是补充资料，没有也能看片，宁可放弃。
	javActorTMDBBudget = 6 * time.Second
	// javActorTMDBErrorTTL 是「这次没查成」的短缓存。
	//
	// 与「查过了、TMDB 确实没这个人」（按 24h 缓存）分开：网络 / 代理临时不通时也按
	// 24h 压住的话，修好代理之后还得等一天才能看到简介。
	javActorTMDBErrorTTL = 10 * time.Minute
)

// JavWallActorTMDB 是详情页那一段「TMDB 档案」。
type JavWallActorTMDB struct {
	ID int `json:"id"`
	// Name 是 TMDB 那边的名字（可能与我们显示的统一名不同）。
	Name string `json:"name"`
	// ProfilePath 是 TMDB 的图片路径（`/abc.jpg`），**不是完整地址** ——
	// 前端要拼 `actor/photo?path=…` 由后端代理（image.tmdb.org 国内直连通不了）。
	ProfilePath  string   `json:"profile_path,omitempty"`
	Birthday     string   `json:"birthday,omitempty"`
	Deathday     string   `json:"deathday,omitempty"`
	PlaceOfBirth string   `json:"place_of_birth,omitempty"`
	KnownFor     string   `json:"known_for_department,omitempty"`
	Biography    string   `json:"biography,omitempty"`
	AlsoKnownAs  []string `json:"also_known_as,omitempty"`
}

type javActorTMDBEntry struct {
	info    *JavWallActorTMDB
	expires time.Time
}

// actorNames 返回用来查 TMDB 的名字候选：统一名在前，其次别名（去重、限 3 个）。
func actorNames(card JavWallActorCard) []string {
	out := make([]string, 0, javActorTMDBMaxQueries)
	seen := make(map[string]struct{}, javActorTMDBMaxQueries)
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || len(out) >= javActorTMDBMaxQueries {
			return
		}
		key := strings.ToLower(name)
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		out = append(out, name)
	}
	add(card.Name)
	for _, a := range card.Aliases {
		add(a)
	}
	return out
}

// lookJavActorTMDB 查一位演员的 TMDB 档案（带 24h 内存缓存）。
//
// 没有 TMDB 密钥、网络不通、查不到人 —— 一律返回 nil，详情页那一段就不出现。
// **不报错**：详情页其余内容照常显示，缺一段补充资料不是失败。
func (s *Service) lookJavActorTMDB(ctx context.Context, card JavWallActorCard) *JavWallActorTMDB {
	names := actorNames(card)
	if len(names) == 0 {
		return nil
	}
	key := strings.ToLower(strings.Join(names, "\x00"))
	if hit, ok := s.readJavActorTMDB(key); ok {
		return hit
	}
	client := s.newTMDBClient()
	if client == nil {
		return nil
	}
	// 整条查询共用一个预算（见 javActorTMDBBudget）：这是同步接口，不能让它跑满
	// 三次 20s+重试。
	ctx, cancel := context.WithTimeout(ctx, javActorTMDBBudget)
	defer cancel()

	var found *JavWallActorTMDB
	// queried 表示至少有一条上游**成功**回来的（哪怕是「零结果」）；
	// 全都失败在网络上时，缓存只能算短期的临时结论。
	queried := false
	for _, name := range names {
		if ctx.Err() != nil {
			break
		}
		results, err := client.SearchPerson(ctx, name)
		if err != nil {
			continue
		}
		queried = true
		if len(results) == 0 {
			continue
		}
		id := pickJavActorPerson(results, names)
		if id <= 0 {
			continue
		}
		raw, err := client.LookupPerson(ctx, id)
		if err != nil {
			continue
		}
		if info := parseJavActorPerson(raw); info != nil {
			found = info
			break
		}
	}
	ttl := javActorTMDBTTL
	if found == nil && !queried {
		// 一次都没问成：多半是代理 / 密钥的问题，短缓存，修好后别等一天。
		ttl = javActorTMDBErrorTTL
	}
	s.writeJavActorTMDB(key, found, ttl)
	return found
}

// pickJavActorPerson 从搜索结果里挑「就是这个人」的那一条。
//
// 判据：结果的 `name` / `original_name` / `also_known_as` 里**有任何一个**与我们
// 手上的候选名逐字相等（忽略大小写与首尾空白）。没有就返回 0 —— 宁可没有档案，
// 也不要把同名另一个人的简介贴上来。
func pickJavActorPerson(results []json.RawMessage, candidates []string) int {
	want := make(map[string]struct{}, len(candidates))
	for _, c := range candidates {
		if key := strings.ToLower(strings.TrimSpace(c)); key != "" {
			want[key] = struct{}{}
		}
	}
	match := func(name string) bool {
		_, ok := want[strings.ToLower(strings.TrimSpace(name))]
		return ok
	}
	for _, raw := range results {
		var p struct {
			ID           int      `json:"id"`
			Name         string   `json:"name"`
			OriginalName string   `json:"original_name"`
			AlsoKnownAs  []string `json:"also_known_as"`
		}
		if json.Unmarshal(raw, &p) != nil || p.ID <= 0 {
			continue
		}
		if match(p.Name) || match(p.OriginalName) {
			return p.ID
		}
		for _, alias := range p.AlsoKnownAs {
			if match(alias) {
				return p.ID
			}
		}
	}
	return 0
}

// parseJavActorPerson 解 `/person/{id}` 的正文。简介为空时也算拿到（页面只显示有的字段）。
func parseJavActorPerson(raw json.RawMessage) *JavWallActorTMDB {
	var p struct {
		ID                 int      `json:"id"`
		Name               string   `json:"name"`
		ProfilePath        string   `json:"profile_path"`
		Birthday           string   `json:"birthday"`
		Deathday           string   `json:"deathday"`
		PlaceOfBirth       string   `json:"place_of_birth"`
		KnownForDepartment string   `json:"known_for_department"`
		Biography          string   `json:"biography"`
		AlsoKnownAs        []string `json:"also_known_as"`
	}
	if json.Unmarshal(raw, &p) != nil || p.ID <= 0 {
		return nil
	}
	if strings.TrimSpace(p.Name) == "" {
		return nil
	}
	out := &JavWallActorTMDB{
		ID:           p.ID,
		Name:         strings.TrimSpace(p.Name),
		ProfilePath:  strings.TrimSpace(p.ProfilePath),
		Birthday:     strings.TrimSpace(p.Birthday),
		Deathday:     strings.TrimSpace(p.Deathday),
		PlaceOfBirth: strings.TrimSpace(p.PlaceOfBirth),
		KnownFor:     strings.TrimSpace(p.KnownForDepartment),
		Biography:    strings.TrimSpace(p.Biography),
	}
	// 别名去重（TMDB 那边会重复列同一个名字的不同写法）。
	seen := make(map[string]struct{}, len(p.AlsoKnownAs))
	for _, a := range p.AlsoKnownAs {
		a = strings.TrimSpace(a)
		if a == "" || strings.EqualFold(a, out.Name) {
			continue
		}
		if _, dup := seen[strings.ToLower(a)]; dup {
			continue
		}
		seen[strings.ToLower(a)] = struct{}{}
		out.AlsoKnownAs = append(out.AlsoKnownAs, a)
	}
	return out
}

// ————————————————————— 缓存 —————————————————————
//
// 键是「查过的那几个名字」拼起来（与 card 的 id 无关：同一个人在两侧车里的 id
// 可能不同，而查 TMDB 靠的本来就是名字）。值里带过期时间，读时判一次 ——
// 不起后台清理 goroutine，这个量级（一位演员一条、几十字节）不值得。

func (s *Service) readJavActorTMDB(key string) (*JavWallActorTMDB, bool) {
	s.javActorTMDBMu.Lock()
	defer s.javActorTMDBMu.Unlock()
	entry, ok := s.javActorTMDB[key]
	if !ok {
		return nil, false
	}
	if time.Now().After(entry.expires) {
		delete(s.javActorTMDB, key)
		return nil, false
	}
	// 命中的是「查过但没有」时也返回 true —— 否则每次打开详情都去打一遍 TMDB。
	return entry.info, true
}

func (s *Service) writeJavActorTMDB(key string, info *JavWallActorTMDB, ttl time.Duration) {
	s.javActorTMDBMu.Lock()
	defer s.javActorTMDBMu.Unlock()
	if s.javActorTMDB == nil {
		s.javActorTMDB = make(map[string]javActorTMDBEntry)
	}
	if ttl <= 0 {
		ttl = javActorTMDBTTL
	}
	s.javActorTMDB[key] = javActorTMDBEntry{info: info, expires: time.Now().Add(ttl)}
}

// ————————————————————— 别名（索引表） —————————————————————

// applyActorUnifiedName 把索引表里的**中文统一名**套到卡片上，并带回别名。
//
// 用户要求（2026-10-21）：演员详情页的主名显示索引表里的中文名，别名一并列出来。
// 索引表里没有这位演员时**原样不动**（与 actormap 一贯的「表里没有就原样返回」一致）。
func applyActorUnifiedName(card *JavWallActorCard) {
	tbl := actormap.Default()
	if tbl == nil || card == nil {
		return
	}
	unified, ok := tbl.Lookup(card.Name)
	if !ok || strings.TrimSpace(unified) == "" {
		return
	}
	// 原名不是统一名时也留在别名里 —— 那是用户最可能在侧车里看到的那一个写法。
	aliases := tbl.Aliases(unified)
	seen := make(map[string]struct{}, len(aliases)+1)
	for _, a := range aliases {
		seen[strings.ToLower(a)] = struct{}{}
	}
	if !strings.EqualFold(strings.TrimSpace(card.Name), unified) {
		if _, dup := seen[strings.ToLower(strings.TrimSpace(card.Name))]; !dup && strings.TrimSpace(card.Name) != "" {
			aliases = append(aliases, strings.TrimSpace(card.Name))
		}
	}
	card.Name = unified
	if len(aliases) > 0 {
		card.Aliases = aliases
	}
}

// FetchJavActorPhoto 取一张 TMDB 人像（`profile_path` 由前端带回来）。
//
// 走 TMDB 客户端（带全局代理设置），与海报墙取 TMDB 图是同一条出口 ——
// image.tmdb.org 国内浏览器经常打不开，所以**必须**由后端代理。
//
// 失败一律用**非 500** 的业务错误（路径不对 / 没配密钥 / 上游没这张图）：
// 前端那张图本来就有占位图兜底，报 500 只会在日志里刷一片红，还让人以为服务坏了。
func (s *Service) FetchJavActorPhoto(ctx context.Context, path, size string) ([]byte, string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, "", domain.Errorf(domain.CodeValidation, "缺少图片路径")
	}
	// 只认 TMDB 的图片路径。这条参数**不能**变成「替服务器打任意地址」的入口。
	if !strings.HasPrefix(path, "/") {
		return nil, "", domain.Errorf(domain.CodeValidation, "图片路径格式不正确")
	}
	client := s.newTMDBClient()
	if client == nil {
		return nil, "", domain.Errorf(domain.CodeValidation, "未配置 TMDB API Key")
	}
	data, err := client.DownloadImage(ctx, path, normalizeActorPhotoSize(size))
	if err != nil {
		return nil, "", domain.Wrap(domain.CodeNotFound, err)
	}
	return data, "image/jpeg", nil
}

// normalizeActorPhotoSize 只放行 TMDB 认识的那几档宽度 —— 原样透传等于让调用方
// 拿 `original` 去拉一张几 MB 的人像。
func normalizeActorPhotoSize(raw string) string {
	switch strings.TrimSpace(raw) {
	case "w45", "w185", "h632", "w300":
		return strings.TrimSpace(raw)
	case "w500":
		// 人像用不到 500 —— 降到 300，体积差 3 倍而头像最大也就显示 120 px。
		return "w300"
	default:
		return "w185"
	}
}
