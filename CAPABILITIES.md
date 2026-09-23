# 各网盘能力对照表

> 本表描述的是 **MyPan 代码里声明的能力**，不是各家网盘的官方文档。
> 每一格都能在源码里找到依据（见文末「维护说明」）。
> 与网盘官方口径不一致时，**以本表为准** —— 它才是程序实际的判断依据。

已注册驱动共 **11 个**（`drivers/all.go:8-18`）。另有**内置下载器**，它不是网盘，是第二套下载通道，单独一节。

---

## 一、驱动总览

| `driver_type` | 名称 | 认证方式 | 卡片标签 |
|---|---|---|---|
| `115_open` | 115网盘Open | OAuth + **网页 Cookie**（转存用） | 官方授权 · OAuth · 网页Cookie · 支持302 · SHA1 |
| `123_open` | 123云盘 Open | OAuth | 官方授权 · OAuth · 支持302 · 支持秒传 |
| `139_cloud` | 移动云盘 | 扫码 / Authorization | 扫码登录 · Authorization · 支持302 |
| `189_cloud` | 天翼云盘 | 扫码 | 个人云 · 家庭云 · 扫码登录 · 支持302 · 支持秒传 |
| `baidu_open` | 百度网盘Open | OAuth | 官方授权 · OAuth · MD5分享 |
| `guangya` | 光鸭云盘 | 扫码 | 扫码登录 · 支持302 · 支持秒传 |
| `localfs` | 本机存储 | — | 本地目录 · 容器挂载 |
| `onedrive` | OneDrive | OAuth | 官方授权 · OAuth · 支持302 |
| `openlist` | OpenList | 手动填地址/凭据 | 远端挂载 · 目录读写 · 支持302 |
| `quark` | 夸克网盘 | 扫码 + Cookie | 扫码登录 · Cookie · 本机代理 |
| `webdav` | WebDAV | 手动填地址/凭据 | 远端挂载 · 目录读写 · 本机代理 |

---

## 二、主矩阵：链接形式 × 网盘

**四条通道互不相干，别混着读**：

- **离线下载** —— 把链接交给网盘，让网盘自己去下
- **转存** —— 把别人的分享链存进自己网盘
- **直链** —— 把网盘里的文件换成一个临时下载地址
- **秒传** —— 用哈希让网盘直接"认出"文件，不传字节

| 网盘 | 磁链<br>`magnet:` | ed2k | 迅雷链<br>`thunder://` | http/https | ftp | 种子<br>`.torrent` | 分享链<br>转存 | 直链 | 302 | 秒传<br>（源→目标） |
|---|:-:|:-:|:-:|:-:|:-:|:-:|:-:|:-:|:-:|:-:|
| **115 网盘 Open** | ✅ | **✅** | — | ✅ | ✅ | **✅** | **✅** | ✅ | ✅ | sha1 → — |
| **光鸭云盘** | ✅ | — | **✅** | ✅ | ✅ | — | — | ✅ | ✅ | md5 → md5 |
| **123 云盘 Open** | — | — | — | ✅ | — | — | — | ✅ | ✅ | md5 → sha1+md5 |
| 夸克 | — | — | — | — | — | — | ⚠️ 只认不存 | ✅ | **❌** | — |
| 百度 Open | — | — | — | — | — | — | — | ✅ | ✅ | md5 → — |
| 天翼云盘 | — | — | — | — | — | — | — | ✅ | ✅ | md5 → md5 |
| 移动云盘 | — | — | — | — | — | — | — | ✅ | ✅ | — |
| OneDrive | — | — | — | — | — | — | — | ✅ | ✅ | — |
| OpenList | — | — | — | — | — | — | — | ✅ | ✅ | — |
| WebDAV | — | — | — | — | — | — | — | ✅ | ✅ | — |
| 本机存储 | — | — | — | — | — | — | — | ✅ | — | — |
| *内置下载器* | ✅ | — | — | ✅ | — | — | — | — | — | — |

> **`—` 的含义是「该驱动根本没实现这条能力」**，不是「没测」也不是「不知道」。
> 所有 `—` 都对应着接口未实现或能力未声明，逐条可在源码核对。

