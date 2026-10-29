package strmscrape

import (
	"context"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"litepan/internal/jav/emby/actormap"
	"litepan/internal/settings"
)

// 演员**资料层**：把番号墙上的演员补齐成一张「有生日、出生地、三围、头像」的档案。
//
// # 为什么需要它
//
// 侧车 / nfo 里只有 id、名字、性别、头像四样（`javwall_actors.go`）。而实测
// （2026-10-21，真实墙上的 135 位演员）：
//
//   - 侧车带头像的只有 8 位；
//   - TMDB 那边 16 位里只对上 6 位，而且**没有一个有生日 / 出生地 / 简介** ——
//     番号演员在 TMDB 基本只是名字占位（用户原话：「5211 上看不到有演员的，生日，
//     出生什么的呀」）。
//
// 所以补了两条**番号圈自己的**路子（用户要求「两条路合并」）：
//
//  1. **avbase**（`https://www.avbase.net/talents/<名字>`）：生日、出身地、身高、
//     三围、血型、爱好、男优标记，还有一个头像。页面是 Next.js，数据在
//     `__NEXT_DATA__` 那段 JSON 里。没有搜索接口 —— 只能拿名字当 slug 直接打，
//     404 就是没有这个人。
//  2. **JAVDB 演员搜索页**（匿名可看）：`/search?q=<名字>&f=actor&locale=zh`，
//     HTML 里有演员短 id、别名（`title` 属性）、头像地址。
//
// 再加两条**不花网络**的信息：
//
//  3. **索引表的 `href`**：`mapping_actor.xml` 里 8000 多条都带
//     `https://javdb.com/actors/z0GW`。**不打开那个页面**（要登录，匿名 302 →
//     /login），只用 id 拼头像地址 —— JAVDB 的头像就是
//     `https://c0.jdbstatic.com/avatars/<id 前两位小写>/<id>.jpg`。
//  4. TMDB 仍然是最后一层补充（见 javwall_actor_tmdb.go，那条路**单独一条请求**）。
//
// # 取哪一张头像
//
// 逐个候选**真的取一次**（走 `jav.FetchImage`，与浏览器之后走的是同一个代理、
// 同一套解码），第一个取得到的就用。理由：JAVDB 对没有头像的演员也给一个
// `actor_unknow.jpg` 占位图（200 + 图片），按「有没有 URL」判断会得到一张灰人像
// —— 那正是「看起来有头像、其实没有」。只有真取一次才分得清。
//
// 代价是每位演员第一次打开详情会多花几百毫秒到几秒。结果按 24h 缓存（见下面的
// `readJavActorProfile`），第二次打开不再打外站。
//
// # 别名从哪来
//
// 索引表的别名（中文统一名那张表）+ avbase 的 `actors[]` + JAVDB 搜索结果的
// `title` 里那一串。三边并集、去重、去主名。
const (
	// javActorProfileTTL 是「查到了」的缓存时长。
	javActorProfileTTL = 24 * time.Hour
	// javActorProfileErrorTTL 是「这次一个源都没接通」的短缓存。
	//
	// 与 TMDB 那层同一个理由：代理临时不通时按 24h 压住的话，修好代理之后
	// 还得等一天才能看到生日。
	javActorProfileErrorTTL = 10 * time.Minute
	// javActorProfileBudget 是一次资料查询的总预算。
	//
	// 四个源串行、每个都可能慢（avbase 单次实测 1~3 秒，JAVDB 搜索 1~2 秒，
	// 维基一次 1~2 秒），不留上限时一次详情能拖到十几秒。这块是补充资料，
	// 没有也能看片。
	//
	// 15 秒这个数：维基那一步（最多 3 个名字 + 一次中文条目）是「介绍」的唯一
	// 来源，排在 JAVDB 之前（见 lookJavActorProfile 里的顺序说明）。预算给紧了
	// 它就会被前面几步吃光，表现是「介绍时有时无」——用户那边看不出是超时。
	javActorProfileBudget = 15 * time.Second
	// javActorProfileMaxNames 是一次查询最多试几个名字（统一名 + 别名）。
	//
	// 4 这个数：实测「中文统一名 + 头三个别名」能覆盖绝大多数（日文原名常常在
	// 第二、三个），再多就是拿外站当搜索引擎刷了。
	javActorProfileMaxNames = 4
	// javActorProfileTimeout 是单个请求的超时。
	javActorProfileTimeout = 8 * time.Second
	// javdbActorAvatarBase 是 JAVDB 头像 CDN。
	//
	// ⚠️ 这个域名本机**直连会被重置**，但 `jav.FetchImage` 会把它换成
	// `tp.spfcas.com` 上去取（见 javdb.NormalizeListCover），所以前端照常走
	// `/api/jav/image` 就行。
	javdbActorAvatarBase = "https://c0.jdbstatic.com/avatars/"
	// javdbActorUnknownAvatar 是 JAVDB 给「没有头像的演员」的占位图。
	// 见到它就当没有（否则墙上会多出一排一模一样的灰人像）。
	javdbActorUnknownAvatar = "actor_unknow.jpg"
)

