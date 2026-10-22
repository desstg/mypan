package strmscrape

import "testing"

// 演员**资料层**的纯函数：不做网络请求，只验「上游页面 → 我们那几个字段」这一段。
// 网络那几条（avbase / JAVDB 搜索）换个站点就得重打，能钉住的是解析与挑选的判据。

// avbase 的演员页：数据在 __NEXT_DATA__ 里，basic_info 的键是固定的那几个。
func TestParseAvbaseTalent(t *testing.T) {
	page := `<!doctype html><html><head><title>彩月七緒</title></head><body>
<script id="__NEXT_DATA__" type="application/json">{"props":{"pageProps":{"talent":{
"id":12345,
"primary":{"name":"彩月七緒","note":"","image_url":"https://pics.dmm.co.jp/mono/actjpgs/satsuki_nao.jpg"},
"meta":{"basic_info":{"birthday":"2002-06-16","prefectures":"東京都","height":164,
"bust":92,"waist":59,"hip":89,"cup":"H","blood_type":"O","hobby":"観劇、旅行"}},
"actors":[{"name":"彩月七緒"},{"name":"さつきなお"}]}}}}</script>
</body></html>`

	got := ParseAvbaseTalent(page)
	if got == nil {
		t.Fatal("没解析出来")
	}
	if got.Name != "彩月七緒" {
		t.Errorf("Name = %q", got.Name)
	}
	if got.ImageURL != "https://pics.dmm.co.jp/mono/actjpgs/satsuki_nao.jpg" {
		t.Errorf("ImageURL = %q", got.ImageURL)
	}
	// basic_info 里既有字符串也有数字，两种都要转成字符串。
	want := map[string]string{
		"birthday":    "2002-06-16",
		"prefectures": "東京都",
		"height":      "164",
		"bust":        "92",
		"waist":       "59",
		"hip":         "89",
		"cup":         "H",
		"blood_type":  "O",
		"hobby":       "観劇、旅行",
	}
	for k, v := range want {
		if got.Info[k] != v {
			t.Errorf("Info[%q] = %q，期望 %q", k, got.Info[k], v)
		}
	}
	if len(got.ActorNames) != 2 || got.ActorNames[1] != "さつきなお" {
		t.Errorf("ActorNames = %v", got.ActorNames)
	}
}

// 不是演员页 / 页面改版：返回 nil，调用方当作「这个源没有」而不是报错。
func TestParseAvbaseTalentNoData(t *testing.T) {
	for _, page := range []string{
		"",
		"<html><body>404</body></html>",
		`<script id="__NEXT_DATA__" type="application/json">{"props":{"pageProps":{}}}</script>`,
		`<script id="__NEXT_DATA__" type="application/json">不是 json</script>`,
	} {
		if got := ParseAvbaseTalent(page); got != nil {
			t.Errorf("页面 %q 不该解析出档案，得到 %+v", page, got)
		}
	}
}

// 男优标记：avbase 的 note 里写「男優 / AV男優」，要认成 gender=1。
func TestIsMaleNote(t *testing.T) {
	for _, note := range []string{"男優", "AV男優", "男优"} {
		if !isMaleNote(note) {
			t.Errorf("isMaleNote(%q) = false", note)
		}
	}
	for _, note := range []string{"", "女優", "舞ワイフ"} {
		if isMaleNote(note) {
			t.Errorf("isMaleNote(%q) = true", note)
		}
	}
}

// JAVDB 演员搜索页：一张卡里同时有短 id、别名（title）、头像、显示名。
func TestParseJavdbActorSearch(t *testing.T) {
	page := `
<div class="box actor-box">
  <a href="/actors/Azq9w" title="七咲琴乃, 今田美玲, &amp;神田千佳">
    <figure class="image"><img class="avatar" src="https://c0.jdbstatic.com/avatars/az/Azq9w.jpg" /></figure>
    <strong>
      七咲琴乃
    </strong>
  </a>
</div>
<div class="box actor-box">
  <a href="/actors/zzzz" >
    <figure class="image"><img class="avatar" src="https://c0.jdbstatic.com/avatars/zz/actor_unknow.jpg" /></figure>
    <strong>别人</strong>
  </a>
</div>`

	hits := ParseJavdbActorSearch(page)
	if len(hits) != 2 {
		t.Fatalf("解析出 %d 条，期望 2 条", len(hits))
	}
	first := hits[0]
	if first.ID != "Azq9w" {
		t.Errorf("ID = %q", first.ID)
	}
	// 显示名上游会缩进成多行，要折成一行
	if first.Name != "七咲琴乃" {
		t.Errorf("Name = %q", first.Name)
	}
	if first.Avatar != "https://c0.jdbstatic.com/avatars/az/Azq9w.jpg" {
		t.Errorf("Avatar = %q", first.Avatar)
	}
	if len(first.Aliases) != 3 || first.Aliases[2] != "&神田千佳" {
		t.Errorf("Aliases = %v（HTML 实体要先反转义）", first.Aliases)
	}
	if hits[1].Aliases != nil {
		t.Errorf("没有 title 的那条不该有别名：%v", hits[1].Aliases)
	}
}

