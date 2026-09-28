package emby

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

// TestPosterCropWindow 取窗规则。**纯函数**，所以这里能穷举那些「看起来该切哪儿」的边界。
func TestPosterCropWindow(t *testing.T) {
	// 有码那种横图：800x538（实测的 JAVDB 封面尺寸）
	// 高度吃满 538，宽 = 538*2/3 = 358.67 → 358；取右边 → x = 800-358 = 442
	cases := []struct {
		label    string
		w, h     int
		strategy CropStrategy
		face     FaceBox
		want     CropRect
	}{
		{
			label: "有码取右边", w: 800, h: 538, strategy: CropRight,
			want: CropRect{X: 442, Y: 0, W: 358, H: 538},
		},
		{
			label: "有码取右边：即使检到了脸也不改",
			w:     800, h: 538, strategy: CropRight,
			face: FaceBox{X: 20, Y: 20, Size: 100, Found: true},
			want: CropRect{X: 442, Y: 0, W: 358, H: 538},
		},
		{
			label: "无脸 → 居中", w: 800, h: 538, strategy: CropFace,
			want: CropRect{X: 221, Y: 0, W: 358, H: 538},
		},
		{
			label: "人脸在左边 → 窗口跟着往左，但夹在边界内",
			w:     800, h: 538, strategy: CropFace,
			face: FaceBox{X: 10, Y: 100, Size: 120, Found: true},
			want: CropRect{X: 0, Y: 0, W: 358, H: 538},
		},
		{
			label: "人脸在右边 → 窗口跟着往右",
			w:     800, h: 538, strategy: CropFace,
			face: FaceBox{X: 700, Y: 100, Size: 100, Found: true},
			want: CropRect{X: 442, Y: 0, W: 358, H: 538},
		},
		{
			label: "人脸居中偏右 → 窗口中心对齐人脸",
			w:     1000, h: 600, strategy: CropFace,
			face: FaceBox{X: 600, Y: 200, Size: 100, Found: true},
			// 宽 = 600*2/3 = 400；人脸中心 650 → x = 650-200 = 450；上限 1000-400 = 600
			want: CropRect{X: 450, Y: 0, W: 400, H: 600},
		},
		{
			label: "已经是 2:3 → 全图", w: 400, h: 600, strategy: CropFace,
			want: CropRect{X: 0, Y: 0, W: 400, H: 600},
		},
		{
			label: "竖图（比 2:3 更窄）→ 按宽度取、纵向滑",
			w:     300, h: 900, strategy: CropFace,
			// 高 = 300/(2/3) = 450；居中 → y = (900-450)/2 = 225
			want: CropRect{X: 0, Y: 225, W: 300, H: 450},
		},
		{
			label: "竖图 + 人脸靠上 → 窗口上移但不越界",
			w:     300, h: 900, strategy: CropFace,
			face: FaceBox{X: 100, Y: 10, Size: 80, Found: true},
			// 人脸中心 y=50，减去 450/3=150 → -100 → 夹到 0
			want: CropRect{X: 0, Y: 0, W: 300, H: 450},
		},
		{
			label: "极窄的图不产生 0 宽", w: 10, h: 1000, strategy: CropFace,
			// 高 = 10/(2/3) = 15；居中 → y = (1000-15)/2 = 492
			want: CropRect{X: 0, Y: 492, W: 10, H: 15},
		},
		{
			label: "空图不 panic", w: 0, h: 0, strategy: CropFace,
			want: CropRect{},
		},
	}
	for _, c := range cases {
		if got := PosterCropWindow(c.w, c.h, PosterRatio, c.strategy, c.face); got != c.want {
			t.Errorf("%s：PosterCropWindow(%d,%d,%v) = %+v, 期望 %+v", c.label, c.w, c.h, c.strategy, got, c.want)
		}
	}
}

// TestBuildPosterDegradesOnBadImage 解不开的图退化成原样复制，**不报失败** ——
// 一次图片格式意外不该把整部片的元数据生成翻掉。
func TestBuildPosterDegradesOnBadImage(t *testing.T) {
	raw := []byte("这不是图片")
	got, err := BuildPoster(raw, false)
	if err == nil {
		t.Error("应当把原因带出来（调用方记 warn）")
	}
	if !bytes.Equal(got, raw) {
		t.Error("解不开时应当原样返回字节")
	}
}

