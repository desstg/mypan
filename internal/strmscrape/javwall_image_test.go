package strmscrape

import (
	"strconv"
	"strings"
	"testing"
)

// 海报墙的图片地址必须带上 `w`（服务端按需缩放的宽度），两种视图取不同档位。
//
// 这条钉的是「卡片 140~260 px 宽、原图 376×538」那个浪费：不带 w 时服务端会把
// 原图整个发出去，一页 50 张实测 2 MB 多、最后一张要好几秒才画完。
func TestJavWallImageURLCarriesWidth(t *testing.T) {
	poster := javWallImageURL(2, "有码/MKON-149", "poster.jpg", "123", "poster")
	thumb := javWallImageURL(2, "有码/MKON-149", "thumb.jpg", "456", "thumb")

	if !strings.Contains(poster, "&w=400") {
		t.Errorf("海报视图应当带 w=400，got %q", poster)
	}
	if !strings.Contains(thumb, "&w=600") {
		t.Errorf("缩略图视图应当带 w=600，got %q", thumb)
	}
	// rev 仍然要在（保存裁剪后必须换 URL，否则浏览器端出旧图）。
	if !strings.Contains(poster, "&v=123") || !strings.Contains(thumb, "&v=456") {
		t.Errorf("rev 丢了：poster=%q thumb=%q", poster, thumb)
	}
}

// 没有图（rel 为空）时返回空串，不能拼出半截地址。
func TestJavWallImageURLEmptyWhenNoPoster(t *testing.T) {
	if got := javWallImageURL(2, "有码/MKON-149", "", "1", "poster"); got != "" {
		t.Errorf("没有文件名时应当是空串，got %q", got)
	}
}

// 未知视图名回落到海报那一档 —— 调用方拼错时宁可给一张稍大的图，也不要 0 宽
// （0 会让服务端走「不缩放」，也就是白改了）。
func TestJavWallImageWidthUnknownViewFallsBack(t *testing.T) {
	if got := javWallImageWidth(""); got != 400 {
		t.Errorf("空视图名 = %d，期望回落到 400", got)
	}
	if got := javWallImageWidth("poster"); got != 400 {
		t.Errorf("poster = %d，期望 400", got)
	}
	if got := javWallImageWidth("thumb"); got != 600 {
		t.Errorf("thumb = %d，期望 600", got)
	}
}

// 拼出来的宽度必须能被 strconv.Atoi 解析（服务端就是那么读的）——
// 防的是「以后有人把宽度改成 "380px" 这种带单位的写法」。
func TestJavWallImageWidthIsPlainInt(t *testing.T) {
	for _, view := range []string{"poster", "thumb", ""} {
		url := javWallImageURL(1, "a", "b.jpg", "", view)
		idx := strings.Index(url, "&w=")
		if idx < 0 {
			t.Fatalf("%s 的地址里没有 w：%q", view, url)
		}
		raw := url[idx+3:]
		if _, err := strconv.Atoi(raw); err != nil {
			t.Errorf("%s 的宽度不是纯整数：%q", view, raw)
		}
	}
}
