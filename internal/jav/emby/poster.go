package emby

// 本包只做**纯计算**：解析侧车、拼 nfo 字节、算裁剪窗口、解码+裁图。
// 一切文件读写都由调用方（internal/strm）负责 —— 那边已经有「存在即跳过 + 临时文件」
// 的落盘姿势（writeMetadataFile），两处各写一套只会让同一批文件有两种写盘风格。

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"math"
	"sort"

	// 注册解码器（jpeg 上面已经按名字引入了，顺带就注册了）：
	// 上游图片按 URL 扩展名有 jpg / png / webp 三种（见 internal/jav/image.go 的
	// imageContentType）。webp 不在标准库里，而这里不为它加 x/image 依赖 ——
	// 解不开图时 BuildPoster 会退化成「原图复制」，那不是错误路径。
	_ "image/gif"
	_ "image/png"
)

// PosterRatio 是 Emby 海报的标准比例（2:3）。缩略图 thumb.jpg 与它不是一个比例：
// JAVDB 的封面是横图（实测 800x538），Emby 的 thumb 就是横的，所以 thumb 原样写、
// poster 从 thumb 裁。
const PosterRatio = 2.0 / 3.0

// PosterWideRatio 是**横向**取窗时实际用的比例，比标准海报宽一点。
//
// 用户看过真封面之后一步步定的：原话「截图还可以截宽一点点，右边不动，左边宽一点点」，
// 然后拿 800x538 的真封面裁了一组候选图（0.667 / 0.70 / 0.72 / 0.75 / 0.78）给他挑，
// 最后选的是 **0.70**（538x0.70 = 376px 宽）。
//
// 取窗的右边**仍然贴齐图片右边**（见 PosterCropWindow 的 CropRight），放宽的部分全部
// 加在左边 —— 所以封面左边那栏（演员名、剧情小字）会多进来一点。
// Emby 对略宽的海报是等比缩放显示，不会有黑边。
//
// **只管横向取窗**：竖图本来就是把宽度取满、靠裁高度凑比例，再放宽只会多切掉高度，
// 与「别卡太紧」正好相反 —— 所以竖图仍按 PosterRatio 走（见 CropRatioFor）。
const PosterWideRatio = 0.70

// CropRatioFor 返回这张图该按哪个比例取窗。
//
// 宽图（比 2:3 更宽，绝大多数 JAVDB 封面）：用放宽后的 PosterWideRatio。
// 竖图：用标准 PosterRatio —— 它本来就是按宽度取满，放宽等于多切高度。
func CropRatioFor(w, h int) float64 {
	if w <= 0 || h <= 0 {
		return PosterRatio
	}
	if float64(w)/float64(h) > PosterRatio {
		return PosterWideRatio
	}
	return PosterRatio
}

// jpegQuality 是重新编码时的质量。90 对海报足够，再高只是白白撑大文件。
const jpegQuality = 90