// TestDetectFaceOnBlankImage 纯色图里没有人脸 —— 这条同时是「级联模型真的被解包了」的
// 冒烟测试：模型损坏时 Unpack 会失败，这里会走「检不到」分支，而下面的
// TestFaceCascadeUnpacks 会把它显式钉住。
func TestDetectFaceOnBlankImage(t *testing.T) {
	if _, found := DetectFace(image.NewRGBA(image.Rect(0, 0, 100, 100))); found {
		t.Error("纯色图里不该检到脸")
	}
}

// TestFaceCascadeUnpacks 模型能解包。
//
// **这条测试的价值在 `.gitattributes`**：`cascades/facefinder` 是个没有扩展名的裸二进制，
// 仓库根那句 `* text=auto eol=lf` 会把它当文本做行尾转换（Windows 检出时）、
// 模型当场损坏，而症状只是「检测不到脸」。这条红了先去看那一行还在不在。
func TestFaceCascadeUnpacks(t *testing.T) {
	if len(facefinder) < 100_000 {
		t.Fatalf("级联模型只有 %d 字节 —— 多半被行尾转换损坏了，检查 .gitattributes", len(facefinder))
	}
	classifier, err := faceClassifier()
	if err != nil {
		t.Fatalf("级联模型解包失败（检查 .gitattributes 有没有标 binary）：%v", err)
	}
	if classifier == nil {
		t.Fatal("解包结果是 nil")
	}
}

// TestCropRatioFor 横向取窗放宽、竖向取窗不放宽。
//
// 放宽只对**宽图**有意义：竖图是把宽度取满、靠裁高度凑比例，再放宽等于多切高度，
// 与「别把人物卡太紧」正好相反。
func TestCropRatioFor(t *testing.T) {
	if got := CropRatioFor(800, 538); got != PosterWideRatio {
		t.Errorf("横图应当用放宽后的比例 %v，got %v", PosterWideRatio, got)
	}
	if got := CropRatioFor(400, 600); got != PosterRatio {
		t.Errorf("正好 2:3 的图不该放宽，got %v", got)
	}
	if got := CropRatioFor(300, 900); got != PosterRatio {
		t.Errorf("竖图不该放宽，got %v", got)
	}
	if got := CropRatioFor(0, 0); got != PosterRatio {
		t.Errorf("尺寸无效时回落标准比例，got %v", got)
	}
	// 放宽是「一点点」：别悄悄变成 16:9 那种
	if PosterWideRatio <= PosterRatio || PosterWideRatio > 0.8 {
		t.Errorf("放宽幅度不像是「一点点」：%v → %v", PosterRatio, PosterWideRatio)
	}
}

// sampleJPEG 造一张确定图案的 jpeg（不依赖 testdata 里的图片文件）。
func sampleJPEG(t *testing.T, w, h int) []byte {
	return sampleJPEGSeed(t, w, h, 0x9E3779B1)
}

// sampleJPEGSeed 同上的可换种子版：**"不相干的图"必须换种子**，
// 否则它就是同一张图案的另一块，反推成功是理所当然的（用例会误报）。
func sampleJPEGSeed(t *testing.T, w, h int, seed uint32) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			// 按 **8x8 块**伪随机（块内同值）：既保证每一列的均值都不同（不然反推
			// 位置就是真歧义，代码会正确地拒绝、测不出东西），又是低频、JPEG 友好
			// （逐像素噪声那种高频图案会被 DCT 抹得列均值乱飘，同样测不出东西）。
			h := uint32(x/8)*seed ^ uint32(y/8)*0x85EBCA6B
			img.Set(x, y, color.RGBA{
				R: uint8(h >> 16), G: uint8(h >> 8), B: uint8(h), A: 255,
			})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestCropPoster 按显式窗口裁：像素对得上、越界被夹、坏图**报错**。