// 挑人：**必须逐字相等**。组合名（`ジャイアント廣田　今井勇太`）不能拿来当本尊，
// 否则会把别人的脸贴上去。
func TestPickJavdbActorHit(t *testing.T) {
	hits := []javdbActorHit{
		{ID: "aaa1", Name: "ジャイアント廣田　今井勇太", Aliases: []string{"ジャイアント廣田"}},
		{ID: "bbb2", Name: "今井勇太", Avatar: javdbActorAvatarURL("bbb2")},
	}
	if got := PickJavdbActorHit(hits, "今井勇太"); got == nil || got.ID != "bbb2" {
		t.Fatalf("应挑名字完全相等的那条，得到 %+v", got)
	}
	// 组合名那条虽然带了「今井勇太」这几个字，但显示名既不等于它、别名里也没有它。
	if got := PickJavdbActorHit(hits, "広田"); got != nil {
		t.Errorf("模糊命中不该被接受，得到 %+v", got)
	}
	// 只能靠别名对上时也接受（别名本身就是「同一个人」的声索）
	hits2 := []javdbActorHit{{ID: "ccc3", Name: "別名義", Aliases: []string{"七咲琴乃"}}}
	if got := PickJavdbActorHit(hits2, "七咲琴乃"); got == nil || got.ID != "ccc3" {
		t.Errorf("别名命中应被接受，得到 %+v", got)
	}
	if got := PickJavdbActorHit(hits, "完全不相干"); got != nil {
		t.Errorf("不该命中，得到 %+v", got)
	}
	if got := PickJavdbActorHit(hits, "  "); got != nil {
		t.Errorf("空查询不该命中，得到 %+v", got)
	}
}

// 同名不同人：优先**有真头像**的那条（JAVDB 常有一条是占位图）。
func TestPickJavdbActorHitPrefersUsableAvatar(t *testing.T) {
	hits := []javdbActorHit{
		{ID: "n1", Name: "同名", Avatar: "https://c0.jdbstatic.com/avatars/n1/actor_unknow.jpg"},
		{ID: "n2", Name: "同名", Avatar: "https://c0.jdbstatic.com/avatars/n2/n2.jpg"},
	}
	if got := PickJavdbActorHit(hits, "同名"); got == nil || got.ID != "n2" {
		t.Fatalf("应挑有真头像的那条，得到 %+v", got)
	}
	// 顺序反过来结论也一样（不看先后，只看有没有可用头像）
	rev := []javdbActorHit{hits[1], hits[0]}
	if got := PickJavdbActorHit(rev, "同名"); got == nil || got.ID != "n2" {
		t.Fatalf("反序后仍应挑有真头像的那条，得到 %+v", got)
	}
}

// 头像地址：目录是 id **前两位的小写**，文件名里的 id 大小写原样保留。
func TestJavdbActorAvatarURL(t *testing.T) {
	cases := []struct{ id, want string }{
		{"z0GW", "https://c0.jdbstatic.com/avatars/z0/z0GW.jpg"},
		{"Azq9w", "https://c0.jdbstatic.com/avatars/az/Azq9w.jpg"},
		{"RdEb4", "https://c0.jdbstatic.com/avatars/rd/RdEb4.jpg"},
		// 太短的 id 拼不出目录
		{"z", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := javdbActorAvatarURL(c.id); got != c.want {
			t.Errorf("javdbActorAvatarURL(%q) = %q，期望 %q", c.id, got, c.want)
		}
	}
}

// 侧车 / 搜索页给的占位图要认出来：`actor_unknow.jpg` 不算头像。
func TestIsUsableActorAvatar(t *testing.T) {
	if isUsableActorAvatar("https://c0.jdbstatic.com/avatars/k4/actor_unknow.jpg") {
		t.Error("占位图不该算可用头像")
	}
	if !isUsableActorAvatar("https://c0.jdbstatic.com/avatars/k4/K4gKv.jpg") {
		t.Error("真头像被当成占位图了")
	}
	if isUsableActorAvatar("  ") {
		t.Error("空串不该算可用头像")
	}
}

// 上游把数字写成 `158` / `158cm` / `158.0` 都有，只取前导数字；认不出给 0。
func TestAtoiOr0(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"158", 158},
		{" 164 ", 164},
		{"158cm", 158},
		{"92.0", 92},
		{"", 0},
		{"不明", 0},
		{"0", 0},
		{"-5", 0},
	}
	for _, c := range cases {
		if got := atoiOr0(c.in); got != c.want {
			t.Errorf("atoiOr0(%q) = %d，期望 %d", c.in, got, c.want)
		}
	}
}

// 查资料时依次试的名字：主名在前，别名去重、去空白、有上限。
func TestJavActorProfileNames(t *testing.T) {
	card := JavWallActorCard{
		Name:    "彩月七緒",
		Aliases: []string{" さつきなお ", "彩月七緒", "", "Satsuki Nao", "第五个"},
	}
	got := javActorProfileNames(card, 4)
	want := []string{"彩月七緒", "さつきなお", "Satsuki Nao", "第五个"}
	if len(got) != len(want) {
		t.Fatalf("names = %v，期望 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("names[%d] = %q，期望 %q（全体 %v）", i, got[i], want[i], got)
		}
	}
}

// 别名合并：去主名、去重（忽略大小写）、保持先后。
func TestMergeActorAliases(t *testing.T) {
	got := mergeActorAliases("彩月七緒", []string{"さつきなお"}, []string{"彩月七緒", "SATSUKI NAO", "さつきなお", ""})
	want := []string{"さつきなお", "SATSUKI NAO"}
	if len(got) != len(want) {
		t.Fatalf("aliases = %v，期望 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("aliases[%d] = %q，期望 %q", i, got[i], want[i])
		}
	}
}
