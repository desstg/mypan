package settings

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
)

// 海报墙上挂的**本地目录**（设置键 KeyStrmWallLocalDirs）。
//
// # 它解决什么
//
// 海报墙的数据源一直是 **STRM 任务**（`strmscrape.resolveTask` 用任务的
// GroupDir/OutputFolder 推出本地根目录）。但用户手上常有一批**别的工具产出的**
// 媒体库目录（`.strm` + nfo + 图片齐全），它们不在任何任务之下 ——
// 这一份注册表就是「把这批目录也挂到墙上」的清单。
//
// # 为什么是 JSON 数组而不是「;」分隔
//
// 与 JavWallHiddenDirs 同一个坑：**空串表示「没存过、回落默认」**，
// 而「一个本地目录都没加」是有效状态。JSON `[]` 非空串，语义完整。
//
// # 为什么 id 不能是纯数字
//
// 它与 `strm_task_id` 在请求里**并存**（`local_dir_id` 是兄弟参数），而
// `strm_task_id` 是纯数字。id 一旦也是数字，两边的值域就糊在一起了，
// 「这个 id 到底是任务还是本地目录」要靠猜。所以用 `crypto/rand` 的十六进制串。

// WallLocalDir 是墙上挂的一个本地目录。
type WallLocalDir struct {
	// ID 是稳定的短标识（`[0-9a-f]{16}`）。**与 root 无关**：改路径、改名都不换它，
	// 前端记住的「上次选中的是哪个」才不会失效。
	ID string `json:"id"`
	// Name 是下拉里显示的名字。默认取 root 的 basename，用户可改 ——
	// 两台机器上各有一个 `.../国产AV` 是常事，不可改的 basename 会让下拉分不清。
	Name string `json:"name"`
	// Root 是**绝对路径**，且必须是「跑 LitePan 那个进程能看到的」路径
	// （部署在容器里时就是容器内路径，不是宿主机的）。
	Root string `json:"root"`
	// MediaKind 决定墙上用哪一面渲染，取值与 STRM 任务的 `MediaKind` **同一套**：
	// `tmdb` / `jav`。刻意不引入第三套词 —— 墙的档位与任务的档位本来就是同一件事。
	//
	// ⚠️ 别和条目层的 `media_type`（`movie` / `tv`，由 nfo 推断）混了：
	// 那个不是用户在这里选的。
	MediaKind string `json:"media_kind"`
	// CreatedAt 是注册时间（RFC3339）。列表排序用它，保证编码稳定可比对。
	CreatedAt string `json:"created_at,omitempty"`
}

// 墙上本地目录支持的媒体类型（与 domain.StrmMediaKind* 同值）。
const (
	WallLocalKindTmdb = "tmdb"
	WallLocalKindJav  = "jav"
)

// NormalizeWallLocalKind 把 media_kind 收敛到支持的两个值；不认识的一律回落 tmdb
// （那是「加这个字段之前的行为」，也是任务那边空串的语义）。
func NormalizeWallLocalKind(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case WallLocalKindJav:
		return WallLocalKindJav
	default:
		return WallLocalKindTmdb
	}
}

// ParseWallLocalDirs 解析设置值。
//
// 第二个返回值报告「库里有值吗」：**空串、坏 JSON、`null` 都算没存过** ——
// 调用方据此知道该不该给一份空的默认。`[]`（解析成功但为空）是有效值。
func ParseWallLocalDirs(raw string) ([]WallLocalDir, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, false
	}
	var parsed []WallLocalDir
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return nil, false
	}
	if parsed == nil {
		// JSON `null` 解出来是 nil 切片，与「空数组」不是一回事（编码器只会写出
		// `[]` / `[{…}]`，写不出 null）。按「没存过」处理才安全。
		return nil, false
	}
	return normalizeWallLocalDirs(parsed), true
}

// EncodeWallLocalDirs 序列化成规范字符串。
//
// **排序 + 去重 + 丢坏项**（normalizeWallLocalDirs 里做）：前端拿前后两个字符串
// 一比就知道「有没有改动」，顺序不稳定会一直误报未保存（与 JavWallHiddenDirs 同一条理由）。
// 空列表编码成 `[]`，绝不编码成空串。
func EncodeWallLocalDirs(dirs []WallLocalDir) string {
	clean := normalizeWallLocalDirs(dirs)
	data, err := json.Marshal(clean)
	if err != nil {
		// 只会序列化 []WallLocalDir，到这里说明 encoding/json 有 bug ——
		// 给 `[]`（= 一个都没挂）比给空串（= 回落默认）安全。
		return "[]"
	}
	return string(data)
}

// normalizeWallLocalDirs 是设置写入路径上的收口（Spec.normalize）与读取路径的归一。
//
// 丢掉：缺 id / 缺 root 的、**root 不是绝对路径**的。
// 后面那条是硬要求：海报端点靠 `isInside(root, full)` 挡路径穿越，而相对路径进
// `filepath.Rel` 会退化成 basename，等于那道闸门失效。
func normalizeWallLocalDirs(dirs []WallLocalDir) []WallLocalDir {
	seen := make(map[string]struct{}, len(dirs))
	out := make([]WallLocalDir, 0, len(dirs))
	for _, d := range dirs {
		id := strings.TrimSpace(d.ID)
		root := strings.TrimSpace(d.Root)
		if id == "" || root == "" {
			continue
		}
		// 大小写不敏感的绝对路径去重：Windows 上 `X:\A` 与 `x:\a` 是同一个目录。
		key := strings.ToLower(filepath.ToSlash(filepath.Clean(root)))
		if _, dup := seen[key]; dup {
			continue
		}
		if _, dupID := seen["id:"+strings.ToLower(id)]; dupID {
			continue
		}
		seen[key] = struct{}{}
		seen["id:"+strings.ToLower(id)] = struct{}{}

		name := strings.TrimSpace(d.Name)
		if name == "" {
			name = filepath.Base(filepath.Clean(root))
		}
		out = append(out, WallLocalDir{
			ID:        id,
			Name:      name,
			Root:      filepath.Clean(root),
			MediaKind: NormalizeWallLocalKind(d.MediaKind),
			CreatedAt: strings.TrimSpace(d.CreatedAt),
		})
	}
	// 稳定排序：先按注册时间、再按名字。同一个集合两次编码得到同一个字符串。
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// normalizeWallLocalDirsValue 是 Spec.normalize 的签名。
//
// 坏值 / 空串一律收敛成 `[]` —— 与 JavWallHiddenDirs 那条「不填默认值」不同：
// 这里没有「动态默认」可言（本地目录只能由用户显式添加），所以 `[]` 就是正确状态。
func normalizeWallLocalDirsValue(raw string) string {
	dirs, ok := ParseWallLocalDirs(raw)
	if !ok {
		return "[]"
	}
	return EncodeWallLocalDirs(dirs)
}