// JavWallActorProfile 是演员详情页那一段「人物档案」。
//
// 所有字段都可能空 —— 能查到什么显示什么，不报错。`Sources` 说明这份档案
// 是哪几个源拼出来的（界面上可以标一下，也方便用户判断该不该信）。
type JavWallActorProfile struct {
	// Name 是**上游给的名字**（avbase / JAVDB 那边的写法，通常是日文原名）。
	// 显示名仍然是卡片上那个中文统一名，这里只是让人对一眼。
	Name string `json:"name,omitempty"`
	// Aliases 是三个源并集后的别名（不含统一名本身）。
	Aliases []string `json:"aliases,omitempty"`
	// Gender 是性别码（1 = 男）。侧车没给时由 avbase 的 `note`（男優 / AV男優）补。
	Gender int `json:"gender,omitempty"`
	// AvatarURL 是**原始上游地址**，前端必须走 `/api/jav/image`
	// （JAVDB 的 CDN 直连不通、dmm 需要代理，都在那一层处理）。
	AvatarURL string `json:"avatar_url,omitempty"`
	// AvatarHDURL 是维基条目首图（960 宽，比 JAVDB 的 300×300 清楚一个量级）。
	//
	// 单独一个字段、**不并进 AvatarURL**：这张图 200KB 上下，而演员墙上一屏
	// 几十张卡片、每张只显示 120px —— 让列表去拉大图是白花流量。所以
	// `avatar_url` 仍旧是探针挑出来的那张小图（列表用），这一张只给**详情抽屉**
	// （那里 120px 的头像要看清脸，而且一次只开一个人）。
	//
	// 空串 = 维基没有条目 / 条目没有图；前端直接回落 `avatar_url`。
	AvatarHDURL string `json:"avatar_hd_url,omitempty"`

	Birthday   string `json:"birthday,omitempty"`
	Birthplace string `json:"birthplace,omitempty"`
	// Height 是身高（cm），0 = 不知道。
	Height int `json:"height,omitempty"`
	// Bust / Waist / Hip 是三围（cm），0 = 不知道。
	Bust  int `json:"bust,omitempty"`
	Waist int `json:"waist,omitempty"`
	Hip   int `json:"hip,omitempty"`
	// Cup 是罩杯（`J`、`E` 这种），空 = 不知道。
	Cup string `json:"cup,omitempty"`
	// BloodType / Hobby 是 avbase 的额外项，有就显示。
	BloodType string `json:"blood_type,omitempty"`
	Hobby     string `json:"hobby,omitempty"`
	// Note 是 avbase 的站点标签（`男優`、`舞ワイフ` 这类），原文照给。
	Note string `json:"note,omitempty"`
	// JavdbID 是 JAVDB 的演员短 id（`z0GW`）。给界面上「打开主页」用得上，
	// 也方便排查头像来源。
	JavdbID string `json:"javdb_id,omitempty"`
	// Intro / IntroLang 是维基导言那一段（**介绍**）与它的语言（`zh` / `ja`）。
	//
	// 中文条目存在时取中文那份（用户看中文），否则日文原文 —— 由 `IntroLang`
	// 告诉前端该怎么标（「日文原文」这类提示）。
	Intro     string `json:"intro,omitempty"`
	IntroLang string `json:"intro_lang,omitempty"`
	// WikiURL 是维基条目页（点「去维基看全文」用）。
	WikiURL string `json:"wiki_url,omitempty"`
	// Sources 是拼出这份档案的源，按命中顺序：`avbase` / `javdb` / `wikipedia` / `actormap`。
	Sources []string `json:"sources,omitempty"`
}

