package strmscrape

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/strm"
)

// 「本地目录」源里的**真实视频文件**（mp4 / mkv …）的出入口。
//
// # 为什么需要它
//
// `.strm` 是一条纯文本，正文就是播放地址 —— 服务端读出来交给播放器即可
// （见 playable.go）。而用户用别的工具整理的媒体库里，作品载体就是**视频文件本身**：
// 它没有正文可读，浏览器也打不开 `X:\...` 这种路径。所以要有这一条
// 「按 rel 取本地文件」的 HTTP 出口，并且**必须支持 Range** —— 播放器拖进度条、
// 边下边播全靠它，一次性把 4GB 读进内存那条路根本不存在。
//
// # 白名单为什么是「视频」而不是「本地目录里的一切」
//
// 这个端点带着后台的登录态，能取到 `root` 下面的**任何**文件就是一条任意文件读取
// 漏洞（侧车 json 里有上游账号信息、目录里还有别的片子）。白名单收在
// `strm.IsVideoFileName`：与「什么算一个作品」是同一条判据，所以能列出来的卡
// 就一定能播，播不了的一定列不出来。
//
// ⚠️ **不要**把它和 `/poster` 那个端点合并：那一个带 `thumbcache` 缩放与一小时缓存，
// 对图是对的，对大文件会把整段视频读进内存再试图解码。

// videoContentTypes 给常见视频扩展名一个准确的 MIME。
//
// `mime.TypeByExtension` 在 Windows 上会读注册表（HKEY_CLASSES_ROOT），同一份二进制
// 在不同机器上可能给出不同答案，甚至给出 `application/octet-stream` —— 而浏览器
// 拿不到 `video/*` 就直接不播。所以这里显式给一份，未知扩展名才回落。
var videoContentTypes = map[string]string{
	".mp4":  "video/mp4",
	".m4v":  "video/mp4",
	".mkv":  "video/x-matroska",
	".webm": "video/webm",
	".mov":  "video/quicktime",
	".avi":  "video/x-msvideo",
	".wmv":  "video/x-ms-wmv",
	".flv":  "video/x-flv",
	".ts":   "video/mp2t",
	".m2ts": "video/mp2t",
	".rmvb": "application/vnd.rn-realmedia-vbr",
	".iso":  "application/octet-stream", // 光盘镜像：浏览器不会播，至少别谎报成视频
}

// VideoContentType 按扩展名给播放用的 MIME。
func VideoContentType(path string) string {
	if ct, ok := videoContentTypes[strings.ToLower(filepath.Ext(path))]; ok {
		return ct
	}
	return "application/octet-stream"
}

// ResolveMediaFile 把 (数据源, rel) 解析成一个**真实视频文件**的绝对路径。
//
// 三道闸门与 ResolvePosterFile 同形：`resolveSource`（数据源必须存在）、
// `isInside`（挡 `..` 逃逸）、白名单（只放视频）。最后再判存在。
func (s *Service) ResolveMediaFile(ctx context.Context, srcRef WallSourceRef, rel string) (string, error) {
	src, err := s.resolveSource(ctx, srcRef)
	if err != nil {
		return "", err
	}
	root := src.Root
	rel = filepath.Clean("/" + strings.TrimSpace(rel))
	rel = strings.TrimPrefix(rel, "/")
	full := filepath.Join(root, rel)
	if !isInside(root, full) {
		return "", domain.Errorf(domain.CodeValidation, "非法路径")
	}
	if !strm.IsVideoFileName(full) {
		return "", domain.Errorf(domain.CodeValidation, "仅允许视频文件")
	}
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		return "", domain.Errorf(domain.CodeNotFound, "文件不存在")
	}
	return full, nil
}

// localMediaURL 拼「取本地视频」的地址。
//
// 与 subtitleURLFromRel 同一个套路：复用取图那条端点的参数形状（`ref.query()` 已经
// 统一了 task / local 两种源），所以前端不需要知道这是哪一类源。
func localMediaURL(ref posterRef, rel string) string {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return ""
	}
	return "/api/admin/strm-scrape/media?" + ref.query() + "&rel=" + pathEscape(rel)
}

// isLocalVideoPlayable 报告这个文件名要不要走「本地视频出口」。
//
// `.strm` 走老的「读正文」那条路；真实视频走新出口。判据就是同一份
// `strm.IsVideoFileName` —— 两处各写一份的话，会出现「卡片认、播放器不认」。
func isLocalVideoPlayable(name string) bool {
	return strm.IsVideoFileName(name)
}
