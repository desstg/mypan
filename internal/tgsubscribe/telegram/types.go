// Package telegram 是 Telegram Bot API 的最小客户端。
//
// 只覆盖本功能用到的部分：拉取 channel_post、校验频道与 bot 权限、从消息里抽资源。
// 不做交互式 bot（没有 callback_data 处理，也不调 sendMessage）。
package telegram

// User 是 Bot API 的用户对象（只保留用到的字段）。
type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

// Chat 是 Bot API 的会话对象。频道与超级群的 ID 都是负的（-100...）。
type Chat struct {
	ID       int64  `json:"id"`
	Type     string `json:"type"` // channel | supergroup | group | private
	Title    string `json:"title"`
	Username string `json:"username"`
}

// MessageEntity 是消息实体。
//
// ⚠️ Offset / Length 的单位是 UTF-16 码元，不是 byte 也不是 rune。
// 含中文或 emoji 的正文里，按 rune 切片会错位甚至越界 —— 必须走 UTF-16 切片。
type MessageEntity struct {
	Type   string `json:"type"` // url | text_link | code | pre | bold | ...
	Offset int    `json:"offset"`
	Length int    `json:"length"`
	URL    string `json:"url"` // 仅 text_link
}

// CopyTextButton 是 Bot API 7.11+ 的「点击复制」按钮内容。
type CopyTextButton struct {
	Text string `json:"text"`
}

// InlineKeyboardButton 是内联键盘按钮。
//
// 资源频道大量使用「点击复制磁链」按钮而不是明文链接，所以 CopyText 必须处理，
// 否则会漏掉相当一部分资源。
type InlineKeyboardButton struct {
	Text     string          `json:"text"`
	URL      string          `json:"url"`
	CopyText *CopyTextButton `json:"copy_text"`
}

type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

// Document 是附件。本期不处理 .torrent，但保留字段以便后续扩展。
type Document struct {
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
}

// Message 是 Bot API 的消息对象（只保留用到的字段）。
type Message struct {
	MessageID       int64                 `json:"message_id"`
	Date            int64                 `json:"date"`
	Chat            Chat                  `json:"chat"`
	Text            string                `json:"text"`
	Caption         string                `json:"caption"`
	Entities        []MessageEntity       `json:"entities"`
	CaptionEntities []MessageEntity       `json:"caption_entities"`
	ReplyMarkup     *InlineKeyboardMarkup `json:"reply_markup"`
	Document        *Document             `json:"document"`
}

// Update 是一次更新。本期只关心频道帖子（含编辑后的）。
type Update struct {
	UpdateID          int64    `json:"update_id"`
	ChannelPost       *Message `json:"channel_post"`
	EditedChannelPost *Message `json:"edited_channel_post"`
}

// Post 返回这条更新里的频道帖子，没有则返回 nil。
// 编辑过的帖子走同一处理路径 —— 频道常先发占位再补磁链。
func (u *Update) Post() *Message {
	if u == nil {
		return nil
	}
	if u.ChannelPost != nil {
		return u.ChannelPost
	}
	return u.EditedChannelPost
}

// ChatMember 用于校验 bot 在频道里的身份。只有 administrator / creator 才能收到频道消息。
type ChatMember struct {
	Status string `json:"status"` // creator | administrator | member | left | kicked | restricted
	User   *User  `json:"user"`
}