type javActorProfileEntry struct {
	info    *JavWallActorProfile
	expires time.Time
}

// ActorProfile 取一位演员的资料（生日 / 出身 / 三围 / 头像…）。
//
// 与 `ActorTMDB` 一样是**单独一条请求**：它要打外站，可能慢也可能不通，
// 不该拖着「他的名字和片」一起等。查不到就返回 nil（`data: null`，不是错误）。
func (s *Service) ActorProfile(ctx context.Context, key string) *JavWallActorProfile {
	card, _, err := s.javActorLookup(ctx, key)
	if err != nil {
		return nil
	}
	out := s.lookJavActorProfile(ctx, card)
	// 用户自传的头像**盖在缓存结果之上**（包括缓存为 nil 的那条：「全都没查到」
	// 按短 TTL 缓存了 nil，而这个人是自己传过头像的，不该也变 nil —— 前端会因此
	// 把墙上那张又换成占位图）。缓存里那份只代表外站那一层。
	//
	// ⚠️ 必须先**复制一份**再改：`lookJavActorProfile` 命中缓存时返回的是缓存里
	// 那个 `*JavWallActorProfile` 本身，就地改会把「自传头像」永久写进缓存 ——
	// 用户点「恢复默认」之后，24 小时内拿到的仍是删掉那张图的地址（404）。
	if card.CustomAvatar && strings.TrimSpace(card.AvatarURL) != "" {
		if out == nil {
			out = &JavWallActorProfile{Name: card.Name, Gender: card.Gender}
		} else {
			cp := *out
			out = &cp
		}
		out.AvatarURL = strings.TrimSpace(card.AvatarURL)
	}
	return out
}