---

## 三、离线下载详解

只有 **3 个驱动**声明了原生离线下载（`OfflineDownloadProvider`），其余 8 个（含 `localfs` / `webdav` / `onedrive`）
连这个接口都没实现。

| | `115_open` | `guangya` | `123_open` | 内置下载器 |
|---|---|---|---|---|
| `http` | ✅ | ✅ | ✅ | ✅ |
| `https` | ✅ | ✅ | ✅ | ✅ |
| `ftp` | ✅ | ✅ | — | — |
| `magnet` | ✅ | ✅ | — | ✅ |
| `ed2k` | ✅ | — | — | — |
| `thunder` | — | ✅ | — | — |
| **批量提交** | ✅ | ✅（上限 50） | ❌ 一次一条 | ✅ |
| **指定根目录为目标** | ✅ | ✅ | ⚠️ 见下 | — |
| **删除远程任务** | ✅ | ✅ | ❌ | — |
| **`.torrent` 种子上传** | ✅ | — | — | —（只吃磁力 URI） |

依据：`drivers/115_Open/offline_download.go:88-97`、`drivers/Guangya/offline_download.go:95-104`、
`drivers/123_Open/offline_download.go:37-46`、`internal/offlinedownload/builtin_magnet.go:70-72`。

### 几个不那么直观的点

**`123_open` 的「指定根目录」是动态的**。`RootTargetAllowed: d.rootID() != "0"`
（`drivers/123_Open/offline_download.go:43`）—— 账号配了非 `0` 的根目录才允许把根目录当目标。
且这个字段**服务端不做校验**，只透传给前端；判定发生在前端表单里
（`web/src/components/file/OfflineDownloadModal.vue:111-123`）。

**`RemoteDelete` 是双重门**：驱动声明了还要实现 `OfflineTaskDeleter` 接口才为真
（`internal/offlinedownload/service.go:164-165`）。所以「能删远程任务」的只有 115 和光鸭。

**提交一个不支持的协议会得到什么**（`internal/offlinedownload/service.go:794-809`）：

- 认不出 scheme → 「离线下载链接格式不正确：…」
- 认得出但驱动不声明 → 「当前网盘不支持 `<scheme>` 链接」

`magnet:?…` / `ed2k://|file|…` / `thunder://…` / `ftp://…` 都能通过格式校验，逐字匹配时**大小写不敏感**。

---

## 四、分享链转存

**只有 115 能转存**，而且该账号必须**额外配一份浏览器 Cookie** —— 115 的分享转存只有网页版接口能做，
开放平台 API 里没有这个动作（`drivers/115_Open/share.go:179-187`）。

| 网盘 | 能识别 115 分享链 | 能识别夸克分享链 | **能转存** |
|---|:-:|:-:|:-:|
| `115_open` | ✅ | ❌ | **✅**（需网页 Cookie） |
| `quark` | ❌ | ✅ | ❌ 只能识别 |
| 其余 9 个驱动 | ❌ | ❌ | ❌ |

### 认得出的域名与格式

`internal/tgsubscribe/telegram/share.go:21-40`：

- **域名**：`115.com`、`115cdn.com`、`anxia.com`（含任意层级子域）、`quark.cn`（覆盖 `pan.quark.cn`）
- **路径**：必须是 `/s/<2-64 位 [A-Za-z0-9_-]>`

接受、与被拒的边界在 `internal/tgsubscribe/telegram/share_test.go:52-172` 里有完整用例
（`?pwd=` 别名、`115cdn` 归一化接受；`/s/a` 太短、`/s/abcd1234/file` 多一层、
`https://evil.com/s/abcd1234`、`https://115.com@evil.com/s/…` 一律拒绝）。

### 转存的两个附加能力

- **提取码**：支持 `?password=` / `?pwd=`，也会从正文里认「提取码/访问码/密码/pwd: XXXX」
- **指定目标目录**：`TargetCID` + 转存前**核对目标目录**（读面包屑拼路径严格比对，
  `drivers/115_Open/share.go:275`、`:352`、`:373`）

### 转不进网盘的分享链会怎样