// CropRect 是裁剪窗口（像素，左上角原点）。
type CropRect struct {
	// json tag 是**snake 小写**：这几个结构体会直接通过 API 出给前端
	// （海报墙编辑器的裁剪框），而前端读的是 `rect.w`。少了 tag 序列化出去是
	// `{"X":…,"W":…}`，前端拿到 undefined、拼出来的样式是 `undefinedpx` ——
	// 表现是**框根本不显示**，而且不报错。这一族坑在本项目里踩过好几次。
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

// CropStrategy 是「从大图的哪一块裁出海报」。
type CropStrategy int

const (
	// CropFace 人脸优先：把人脸放进窗口（尽量居中），检不到脸时退化为居中。
	CropFace CropStrategy = iota
	// CropRight 取右侧：**有码片专用**。
	//
	// 为什么有码不需要人脸识别：有码的封面是横图，右半边就是正封面、左边那条是
	// 封底或样品条，取右边一定是人。用户明确要求这一档「直接截大图的右边，
	// 不用人脸识别」—— 少跑一次级联检测，这一档还更快。
	CropRight
)

// FaceBox 是一张人脸的方框。Found 为假时其余字段无意义。
type FaceBox struct {
	X, Y, Size int
	Found      bool
}

// PosterCropWindow 算裁剪窗口。**纯函数**：不碰图片、不碰盘，只做整数运算，
// 所以「有码取右边」这类规则能被穷举测试。
//
// 两种取窗方式，按图片比例自动选：
//
//	宽图（w/h > 2/3）：高度吃满，宽度 = h*2/3，横向滑动 → 有码贴右、人脸优先、否则居中
//	窄图（w/h <= 2/3）：宽度吃满，高度 = w*3/2，纵向滑动 → 人脸优先，否则居中
//
// 窄图那一路不是凑数：有些封面本身就是竖的（尤其是国产/无码那批），
// 只按宽度裁会得到一张比 2:3 更矮的「海报」，Emby 会把它拉变形。
func PosterCropWindow(w, h int, ratio float64, strategy CropStrategy, face FaceBox) CropRect {
	if w <= 0 || h <= 0 {
		return CropRect{}
	}
	if ratio <= 0 {
		ratio = PosterRatio
	}

	// 宽图：高度吃满，横着找窗口。
	if float64(w)/float64(h) > ratio {
		cw := int(float64(h) * ratio)
		if cw < 1 {
			cw = 1
		}
		if cw > w {
			cw = w
		}
		x := (w - cw) / 2 // 默认居中
		if strategy == CropRight {
			x = w - cw
		} else if face.Found {
			// 让窗口中心对齐人脸中心，再夹进边界。
			x = faceCenterX(face) - cw/2
		}
		return CropRect{X: clamp(x, 0, w-cw), Y: 0, W: cw, H: h}
	}

	// 窄图：宽度吃满，竖着找窗口。
	ch := int(float64(w) / ratio)
	if ch < 1 {
		ch = 1
	}
	if ch > h {
		ch = h
	}
	y := (h - ch) / 2
	if face.Found {
		// 竖着滑动时把人脸往上放一点（人像构图里脸通常在上半部），
		// 而不是死死居中 —— 居中会经常把头顶切掉。
		y = faceCenterY(face) - ch/3
	}
	return CropRect{X: 0, Y: clamp(y, 0, h-ch), W: w, H: ch}
}

func faceCenterX(face FaceBox) int { return face.X + face.Size/2 }
func faceCenterY(face FaceBox) int { return face.Y + face.Size/2 }

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// BuildPoster 把 thumb 的字节裁成 2:3 的 poster 字节（JPEG）。
//
// 三种情况：有码直接取右；其余走人脸识别、检不到就居中；**解不开图时原样返回** ——
// 比例不对的 poster 会被 Emby letterbox，而没有 poster 就是一块灰占位，
// 前者明显更好，而且这条路不该把生成整条链拖失败。
func BuildPoster(thumb []byte, censored bool) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(thumb))
	if err != nil {
		return thumb, fmt.Errorf("缩略图解不开，poster 原样复制：%w", err)
	}
	b := img.Bounds()
	strategy := CropFace
	if censored {
		strategy = CropRight
	}
	var face FaceBox
	if strategy == CropFace {
		face, _ = DetectFace(img)
	}
	rect := PosterCropWindow(b.Dx(), b.Dy(), CropRatioFor(b.Dx(), b.Dy()), strategy, face)

	crop := cropImage(img, rect)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, crop, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return thumb, fmt.Errorf("poster 编码失败，原样复制缩略图：%w", err)
	}
	return buf.Bytes(), nil
}

// cropImage 把 img 的 rect 那块逐像素搬到新图上。
//
// 用 draw.Draw 要 import golang.org/x/image（或自己做子图重映射），而这里是纯粹的
// 矩形搬运，手写循环反而更直白、也更好断言。
func cropImage(img image.Image, rect CropRect) *image.RGBA {
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, rect.W, rect.H))
	for y := 0; y < rect.H; y++ {
		for x := 0; x < rect.W; x++ {
			dst.Set(x, y, img.At(b.Min.X+rect.X+x, b.Min.Y+rect.Y+y))
		}
	}
	return dst
}