// lookJavActorProfile 是资料层的主体：本地表 → 各源 → 合并 → 缓存。
func (s *Service) lookJavActorProfile(ctx context.Context, card JavWallActorCard) *JavWallActorProfile {
	names := javActorProfileNames(card, javActorProfileMaxNames)
	if len(names) == 0 {
		return nil
	}
	cacheKey := strings.ToLower(strings.Join(names, "\x00"))
	if hit, ok := s.readJavActorProfile(cacheKey); ok {
		return hit
	}

	ctx, cancel := context.WithTimeout(ctx, javActorProfileBudget)
	defer cancel()
	client := s.newActorProfileClient()

	out := &JavWallActorProfile{}
	// 本地那一层（索引表）：不花网络，先垫上。
	tbl := actormap.Default()
	if hit, ok := tbl.Lookup(card.Name); ok && hit != "" {
		out.JavdbID = tbl.JavdbID(hit)
		if len(out.JavdbID) > 0 {
			out.Sources = append(out.Sources, "actormap")
		}
	}
	// 侧车 / nfo 里已经带回来的头像：那是这套片子自己的数据，排第一。
	//
	// 用户自传的那张（`card.CustomAvatar`）**不进 candidates**，也不在下面任何
	// 一步被读：它是本机同源地址，不在 `/api/jav/image` 的白名单里，拿去
	// FetchImage 探针必然失败 —— 结果就是整条链把用户亲手选的那张跳过、换成外站
	// 的小图（正是这次要治的「两处不一样」）。它在最后单独顶掉返回值，不参与
	// 缓存（缓存只存外站那一层，见 ActorProfile）。
	candidates := make([]string, 0, 4)
	if v := strings.TrimSpace(card.AvatarURL); v != "" && !card.CustomAvatar {
		candidates = append(candidates, v)
	}

	// ① avbase：字段最全，一次请求拿到生日 / 出身 / 三围 / 别名 / 头像。
	var av *avbaseTalentInfo
	for _, name := range names {
		if ctx.Err() != nil {
			break
		}
		got, err := fetchAvbaseTalent(ctx, client, name)
		if err != nil || got == nil {
			continue
		}
		av = got
		break
	}
	if av != nil {
		out.Sources = append(out.Sources, "avbase")
		if strings.TrimSpace(out.Name) == "" {
			out.Name = av.Name
		}
		out.Birthday = av.Info["birthday"]
		out.Birthplace = av.Info["prefectures"]
		out.Height = atoiOr0(av.Info["height"])
		out.Bust = atoiOr0(av.Info["bust"])
		out.Waist = atoiOr0(av.Info["waist"])
		out.Hip = atoiOr0(av.Info["hip"])
		out.Cup = av.Info["cup"]
		out.BloodType = av.Info["blood_type"]
		out.Hobby = av.Info["hobby"]
		out.Note = av.Note
		if isMaleNote(av.Note) {
			out.Gender = 1
		}
		out.Aliases = append(out.Aliases, av.ActorNames...)
		if v := strings.TrimSpace(av.ImageURL); v != "" {
			candidates = append(candidates, v)
		}
	}

	// ② 维基百科：**介绍**（导言那一段）+ 一张**高清**首图（960 宽）。
	//
	// 用户 2026-10-22 问「演员还什么信息，介绍的吗？」「头像如有高清的更好」——
	// 前三个源都没有介绍，头像也都在 300 px 以内（dmm 那张只有 90×122）。
	// 入口用 avbase 给的维基标题（`meta.wikipedia`），没有才拿名字试；认人判据
	// 见 acceptJavActorWiki（标题逐字相等 + 导言里提到是演员）。
	//
	// ⚠️ 排在 JAVDB 之前，不是随手放的：整个预算 15 秒，JAVDB 那一步是**逐个
	// 名字**打搜索页（最多 4 次、每次 1~2 秒），放在前面很容易把预算吃光，
	// 于是「介绍」时有时无 —— 而介绍只有这一个源，JAVDB 那份只是补充别名与头像。
	wikiNames := make([]string, 0, 3)
	if av != nil && av.Wikipedia != "" {
		wikiNames = append(wikiNames, av.Wikipedia)
	}
	wikiNames = append(wikiNames, names...)
	if wiki := s.fetchJavActorWiki(ctx, client, wikiNames); wiki != nil {
		out.Sources = append(out.Sources, "wikipedia")
		out.Intro = wiki.Extract
		out.IntroLang = wiki.Lang
		out.WikiURL = wiki.URL
		// 高清图**不混进 candidates**：那张 960 宽的图 200KB 上下，而墙上的卡片
		// 只有 120px 宽、几十张一屏 —— 塞进列表就是白花流量。它单独走
		// `avatar_hd_url`，只有详情抽屉用（那里要看清楚）。
		//
		// 也**不在这里真取一次**：地址是维基 `pageimages` 给的（一定存在，
		// 不像 JAVDB 那种「格式对但 404」），而列表页的后台补头像也会打到这个
		// 接口 —— 每位多下 200KB，十几位就是几 MB。图挂了的兜底交给前端那条链
		// （见 JavWallActorDetailDrawer 的 photoChain）。
		out.AvatarHDURL = wiki.ImageURL
	}

	// ③ JAVDB 演员搜索（匿名可看）：别名 + 头像 + 短 id。
	var jd *javdbActorHit
	for _, name := range names {
		if ctx.Err() != nil {
			break
		}
		got, err := fetchJavdbActorHit(ctx, client, s.javdbSiteBase(), name)
		if err != nil || got == nil {
			continue
		}
		jd = got
		break
	}
	if jd != nil {
		out.Sources = append(out.Sources, "javdb")
		if strings.TrimSpace(out.Name) == "" {
			out.Name = jd.Name
		}
		if out.JavdbID == "" {
			out.JavdbID = jd.ID
		}
		out.Aliases = append(out.Aliases, jd.Aliases...)
		if isUsableActorAvatar(jd.Avatar) {
			candidates = append(candidates, jd.Avatar)
		}
	}

	// ④ 索引表里的 href 拼出来的头像（零网络，实测最稳的那批）。
	if out.JavdbID != "" {
		if u := javdbActorAvatarURL(out.JavdbID); u != "" {
			// 放在 avbase 之后：两边都有的演员，JAVDB 那张更常见是本人的正面照。
			candidates = append(candidates, u)
		}
	}

	out.Aliases = mergeActorAliases(card.Name, card.Aliases, out.Aliases)
	if out.Gender == 0 {
		out.Gender = card.Gender
	}
	out.AvatarURL = s.pickActorAvatar(ctx, candidates)

	// 一个字都没查到：按短 TTL 缓存，别让用户每次打开都再打一轮外站。
	if len(out.Sources) == 0 && out.AvatarURL == "" {
		ttl := javActorProfileErrorTTL
		if len(candidates) > 0 {
			// 有候选但一张都取不到：多半是网络 / 代理问题，同样短缓存。
			ttl = javActorProfileErrorTTL
		}
		s.writeJavActorProfile(cacheKey, nil, ttl)
		return nil
	}
	s.writeJavActorProfile(cacheKey, out, javActorProfileTTL)
	return out
}