不报错。它们被收进「匹配历史」供你复制链接，TG 设置页里那句提示写的正是这件事：

> 夸克/百度/阿里等也可以填进来，但当前版本只会把它们收进匹配历史供你复制链接，**推不进网盘**
> —— `web/src/components/admin/TGSettingsPanel.vue:405-406`

---

## 五、直链与播放

**所有已注册驱动都能给出直链**（`internal/driver/driver.go:106-108` 的 `Downloader` 接口），
差别只在**能不能 302** —— 302 让客户端直连网盘、不占服务器带宽。

| | 能出直链 | 支持 302 | 只能本机代理 |
|---|:-:|:-:|:-:|
| `115_open` / `123_open` / `139_cloud` / `189_cloud` / `guangya` / `onedrive` | ✅ | ✅ | — |
| `baidu_open` / `openlist` / `webdav` | ✅ | ✅（可配，**默认走代理**） | 默认 |
| **`quark`** | ✅ | **❌ 永远不行** | **强制** |
| `localfs` | ✅（本机文件路径，非 URL） | — | ✅ |

夸克是所有驱动里唯一**写死不能 302** 的：`Mode: domain.DownloadProxy` + `ForceProxy: true`
（`drivers/Quark/ops.go:58-68`），配置项里也只有代理一项 —— 它的流量**全部经过服务器**。

直链被这些地方消费：播放网关（`internal/playback/`）、STRM 生成（`internal/strm/`）、
WebDAV 挂载（`internal/share/dav/`）、FUSE 读（`internal/share/fuse/`）、
Emby 代理（`internal/embyproxy/`）、飞牛反代（`internal/fnosproxy/`）、封面抽帧。

---

## 六、秒传

「源」= 能给得出哈希；「目标」= 能凭哈希直接入库（不传字节）。

| 网盘 | 作为**源** | 作为**目标** |
|---|---|---|
| `115_open` | ✅ sha1 | — |
| `123_open` | ✅ md5 | ✅ **sha1 + md5** |
| `189_cloud` | ✅ md5 | ✅ md5（**唯一支持预检**） |
| `guangya` | ✅ md5 | ✅ md5 |
| `baidu_open` | ✅ md5 | — |

**预检**（先探测目标盘有没有这个文件、有就跳过传输）只有天翼支持（`RapidUploadProber`）。

### 一处容易看漏的实现细节

115 与 123 都**声明**了能提供哈希，但它们**没有实现 `TransferHashResolver` 接口**。
取值实际走的是三段式回落（`internal/crosstransfer/service.go:538-565`）：

1. 文件项本身就带哈希 → 直接用
2. 驱动实现了 `TransferHashResolver` → 调它（**只有 189 / 百度 / 光鸭**）
3. 驱动实现了 `InfoGetter` → 读文件信息再取哈希（115 与 123 走这条）

所以「115 能不能当秒传源」不能只看接口断言清单 —— 按接口断言查会漏掉它。

---

## 七、其它能力

| 能力 | 支持的驱动 | 说明 |
|---|---|---|
| **递归列目录**（`FullListLister`） | **仅 `115_open`** | 一次拉取全部子孙文件，STRM 增强扫描用它替代逐目录递归，大幅减少请求量（`internal/file/service.go:117-160`） |
| **扫码登录**（`QRLoginProvider`） | 115 / 移动 / 天翼 / 光鸭 / 夸克 | 其余走 OAuth 或手动填凭据 |
| **OAuth**（`OAuthConsumer`） | 115 / 123 / 百度 / OneDrive | |
| **账号资料**（昵称/会员/容量） | 天翼 / OneDrive / 夸克 | 仅在存储管理页按天后台刷新 |
| **连接错误解释** | 除 115 外全部 | 把底层错误翻译成人话 |
| **请求间隔限制** | 除本机存储 / WebDAV 外全部 | 防触发网盘风控 |
| **自动刷新凭据** | 除本机存储 / OpenList / WebDAV 外全部 | |

---

## 八、内置下载器

它**不是网盘**，是 `offlinedownload` 服务里的第二套通道（`provider_kind = "builtin"`）。
界面呈现为离线下载弹窗里的第二个页签，也是 TG / 番号推送里「推送通道」的一个选项。