// ThumbSize 只读文件头就能拿到尺寸（不解整张图）—— 编辑器要靠它把「原生像素」
// 与「屏幕上的比例」换算起来。
func ThumbSize(thumb []byte) (int, int, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(thumb))
	if err != nil {
		return 0, 0, fmt.Errorf("读不出图片尺寸：%w", err)
	}
	return cfg.Width, cfg.Height, nil
}

// DefaultPosterRect 算「生成器会给的」那个窗口（编辑器首次打开、或反推失败时的兜底）。
//
// censored 由**本地侧车**给（`type == "0"`）：有码贴右、其余走人脸检测。
func DefaultPosterRect(thumb []byte, censored bool) (CropRect, error) {
	img, _, err := image.Decode(bytes.NewReader(thumb))
	if err != nil {
		return CropRect{}, fmt.Errorf("缩略图解不开：%w", err)
	}
	b := img.Bounds()
	strategy := CropFace
	if censored {
		strategy = CropRight
	}
	var face FaceBox
	if strategy == CropFace {
		face, _ = DetectFace(img)
	}
	return PosterCropWindow(b.Dx(), b.Dy(), CropRatioFor(b.Dx(), b.Dy()), strategy, face), nil
}

// CropPoster 按**显式窗口**裁 poster。
//
// 与 BuildPoster 的分工：那个是「自己算窗口」（扫描路径，best-effort，解不开图
// 就把原图带回来）；这个是「用户指定窗口」（编辑器路径，解不开图**必须报错** ——
// 用户点了保存却什么都没改，比报错难查得多）。像素搬运与 JPEG 质量两边一致，
// 免得同一张海报两种手感。
// marks 非空时**在编码之前**把水印画到裁出来的图上：这样整条路只有**一代 JPEG**。
//
// 为什么不先编码再调 ComposePoster：那样要多解码一代、多编码一代。实测那一代
// 的代价是 60.65 dB（肉眼看不出），但白多两次整图编解码（376×538 大约各 10ms），
// 而且在"水印"这种"本来就该在像素上做"的事上多绕一圈没有意义。
func CropPoster(thumb []byte, rect CropRect, marks []Watermark, scale, margin float64) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(thumb))
	if err != nil {
		return nil, fmt.Errorf("缩略图解不开：%w", err)
	}
	b := img.Bounds()
	rect = ClampRect(rect, b.Dx(), b.Dy())
	if rect.W <= 0 || rect.H <= 0 {
		return nil, fmt.Errorf("裁剪窗口超出图片范围")
	}
	crop := cropImage(img, rect)
	if len(marks) > 0 {
		if err := drawWatermarks(crop, marks, scale, margin); err != nil {
			return nil, err
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, crop, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, fmt.Errorf("poster 编码失败：%w", err)
	}
	return buf.Bytes(), nil
}

// ClampRect 把窗口夹进图片边界内（宽高至少 1 像素）。
func ClampRect(rect CropRect, w, h int) CropRect {
	if w <= 0 || h <= 0 {
		return CropRect{}
	}
	if rect.W > w {
		rect.W = w
	}
	if rect.H > h {
		rect.H = h
	}
	if rect.W < 1 {
		rect.W = 1
	}
	if rect.H < 1 {
		rect.H = 1
	}
	rect.X = clamp(rect.X, 0, w-rect.W)
	rect.Y = clamp(rect.Y, 0, h-rect.H)
	return rect
}