// pickActorAvatar 逐个候选真取一次，返回第一个取得到的地址。
//
// 走 `s.jav.FetchImage`：与浏览器之后请求 `/api/jav/image` 是**同一条路**
// （同一个白名单、同一次域名替换、同一套 XOR 解码）—— 探针与真实展示必须同源，
// 否则会出现「探针说能取、浏览器拿到 403」。
//
// 拿不到 `jav` 服务（测试 / 未装配）时退回「第一个非空候选」：至少不会把地址丢掉。
func (s *Service) pickActorAvatar(ctx context.Context, candidates []string) string {
	seen := make(map[string]struct{}, len(candidates))
	uniq := candidates[:0]
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if _, dup := seen[c]; dup {
			continue
		}
		seen[c] = struct{}{}
		uniq = append(uniq, c)
	}
	if len(uniq) == 0 {
		return ""
	}
	if s.jav == nil {
		return uniq[0]
	}
	for _, c := range uniq {
		if ctx.Err() != nil {
			break
		}
		if _, _, err := s.jav.FetchImage(ctx, c); err == nil {
			return c
		}
	}
	return ""
}

// newActorProfileClient 构造资料层用的 HTTP 客户端。
//
// 与 TMDB 那条路同一套：代理跟随全局设置（`settings.ProxyURL`，看开关），
// 不开代理就直连。avbase / javdb 在这台机器上直连都不通，所以代理是常态。
func (s *Service) newActorProfileClient() *http.Client {
	transport := &http.Transport{
		MaxIdleConns:        8,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     60 * time.Second,
	}
	if s.settings != nil {
		if proxy := settings.ProxyURL(s.settings); proxy != "" {
			if pu, err := url.Parse(proxy); err == nil {
				transport.Proxy = http.ProxyURL(pu)
			}
		}
	}
	return &http.Client{Timeout: javActorProfileTimeout, Transport: transport}
}

// javdbSiteBase 是 JAVDB 官网地址（搜索页用）。
func (s *Service) javdbSiteBase() string {
	base := "https://javdb.com"
	if s.settings != nil {
		if v := strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyJavSiteBase)); v != "" {
			base = v
		}
	}
	return strings.TrimRight(base, "/")
}

// actorProfileUA 是抓 avbase / javdb 用的浏览器 UA。
//
// avbase 不带浏览器 UA 直接 403（实测），JAVDB 也要一个像浏览器的 UA。
const actorProfileUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/122.0 Safari/537.36"

// ————————————————————— avbase —————————————————————

// avbaseTalentInfo 是 avbase 演员页里我们关心的那些字段。
type avbaseTalentInfo struct {
	Name       string
	Note       string
	ImageURL   string
	Info       map[string]string
	ActorNames []string
	// Wikipedia 是 avbase 给的**日文维基条目标题**（`meta.wikipedia`，实测就是该演员
	// 在 ja.wikipedia 的条目名，常常与名字不完全一样 —— 例如「深田えいみ」）。
	//
	// 它是查维基介绍 / 高清人像最可靠的入口：比「拿名字硬打」准得多，
	// 也省掉一次同名不同人的风险。空串 = avbase 没给。
	Wikipedia string
}

// fetchAvbaseTalent 打 `https://www.avbase.net/talents/<名字>`。
//
// 没有搜索接口：名字当 slug 用，404 就是没有这个人（**不是错误**，返回 nil）。
// 名字里的空格 / 斜杠要转义（演员名有带空格的，如 `Abella Danger`）。
func fetchAvbaseTalent(ctx context.Context, client *http.Client, name string) (*avbaseTalentInfo, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil
	}
	u := "https://www.avbase.net/talents/" + url.PathEscape(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", actorProfileUA)
	req.Header.Set("Accept-Language", "ja,en;q=0.8")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &actorProfileHTTPError{Source: "avbase", Status: resp.StatusCode}
	}
	// 一页 Next.js 的 HTML 一两百 KB，给 4MB 上限。
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	return ParseAvbaseTalent(string(body)), nil
}

