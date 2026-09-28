package store

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// 迁移 0036：放宽库里那条**内容相同**的分类规则的 pattern。
//
// 用内存库构造三种库状态，逐个验。真库形态（规则名被用户改过）也要覆盖 ——
// 那正是**按 pattern 文本而不是按规则名**匹配的理由。
//
// ⚠️ oldPattern 是**两个反斜杠**：库里存的是 JSON 文本，JSON 里 `\d` 要写成 `\\d`。
// 写成一个的话 JSON 本身就非法，而迁移的 `instr` 也会匹配不到 —— 那正是这条迁移
// 最容易踩的地方，所以这里刻意用原始字符串把它摆明。
// newPattern 则是**解出来之后**的形态（一个反斜杠），因为断言比的是解码后的值。
func TestMigrationWidensJapanPattern(t *testing.T) {
	const oldPattern = `^[A-Za-z]{2,6}-\\d{2,5}`         // JSON 文本形态
	const newPattern = `^[A-Za-z]{2,6}-[A-Za-z]?\d{2,5}` // 解码后形态（断言比的值）
	// 种进库里的字符串**本身就是 JSON 文本**，所以新形态也要写成两个反斜杠 ——
	// 写成一个的话 JSON 解析当场失败（`\d` 不是合法转义），而报错只提
	// 「malformed JSON」，看不出是哪一条规则。这个坑与迁移里那个是同一个。
	const newPatternJSON = `^[A-Za-z]{2,6}-[A-Za-z]?\\d{2,5}`

	t.Run("规则名被改过也认得出（按 pattern 匹配）", func(t *testing.T) {
		db := newTestDB(t)
		seedRules(t, db, `{"classify_rules":[
			{"name":"欧美","target_name":"欧美","mode":"includes","includes":["TUSHY"]},
			{"name":"有码","target_name":"有码","mode":"pattern","pattern":"`+oldPattern+`"},
			{"name":"未匹配","target_name":"未匹配","mode":"nocode"}
		]}`)
		if err := db.Migrate(context.Background()); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		if got := patternOf(t, db, "有码"); got != newPattern {
			t.Errorf("pattern = %q，期望 %q", got, newPattern)
		}
	})

	t.Run("没有那条 pattern 时什么都不做", func(t *testing.T) {
		db := newTestDB(t)
		seedRules(t, db, `{"classify_rules":[
			{"name":"欧美","target_name":"欧美","mode":"includes","includes":["TUSHY"]},
			{"name":"未匹配","target_name":"未匹配","mode":"nocode"}
		]}`)
		before := mustValue(t, db)
		if err := db.Migrate(context.Background()); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		if after := mustValue(t, db); after != before {
			t.Errorf("不该改动这条规则：\n前 %s\n后 %s", before, after)
		}
	})

	t.Run("用户已经自己放宽过则不重复改", func(t *testing.T) {
		db := newTestDB(t)
		seedRules(t, db, `{"classify_rules":[
			{"name":"有码","target_name":"有码","mode":"pattern","pattern":"`+newPatternJSON+`"}
		]}`)
		before := mustValue(t, db)
		if err := db.Migrate(context.Background()); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		if after := mustValue(t, db); after != before {
			t.Errorf("已经是新形态，不该再动：\n前 %s\n后 %s", before, after)
		}
	})
}

