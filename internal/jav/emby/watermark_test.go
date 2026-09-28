package emby

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

// 水印合成的用例。图标用代码画（不依赖真图），重点盯三件事：
// 四个角各贴得对、缩放到指定位、以及**坏图标要报错而不是静默跳过**。

func testIcon(t *testing.T, w, h int, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestComposePosterAnchors 四个角各贴一次，像素落点要对。
//
// 白色海报 + 纯红图标，贴完直接看那四个角是不是红的 —— 位置错了就抓得到。
func TestComposePosterAnchors(t *testing.T) {
	white := image.NewRGBA(image.Rect(0, 0, 400, 600))
	for y := 0; y < 600; y++ {
		for x := 0; x < 400; x++ {
			white.Set(x, y, color.RGBA{255, 255, 255, 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, white, nil); err != nil {
		t.Fatal(err)
	}
	poster := buf.Bytes()
	red := testIcon(t, 200, 200, color.RGBA{255, 0, 0, 255})

	cases := []struct {
		anchor Anchor
		// 该是红色的采样点（用比例算，避免写死边距）
		probe func(w, h int) image.Point
	}{
		{AnchorTopLeft, func(w, h int) image.Point { return image.Pt(6, 6) }},
		{AnchorTopRight, func(w, h int) image.Point { return image.Pt(w-6, 6) }},
		{AnchorBottomLeft, func(w, h int) image.Point { return image.Pt(6, h-6) }},
		{AnchorBottomRight, func(w, h int) image.Point { return image.Pt(w-6, h-6) }},
	}
	for _, c := range cases {
		out, err := ComposePoster(poster, []Watermark{{Image: red, Anchor: c.anchor}}, 0.15)
		if err != nil {
			t.Fatalf("%s: %v", c.anchor, err)
		}
		img, err := jpeg.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatalf("%s: 结果不是合法 JPEG：%v", c.anchor, err)
		}
		b := img.Bounds()
		p := c.probe(b.Dx(), b.Dy())
		r, g, bl, _ := img.At(p.X, p.Y).RGBA()
		if r>>8 < 180 || g>>8 > 90 || bl>>8 > 90 {
			t.Errorf("%s: (%d,%d) 应当是红的，got r=%d g=%d b=%d", c.anchor, p.X, p.Y, r>>8, g>>8, bl>>8)
		}
		// 反方向的角不该被动
		var other image.Point
		switch c.anchor {
		case AnchorTopLeft:
			other = image.Pt(b.Dx()-6, b.Dy()-6)
		case AnchorTopRight:
			other = image.Pt(6, b.Dy()-6)
		case AnchorBottomLeft:
			other = image.Pt(b.Dx()-6, 6)
		case AnchorBottomRight:
			other = image.Pt(6, 6)
		}
		or, og, ob, _ := img.At(other.X, other.Y).RGBA()
		if or>>8 > 240 && og>>8 > 240 && ob>>8 > 240 {
			// 白的 —— 对
		} else {
			t.Errorf("%s: 对角的 (%d,%d) 不该被动", c.anchor, other.X, other.Y)
		}
	}
}

// TestComposePosterScale 缩放：图标宽度应当约等于「海报宽 × 比例」。
func TestComposePosterScale(t *testing.T) {
	white := image.NewRGBA(image.Rect(0, 0, 400, 600))
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, white, nil)
	icon := testIcon(t, 200, 100, color.RGBA{0, 0, 255, 255})

	out, err := ComposePoster(buf.Bytes(), []Watermark{{Image: icon, Anchor: AnchorBottomRight}}, 0.25)
	if err != nil {
		t.Fatal(err)
	}
	img, _ := jpeg.Decode(bytes.NewReader(out))
	// 从右下角往左扫，量一下蓝块的宽度
	b := img.Bounds()
	// 从边距内侧扫（边距 = 图标宽/16，100 宽时约 6px）—— 贴着最外一圈扫会落在
	// 边距那条白边上，量出 0。
	y := b.Dy() - 12
	width := 0
	for x := b.Dx() - 7; x >= 0; x-- {
		_, _, bl, _ := img.At(x, y).RGBA()
		if bl>>8 > 120 {
			width++
			continue
		}
		break
	}
	want := int(400 * 0.25) // 100
	if width < want-6 || width > want+6 {
		t.Errorf("缩放后宽度 = %d，期望约 %d", width, want)
	}
}

// TestComposePosterErrors 坏图标要**报错**，不能静默跳过。
//
// 编辑器那条路是用户点了保存 —— 悄悄少贴一个角标比报错难查得多
// （与 CropPoster「解不开图必须报错」同一条规矩）。
func TestComposePosterErrors(t *testing.T) {
	white := image.NewRGBA(image.Rect(0, 0, 100, 150))
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, white, nil)

	if _, err := ComposePoster(buf.Bytes(), []Watermark{{Image: []byte("不是图"), Anchor: AnchorTopLeft}}, 0.15); err == nil {
		t.Error("坏图标应当报错")
	}
	if _, err := ComposePoster([]byte("不是海报"), []Watermark{{Image: testIcon(t, 10, 10, color.Black), Anchor: AnchorTopLeft}}, 0.15); err == nil {
		t.Error("坏海报应当报错")
	}
	// 没有水印时原样返回（不重编码）
	same, err := ComposePoster(buf.Bytes(), nil, 0.15)
	if err != nil || !bytes.Equal(same, buf.Bytes()) {
		t.Error("没有水印时应当原样返回")
	}
}

// TestLoadWatermarks 装图标：用户目录优先、缺的落回内置、认不出的名字报错。
func TestLoadWatermarks(t *testing.T) {
	marks, err := LoadWatermarks("", []string{"4k", "leak"})
	if err != nil {
		t.Fatalf("内置那套应当能装上：%v", err)
	}
	if len(marks) != 2 || marks[0].Anchor != AnchorBottomLeft || marks[1].Anchor != AnchorTopRight {
		t.Errorf("锚点不对：%+v", marks)
	}
	// 认不出的名字报错（并点名支持哪些）
	if _, err := LoadWatermarks("", []string{"不存在的"}); err == nil {
		t.Error("认不出的水印名应当报错")
	}
	// 用户目录里没有那个文件 → 落回内置，不报错
	if _, err := LoadWatermarks(t.TempDir(), []string{"sub"}); err != nil {
		t.Errorf("用户目录缺文件时应当落回内置：%v", err)
	}
}
