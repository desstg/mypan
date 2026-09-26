package emby

import (
	_ "embed"
	"image"
	"sync"

	pigo "github.com/esimov/pigo/core"
)

// facefinder 是 pigo 的人脸级联模型（转自 pico.js 的 facefinder，MIT）。
//
// **必须内嵌而不是运行期 ReadFile**：pigo 上游的用法是让调用方给一个路径
// （它自带的 CLI 就是 `-cf cascade/facefinder`），而我们的二进制要能单独部署，
// 不能依赖工作目录里躺着某个文件。
//
// ⚠️ 这个文件**没有扩展名**，而仓库根的 `.gitattributes` 第一行是 `* text=auto eol=lf`
// —— 不显式标 `binary` 的话，Windows 检出会把它当文本做行尾转换，模型当场损坏，
// 而症状只是「检测不到脸」（`Unpack` 失败会更直白，但两种都不报错到能让人想到 git）。
// 所以 `.gitattributes` 里那条 `internal/jav/emby/cascades/facefinder binary` 是**必需的**，
// 不是可选的整洁。
//
//go:embed cascades/facefinder
var facefinder []byte

// 检测参数。MinSize 40 是下限：更小的「脸」多半是封面上的缩略图或纹身，
// 而 JAVDB 的封面里人脸通常占画面的 1/5 以上。
const (
	faceMinSize     = 40
	faceMaxSize     = 1000
	faceShiftFactor = 0.1
	faceScaleFactor = 1.1
	// faceClusterIoU 是重叠框的合并阈值（pigo 的 ClusterDetections 参数）。
	faceClusterIoU = 0.2
)

// faceClassifier 只解包一次（解包是几毫秒的活，而扫描里每部片都要用）。
//
// `sync.OnceValues` 之后的结构是**只读**的，所以可以并发用 —— 这正是
// 「海报裁切放后台队列、多个任务并行」的前提。
var faceClassifier = sync.OnceValues(func() (*pigo.Pigo, error) {
	return pigo.NewPigo().Unpack(facefinder)
})

// DetectFace 在图上找出**最可能的那张脸**，找不到返回 Found=false。
//
// 「最可能」取的是级联得分 Q 最大的那个框 —— 封面上一张图里常有好几个候选
// （人脸、封底的小图、甚至印刷体的字），取 Q 最大的那一张最稳。
//
// 分框语义（pigo 的约定，别搞反）：`Col` 是**中心列**、`Row` 是**中心行**、
// `Scale` 是方框边长，所以左上角是 `(Col-Scale/2, Row-Scale/2)`。
func DetectFace(img image.Image) (FaceBox, bool) {
	classifier, err := faceClassifier()
	if err != nil || classifier == nil {
		// 模型解不开时**不报错**：调用方会退化成「居中裁」，
		// 那是可用的海报，而报错会让整条生成链变成失败。
		return FaceBox{}, false
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return FaceBox{}, false
	}

	pixels := pigo.RgbToGrayscale(img)
	cParams := pigo.CascadeParams{
		MinSize:     faceMinSize,
		MaxSize:     faceMaxSize,
		ShiftFactor: faceShiftFactor,
		ScaleFactor: faceScaleFactor,
		ImageParams: pigo.ImageParams{Pixels: pixels, Rows: h, Cols: w, Dim: w},
	}
	// 角度固定 0：封面里没有旋转的人脸，而每一档角度都要跑一遍完整级联。
	dets := classifier.RunCascade(cParams, 0.0)
	dets = classifier.ClusterDetections(dets, faceClusterIoU)

	best := pigo.Detection{Q: -1}
	for _, det := range dets {
		if det.Q > best.Q {
			best = det
		}
	}
	if best.Q < 0 {
		return FaceBox{}, false
	}
	return FaceBox{
		X:     best.Col - best.Scale/2,
		Y:     best.Row - best.Scale/2,
		Size:  best.Scale,
		Found: true,
	}, true
}