// ParseAvbaseTalent 从 avbase 演员页的 HTML 里取档案（纯函数，不碰网络）。
//
// 数据在 `<script id="__NEXT_DATA__" type="application/json">` 那段里，
// 形状：`props.pageProps.talent = {id, primary:{name,note,image_url},
// meta:{basic_info:{birthday,prefectures,height,bust,cup,waist,hip,blood_type,hobby}},
// actors:[{name}]}`。
//
// 认不出来（页面改版 / 不是演员页）返回 nil —— 调用方当作「这个源没有」，而不是报错：
// 演员资料是补充，少一个源不该让整页空掉。
func ParseAvbaseTalent(pageHTML string) *avbaseTalentInfo {
	raw := extractNextData(pageHTML)
	if raw == "" {
		return nil
	}
	var doc struct {
		Props struct {
			PageProps struct {
				Talent *struct {
					ID      int `json:"id"`
					Primary struct {
						Name     string `json:"name"`
						Note     string `json:"note"`
						ImageURL string `json:"image_url"`
					} `json:"primary"`
					Meta struct {
						BasicInfo map[string]json.RawMessage `json:"basic_info"`
						Wikipedia string                     `json:"wikipedia"`
					} `json:"meta"`
					Actors []struct {
						Name string `json:"name"`
					} `json:"actors"`
				} `json:"talent"`
			} `json:"pageProps"`
		} `json:"props"`
	}
	if json.Unmarshal([]byte(raw), &doc) != nil || doc.Props.PageProps.Talent == nil {
		return nil
	}
	t := doc.Props.PageProps.Talent
	if t.ID == 0 && strings.TrimSpace(t.Primary.Name) == "" {
		return nil
	}
	info := make(map[string]string, len(t.Meta.BasicInfo))
	for k, v := range t.Meta.BasicInfo {
		if s := jsonScalarString(v); s != "" {
			info[k] = s
		}
	}
	out := &avbaseTalentInfo{
		Name:      strings.TrimSpace(t.Primary.Name),
		Note:      strings.TrimSpace(t.Primary.Note),
		ImageURL:  strings.TrimSpace(t.Primary.ImageURL),
		Info:      info,
		Wikipedia: strings.TrimSpace(t.Meta.Wikipedia),
	}
	for _, a := range t.Actors {
		if n := strings.TrimSpace(a.Name); n != "" {
			out.ActorNames = append(out.ActorNames, n)
		}
	}
	return out
}

// extractNextData 取 `<script id="__NEXT_DATA__" ...>` 里的 JSON 正文。
//
// 不引 HTML 解析器：这一段的形态固定，一个正则就够；`.*?` 配 `(?s)`
// 在 200KB 的页面里也不会回溯爆炸（脚本块只有一个）。
func extractNextData(pageHTML string) string {
	m := reNextData.FindStringSubmatch(pageHTML)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

var reNextData = regexp.MustCompile(`(?s)<script id="__NEXT_DATA__" type="application/json">(.*?)</script>`)

// jsonScalarString 把 JSON 里的标量转成字符串（avbase 的 basic_info 有时是数字）。
func jsonScalarString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return strings.TrimSpace(n.String())
	}
	return ""
}

// ————————————————————— JAVDB 演员搜索 —————————————————————

// javdbActorHit 是 JAVDB 演员搜索结果里的一条。
type javdbActorHit struct {
	ID      string
	Name    string
	Avatar  string
	Aliases []string
}

// fetchJavdbActorHit 打 JAVDB 的演员搜索页。
//
// ⚠️ 走的是**搜索页**，不是 `/actors/<id>`：后者匿名访问会 302 到 /login
// （实测，带 `over18=1` cookie 也一样）。搜索页匿名可见，HTML 里就带着
// 演员短 id、别名（`title`）与头像地址 —— 足够我们要的三样。
//
// 结果里**必须逐条核对名字**：搜索是模糊的（搜「今井勇太」会带出一个
// `ジャイアント廣田　今井勇太`），认错了就会把别人的脸贴上去。
func fetchJavdbActorHit(ctx context.Context, client *http.Client, siteBase, name string) (*javdbActorHit, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil
	}
	u := siteBase + "/search?q=" + url.QueryEscape(name) + "&f=actor&locale=zh"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", actorProfileUA)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	// 站内语言 + 年龄确认：少了这两个 cookie，部分节点会先甩一个确认页。
	req.Header.Set("Cookie", "locale=zh; over18=1")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, &actorProfileHTTPError{Source: "javdb", Status: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	return PickJavdbActorHit(ParseJavdbActorSearch(string(body)), name), nil
}