//
// 报错这条是有意的（与 BuildPoster 的降级语义相反）：这里是用户在编辑器里点了
// 「保存海报裁剪」，悄悄什么都不做比报错难查得多。
func TestCropPoster(t *testing.T) {
	thumb := sampleJPEG(t, 800, 538)
	out, err := CropPoster(thumb, CropRect{X: 400, Y: 0, W: 300, H: 400}, nil, 0, 0)
	if err != nil {
		t.Fatalf("CropPoster：%v", err)
	}
	img, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 300 || img.Bounds().Dy() != 400 {
		t.Errorf("尺寸 = %dx%d，期望 300x400", img.Bounds().Dx(), img.Bounds().Dy())
	}
	// 逐像素对：取源图的同一块比
	src, _ := jpeg.Decode(bytes.NewReader(thumb))
	for _, p := range [][2]int{{0, 0}, {150, 200}, {299, 399}} {
		want := src.At(400+p[0], 0+p[1])
		got := img.At(p[0], p[1])
		wr, wg, wb, _ := want.RGBA()
		gr, gg, gb, _ := got.RGBA()
		// JPEG 有损，给一点容差（这块图案在块边界上有跳变，振铃会大一点）
		if absInt(int(wr>>8)-int(gr>>8)) > 24 || absInt(int(wg>>8)-int(gg>>8)) > 24 || absInt(int(wb>>8)-int(gb>>8)) > 24 {
			t.Errorf("(%d,%d) 像素对不上：源 %v / 裁出 %v", p[0], p[1], want, got)
		}
	}

	// 起点越界 → 夹进边界，宽高不变（没超过图宽就不缩）
	out, err = CropPoster(thumb, CropRect{X: 700, Y: 400, W: 300, H: 400}, nil, 0, 0)
	if err != nil {
		t.Fatalf("越界应当夹紧而不是报错：%v", err)
	}
	img, _ = jpeg.Decode(bytes.NewReader(out))
	if img.Bounds().Dx() != 300 || img.Bounds().Dy() != 400 {
		t.Errorf("尺寸 = %dx%d，期望 300x400", img.Bounds().Dx(), img.Bounds().Dy())
	}
	// 宽高本身超过图 → 缩到图内
	out, err = CropPoster(thumb, CropRect{X: 0, Y: 0, W: 900, H: 600}, nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	img, _ = jpeg.Decode(bytes.NewReader(out))
	if img.Bounds().Dx() != 800 || img.Bounds().Dy() != 538 {
		t.Errorf("超过图片尺寸时应缩到 %dx%d，got %dx%d", 800, 538, img.Bounds().Dx(), img.Bounds().Dy())
	}

	if _, err := CropPoster([]byte("不是图片"), CropRect{X: 0, Y: 0, W: 10, H: 10}, nil, 0, 0); err == nil {
		t.Error("解不开图时必须报错（编辑器路径）")
	}
}

// TestDefaultPosterRect 默认窗口就是生成器会给的那一个。
func TestDefaultPosterRect(t *testing.T) {
	thumb := sampleJPEG(t, 800, 538)
	for _, censored := range []bool{true, false} {
		got, err := DefaultPosterRect(thumb, censored)
		if err != nil {
			t.Fatalf("DefaultPosterRect：%v", err)
		}
		img, _ := jpeg.Decode(bytes.NewReader(thumb))
		strategy := CropFace
		if censored {
			strategy = CropRight
		}
		face, _ := DetectFace(img)
		want := PosterCropWindow(800, 538, CropRatioFor(800, 538), strategy, face)
		if got != want {
			t.Errorf("censored=%v 的默认窗口 = %+v，期望 %+v", censored, got, want)
		}
	}
}

func TestThumbSize(t *testing.T) {
	w, h, err := ThumbSize(sampleJPEG(t, 800, 538))
	if err != nil {
		t.Fatalf("ThumbSize：%v", err)
	}
	if w != 800 || h != 538 {
		t.Errorf("尺寸 = %dx%d", w, h)
	}
	if _, _, err := ThumbSize([]byte("xx")); err == nil {
		t.Error("坏图应当报错")
	}
}

// TestMatchPosterRect 反推当前裁剪位置：自己裁出来的必须认得，无关的图必须说不认识。
func TestMatchPosterRect(t *testing.T) {
	thumb := sampleJPEG(t, 800, 538)
	rect := CropRect{X: 420, Y: 0, W: 360, H: 538}
	poster, err := CropPoster(thumb, rect, nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := MatchPosterRect(thumb, poster)
	if !ok {
		t.Fatal("自己裁出来的海报应当能反推出位置")
	}
	if absInt(got.X-rect.X) > 1 || got.W != rect.W || got.H != rect.H {
		t.Errorf("反推 = %+v，期望 %+v（±1px）", got, rect)
	}

	// 另一张不相干的图（换种子 → 图案与 thumb 无关）→ 不该硬认
	other := sampleJPEGSeed(t, 360, 538, 0x27D4EB2F)
	if _, ok := MatchPosterRect(thumb, other); ok {
		t.Error("不相干的图不该反推成功")
	}
	// 高度不同（竖裁过）→ v1 直接放弃
	tall, err := CropPoster(sampleJPEG(t, 800, 800), CropRect{X: 0, Y: 0, W: 400, H: 600}, nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := MatchPosterRect(thumb, tall); ok {
		t.Error("高度不同时不该反推成功")
	}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
