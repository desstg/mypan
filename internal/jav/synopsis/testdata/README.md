# 测试用 fixture

这些是**真实抓取**的页面，不是手写的假数据 —— 补元数据的解析器要对付的是这些站
实际吐出来的东西，手写样本会把真实结构里的坑（换域名 301、404 回首页、番号带前缀、
全角空格导致下标错位）全部绕过。

抓取日期：2026-09-27（fc2 那份是 2026-10-15 补的）。抓取方式（带浏览器 UA，这几家对默认 UA 不友好）：

```bash
curl -s -A "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0 Safari/537.36" \
     "https://www.airav.io/cn/video?hid=QC-BT-4720640" -o airav_detail.html
curl -s -A "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0 Safari/537.36" \
     "https://missav123.com/cn/mium-1415" -o missav_detail.html
```

## 各自覆盖什么

| 文件 | 来源 | 覆盖点 |
|---|---|---|
| `jav321_real.html` | jav321 详情页 | 正文段落里的简介、`pics.dmm.co.jp` 封面 |
| `carib_real.html` | caribbeancom 详情页 | **euc-jp 编码**（转码那一步的活证据）、多段固定文案里挑最长的一段 |
| `airav_detail.html` | airav `WAAA-697` | `<title>` 与 meta description 里的「番号 + 标题」、**带前缀番号**（`300MIUM-…`）、信息表（女优/标籤/厂商） |
| `missav_detail.html` | missav `MIUM-1415` | `og:description` 是**一段纯简介且不含番号**（判据不能用番号把关的活证据） |
| `fc2_detail.html` | FC2 官方 `article/4983582` | **JSON-LD（`@type: Product`）**、`sku` 当命中判据、`image` 是 `{"url": …}` **对象**（不是字符串）、`brand.name` 是 **null**、販売日是 `2026/09/27` 要归一 |

## 实测到的结构要点（改解析器前先看这里）

- **airav 的域名会跳**：`https://airav.io`（裸域）实测 301 到 `airavplus2.cc`，
  而裸域的 301 在这台机器上会走 IPv6 出口被对端硬断（`wsarecv: An existing connection
  was forcibly closed`）。客户端必须写 **`https://www.airav.io`**。
- **airav 没有独立的简介区块**：详情页只有信息表 + 播放器 + 推荐位，简介只在
  `<title>` / `name="description"` 里，而那是**一行标题**不是剧情梗概。
- **missav 的 slug 就是番号小写**（`/cn/mium-1415`），`ssis001` 与 `ssis-001` 都能打开
  同一条；但 `fc2ppv-4956715` / `carib-092526-001` / `1pondo-092426_001` 都是 **404**
  —— FC2 与素人那批它用的是另一套命名（拿不到就交给后面的源）。
- **missav 在 404 时回的是首页**（HTTP 404 + 「MissAV | 免费高清AV在线看」标题 +
  一段站点营销 description）。**必须靠状态码判**，光看 description 有内容就会把
  站点标语写进剧情简介。
- 这两家给的都可能是**一行标题**（不是剧情梗概），所以 `minIntroRunes` 放到了 8；
  要真正的长篇简介得靠别的源。
- **FC2 官方站有两个真实形态差点被漏掉**（2026-10-15 实测）：`image` 是
  `{"url": …}` **对象**（不是字符串），`brand.name` 是 **null**。前者漏了的表现是
  「封面静默为空」，后者漏了会得到 `maker: "null"` 这种垃圾值。
  **教训**：最初的用例夹具把 `image` 写成了字符串，于是用例全绿、真页面上封面一个
  都取不到 —— 夹具的形状错了，用例就只是在验一个不存在的世界。改夹具请对着真页面抄。
- **FC2 的番号是纯数字**（`4983582`），所以命中判据必须是**相等**而不是包含
  （`airav` 那套 `foldKey` 包含匹配在纯数字上必然误命中邻号，会把别人的剧情写进来）。