// ParseJavdbActorSearch 解析演员搜索页的演员卡片（纯函数）。
//
// 每张卡的形态（实测）：
//
//	<div class="box actor-box">
//	  <a href="/actors/Azq9w" title="七咲琴乃, 今田美玲, 神田千佳, 沢北琴乃">
//	    <figure class="image"><img class="avatar" src="https://c0.jdbstatic.com/avatars/az/Azq9w.jpg" /></figure>
//	    <strong>七咲琴乃</strong>
//	  </a>
//	</div>
func ParseJavdbActorSearch(pageHTML string) []javdbActorHit {
	blocks := reJavdbActorBox.FindAllStringSubmatch(pageHTML, -1)
	out := make([]javdbActorHit, 0, len(blocks))
	for _, m := range blocks {
		block := m[1]
		href := reJavdbActorHref.FindStringSubmatch(block)
		if len(href) < 2 {
			continue
		}
		hit := javdbActorHit{ID: javdbIDFromHrefPath(href[1])}
		if len(href) > 2 {
			for _, alias := range strings.Split(html.UnescapeString(href[2]), ",") {
				if a := strings.TrimSpace(alias); a != "" {
					hit.Aliases = append(hit.Aliases, a)
				}
			}
		}
		if img := reJavdbActorImg.FindStringSubmatch(block); len(img) > 1 {
			hit.Avatar = html.UnescapeString(strings.TrimSpace(img[1]))
		}
		if name := reJavdbActorStrong.FindStringSubmatch(block); len(name) > 1 {
			// 上游会把名字缩进成多行，`(?s)` + 非贪婪已经处理，这里再去一次空白。
			hit.Name = html.UnescapeString(strings.Join(strings.Fields(name[1]), " "))
		}
		if hit.Name == "" {
			continue
		}
		out = append(out, hit)
	}
	return out
}

var (
	reJavdbActorBox    = regexp.MustCompile(`(?s)<div class="box actor-box">(.*?)</div>`)
	reJavdbActorHref   = regexp.MustCompile(`<a href="(/actors/[^"]+)"(?:\s+title="([^"]*)")?`)
	reJavdbActorImg    = regexp.MustCompile(`<img[^>]*src="([^"]*)"`)
	reJavdbActorStrong = regexp.MustCompile(`(?s)<strong>\s*([^<]*?)\s*</strong>`)
)

// PickJavdbActorHit 从搜索结果里挑「就是这个人」的那一条。
//
// 判据：显示的 `strong` 名字或 `title` 里的某个别名与查询名**逐字相等**
// （去首尾空白、忽略大小写）。没有就返回 nil —— 宁可没有头像，也不要把
// 别人的脸贴上来（`ジャイアント廣田　今井勇太` 那种「组合名」就是这么挡掉的）。
//
// 多条都命中时（同名不同人），优先**名字完全相同**的那条，其次头像可用的那条：
// JAVDB 里同名演员常常一条有头像一条是占位图。
func PickJavdbActorHit(hits []javdbActorHit, want string) *javdbActorHit {
	want = strings.ToLower(strings.TrimSpace(want))
	if want == "" {
		return nil
	}
	var exact, loose *javdbActorHit
	for i := range hits {
		hit := &hits[i]
		if !strings.EqualFold(strings.TrimSpace(hit.Name), want) {
			matched := false
			for _, a := range hit.Aliases {
				if strings.EqualFold(a, want) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
			if loose == nil || (!isUsableActorAvatar(loose.Avatar) && isUsableActorAvatar(hit.Avatar)) {
				loose = hit
			}
			continue
		}
		if exact == nil || (!isUsableActorAvatar(exact.Avatar) && isUsableActorAvatar(hit.Avatar)) {
			exact = hit
		}
	}
	if exact != nil {
		return exact
	}
	return loose
}

// javdbIDFromHrefPath 从 `/actors/Azq9w` 里取 `Azq9w`。
func javdbIDFromHrefPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "/actors/")
	return strings.Trim(path, "/")
}

