package strmscrape

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"image"
	"image/jpeg"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "image/gif"
	_ "image/png"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"litepan/internal/domain"
)

// 演员的**自定义头像**：用户在详情页自己传一张，盖过侧车 / 资料层那只。
//
// # 为什么是文件而不是一张表
//
// 本包至今没有 repository（数据源是扫盘 + 侧车 + 若干内存缓存），为「一位演员
// 一张图」专门接一个 store 不划算：键就是演员 key，落成一个文件名就够了。
//
// # 为什么必须落盘到服务端（用户明确要求）
//
// 用户上传时选的是**当时那台电脑或手机上的**文件。它只被读一次，像素随即重编码
// 存进服务端——之后头像由 `/api/admin/.../actor/avatar?...` 提供，与那台设备、
// 那份原文件彻底无关：换手机、换电脑、清空相册都不影响。`data/` 在
// docker-compose 里是挂载卷（`./data:/app/data`），容器重建后图还在。
//
// 同理，前端**绝不能**拿 `URL.createObjectURL` 的 blob 地址当上传结果 —— 那只在
// 本机本次会话有效。
//
// # 存的是「规范形态」
//
// 解出来的像素缩到宽 ≤ 480 / 高 ≤ 720（不放大）再编成 JPEG q88。卡片上那张只有
// 120 px 宽，用户从手机相册丢进来的 8 MB 原图存着纯浪费，而每次展示都还要整张
// 解码一遍。取舍与 internal/thumbcache 一致。

const (
	// JavActorAvatarMaxBytes 是上传上限。手机相册里一张 4K 照片 2~6 MB，8 MB 够宽。
	//
	// 导出给 API 层用：那里要先按它 `MaxBytesReader`，再读一次核上限 ——
	// 两道闸同源，改这里两边一起变。
	JavActorAvatarMaxBytes = int64(8 << 20)
	// javActorAvatarMaxW / MaxH 是存档尺寸上限（保持比例、不放大）。
	//
	// 480×720 是「详情抽屉 120 px 宽 × 2 倍屏 + 将来放大看」的余量：再大就是
	// 白占磁盘；再小在 2 倍屏上会糊。
	javActorAvatarMaxW = 480
	javActorAvatarMaxH = 720
	// javActorAvatarJPEGQuality 与 thumbcache 的 88 同口径。
	javActorAvatarJPEGQuality = 88
)

// JavWallActorAvatar 是一次上传 / 删除的结果（前端拿它直接换图）。
type JavWallActorAvatar struct {
	Key string `json:"key"`
	// AvatarURL 是**服务端**地址（`/api/admin/...&v=<mtime>`），可直接当 src 用。
	// 删除时为空串 —— 表示回到侧车 / 资料层那张。
	AvatarURL string `json:"avatar_url"`
	// UpdatedAt 是文件的 mtime（RFC3339），只给人看。
	UpdatedAt string `json:"updated_at,omitempty"`
}

// javActorAvatarDir 是自定义头像的落盘目录。
func (s *Service) javActorAvatarDir() string {
	if s == nil {
		return ""
	}
	dir := strings.TrimSpace(s.dataDir)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "jav_actor_avatar")
}

// javActorAvatarFile 由演员 key 算出文件名。
//
// 用 sha1 而不是直接用 key：key 可能是 `name:某某`（带冒号、空格、斜杠、中日文），
// 直接当文件名在不同文件系统上行为不一，还可能被 `..` 逃出目录。哈希之后就只剩
// 十六进制，与 internal/thumbcache 的 cacheKey 同一个套路。
func javActorAvatarFile(dir, key string) (string, bool) {
	key = strings.TrimSpace(key)
	dir = strings.TrimSpace(dir)
	if key == "" || dir == "" {
		return "", false
	}
	sum := sha1.Sum([]byte(key))
	return filepath.Join(dir, hex.EncodeToString(sum[:])+".jpg"), true
}