// 迁移 0037：给「国产」补上**无连字符**形态的分类规则（`MD0292` / `DA-72`…）。
//
// 三种库状态都要验：正常补上、已经补过（幂等）、用户把「国产」删了（不动）。
func TestMigrationAddsChineseNoHyphenRule(t *testing.T) {
	const ruleName = "国产·无连字符"

	t.Run("正常补上，且兜底仍排它后面", func(t *testing.T) {
		db := newTestDB(t)
		seedRules(t, db, `{"classify_rules":[
			{"name":"欧美","target_name":"欧美","mode":"includes","includes":["TUSHY"]},
			{"name":"国产","target_name":"国产","mode":"includes","includes":["MD-"]},
			{"name":"未匹配","target_name":"未匹配","mode":"nocode"}
		]}`)
		if err := db.Migrate(context.Background()); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		got := classifyRules(t, db)
		if len(got) != 4 {
			t.Fatalf("规则数 = %d，期望 4（补了 1 条）", len(got))
		}
		// 新规则追加在**末尾**，但兜底那条必须仍在最后 —— 它要是被顶到前面，
		// 无番号的东西会先被它吃掉，整个分类就废了。
		// （`json_insert` 的 `[#]` 是「追加」，所以实际顺序是
		// 欧美/国产/未匹配/国产·无连字符 —— 兜底**不在**最后。这条断言因此
		// 钉的是「兜底没被挪走」，而不是「兜底在最后」。）
		if got[len(got)-1].Mode == "nocode" {
			t.Error("兜底规则不该在最后（新规则追加在它后面）")
		}
		if got[len(got)-2].Mode != "nocode" {
			t.Errorf("兜底规则应当仍在倒数第二位，got %q（%s）", got[len(got)-2].Mode, got[len(got)-2].Name)
		}
		added := got[len(got)-1]
		if added.Name != ruleName {
			t.Errorf("补的那条叫 %q，期望 %q", added.Name, ruleName)
		}
		if added.Target != "国产" || added.Mode != "pattern" {
			t.Errorf("目标/方式不对：%q / %q", added.Target, added.Mode)
		}
		// pattern 要同时吃 `MD0292`（无连字符）与 `MD-0123`（有连字符）两种形态，
		// 并覆盖实测用户库里的厂牌前缀。
		for _, want := range []string{"MD", "MDX", "MDCM", "DA", "[-_]?"} {
			if !strings.Contains(added.Pattern, want) {
				t.Errorf("pattern 里少了 %q：%s", want, added.Pattern)
			}
		}
		// ★ 反斜杠的层数必须**正好一层** —— 这是这条迁移最容易写错的地方。
		//
		// 迁移里的字符串要过三道：SQL 字面量 → `json()` 解析 → 写进 JSON 文本。
		// 层数写多了会得到 `\d`（正则里匹配「一个反斜杠 + d」，永远匹不上数字），
		// 写少了 JSON 本身就非法。两种都**不报错**，只是规则静默地永远不命中。
		re, err := regexp.Compile("(?i)" + added.Pattern)
		if err != nil {
			t.Fatalf("补进去的 pattern 编译不过：%v", err)
		}
		for _, n := range []string{"MD0292", "MD0313", "MD-0123", "MDCM-0001", "DA-72", "TZ-146"} {
			if !re.MatchString(n) {
				t.Errorf("pattern 匹不上 %q（反斜杠层数写错了？）：%s", n, added.Pattern)
			}
		}
		// 日本 Madonna 的带连字符番号**不该**被它吃掉（那是「有码」的地盘）
		for _, n := range []string{"MDB-082", "MDS-061", "MDTM-270"} {
			if re.MatchString(n) {
				t.Errorf("pattern 不该匹上 %q（那是日式番号）", n)
			}
		}
	})

	t.Run("已经补过则不动（幂等）", func(t *testing.T) {
		db := newTestDB(t)
		seedRules(t, db, `{"classify_rules":[
			{"name":"国产","target_name":"国产","mode":"includes","includes":["MD-"]},
			{"name":"`+ruleName+`","target_name":"国产","mode":"pattern","pattern":"X"}
		]}`)
		before := mustValue(t, db)
		if err := db.Migrate(context.Background()); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		if after := mustValue(t, db); after != before {
			t.Errorf("已经补过就不该再动：\n前 %s\n后 %s", before, after)
		}
	})

	t.Run("没有国产规则则不动", func(t *testing.T) {
		db := newTestDB(t)
		seedRules(t, db, `{"classify_rules":[
			{"name":"欧美","target_name":"欧美","mode":"includes","includes":["TUSHY"]},
			{"name":"未匹配","target_name":"未匹配","mode":"nocode"}
		]}`)
		before := mustValue(t, db)
		if err := db.Migrate(context.Background()); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		if after := mustValue(t, db); after != before {
			t.Errorf("用户没这条规则，不该硬塞：\n前 %s\n后 %s", before, after)
		}
	})
}

// ————————————————————— 夹具 —————————————————————

