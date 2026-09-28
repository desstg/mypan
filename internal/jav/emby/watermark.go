package emby

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
)

// 海报水印：把几张图标贴到裁好的 poster 上。
//
// 图标由用户提供（`web/public/logos/suiyin/` 那套，同时也 `go:embed` 进这里当默认值）。
// **本包不读文件、不读设置**（与整个包「纯计算」的定位一致，见 sidecar.go 顶部）——
// 图标字节与大小比例都由调用方注入。
//
// 位置是固定的四角（用户定的）：
//
//	4k.png   左下      8k.png  左下（与 4K 互斥，同一位置）
//	leak.png 右上      sub.png 左上        umr.png 右下

// Anchor 是水印贴在海报的哪个角。
type Anchor string

const (
	AnchorTopLeft     Anchor = "top_left"
	AnchorTopRight    Anchor = "top_right"
	AnchorBottomLeft  Anchor = "bottom_left"
	AnchorBottomRight Anchor = "bottom_right"
)

// Watermark 是一张要贴上去的图标（PNG 字节 + 贴哪个角）。
type Watermark struct {
	// Image 是图标的**原始字节**（PNG），由调用方从内置或用户目录读出来。
	Image  []byte
	Anchor Anchor
}

// DefaultWatermarkScale 是水印宽度占海报宽度的比例（默认值）。
//
// 用户提出的这套图标很大（4k/8k 是 1510×1370，另三张 3671×3756），而 poster 通常
// 也就三四百像素宽 —— 不缩放的话一张图标就盖满整张海报。
//
// **这个数是拿真图出样张挑出来的**：0.08 / 0.10 / 0.12 / 0.15 / 0.20 五档，
// 0.20 那个左上角的「字幕」标会压到标题字上。用户先选 0.15、又定 0.18
// （比 15% 略大一点，同时把贴边距收到 1/16 让它更靠边）。
// 设置项 jav_watermark_scale，用户可微调。
const DefaultWatermarkScale = 0.18

// 图标默认放在哪张图的哪个角 —— 与用户给的图标一一对应。
//
// 键是「水印 id」，与前端选项和设置项用的是同一套字符串。
var defaultWatermarkAnchors = map[string]Anchor{
	"4k":   AnchorBottomLeft,
	"8k":   AnchorBottomLeft,
	"leak": AnchorTopRight,
	"sub":  AnchorTopLeft,
	"umr":  AnchorBottomRight,
}

// WatermarkNames 是认得的图标名（= 文件名去掉 .png）。顺序即界面上那一排的顺序。
var WatermarkNames = []string{"sub", "leak", "umr", "4k", "8k"}

// WatermarkAnchor 返回某个图标固定贴哪个角；不认识的名字返回空。
func WatermarkAnchor(name string) (Anchor, bool) {
	a, ok := defaultWatermarkAnchors[name]
	return a, ok
}

// ComposePoster 把若干水印贴到 poster 上，返回新的 JPEG 字节。
//
// scale 是水印宽度占海报宽度的比例（<=0 用 DefaultWatermarkScale）。
// 贴边距用 DefaultWatermarkMargin —— 设置页里两个滑杆分别调大小与边距。
//
// 失败返回 error（**不降级**）：编辑器那条路是用户点了保存，悄悄什么都不做比报错难查
// ——与 CropPoster 同一条规矩。扫描路径的调用方自己决定要不要降级。
func ComposePoster(poster []byte, marks []Watermark, scale float64) ([]byte, error) {
	return ComposePosterWithMargin(poster, marks, scale, DefaultWatermarkMargin)
}

// DefaultWatermarkMargin 是贴边距占「水印宽度」的比例。
//
// 一路收下来的：1/8 → 1/16 → **1/50**。每次都是用户看过真图说"还不够靠边"。
// 18% 那档一个图标约 68px 宽，1/50 就是 ~1px —— 基本上就是贴在边框上。
// 设置页里可调（0 就是像素级贴边）。
const DefaultWatermarkMargin = 1.0 / 50.0

// ComposePosterWithMargin 同 ComposePoster，但贴边距可调。
func ComposePosterWithMargin(poster []byte, marks []Watermark, scale, margin float64) ([]byte, error) {
	if len(marks) == 0 {
		return poster, nil
	}
	if scale <= 0 {
		scale = DefaultWatermarkScale
	}
	base, _, err := image.Decode(bytes.NewReader(poster))
	if err != nil {
		return nil, fmt.Errorf("海报解不开：%w", err)
	}
	b := base.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), base, b.Min, draw.Src)

	if err := drawWatermarks(dst, marks, scale, margin); err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, fmt.Errorf("海报编码失败：%w", err)
	}
	return buf.Bytes(), nil
}

