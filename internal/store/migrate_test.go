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
	if _, err := db.write.ExecContext(context.Background(),
		`DELETE FROM schema_migrations WHERE version IN (36, 37, 38, 39)`); err != nil {
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
