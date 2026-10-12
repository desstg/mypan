package pan123open

import (
	"encoding/json"
	"testing"
)

// 网页离线任务列表的字段形状必须**宽松收下**。
//
// 钉的是 2026-10-11 真机踩到的那个坑：123 在同一份响应里混着小数与整数
// （progress 17.47、downloaded 276445467.0），字段类型一旦收窄成 int，
// **整包解析当场失败** —— 而失败被公共层当成「刷新出错」，于是所有 123 磁力
// 任务永远停在 pending，「转存记录」里一直显示「处理中」，网盘那边其实早在下了。
//
// 判据：任何一处形状变化都不该让整次刷新失败。所以这里把小数、整数、
// 数字字符串、null 四种形态都喂一遍。
func TestWebOfflineTaskListAcceptsMixedNumberShapes(t *testing.T) {
	raw := []byte(`{"list":[
		{"task_id":10654799,"name":"战狼2","status":0,"size":276445467,"downloaded":276445467.0,"progress":17.47,"file_id":"abc"},
		{"task_id":"10654800","name":"整数与字符串","status":2,"size":"123","downloaded":"1.5","progress":"100","file_id":""},
		{"task_id":10654801,"name":"缺字段","status":1}
	]}`)
	var data webOfflineTaskListData
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("混合形状不该让解析失败: %v", err)
	}
	if len(data.List) != 3 {
		t.Fatalf("应当解析出 3 条，实际 %d 条", len(data.List))
	}

	first := data.List[0]
	if first.TaskID.int64() != 10654799 || first.Progress.float64() != 17.47 || first.Size.int64() != 276445467 {
		t.Errorf("第一条解析错误: %+v", first)
	}
	if got := clampPercent(first.Progress.float64()); got != 17 {
		t.Errorf("小数进度应当四舍五入成 17，实际 %d", got)
	}

	second := data.List[1]
	if second.TaskID.int64() != 10654800 || second.Size.int64() != 123 || second.Progress.float64() != 100 {
		t.Errorf("数字字符串形态解析错误: %+v", second)
	}

	// 字段整个缺失时留零值，不该报错 —— 「少一个字段」与「类型变了」一样，
	// 都不该把一次刷新判死。
	if data.List[2].Progress != 0 || data.List[2].Size != 0 {
		t.Errorf("缺字段时应当留零值，实际 %+v", data.List[2])
	}
}

// null 也要收下：123 对还没算出进度的任务偶尔回 null。
func TestWebNumberAcceptsNull(t *testing.T) {
	var n webNumber
	if err := json.Unmarshal([]byte(`null`), &n); err != nil {
		t.Fatalf("null 不该报错: %v", err)
	}
	if n != 0 {
		t.Errorf("null 应当归零，实际 %v", n)
	}
}

// 任务号必须带 web: 前缀 —— 直链走开放平台、磁力走网页，两套 id 空间都是纯数字，
// 没有前缀就无从判断该问哪套接口。
func TestWebTaskPrefixDistinguishesOpenPlatformIDs(t *testing.T) {
	if !isWebTaskID(webTaskPrefix + "10654799") {
		t.Error("web: 前缀应当被判为网页任务")
	}
	if isWebTaskID("10654799") {
		t.Error("裸数字是开放平台的任务号，不该被判为网页任务")
	}
}

// 目录路径比对的归一化：转存前拿它挡「ID 与路径对不上」。
//
// 判错的两个方向都危险 —— 太松会把文件存进错目录（真实写入，不可撤销），
// 太紧会把本来正确的目标拒掉。
func TestSameDirPath(t *testing.T) {
	same := [][2]string{
		{"/电影资源", "电影资源"},
		{"/电影资源/", "/电影资源"},
		{"//a//b//", "/a/b"},
		{"/", ""},
		{"/", "/"},
	}
	for _, c := range same {
		if !sameDirPath(c[0], c[1]) {
			t.Errorf("sameDirPath(%q, %q) = false，应当视为同一目录", c[0], c[1])
		}
	}
	diff := [][2]string{
		{"/电影资源", "/电影"},
		{"/a/b", "/a/c"},
		{"/电影资源", "/电影资源/子目录"},
	}
	for _, c := range diff {
		if sameDirPath(c[0], c[1]) {
			t.Errorf("sameDirPath(%q, %q) = true，这是两个不同目录", c[0], c[1])
		}
	}
}

// parentFileID 必须是数字：发字符串会回
// `fileList.Alias.parentFileID格式异常`（真机 2026-10-11 实测）。
func TestParentFileIDMustBeNumeric(t *testing.T) {
	const root = "0"
	id, err := parseDirID(root)
	if err != nil || id != 0 {
		t.Fatalf("根目录 0 应当解析成数字 0，实际 %d err=%v", id, err)
	}
	id, err = parseDirID("12704834")
	if err != nil || id != 12704834 {
		t.Fatalf("正常目录 id 解析错误：%d err=%v", id, err)
	}
	if _, err := parseDirID("not-a-number"); err == nil {
		t.Error("非数字目录 id 应当报错，而不是发出去拿一句看不懂的接口错误")
	}
}
