package javdb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// magnetFixture 是 `/v1/movies/{id}/magnets` 的真实响应形状（逐字抄自实测）。
const magnetFixture = `{"success":1,"data":{"magnets":[
{"cnsub":false,"created_at":"2026-09-23","files_count":2,
 "hash":"4b4257447654a6418109039bcad8ddc07a68eb35",
 "hd":true,"name":"Tushy.26.09.20.Geishakyd.Beauty.Saves.Marriage.With.Anal.XXX.1080p.MP4-P2P[XC]",
 "pikpak_url":"https://keepshare.org/aa36p03v/magnet%3A%3Fxt%3Durn%3Abtih%3A4b42",
 "size":3706},
{"cnsub":true,"created_at":"2026-09-24","files_count":0,
 "hash":"264C18A652BB852103F5523BA38FCE7A0711C504",
 "hd":1,"name":"ABF-387-C","size":5136},
{"cnsub":false,"created_at":"2026-09-24","files_count":5,
 "hash":"","hd":false,"name":"没有 hash 的条目","size":100}
]}}`

func TestNormalizeMagnet(t *testing.T) {
	var env struct {
		Data struct {
			Magnets []Magnet `json:"magnets"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(magnetFixture), &env); err != nil {
		t.Fatalf("夹具解不开: %v", err)
	}
	if len(env.Data.Magnets) != 3 {
		t.Fatalf("夹具应当有 3 条，got %d", len(env.Data.Magnets))
	}

	// 第一条：欧美、1080p、有文件数。
	first, ok := NormalizeMagnet(env.Data.Magnets[0])
	if !ok {
		t.Fatal("第一条应当可用")
	}
	// size 的单位是 MB —— 3706MB = 3.6GB。当成字节会得到一颗 3.7KB 的「磁链」，
	// 设了体积下限的订阅会把它整批判成「太小」而丢弃，且不报错。
	if !first.HasSize || first.SizeBytes != 3706*1024*1024 {
		t.Errorf("size 应当按 MB 换算：got %d（has=%v）", first.SizeBytes, first.HasSize)
	}
	if first.Magnet != "magnet:?xt=urn:btih:4b4257447654a6418109039bcad8ddc07a68eb35" {
		t.Errorf("magnet 要自己拼: %q", first.Magnet)
	}
	if !first.HasHD || first.HasSub {
		t.Errorf("hd/cnsub 应当直接映射: hd=%v sub=%v", first.HasHD, first.HasSub)
	}
	if !first.HasFiles || first.FileCount != 2 {
		t.Errorf("files_count=2 应当记下来: %v/%d", first.HasFiles, first.FileCount)
	}
	if first.Date != "2026-09-23" {
		t.Errorf("date = %q", first.Date)
	}

	// 第二条：hash 大写要折成小写（btih 大小写不敏感，不折会攒出两份重复行）。
	// hd 给的是数字 1，cnsub 给的是 true —— 同一族的字段上游换过表示，都得认。
	second, ok := NormalizeMagnet(env.Data.Magnets[1])
	if !ok {
		t.Fatal("第二条应当可用")
	}
	if second.Btih != strings.ToLower("264C18A652BB852103F5523BA38FCE7A0711C504") {
		t.Errorf("btih 应当折成小写: %q", second.Btih)
	}
	if !second.HasHD || !second.HasSub {
		t.Errorf("hd=1 / cnsub=true 都要认: hd=%v sub=%v", second.HasHD, second.HasSub)
	}
	// files_count=0 是**未知**，不是「0 个文件」。当成 0 会让设了「最大文件数」的
	// 订阅把「不知道」判成「合格」—— 与 JAVBUS 那边（给不出就拒收）不一致。
	if second.HasFiles {
		t.Errorf("files_count=0 应当算未知，got FileCount=%d HasFiles=%v", second.FileCount, second.HasFiles)
	}

	// 第三条：hash 不是 40 位十六进制 → 拼不出合法磁链，必须丢掉。
	// 留着的话推送时会变成一条网盘认不出的链接，而它看起来「差不多是对的」。
	if _, ok := NormalizeMagnet(env.Data.Magnets[2]); ok {
		t.Error("hash 不合法时应当判为不可用")
	}
}

func TestMagnetsByIDHitsRightEndpoint(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(magnetFixture))
	}))
	defer srv.Close()

	c, err := New(Options{APIBase: srv.URL, Retries: 1})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	items, err := c.MagnetsByID(context.Background(), "DRAB72")
	if err != nil {
		t.Fatalf("MagnetsByID: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("应当解出 3 条，got %d", len(items))
	}
	// 路径必须是 `/v1/movies/{id}/magnets`，且用的是**影片 id** ——
	// 传番号会 404 或「資源未找到」，那是这个端点最容易踩的一脚。
	if gotPath != "/v1/movies/DRAB72/magnets" {
		t.Errorf("路径 = %q，应当是 /v1/movies/{id}/magnets", gotPath)
	}

	// 空 id 不打上游：那只会换来一次注定失败的请求。
	before := gotPath
	if items, err := c.MagnetsByID(context.Background(), "  "); err != nil || items != nil {
		t.Errorf("空 id 应当直接返回空: %v / %v", items, err)
	}
	if gotPath != before {
		t.Error("空 id 不该打上游")
	}
}
