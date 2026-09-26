package emby

// 本包只做**纯计算**：解析侧车、拼 nfo 字节、算裁剪窗口、解码+裁图。
// 一切文件读写都由调用方（internal/strm）负责 —— 那边已经有「存在即跳过 + 临时文件」
// 的落盘姿势（writeMetadataFile），两处各写一套只会让同一批文件有两种写盘风格。

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"

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
	X, Y, W, H int
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

	crop := image.NewRGBA(image.Rect(0, 0, rect.W, rect.H))
	// 逐像素搬：用 draw.Draw 要 import golang.org/x/image 或 image/draw 的
	// 子图重映射，而这里是纯粹的矩形搬运，手写循环反而更直白也更好断言。
	for y := 0; y < rect.H; y++ {
		for x := 0; x < rect.W; x++ {
			crop.Set(x, y, img.At(b.Min.X+rect.X+x, b.Min.Y+rect.Y+y))
		}
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, crop, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return thumb, fmt.Errorf("poster 编码失败，原样复制缩略图：%w", err)
	}
	return buf.Bytes(), nil
}