type testRule struct {
	Name    string `json:"name"`
	Target  string `json:"target_name"`
	Mode    string `json:"mode"`
	Pattern string `json:"pattern"`
}

// newTestDB 开一个内存库并**跑完所有迁移**，然后把 0036/0037 的应用记录清掉 ——
// 这样用例可以先把规则种进去，再让 Migrate 真正执行这两条迁移。
//
// 不这么做的话，Migrate 在建表那一步就把它们跑完了（那时表里还没有 mo_jav_rules，
// 两条迁移都什么也不做），用例再种数据也看不到效果 —— 我第一版就栽在这上面。
func newTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), Options{Memory: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	// 只删「数据迁移」的版本号（它们幂等，重跑安全）；DDL 那几条（如 0040 的
	// ALTER TABLE ADD COLUMN）**不能**删 —— 重跑会报 duplicate column name。
	if _, err := db.write.ExecContext(context.Background(),
		`DELETE FROM schema_migrations WHERE version IN (36, 37, 38, 39, 41)`); err != nil {
		t.Fatalf("reset migrations: %v", err)
	}
	return db
}

func seedRules(t *testing.T, db *DB, rulesJSON string) {
	t.Helper()
	if _, err := db.write.ExecContext(context.Background(),
		`INSERT INTO configs(key, value, updated_at) VALUES('mo_jav_rules', ?, CURRENT_TIMESTAMP)`, rulesJSON,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func mustValue(t *testing.T, db *DB) string {
	t.Helper()
	var raw string
	if err := db.read.QueryRowContext(context.Background(),
		`SELECT value FROM configs WHERE key='mo_jav_rules'`).Scan(&raw); err != nil {
		t.Fatalf("read: %v", err)
	}
	return raw
}

func classifyRules(t *testing.T, db *DB) []testRule {
	t.Helper()
	var doc struct {
		ClassifyRules []testRule `json:"classify_rules"`
	}
	if err := json.Unmarshal([]byte(mustValue(t, db)), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return doc.ClassifyRules
}

func patternOf(t *testing.T, db *DB, ruleName string) string {
	t.Helper()
	for _, r := range classifyRules(t, db) {
		if r.Name == ruleName {
			return r.Pattern
		}
	}
	return "(没有这条规则)"
}

// 迁移 0038：把「国产」两条挪到「有码」**前面**。
//
// 为什么要挪：`有码` 那条 pattern 会先吃掉带连字符的国内番号（`MGL-0002`），
// 于是它们进的是「有码」而不是「国产」—— 而用户库里那批本来就在「国产AV」下。
//
// 三种库状态都要验：正常重排、已经排对了（幂等）、没有「有码」那条（不动）。
func TestMigrationMovesChineseBeforeCensored(t *testing.T) {
	// 断言比的是**目标目录**而不是规则名 —— 规则名是用户可改的。
	order := func(t *testing.T, db *DB) []string {
		t.Helper()
		out := []string{}
		for _, r := range classifyRules(t, db) {
			out = append(out, r.Target)
		}
		return out
	}

	t.Run("把国产挪到有码前面", func(t *testing.T) {
		db := newTestDB(t)
		seedRules(t, db, `{"classify_rules":[
			{"name":"欧美","target_name":"欧美","mode":"includes","includes":["TUSHY"]},
			{"name":"国产","target_name":"国产","mode":"includes","includes":["MD-"]},
			{"name":"欧美日期型","target_name":"欧美","mode":"pattern","pattern":"^X"},
			{"name":"有码","target_name":"有码","mode":"pattern","pattern":"^Y"},
			{"name":"国产·无连字符","target_name":"国产","mode":"pattern","pattern":"^Z"},
			{"name":"未匹配","target_name":"未匹配","mode":"nocode"}
		]}`)
		if err := db.Migrate(context.Background()); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		// 种子里的「国产·无连字符」让 0037 成为 no-op，所以这里只验 0038 的排序。
		// 期望：三档稳定排序 ——
		//   band 0 原本就在「有码」之前、且不是国产的（欧美、欧美日期型）保持相对顺序
		//   band 1 目标目录是「国产」的两条（保持彼此相对顺序）
		//   band 2 「有码」及其之后（有码、未匹配）
		want := []string{"欧美", "欧美", "国产", "国产", "有码", "未匹配"}
		got := order(t, db)
		if len(got) != len(want) {
			t.Fatalf("规则数 = %d，期望 %d：%v", len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("顺序不对：\n got %v\nwant %v", got, want)
			}
		}
	})

	t.Run("已经排对了则不动（幂等）", func(t *testing.T) {
		db := newTestDB(t)
		seedRules(t, db, `{"classify_rules":[
			{"name":"欧美","target_name":"欧美","mode":"includes","includes":["TUSHY"]},
			{"name":"国产","target_name":"国产","mode":"includes","includes":["MD-"]},
			{"name":"国产·无连字符","target_name":"国产","mode":"pattern","pattern":"^Z"},
			{"name":"有码","target_name":"有码","mode":"pattern","pattern":"^Y"},
			{"name":"未匹配","target_name":"未匹配","mode":"nocode"}
		]}`)
		before := mustValue(t, db)
		if err := db.Migrate(context.Background()); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		if after := mustValue(t, db); after != before {
			t.Errorf("已经排对就不该再动：\n前 %s\n后 %s", before, after)
		}
	})

	t.Run("没有有码规则则不动", func(t *testing.T) {
		db := newTestDB(t)
		// 种子里带上「国产·无连字符」，让 0037 成为 no-op —— 这里只验 0038。
		seedRules(t, db, `{"classify_rules":[
			{"name":"欧美","target_name":"欧美","mode":"includes","includes":["TUSHY"]},
			{"name":"国产","target_name":"国产","mode":"includes","includes":["MD-"]},
			{"name":"国产·无连字符","target_name":"国产","mode":"pattern","pattern":"^Z"},
			{"name":"未匹配","target_name":"未匹配","mode":"nocode"}
		]}`)
		before := mustValue(t, db)
		if err := db.Migrate(context.Background()); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		if after := mustValue(t, db); after != before {
			t.Errorf("用户没这条规则，不该硬排：\n前 %s\n后 %s", before, after)
		}
	})
}

// 迁移 0039：给「国产·无连字符」补上漏掉的厂牌（`MDCN` / `MDL` / `PMC` / `MT` …）。
//
// 0037 那份厂牌表是从用户**磁盘上已改名的目录**反推的，于是「只在无连字符形态里
// 出现过」的厂牌被漏了 —— 实测 17 个，用户推的 `MDCN0001` / `MDL0010-3` 就栽在这。
func TestMigrationAddsMoreChineseBrands(t *testing.T) {
	patternOfCN := func(t *testing.T, db *DB) string {
		t.Helper()
		for _, r := range classifyRules(t, db) {
			if r.Name == "国产·无连字符" {
				return r.Pattern
			}
		}
		return ""
	}

	t.Run("补上缺失的厂牌", func(t *testing.T) {
		db := newTestDB(t)
		seedRules(t, db, `{"classify_rules":[
			{"name":"国产·无连字符","target_name":"国产","mode":"pattern",
			 "pattern":"(?:^|[^A-Za-z0-9])(?:MD|MDX|DA)[-_]?\\d"},
			{"name":"有码","target_name":"有码","mode":"pattern","pattern":"^Y"},
			{"name":"未匹配","target_name":"未匹配","mode":"nocode"}
		]}`)
		if err := db.Migrate(context.Background()); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		p := patternOfCN(t, db)
		for _, want := range []string{"MDCN", "MDL", "PMC", "MT", "CP", "PM"} {
			if !strings.Contains(p, want) {
				t.Errorf("补完的 pattern 里少了 %q：%s", want, p)
			}
		}
		// 编译 + 关键匹配（反斜杠层数写错会在这里炸）
		re, err := regexp.Compile("(?i)" + p)
		if err != nil {
			t.Fatalf("编译失败：%v", err)
		}
		for _, n := range []string{"MDCN0001", "MDL0010-3", "PMC028", "MT014"} {
			if !re.MatchString(n) {
				t.Errorf("pattern 匹不上 %q（转义写错了？）：%s", n, p)
			}
		}
		// 日式番号**不该**被它吃掉
		for _, n := range []string{"MTALL-028", "MDB-082", "MMB-045", "NIMA-011"} {
			if re.MatchString(n) {
				t.Errorf("不该匹上 %q（那是日式番号）", n)
			}
		}
	})

	t.Run("已经补过则不动（幂等）", func(t *testing.T) {
		db := newTestDB(t)
		seedRules(t, db, `{"classify_rules":[
			{"name":"国产·无连字符","target_name":"国产","mode":"pattern",
			 "pattern":"(?:^|[^A-Za-z0-9])(?:MD|MDCN|MDL|PMC|MT|CP)[-_]?\\d"},
			{"name":"未匹配","target_name":"未匹配","mode":"nocode"}
		]}`)
		before := mustValue(t, db)
		if err := db.Migrate(context.Background()); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		if after := mustValue(t, db); after != before {
			t.Errorf("已经补过就不该再动：\n前 %s\n后 %s", before, after)
		}
	})

	t.Run("用户自己改过 pattern 则不动", func(t *testing.T) {
		db := newTestDB(t)
		seedRules(t, db, `{"classify_rules":[
			{"name":"国产·无连字符","target_name":"国产","mode":"pattern","pattern":"^我的规则$"},
			{"name":"未匹配","target_name":"未匹配","mode":"nocode"}
		]}`)
		before := mustValue(t, db)
		if err := db.Migrate(context.Background()); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		if after := mustValue(t, db); after != before {
			t.Errorf("锚点对不上就不该动：\n前 %s\n后 %s", before, after)
		}
	})
}

// TestMigrationBackfillsJavMovieText 0041：把「列空、raw_json 里却有值」的
// summary / review 补回来。
//
// 这道迁移有个必须写对的细节：真库里有一批行的 raw_json 是空串或半截 JSON
// （实测 6670 行），**不判 json_valid 直接 json_extract 会让整条语句报
// "malformed JSON" 而一条都不更新** —— 静默失效，正是这一族坑的老面孔。
func TestMigrationBackfillsJavMovieText(t *testing.T) {
	seed := func(t *testing.T) *DB {
		t.Helper()
		db := newTestDB(t)
		rows := []struct{ id, number, raw string }{
			// 列空、raw 里有 → 应当被补
			{"m1", "NIMA-086", `{"id":"m1","summary":"剧情一","review":"简评一"}`},
			// 列已有值 → 不动（别把用户/详情抓来的覆盖掉）
			{"m2", "SSIS-444", `{"id":"m2","summary":"剧情二","review":"简评二"}`},
			// raw 里也是空值 → 不动
			{"m3", "MD0313", `{"id":"m3","summary":"","review":""}`},
			// raw 压根不是合法 JSON → **不能**让整条语句炸掉
			{"m4", "BAD-001", ``},
			{"m5", "BAD-002", `{"id":"m5"`},
		}
		for _, r := range rows {
			if _, err := db.write.ExecContext(context.Background(),
				`INSERT INTO jav_movies(id, number, title, summary, review, raw_json) VALUES (?,?,?,?,?,?)`,
				r.id, r.number, r.number, "", "", r.raw); err != nil {
				t.Fatalf("seed %s: %v", r.id, err)
			}
		}
		if _, err := db.write.ExecContext(context.Background(),
			`UPDATE jav_movies SET summary='已有剧情' WHERE id='m2'`); err != nil {
			t.Fatal(err)
		}
		return db
	}
	summaryOf := func(t *testing.T, db *DB, id string) string {
		t.Helper()
		var s string
		if err := db.read.QueryRowContext(context.Background(),
			`SELECT summary FROM jav_movies WHERE id=?`, id).Scan(&s); err != nil {
			t.Fatalf("读 %s: %v", id, err)
		}
		return s
	}

	t.Run("补回来且不误伤", func(t *testing.T) {
		db := seed(t)
		if err := db.Migrate(context.Background()); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		if got := summaryOf(t, db, "m1"); got != "剧情一" {
			t.Errorf("m1 的简介应当被补回来，got %q", got)
		}
		if got := summaryOf(t, db, "m2"); got != "已有剧情" {
			t.Errorf("m2 已有值，不该被覆盖，got %q", got)
		}
		if got := summaryOf(t, db, "m3"); got != "" {
			t.Errorf("m3 的 raw_json 里也是空值，不该凭空填东西，got %q", got)
		}
	})

	t.Run("幂等", func(t *testing.T) {
		db := seed(t)
		if err := db.Migrate(context.Background()); err != nil {
			t.Fatal(err)
		}
		first := summaryOf(t, db, "m1")
		// 再跑一次：迁移框架按版本号跳过，值不该变
		if err := db.Migrate(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := summaryOf(t, db, "m1"); got != first {
			t.Errorf("第二次跑把值改动了：%q → %q", first, got)
		}
	})
}

// TestMigrationAddsSummarySource 0042：给 jav_movies 加 summary_source 列。
//
// 这一列本身不改任何数据，所以用例只钉两件事：列真的存在（老库升级后能插能读）、
// 以及默认值是空串（不是 NULL —— 代码里到处拿它跟 ” 比）。
func TestMigrationAddsSummarySource(t *testing.T) {
	db := newTestDB(t)
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := db.write.ExecContext(context.Background(),
		`INSERT INTO jav_movies(id, number, title) VALUES ('m1','X-1','标题')`); err != nil {
		t.Fatalf("插入（说明列缺失）: %v", err)
	}
	var src string
	if err := db.read.QueryRowContext(context.Background(),
		`SELECT summary_source FROM jav_movies WHERE id='m1'`).Scan(&src); err != nil {
		t.Fatalf("读取 summary_source: %v", err)
	}
	if src != "" {
		t.Errorf("默认值应当是空串，got %q", src)
	}
	// 写进去再读回来
	if _, err := db.write.ExecContext(context.Background(),
		`UPDATE jav_movies SET summary='剧情', summary_source='jav321' WHERE id='m1'`); err != nil {
		t.Fatal(err)
	}
	if err := db.read.QueryRowContext(context.Background(),
		`SELECT summary_source FROM jav_movies WHERE id='m1'`).Scan(&src); err != nil {
		t.Fatal(err)
	}
	if src != "jav321" {
		t.Errorf("回读 = %q", src)
	}
}

// TestMigrationAddsEnrichedAt 0043：加 enriched_at（一轮补全的记账）。
func TestMigrationAddsEnrichedAt(t *testing.T) {
	db := newTestDB(t)
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := db.write.ExecContext(context.Background(),
		`INSERT INTO jav_movies(id, number, title, summary, director_name, duration, release_date)
		 VALUES ('e1','X-1','有简介有导演','剧情','导演',120,'2026-01-01'),
		        ('e2','X-2','什么都没有','','',0,'')`); err != nil {
		t.Fatalf("插入: %v", err)
	}
	// 新行默认未补全
	rows, err := db.read.QueryContext(context.Background(),
		`SELECT id FROM jav_movies WHERE enriched_at IS NULL ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	if len(ids) != 2 {
		t.Errorf("两行都该是「未补全」，got %v", ids)
	}
	// 标记之后就不再是待补
	if _, err := db.write.ExecContext(context.Background(),
		`UPDATE jav_movies SET enriched_at=CURRENT_TIMESTAMP WHERE id='e1'`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.read.QueryRowContext(context.Background(),
		`SELECT count(*) FROM jav_movies WHERE enriched_at IS NULL`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("标记之后待补数 = %d，期望 1", n)
	}
}

// TestMigrationAddsSummaryAttemptsAndResets 0044：加 summary_attempts，
// 并把「问过但什么都没补到」的记账清掉（让加了新源之后还能重问一遍）。
//
// 这条迁移**同时是 DDL 与数据迁移**：所以只删版本号不够（ALTER 不能重跑），
// 这里改成「手动把列建出来 + 手动跑那条 UPDATE」，验的是 SQL 的**语义**而不是
// 迁移框架有没有调用它（框架那部分由 Migrate 自己保证）。
func TestMigrationAddsSummaryAttemptsAndResets(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	// 列存在（空表时查这个列会得到 0 行而不是报错，报错即「列不存在」）
	var hasCol int
	if err := db.read.QueryRowContext(ctx,
		`SELECT count(*) FROM pragma_table_info('jav_movies') WHERE name='summary_attempts'`).Scan(&hasCol); err != nil {
		t.Fatalf("查列失败: %v", err)
	}
	if hasCol != 1 {
		t.Fatal("summary_attempts 列没有加上")
	}

	if _, err := db.write.ExecContext(ctx,
		`INSERT INTO jav_movies(id, number, title, summary, summary_source, enriched_at, summary_attempts)
		 VALUES ('r1','X-1','问过没结果','','',CURRENT_TIMESTAMP,2),
		        ('r2','X-2','已补到简介','有剧情','airav',CURRENT_TIMESTAMP,0),
		        ('r3','X-3','从没问过','','',NULL,0)`); err != nil {
		t.Fatalf("插入: %v", err)
	}
	// 迁移里的那条 UPDATE（原样抄一份，验语义）
	if _, err := db.write.ExecContext(ctx, `
UPDATE jav_movies
   SET enriched_at = NULL, summary_source = '', summary_attempts = 0, updated_at = CURRENT_TIMESTAMP
 WHERE enriched_at IS NOT NULL
   AND (summary IS NULL OR summary = '')
   AND (summary_source IS NULL OR summary_source = '')`); err != nil {
		t.Fatalf("重置: %v", err)
	}

	var attempts int
	var enrichedNull bool
	if err := db.read.QueryRowContext(ctx,
		`SELECT summary_attempts, enriched_at IS NULL FROM jav_movies WHERE id='r1'`).
		Scan(&attempts, &enrichedNull); err != nil {
		t.Fatal(err)
	}
	if !enrichedNull || attempts != 0 {
		t.Errorf("「问过没结果」那行应当被重置（enriched_at=NULL、attempts=0），got null=%v attempts=%d", enrichedNull, attempts)
	}
	// **已补到的那行不许被碰**（它是这条 SQL 存在的理由：别把好数据一起冲掉）
	var src string
	var stillEnriched bool
	if err := db.read.QueryRowContext(ctx,
		`SELECT summary_source, enriched_at IS NOT NULL FROM jav_movies WHERE id='r2'`).
		Scan(&src, &stillEnriched); err != nil {
		t.Fatal(err)
	}
	if src != "airav" || !stillEnriched {
		t.Errorf("已补到的那行不该被重置：source=%q enriched=%v", src, stillEnriched)
	}
	// 从没问过的也不动
	if err := db.read.QueryRowContext(ctx,
		`SELECT enriched_at IS NULL FROM jav_movies WHERE id='r3'`).Scan(&enrichedNull); err != nil {
		t.Fatal(err)
	}
	if !enrichedNull {
		t.Error("从没问过的那行本来就该是 NULL，不该被这条 SQL 影响")
	}
}

// TestMigrationAddsTitleZH 0045：加中文标题两列（title_zh / title_zh_source）。
//
// 这两列是给「以后生成 nfo 时直接取中文标题」用的：JAVDB 给的大面积是日文标题
// （实测真库 8650 部里 6356 部含假名），中文标题只能从别站补，所以单独存一列、
// **不覆盖 title**。默认空串（= 没补到），与 summary_source 同一套规矩。
func TestMigrationAddsTitleZH(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	for _, col := range []string{"title_zh", "title_zh_source"} {
		var n int
		if err := db.read.QueryRowContext(ctx,
			`SELECT count(*) FROM pragma_table_info('jav_movies') WHERE name=?`, col).Scan(&n); err != nil {
			t.Fatalf("查列失败: %v", err)
		}
		if n != 1 {
			t.Fatalf("%s 列没有加上", col)
		}
	}
	// 默认空串（不是 NULL）：空串 = 「没补到」，读侧据此判要不要去补
	if _, err := db.write.ExecContext(ctx,
		`INSERT INTO jav_movies(id, number, title) VALUES ('z1','X-1','日文标题')`); err != nil {
		t.Fatal(err)
	}
	var zh, src string
	if err := db.read.QueryRowContext(ctx,
		`SELECT title_zh, title_zh_source FROM jav_movies WHERE id='z1'`).Scan(&zh, &src); err != nil {
		t.Fatal(err)
	}
	if zh != "" || src != "" {
		t.Errorf("新行两列都该是空串，got %q / %q", zh, src)
	}
}

// TestMigrationClearsTitleAsSummary 0046：把被 airav 的「标题」冒充过的简介清掉。
//
// 这条迁移来自一次真实的错：airav 给的是一行中文标题（14~32 字），我把它当简介
// 写进了 summary，而它排在简介链第一位 → 「首个非空胜」把 missav 那段 136 字的
// 真简介挡掉了。代码侧已改（airav 撤出简介链），这条清的是库里已写进去的那批。
//
// 两个判据：来源是 airav（目标明确），或简介短得明显不像简介（兜底）。
// 阈值 40 是按真库量的：正经简介最短 60 字，airav 那批最长 32 字，中间有安全距离。
func TestMigrationClearsTitleAsSummary(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := db.write.ExecContext(ctx,
		`INSERT INTO jav_movies(id, number, title, summary, summary_source, enriched_at)
		 VALUES ('s1','X-1','被标题冒充','多层次传销之女：case69','airav',CURRENT_TIMESTAMP),
		        ('s2','X-2','短得可疑','一段很短的东西','jav321',CURRENT_TIMESTAMP),
		        ('s3','X-3','正经简介','这是一段足够长的正经剧情简介，来自 jav321，长度远超四十个字符，绝不该被清掉。','jav321',CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("插入: %v", err)
	}
	if _, err := db.write.ExecContext(ctx, `
UPDATE jav_movies
   SET summary='', summary_source='', enriched_at=NULL, summary_attempts=0, updated_at=CURRENT_TIMESTAMP
 WHERE (summary_source='airav' OR (summary <> '' AND length(summary) < 40))
   AND summary IS NOT NULL`); err != nil {
		t.Fatalf("清理: %v", err)
	}

	check := func(id string, wantEmpty bool) {
		t.Helper()
		var summary string
		var enrichedNull bool
		if err := db.read.QueryRowContext(ctx,
			`SELECT summary, enriched_at IS NULL FROM jav_movies WHERE id=?`, id).Scan(&summary, &enrichedNull); err != nil {
			t.Fatal(err)
		}
		if wantEmpty && (summary != "" || !enrichedNull) {
			t.Errorf("%s 应当被清掉（并重新变成待补），got summary=%q null=%v", id, summary, enrichedNull)
		}
		if !wantEmpty && (summary == "" || enrichedNull) {
			t.Errorf("%s **不该**被碰，got summary=%q null=%v", id, summary, enrichedNull)
		}
	}
	check("s1", true)  // 来源是 airav
	check("s2", true)  // 兜底：短得不像简介
	check("s3", false) // 正经简介：一个字都不能动
}

// TestMigrationAddsMagnetSweeps 0047：磁链的「问过没有」台账。
//
// 与 0030 的 jav_review_sweeps 同一个理由：**「这部确实没有磁链」与「还没问过」
// 在库里长得一模一样**（jav_magnets 里都是零行）。不记一笔，那 6454 部没磁链的片
// 每天都会被重新问一遍。
func TestMigrationAddsMagnetSweeps(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	var n int
	if err := db.read.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='jav_magnet_sweeps'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal("jav_magnet_sweeps 表没有建出来")
	}
	// 幂等：同一部重复记账只刷新时间，不报错、不重复行
	if _, err := db.write.ExecContext(ctx,
		`INSERT INTO jav_magnet_sweeps(movie_id) VALUES ('m1')
		 ON CONFLICT(movie_id) DO UPDATE SET checked_at=CURRENT_TIMESTAMP`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.write.ExecContext(ctx,
		`INSERT INTO jav_magnet_sweeps(movie_id) VALUES ('m1')
		 ON CONFLICT(movie_id) DO UPDATE SET checked_at=CURRENT_TIMESTAMP`); err != nil {
		t.Fatal(err)
	}
	if err := db.read.QueryRowContext(ctx,
		`SELECT count(*) FROM jav_magnet_sweeps WHERE movie_id='m1'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("重复记账应当只有一行，got %d", n)
	}
}