// javdbActorAvatarURL 由演员短 id 拼头像地址。
//
// 目录是 **id 前两位的小写**（实测：`z0GW` → `/avatars/z0/z0GW.jpg`）。
// 不能把 id 原样小写后当前两位用 —— 那样 `z0GW` 会变成 `/z0/z0gw.jpg`，
// 而文件名的大小写是敏感的。
func javdbActorAvatarURL(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	if len(id) < 2 {
		return ""
	}
	return javdbActorAvatarBase + strings.ToLower(id[:2]) + "/" + id + ".jpg"
}

// isUsableActorAvatar 判断一个头像地址是不是「真的头像」。
func isUsableActorAvatar(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	return !strings.Contains(raw, javdbActorUnknownAvatar)
}

// isMaleNote 判断 avbase 的标签是不是「男优」。
func isMaleNote(note string) bool {
	note = strings.TrimSpace(note)
	return strings.Contains(note, "男優") || strings.Contains(note, "男优")
}

// ————————————————————— 合并 —————————————————————

// javActorProfileNames 返回查资料时依次试的名字：主名在前，其次别名。
func javActorProfileNames(card JavWallActorCard, limit int) []string {
	out := make([]string, 0, limit)
	seen := make(map[string]struct{}, limit)
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || len(out) >= limit {
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

// mergeActorAliases 把几处别名并起来：去重、去主名、保持先后（索引表在前）。
func mergeActorAliases(main string, lists ...[]string) []string {
	out := make([]string, 0, 16)
	seen := map[string]struct{}{strings.ToLower(strings.TrimSpace(main)): {}}
	for _, list := range lists {
		for _, a := range list {
			a = strings.TrimSpace(a)
			if a == "" {
				continue
			}
			key := strings.ToLower(a)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, a)
		}
	}
	return out
}

// atoiOr0 把 avbase 那种字符串数字转 int（`"158"`），转不了给 0。
func atoiOr0(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	// 上游偶尔写成 `158cm` / `158.0`，只取前导数字。
	end := 0
	for end < len(raw) && raw[end] >= '0' && raw[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0
	}
	n, err := strconv.Atoi(raw[:end])
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// actorProfileHTTPError 是资料层上游返回非 200 的错误（日志里能看出是哪个源）。
type actorProfileHTTPError struct {
	Source string
	Status int
}

func (e *actorProfileHTTPError) Error() string {
	return e.Source + " 返回 HTTP " + strconv.Itoa(e.Status)
}

// javdbAvatarFromIndex 由索引表里的 JAVDB id 拼一张头像地址。
//
// 用在**没有网络**的地方（演员墙列表、影片详情抽屉的演员格）：这两个位置一屏
// 几十张卡，不能为了头像去打外站。取不取得到由浏览器那一侧决定 —— 走
// `/api/jav/image`，取不到就落到 `MediaImage` 的占位图。
//
// 索引表里没有这位演员时返回空串（沿用 actormap「表里没有就原样」的一贯口径）。
func javdbAvatarFromIndex(unifiedName string) string {
	tbl := actormap.Default()
	if tbl == nil {
		return ""
	}
	unified, ok := tbl.Lookup(strings.TrimSpace(unifiedName))
	if !ok || unified == "" {
		return ""
	}
	return javdbActorAvatarURL(tbl.JavdbID(unified))
}

// ————————————————————— 缓存 —————————————————————
//
// 与 TMDB 那层同一套：键是「查过的那几个名字」，值带过期时间，读时判一次 ——
// 不起后台清理 goroutine，这个量级（一位演员一条）不值得。

func (s *Service) readJavActorProfile(key string) (*JavWallActorProfile, bool) {
	s.javActorProfileMu.Lock()
	defer s.javActorProfileMu.Unlock()
	entry, ok := s.javActorProfile[key]
	if !ok {
		return nil, false
	}
	if time.Now().After(entry.expires) {
		delete(s.javActorProfile, key)
		return nil, false
	}
	return entry.info, true
}

func (s *Service) writeJavActorProfile(key string, info *JavWallActorProfile, ttl time.Duration) {
	s.javActorProfileMu.Lock()
	defer s.javActorProfileMu.Unlock()
	if s.javActorProfile == nil {
		s.javActorProfile = make(map[string]javActorProfileEntry)
	}
	if ttl <= 0 {
		ttl = javActorProfileTTL
	}
	s.javActorProfile[key] = javActorProfileEntry{info: info, expires: time.Now().Add(ttl)}
}