// drawWatermarks 把水印画到**已经解好码的图**上（就地图）。
//
// 抽出来是为了让"裁 + 贴"能在**同一次编码**里做完（CropPoster 用它）——
// 那样整条路只有一代 JPEG，见那里的注释。
func drawWatermarks(dst *image.RGBA, marks []Watermark, scale, margin float64) error {
	if len(marks) == 0 {
		return nil
	}
	if scale <= 0 {
		scale = DefaultWatermarkScale
	}
	for _, mark := range marks {
		if len(mark.Image) == 0 {
			continue
		}
		markImg, _, decodeErr := image.Decode(bytes.NewReader(mark.Image))
		if decodeErr != nil {
			// 用户自己指定的目录里塞了个坏文件时要点名是哪一个 —— 只说「解码失败」
			// 会让人把整批图标都怀疑一遍。
			return fmt.Errorf("水印图标解不开（%s）：%w", mark.Anchor, decodeErr)
		}
		// **先裁掉图标自带的透明边**再缩放：贴边距是拿图标的外框算的，而图标自己
		// 若带一圈空白（用户给的 4k.png / 8k.png 四周各空 6.6%），那圈空白也算进了
		// 外框 —— 于是"贴到边"看起来仍然离边一截，而且每个图标空得还不一样多。
		// 裁成实心区之后，五个图标贴边距一致，换成别的素材也自动成立。
		trimmed := trimTransparent(markImg)
		scaled := scaleImage(trimmed, int(float64(dst.Bounds().Dx())*scale))
		placeImage(dst, scaled, mark.Anchor, margin)
	}
	return nil
}

// trimTransparent 裁掉四周的全透明/近全透明像素，返回实心那块。
//
// 阈值取 alpha >= 8（1/32）：低于它的像素肉眼与全透明无异，留着只会把外框撑大。
// 整张全透明时原样返回（后面按"没贴"处理，不会 panic）。
func trimTransparent(src image.Image) image.Image {
	b := src.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return src
	}
	minX, minY, maxX, maxY := b.Max.X, b.Max.Y, b.Min.X-1, b.Min.Y-1
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			_, _, _, a := src.At(x, y).RGBA()
			if a>>8 < 8 {
				continue
			}
			if x < minX {
				minX = x
			}
			if y < minY {
				minY = y
			}
			if x > maxX {
				maxX = x
			}
			if y > maxY {
				maxY = y
			}
		}
	}
	if maxX < minX || maxY < minY {
		return src // 整张透明
	}
	if minX == b.Min.X && minY == b.Min.Y && maxX == b.Max.X-1 && maxY == b.Max.Y-1 {
		return src // 本来就没有空白边，省一次拷贝
	}
	// 用 SubImage 视图（零拷贝），缩放那一步逐像素读，不关心底层怎么存。
	return subImage(src, image.Rect(minX, minY, maxX+1, maxY+1))
}

// subImage 取 src 的一个矩形视图（等价于标准库的 SubImage，但源是 image.Image）。
func subImage(src image.Image, r image.Rectangle) image.Image {
	type subImager interface {
		SubImage(image.Rectangle) image.Image
	}
	if si, ok := src.(subImager); ok {
		return si.SubImage(r)
	}
	// 极少数实现没有 SubImage：拷一份。
	dst := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	for y := 0; y < r.Dy(); y++ {
		for x := 0; x < r.Dx(); x++ {
			dst.Set(x, y, src.At(r.Min.X+x, r.Min.Y+y))
		}
	}
	return dst
}

// scaleImage 把图缩放到指定宽度（保持比例），用**最近邻**。
//
// 不用 golang.org/x/image/draw：本包至今没有那个依赖，而图标缩放对画质要求不高
// （贴上去就一两百像素宽，而且是"一眼能认出是什么"的角标）。手写循环与
// 本包既有的 cropImage 也是同一个风格。
func scaleImage(src image.Image, width int) *image.RGBA {
	b := src.Bounds()
	if width <= 0 || b.Dx() <= 0 {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	if width >= b.Dx() {
		// 不放大：图标本来就比目标大，放大只会糊，而且没必要。
		width = b.Dx()
	}
	height := b.Dy() * width / b.Dx()
	if height < 1 {
		height = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		sy := b.Min.Y + y*b.Dy()/height
		for x := 0; x < width; x++ {
			sx := b.Min.X + x*b.Dx()/width
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	return dst
}

// placeImage 把图标按锚点贴到海报上（带边距）。
func placeImage(dst *image.RGBA, mark *image.RGBA, anchor Anchor, marginRatio float64) {
	if mark == nil {
		return
	}
	db, mb := dst.Bounds(), mark.Bounds()
	// 边距 = 水印宽度的 1/16（跟着缩放走，用户要求"靠边一点"）。
	// 边距 = 水印宽度 × marginRatio（跟着缩放走，设置页里可调）。
	if marginRatio < 0 {
		marginRatio = DefaultWatermarkMargin
	}
	margin := int(float64(mb.Dx()) * marginRatio)
	var x, y int
	switch anchor {
	case AnchorTopLeft:
		x, y = margin, margin
	case AnchorTopRight:
		x, y = db.Dx()-mb.Dx()-margin, margin
	case AnchorBottomLeft:
		x, y = margin, db.Dy()-mb.Dy()-margin
	case AnchorBottomRight:
		x, y = db.Dx()-mb.Dx()-margin, db.Dy()-mb.Dy()-margin
	default:
		return
	}
	// 夹进边界：海报比图标还窄时（极端比例）不越界。
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	r := image.Rect(x, y, x+mb.Dx(), y+mb.Dy()).Intersect(db)
	if r.Empty() {
		return
	}
	draw.Draw(dst, r, mark, image.Pt(r.Min.X-x, r.Min.Y-y), draw.Over)
}