// actorAvatarPath 返回这位演员的自定义头像文件；没有（或目录没配）时 ok 为假。
func (s *Service) actorAvatarPath(key string) (string, bool) {
	path, ok := javActorAvatarFile(s.javActorAvatarDir(), key)
	if !ok {
		return "", false
	}
	if info, err := os.Stat(path); err != nil || info.IsDir() || info.Size() == 0 {
		return "", false
	}
	return path, true
}

// actorAvatarURL 拼自定义头像的地址；没有自定义头像时返回空串。
//
// `v` 带文件的 mtime：换一张图之后浏览器不会拿旧缓存（地址变了就是另一份资源）。
// 没有它的话，用户传完新图看到的还是上一张，而「怎么传都不变」是最难查的一类。
func (s *Service) actorAvatarURL(key string) string {
	path, ok := s.actorAvatarPath(key)
	if !ok {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	q := url.Values{}
	q.Set("key", key)
	// 纳秒而不是秒：同一秒里连传两张（用户不满意立刻换一张）是很常见的操作，
	// 秒级精度下地址不变，浏览器会拿缓存里的上一张 —— 看起来就是「传了没反应」。
	q.Set("v", fmt.Sprintf("%d", info.ModTime().UnixNano()))
	return "/api/admin/strm-scrape/jav-wall/actor/avatar?" + q.Encode()
}

// ResolveJavActorAvatarFile 供 GET 路由用：给出磁盘上的那份自定义头像。
func (s *Service) ResolveJavActorAvatarFile(key string) (string, bool) {
	return s.actorAvatarPath(key)
}

// SaveJavActorAvatar 校验并落盘一张自定义头像。
//
// 校验顺序是「先用意 → 再解图 → 再写盘」：解不开的字节**绝不落盘**（否则界面上会
// 出现一张裂图，而用户以为上传成功了）。
func (s *Service) SaveJavActorAvatar(ctx context.Context, key string, data []byte) (*JavWallActorAvatar, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, domain.Errorf(domain.CodeValidation, "缺少演员标识")
	}
	if ctx != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if len(data) == 0 {
		return nil, domain.Errorf(domain.CodeValidation, "没有收到图片文件")
	}
	if int64(len(data)) > JavActorAvatarMaxBytes {
		return nil, domain.Errorf(domain.CodeValidation,
			"图片太大（%d MB），上限 %d MB", len(data)>>20, JavActorAvatarMaxBytes>>20)
	}
	dir := s.javActorAvatarDir()
	if dir == "" {
		return nil, domain.Errorf(domain.CodeInternal, "服务未配置数据目录，无法保存头像")
	}
	path, ok := javActorAvatarFile(dir, key)
	if !ok {
		return nil, domain.Errorf(domain.CodeValidation, "缺少演员标识")
	}
	encoded, err := normalizeActorAvatar(data)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, domain.Errorf(domain.CodeInternal, "创建头像目录失败：%v", err)
	}
	// 先写临时文件再改名：并发 / 中途失败时，磁盘上要么是完整的新图、要么是旧的，
	// 不会出现半张 JPEG（浏览器里显示成下半截灰）。
	tmp := path + fmt.Sprintf(".%d.tmp", os.Getpid())
	if err := os.WriteFile(tmp, encoded, 0o644); err != nil {
		_ = os.Remove(tmp)
		return nil, domain.Errorf(domain.CodeInternal, "保存头像失败：%v", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return nil, domain.Errorf(domain.CodeInternal, "保存头像失败：%v", err)
	}
	out := &JavWallActorAvatar{Key: key, AvatarURL: s.actorAvatarURL(key)}
	if info, err := os.Stat(path); err == nil {
		out.UpdatedAt = info.ModTime().Format(time.RFC3339)
	}
	return out, nil
}