| 项 | 值 |
|---|---|
| 支持协议 | `http` · `https` · **`magnet`** |
| **不支持** | `ed2k`、`ftp`、`thunder://` |
| `.torrent` 文件 | **不收** —— 只吃磁力 URI（`internal/offlinedownload/builtin_magnet.go:562`） |
| HTTP/HTTPS | 支持断点续传 |
| BT | 基于 DHT，TCP/uTP 双协议，默认监听 **42069**（TCP + UDP） |
| 完成后 | 自动交接给上传任务传到目标网盘 |

报错文案：「内置下载器当前只支持 HTTP/HTTPS/Magnet：…」（`internal/offlinedownload/builtin_magnet.go:112-125`）。

---

## 九、容易踩的认知差

这几条都是**代码事实**，且都会让人对能力表产生错误预期：

1. **`Capabilities.Supported` 与 `BuiltinEnabled` 无条件硬编码为 `true`**
   （`internal/offlinedownload/service.go:172`、`:179`）。
   于是**不支持离线下载的网盘也会回「支持」**，前端那句「当前网盘不支持离线下载」
   只在请求失败时才可能出现 —— 不支持离线的驱动照样能进那个弹窗，并默认落到内置下载器。

2. **`ftp` 与 `thunder://` 没有任何自动路径能产出**。
   TG / 番号的资源抽取器只有 **磁力 / ed2k / 分享 / 直链** 四种
   （`internal/tgsubscribe/resource.go:100-106`），所以这两个协议**只能在手动新建离线下载时填**。

3. **前端内置通道的协议列表漏了 `magnet`**：`builtin_url_schemes` 为空时回落到 `["http","https"]`
   （`web/src/components/file/OfflineDownloadModal.vue:55-59`），而内置实际支持磁力。
   文案也只写「可使用内置下载器处理 HTTP/HTTPS 链接」、占位符写「请输入一个 HTTP/HTTPS 下载链接」（`:238`、`:310`）。

4. **协议白名单散落在各模块里，是第二处真相**。改驱动的能力声明**不会**同步它们：
   - `internal/jav/pusher.go:22-28` 的 `javSchemes` 只放行 `magnet` / `ed2k`
   - `internal/tgsubscribe/pusher.go:28-38` 的 `kindSchemes` 按 kind 映射到 `{magnet}` / `{ed2k}` / `{http,https}`
   - `internal/tgsubscribe/telegram/share.go:21-40` 的分享域名白名单

5. **文档里的能力表可能落后于代码**。`docs/GUIDE.md:66` 那张表只列了 5 项，
   且「能提供秒传哈希」一栏漏了 115 与 123（见第六节）。

---

## 十、维护说明

改动能力相关代码后，请一并核对本表：

| 改了什么 | 去哪里看 | 影响本表哪一节 |
|---|---|---|
| 驱动增删 | `drivers/all.go:8-18` | 一、二 |
| 离线下载协议 | `drivers/*/offline_download.go` 的 `OfflineDownloadCapabilities()` | 二、三 |
| 卡片标签 / 秒传声明 | `drivers/*/driver.go` 的 `Config`（`CardTags` / `ProvideHashes` / `RapidUploadHashes`） | 一、二、六 |
| 分享转存 | `internal/driver/share.go` + `Drivers/115_Open/share.go` | 四 |
| 直链 / 代理模式 | `drivers/*/ops.go` 的 `ResolveDownload` | 五 |
| 内置下载器协议 | `internal/offlinedownload/builtin_magnet.go:70-72` | 八 |

**核对手法**：`driver.Config` 的字段定义在 `internal/driver/driver.go:25-55`；
可选接口清单可以直接 grep 各驱动的接口断言：

```bash
for d in drivers/*/; do
  printf "%-12s " "$(basename "$d")"
  grep -oh "_ driver\.[A-Za-z]*" "$d"*.go | sed 's/_ driver\.//' | sort -u | tr '\n' ' '; echo
done
```

> 注意 `drivers/template/` 是脚手架，**未注册进 `all.go`**，不要把它当成可用驱动。
