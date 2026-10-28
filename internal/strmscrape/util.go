package strmscrape

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

func relUnder(root, full string) string {
	if absRoot, err := filepath.Abs(root); err == nil {
		root = absRoot
	}
	if absFull, err := filepath.Abs(full); err == nil {
		full = absFull
	}
	rel, err := filepath.Rel(root, full)
	if err != nil {
		return filepath.Base(full)
	}
	return rel
}

func isInside(root, full string) bool {
	if absRoot, err := filepath.Abs(root); err == nil {
		root = absRoot
	}
	if absFull, err := filepath.Abs(full); err == nil {
		full = absFull
	}
	rel, err := filepath.Rel(root, full)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func pathToItemID(rel string) string {
	sum := sha1.Sum([]byte(filepath.ToSlash(rel)))
	return hex.EncodeToString(sum[:8])
}

func pathEscape(p string) string {
	return url.QueryEscape(filepath.ToSlash(p))
}

// blankImagePath 判定一张图的路径是不是「其实没有图」。
//
// 除了空串与纯空白，还要挡住字面量 "<nil>"：anyString 曾把 JSON 的 null 直接交给
// fmt.Sprint，于是 TMDB 没海报时的 poster_path=null 变成了字符串 "<nil>"，
// 被当成真路径去下载 → 整部片「刮削失败」。已经落盘的侧车/nfo 里这些脏值不会自己
// 消失，所以读取侧也要挡一道，而不只是修好写入口。
func blankImagePath(p string) bool {
	p = strings.TrimSpace(p)
	return p == "" || p == "<nil>"
}

func anyString(v any) string {
	switch t := v.(type) {
	case nil:
		// TMDB 的 JSON 里 poster_path / backdrop_path 为 null 是常态（没图）。
		// 不单独拦这一种会落到 default 分支，fmt.Sprint(nil) 打出 "<nil>"，
		// 于是「没有图」被当成「图在 <nil> 这个路径上」去下载，整部片刮削失败。
		return ""
	case string:
		return t
	case fmt.Stringer:
		return t.String()
	default:
		return fmt.Sprint(v)
	}
}