// DeleteJavActorAvatar 删掉自定义头像（没有自定义头像不算错 —— 幂等）。
func (s *Service) DeleteJavActorAvatar(key string) *JavWallActorAvatar {
	key = strings.TrimSpace(key)
	out := &JavWallActorAvatar{Key: key}
	path, ok := javActorAvatarFile(s.javActorAvatarDir(), key)
	if !ok {
		return out
	}
	_ = os.Remove(path)
	// 删除之后**不返回任何地址**：前端据此回到侧车 / 资料层那张。
	return out
}

// normalizeActorAvatar 把任意输入图缩到存档尺寸并编成 JPEG。
//
// 解不开时返回 VALIDATION —— 这句话是给用户看的（他可能选成 .pdf 或 .heic 了）。
func normalizeActorAvatar(data []byte) ([]byte, error) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, domain.Errorf(domain.CodeValidation,
			"这不是一张能识别的图片（支持 JPG / PNG / GIF / WebP）；"+
				"iPhone 的 HEIC 请先在相册里导出成 JPG")
	}
	b := src.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return nil, domain.Errorf(domain.CodeValidation, "图片尺寸无效")
	}
	dst := fitActorAvatarSize(b.Dx(), b.Dy())
	if dst.Dx() == 0 || dst.Dy() == 0 {
		return nil, domain.Errorf(domain.CodeValidation, "图片尺寸无效")
	}
	canvas := image.NewRGBA(dst)
	if dst.Dx() == b.Dx() && dst.Dy() == b.Dy() {
		// 已经在上限之内：只做格式转换，不做重采样（重采样会白白丢一点锐度）。
		draw.Draw(canvas, dst, src, b.Min, draw.Src)
	} else {
		// CatmullRom 而不是双线性：人像缩小后脸部细节最怕糊。
		draw.CatmullRom.Scale(canvas, dst, src, b, draw.Over, nil)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, canvas, &jpeg.Options{Quality: javActorAvatarJPEGQuality}); err != nil {
		return nil, domain.Errorf(domain.CodeInternal, "图片编码失败：%v", err)
	}
	return buf.Bytes(), nil
}

// fitActorAvatarSize 算缩略后的目标尺寸：保持比例、两个方向上都不超过上限、
// **绝不放大**（放大只会糊，还让文件变大）。
func fitActorAvatarSize(w, h int) image.Rectangle {
	if w <= 0 || h <= 0 {
		return image.Rect(0, 0, 0, 0)
	}
	tw, th := w, h
	if tw > javActorAvatarMaxW {
		th = th * javActorAvatarMaxW / tw
		tw = javActorAvatarMaxW
	}
	if th > javActorAvatarMaxH {
		tw = tw * javActorAvatarMaxH / th
		th = javActorAvatarMaxH
	}
	if tw < 1 {
		tw = 1
	}
	if th < 1 {
		th = 1
	}
	return image.Rect(0, 0, tw, th)
}

// applyCustomAvatars 把有自定义头像的那几张卡就地换掉地址。
//
// **在请求期调用，而不是建索引时**：演员索引是后台建 + 带签名缓存的
// （见 javwall_actor_index.go），放建索引里会让「刚上传的头像」要等一轮重建才出现。
// 这里每条只是一次 stat，一页几十张可以忽略。
func (s *Service) applyCustomAvatars(cards []JavWallActorCard) {
	if s == nil || len(cards) == 0 {
		return
	}
	for i := range cards {
		s.applyCustomAvatar(&cards[i])
	}
}

// applyCustomAvatar 是单张版本（详情 / TMDB 档案 / 影片详情里的演员格都走它）。
func (s *Service) applyCustomAvatar(card *JavWallActorCard) {
	if s == nil || card == nil {
		return
	}
	if u := s.actorAvatarURL(card.Key); u != "" {
		card.AvatarURL = u
		// 自传头像一定是有意为之：墙上那一格不该再被「补小图」覆盖。
		card.CustomAvatar = true
	}
}
