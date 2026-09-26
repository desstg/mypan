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

// sampleJPEG 造一张纯色 jpeg（不依赖 testdata 里的图片文件）。
func sampleJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
