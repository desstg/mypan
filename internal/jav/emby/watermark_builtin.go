package emby

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// 内置水印图标：随二进制一起发（照 cascade/facefinder 的先例）。
//
// 为什么复制一份进 Go 包，而不是让后端去读 `web/public/logos/suiyin/`：
// 那个目录是**前端产物**，`vite build` 带 `emptyOutDir` 会把它清掉重建
// （见 web/vite.config.ts），部署形态也不一定与源码同机。`go:embed` 才是可靠的。
//
// 图标名与位置的对应关系写在 watermark.go 的 defaultWatermarkAnchors。
//
//go:embed watermarks/4k.png
var default4K []byte

//go:embed watermarks/8k.png
var default8K []byte

//go:embed watermarks/leak.png
var defaultLeak []byte

//go:embed watermarks/sub.png
var defaultSub []byte

//go:embed watermarks/umr.png
var defaultUmr []byte

// builtinWatermarks 是内置那套（键 = 水印 id）。
func builtinWatermarks() map[string][]byte {
	return map[string][]byte{
		"4k":   default4K,
		"8k":   default8K,
		"leak": defaultLeak,
		"sub":  defaultSub,
		"umr":  defaultUmr,
	}
}

// LoadWatermarks 按 id 列表装出水印，供 ComposePoster 用。
//
// dir 非空时**同名文件优先**（用户自己那套图标），缺的落回内置那套 ——
// 这样用户只换其中一张时，其余四张照旧能用，不必凑齐五个文件。
//
// 用户目录里**完全没有**这个文件、内置也没有时，报错并点名是哪一个：
// 「贴上去发现少了一个角标」比报错难查得多。
func LoadWatermarks(dir string, ids []string) ([]Watermark, error) {
	builtin := builtinWatermarks()
	out := make([]Watermark, 0, len(ids))
	for _, id := range ids {
		id = strings.ToLower(strings.TrimSpace(id))
		if id == "" {
			continue
		}
		anchor, ok := WatermarkAnchor(id)
		if !ok {
			return nil, fmt.Errorf("不认得的水印 %q（只支持 %s）", id, strings.Join(WatermarkNames, " / "))
		}
		data, err := loadWatermarkImage(dir, id, builtin[id])
		if err != nil {
			return nil, err
		}
		out = append(out, Watermark{Image: data, Anchor: anchor})
	}
	return out, nil
}

// loadWatermarkImage 先看用户目录（`<dir>/<id>.png`），再看内置。
func loadWatermarkImage(dir, id string, builtin []byte) ([]byte, error) {
	dir = strings.TrimSpace(dir)
	if dir != "" {
		path := filepath.Join(dir, id+".png")
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			return data, nil
		}
	}
	if len(builtin) > 0 {
		return builtin, nil
	}
	return nil, fmt.Errorf("水印图标 %s.png 找不到（内置那套里也没有）", id)
}