// MatchPosterRect 反推「当前这张 poster 是从 thumb 的哪一块裁出来的」。
//
// 为什么值得做：编辑器打开时选框如果不在海报的真实位置上，用户为了改标题点保存、
// 顺手把框留原样倒没事；但只要他碰一下框，海报就会跑到另一个位置 —— 而他以为
// 自己在微调。框的起点必须是他看到的现状。
//
// 能这么算的前提是：**裁切不缩放**（poster 与 thumb 同高），所以只有横向一个自由度。
// 算法：逐列灰度均值 → 在 [0, tw-pw] 上找平均绝对差最小的 x → 置信闸门。
//
// 拿不准（竖裁、缩放、图坏了、差异不够显著）就返回 false，调用方回落默认窗口。
func MatchPosterRect(thumb, poster []byte) (CropRect, bool) {
	tw, th, err := ThumbSize(thumb)
	if err != nil {
		return CropRect{}, false
	}
	pw, ph, err := ThumbSize(poster)
	if err != nil {
		return CropRect{}, false
	}
	// 高度对不上就**放弃** —— 这一条是刻意的，不是没做。
	//
	// 实测用户库里有一批 poster 是「宽度相同、高度不同」（`340x487` 配 `800x538`，
	// 那是从更早一版 `800x487` 的封面裁的，后来上游把封面重新裁了一版）。这种关系下
	// "当前这张 poster 对应 thumb 的哪一块"**没有唯一答案**（它甚至可能不是这段像素的
	// 重采样），硬猜一个框出来比回落默认窗口糟得多：框看起来"就是当前位置"，
	// 用户微调一下反而把海报裁到别处去了。
	//
	// 回落默认窗口之后，只要用户点一次「裁剪」，新海报就与当前 thumb 对齐了 ——
	// 下一次打开就是 matched。
	if ph != th || pw > tw || pw < 8 || ph < 8 {
		return CropRect{}, false
	}
	tImg, _, err := image.Decode(bytes.NewReader(thumb))
	if err != nil {
		return CropRect{}, false
	}
	pImg, _, err := image.Decode(bytes.NewReader(poster))
	if err != nil {
		return CropRect{}, false
	}
	tCols := columnMeans(tImg, 64)
	pCols := columnMeans(pImg, 64)
	if len(tCols) < pw || len(pCols) < pw {
		return CropRect{}, false
	}

	// 逐个候选位置算 MAD，取最优，并留一份用来判「有没有歧义」。
	mads := make([]float64, 0, len(tCols))
	bestX, best := 0, math.MaxFloat64
	for x := 0; x+pw <= len(tCols); x++ {
		mad := colsMAD(tCols, pCols, x, pw)
		mads = append(mads, mad)
		if mad < best {
			best, bestX = mad, x
		}
	}

	// 置信闸门：**最优位置要比「随便一个位置」好得多**。
	//
	// 拿中位数当"随便一个位置"的水位，而不是拿次优 + 一个绝对差值：绝对差值的尺度
	// 随图片噪声变（同一张图重编码一次 best 可能只有 0.1，真封面重编码则可能到 2），
	// 定死一个阈值不是太严就是太松。实测自己裁出来的海报 best≈0.1、中位数≈4 ——
	// 一眼就看得出它是唯一的；而列图案重复的图（那种真歧义）best 与中位数几乎相等，
	// 正好被挡在门外。
	const maxMAD = 6.0 // 绝对上限：连最像的位置都差得远，就不是从这张图来的
	if best > maxMAD {
		return CropRect{}, false
	}
	sorted := append([]float64(nil), mads...)
	sort.Float64s(sorted)
	median := sorted[len(sorted)/2]
	if median <= 0 || best > median*0.5 {
		return CropRect{}, false
	}
	return CropRect{X: bestX, Y: 0, W: pw, H: ph}, true
}

// colsMAD 算「把 poster 的列贴在 thumb 的第 x 列」时的平均绝对差。
func colsMAD(thumbCols, posterCols []float64, x, pw int) float64 {
	var sum float64
	for j := 0; j < pw; j++ {
		sum += absFloat(thumbCols[x+j] - posterCols[j])
	}
	return sum / float64(pw)
}

// columnMeans 逐列算灰度均值（每列最多取 sampleRows 行，够用且快）。
func columnMeans(img image.Image, sampleRows int) []float64 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil
	}
	step := 1
	if h > sampleRows && sampleRows > 0 {
		step = h / sampleRows
	}
	out := make([]float64, w)
	for y := 0; y < h; y += step {
		for x := 0; x < w; x++ {
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			// 0.299/0.587/0.114 的灰度权重；RGBA() 返回 16 位，>>8 回 8 位。
			out[x] += float64((299*(r>>8)+587*(g>>8)+114*(bl>>8))/1000) / float64(h/step+1)
		}
	}
	return out
}

func nearX(a, b, tol int) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tol
}

func absFloat(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
