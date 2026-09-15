package dav

import (
	"html"
	"net/http"
	"net/url"
	"strings"
)

func (s *Server) serveDirListing(w http.ResponseWriter, r *http.Request, node *Node, reqPath string) {
	items, err := s.resolver.ListChildren(r.Context(), node)
	if err != nil {
		writeDomainErr(w, err)
		return
	}
	base := strings.TrimSuffix(reqPath, "/")
	if base == "" {
		base = "/"
	}
	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html><head><meta charset=\"utf-8\"><title>Index of ")
	b.WriteString(html.EscapeString(base))
	b.WriteString("</title></head><body><h1>Index of ")
	b.WriteString(html.EscapeString(base))
	b.WriteString("</h1><ul>")
	if base != "/" {
		parent := parentPath(base)
		b.WriteString(`<li><a href="`)
		b.WriteString(html.EscapeString(publicHref(parent)))
		b.WriteString(`">../</a></li>`)
	}
	for _, it := range items {
		name := it.Name
		if it.IsDir {
			name += "/"
		}
		href := publicHref(joinHref(base, name))
		b.WriteString(`<li><a href="`)
		b.WriteString(html.EscapeString(href))
		b.WriteString(`">`)
		b.WriteString(html.EscapeString(it.Name))
		if it.IsDir {
			b.WriteString("/")
		}
		b.WriteString("</a></li>")
	}
	b.WriteString("</ul></body></html>")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(b.String()))
}

func parentPath(p string) string {
	p = strings.TrimSuffix(pathClean(p), "/")
	if p == "" || p == "/" {
		return "/"
	}
	i := strings.LastIndex(p, "/")
	if i <= 0 {
		return "/"
	}
	return p[:i] + "/"
}

func joinHref(base, name string) string {
	base = strings.TrimSuffix(base, "/")
	seg := url.PathEscape(strings.TrimSuffix(name, "/"))
	if strings.HasSuffix(name, "/") {
		seg += "/"
	}
	if base == "" || base == "/" {
		return "/" + seg
	}
	return base + "/" + seg
}

func publicHref(resourcePath string) string {
	if resourcePath == "" || resourcePath == "/" {
		return mountPrefix + "/"
	}
	if !strings.HasPrefix(resourcePath, "/") {
		resourcePath = "/" + resourcePath
	}
	return mountPrefix + resourcePath
}

func pathClean(p string) string {
	if p == "" {
		return "/"
	}
	return "/" + strings.Trim(strings.TrimPrefix(p, "/"), "/")
}
