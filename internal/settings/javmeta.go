package settings

import "encoding/json"

// 「番号元数据」那六个开关的值格式（设置键 KeyStrmJavMetaItems）。
//
// # 为什么是一个键装 JSON 对象，而不是六个 bool 键
//
//   - 这六个开关天然是一组、永远一起读写，六个 bool 键要在 registry、ConfigView、
//     前端表单里各写六遍；
//   - 用「;」分隔的列表在这里有个必然踩的坑：**全不勾时串是空串**，而
//     settings.Service.String() 把空串当「没存过」回落默认值 —— 于是
//     「一个都不要」永远存不下去（用户取消全选、保存、界面又全勾上）。
//     JSON 对象 `{"subtitle":false,…}` 非空，这个坑不存在。
//
// JSON 单键在本项目是既成做法（jav_api_nodes / mo_jav_rules / cover_extract_style）。
//
// **字段顺序即界面顺序，也是序列化顺序**。顺序稳定是必须的：前端拿前后两个字符串
// 一比就知道「有没有改动」，顺序一变就会一直误报未保存。
type JavMetaItems struct {
	// Subtitle 是占位：界面上有这个复选框，但（截至本次）字幕不做任何事。
	// 留着是因为键少了会让「存回来的 JSON 缺一项」，前端再读一次会把它当成
	// 「用户取消勾选」—— 界面上那个勾会自己消失。
	Subtitle bool `json:"subtitle"`
	// Preview 剧照 → extrafanart/fanartN.jpg
	Preview bool `json:"preview"`
	// Thumb thumb.jpg（封面原图，横图）
	Thumb bool `json:"thumb"`
	// Poster poster.jpg（从 thumb 按 2:3 裁）
	Poster bool `json:"poster"`
	// Fanart fanart.jpg（thumb 的字节复制）
	Fanart bool `json:"fanart"`
	// NFO <主干>.nfo
	NFO bool `json:"nfo"`
}

// DefaultJavMetaItems 全开。
//
// 默认全开是有意的：用户打开「媒体类型 = 番号影片」时想要的就是「一整套都有」，
// 让他再去勾六个框是多余的仪式。
func DefaultJavMetaItems() JavMetaItems {
	return JavMetaItems{Subtitle: true, Preview: true, Thumb: true, Poster: true, Fanart: true, NFO: true}
}

// ParseJavMetaItems 解析设置值。空串、坏 JSON、缺项**一律按全开**兜底。
//
// 「缺项按全开」而不是「按关闭」：解析失败时静默关掉全部，会让用户面对一个
// 「什么都没生成」的任务却找不到原因 —— 那是这个功能最不该有的失败模式。
// 实现上先把默认值灌进结构体再 Unmarshal，缺的键自然保持 true。
func ParseJavMetaItems(raw string) JavMetaItems {
	out := DefaultJavMetaItems()
	if raw == "" {
		return out
	}
	// 忽略错误：JSON 坏了就是「没见过这份值」，全开是唯一安全的答案。
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

// Encode 序列化成规范字符串（键顺序固定，与结构体声明顺序一致）。
//
// 解析失败不可能发生：结构体全是 bool，没有能报错的形状。
func (j JavMetaItems) Encode() string {
	data, err := json.Marshal(j)
	if err != nil {
		// 到了这里说明 encoding/json 有 bug，不是输入的问题 ——
		// 返回默认值比返回空串安全（空串会被读侧当成「没存过」再兜一次默认）。
		return `{"subtitle":true,"preview":true,"thumb":true,"poster":true,"fanart":true,"nfo":true}`
	}
	return string(data)
}

// normalizeJavMetaItems 是设置写入路径上的收口（Spec.normalize）。
//
// 它保证库里**永远不会出现空串或坏 JSON**：用户在界面上全不勾时存的是
// `{"subtitle":false,…}`，而那份值读回来仍是「全不勾」。
func normalizeJavMetaItems(raw string) string {
	return ParseJavMetaItems(raw).Encode()
}
