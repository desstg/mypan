# 测试用 fixture

这些是**真实抓取**的 Telegram 公开频道预览页，不是手写的假数据 —— 解析器要对付的是
Telegram 实际吐出来的东西，手写样本会把真实结构里的坑（嵌套 `<div>`、`tg-emoji`、
广告帖、行容器包按钮）全部绕过。

抓取日期：2026-09-16。抓取方式：

```bash
curl -s -A "Mozilla/5.0" "https://t.me/s/QukanMovie"           -o qukanmovie_newest.html
curl -s -A "Mozilla/5.0" "https://t.me/s/oneonefivewpfx"       -o oneonefivewpfx_newest.html
```

## 各自覆盖什么

| 文件 | 频道 | 覆盖点 |
|---|---|---|
| `qukanmovie_newest.html` | @QukanMovie（115影视资源分享频道） | 20 条帖子、`data-view` 齐全、正文里直发 115 分享链、**64 个 `url_button`（17 个键盘块）** |
| `oneonefivewpfx_newest.html` | @oneonefivewpfx（115网盘资源收藏） | 20 条帖子、**整页 0 个内联按钮**（无键盘分支）、正文链接走中转域名 |

## 实测到的结构要点

- 帖子容器：`<div class="tgme_widget_message text_not_supported_wrap js-widget_message" data-post="QukanMovie/11139" data-view="<base64>">`
- `data-view` 是 base64 的 `{"c":-2245898899,"p":11139,"t":1789550655,"h":"..."}`
  - `c` 是频道内部 id 的**负数**形式，`chat_id = -10^12 - |c|`
  - 已用 Bot API `getChat` 交叉核对：`c=-2245898899` → `-1002245898899`；`c=-2167886055` → `-1002167886055`
- 正文：`<div class="tgme_widget_message_text js-message_text">`，内联标签只用到 `a` / `br` / `b` / `i` / `blockquote` / `code`
- 内联按钮：`<div class="tgme_widget_message_inline_keyboard">` → 行容器 `<div class="tgme_widget_message_inline_row">` → `<a class="tgme_widget_message_inline_button url_button" href="...">`
- 翻页：页头有 `<link rel="prev" href="/s/QukanMovie?before=11139">`
- **`copy_button` 出现次数为 0** —— 网页预览不渲染「点击复制」按钮，这是本方案最主要的限制，
  理由与影响见 `internal/tgsubscribe/preview/preview.go` 的包注释。
