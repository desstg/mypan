# MyPan

> 本项目上游是开源项目 **LitePan**：https://github.com/Ponphil/LitePan

**MyPan 不是网盘管理器，是一条完整的影片流水线。** 资源从哪儿来、怎么落到网盘、
怎么变得整整齐齐、怎么被播放器认出来 —— 全流程打通，中间不需要你手动搬一次文件。

- 项目主页：https://github.com/desstg/mypan
- 完整使用说明：https://github.com/desstg/mypan/blob/main/docs/GUIDE.md
- 各网盘能力对照表：https://github.com/desstg/mypan/blob/main/CAPABILITIES.md

<!--
================================================================================
 这份文件是给 **Docker Hub 的 Repository overview** 用的，不是给 GitHub 看的。
 改完请手动粘到 https://hub.docker.com/r/desstg/mypan 的 Write 标签页。

 为什么与 README.md 分开：
   Docker Hub 的描述框**不解析相对路径** —— 它只做「基础 Markdown」，
   而 README 里那些 `docs/pictures/*.png` 在 Hub 上会被解析成
   `https://hub.docker.com/r/desstg/mypan/docs/pictures/...`，**全部 404**；
   `./CAPABILITIES.md` 这类相对链接同样会指向 Hub 上不存在的地址（也 404）。
   所以这份文件里：**没有任何图片**，**所有链接都是绝对地址**。
   改的时候别把图片或相对链接加回来。

 实测（2026-09-29）：Hub 上原描述里的 6 张图全是破图、2 个相对链接点了 404，
  GitHub 那侧一切正常 —— 两边要求不同，所以必须分成两份。
================================================================================

---

## 一条龙：从资源到播放

```
 ① 发现资源          ② 落到网盘            ③ 目录整理              ④ STRM           ⑤ 刮削           ⑥ 播放
┌───────────┐    ┌──────────────┐    ┌──────────────────┐    ┌────────────┐   ┌───────────┐   ┌──────────┐
│ 磁力 / ed2k│    │ 网盘原生离线  │    │ 删广告小文件      │    │ 生成 .strm │   │ 写 nfo    │   │ Emby     │
│ 分享链     │ →  │ 或内置下载器  │ →  │ 批量改名          │ →  │ 指向本机   │ → │ 存海报    │ → │ Jellyfin │
│ 直链 / 种子 │    │ 转存别人的分享│    │ 移进作品文件夹    │    │ 播放网关   │   │ 海报墙    │   │ 飞牛 …   │
└───────────┘    └──────────────┘    └──────────────────┘    └────────────┘   └───────────┘   └──────────┘
   TG 订阅 /          离线下载 /          目录整理 /             STRM 任务         刮削            302 直链
   番号订阅            分享转存            番号自动归档                          自动联动        或本机代理
```

更具体地说，每一步都在做这些事：

| 环节 | 干什么 | 关键能力 |
|---|---|---|
| **① 发现** | 从 TG 频道、网盘搜索、番号站点把资源找出来 | TG 影片订阅 · 番号（JAV）订阅 · 盘搜 |
| **② 落盘** | 把链接交给网盘自己去下，或让 MyPan 自己下 | 网盘原生离线 · 内置 BT 下载器 · 分享链转存 |
| **③ 整理** | 把乱七八糟的目录变成规规矩矩的作品文件夹 | 删广告小文件 · 批量改名 · 移进目标文件夹 · 先预览后应用 |
| **④ STRM** | 给每个视频生成一个 `.strm` 文本文件，播放器当它是本地片 | STRM 任务 · 播放网关 · 302 直链 / 本机代理 |
| **④′ 番号元数据** | 番号影片按侧车 JSON 在 `.strm` 同层写 nfo 与封面 / 剧照 | 媒体类型＝番号影片 · 封面裁海报 · extrafanart |
| **⑤ 刮削** | 写 nfo、下海报，让海报墙认得这部片 | TMDB 匹配 · 元数据写入 · 可追更 |
| **⑥ 播放** | 播放器直连网盘播放，不占服务器带宽 | Emby / Jellyfin / 飞牛代理 · WebDAV · FUSE |

**其中「整理」那一步是真正省事的地方**：不用你一个个文件夹去翻。
程序会把广告 `.url` / `.txt` / `.jpg` 这类小垃圾挑出来、把文件名规整成番号、
把散在根目录的片子移进它该在的作品目录里 —— 而且**先给你看一遍计划，你点确认才动**。

> 整条流水线可以用「自动联动」串起来：离线下载一完成，自动触发整理 → STRM → 刮削 → 刷库。
> 你只管订阅，剩下的它自己跑。

## 功能简述

- **多网盘聚合** —— 11 种网盘 / 存储，多账号统一管理，一个界面看完。
- **跨盘秒传** —— 能秒传就秒传，否则自动上传。
- **STRM 直连播放** —— 生成 `.strm`，对接 Emby / Jellyfin。
- **STRM 刮削** —— 写 nfo / 海报，海报墙可追更。
- **目录整理** —— 删广告小文件、批量改名、归档到位，预览后再应用。
- **自动联动** —— 整理、STRM、刮削、刷库串起来。

## 订阅：让资源自己找上门

- **TG 影片订阅** —— 订阅电影 / 剧集，程序去 Telegram 频道里搜资源并自动推送
  （磁力 / ed2k / 115 分享链 / 直链都能认）
- **番号（JAV）订阅** —— 按影片、演员、清单订阅，自动抓 JAVDB / JAVBUS 的磁链并推送；
  评论区里用户分享的链接也能一并纳入候选
- **推送通道自动判定** —— 按链接协议与目标网盘的实际能力挑「网盘原生离线」还是「内置下载器」，
  推不动的会告诉你为什么

## 挂载与更多功能

支持 WebDAV 与 FUSE 本地挂载，另有 302 直链、缓存保持、命名对齐、离线下载、
通知中心、封面抽帧等能力。

---

## 快速开始

**Docker Compose 部署** · 镜像标签：`latest` 或指定 `v2.7.10`

### 一、先确认共享挂载已开启（用到 FUSE 挂载才需要）

mypan 用 fuse3 挂载云存储。**容器里的挂载要能在宿主机上看见**，宿主机那个目录的
挂载传播属性必须是 `shared` —— 不是的话，容器里的挂载会直接失败并报「权限被拒绝」。

先把下面路径换成你准备给容器用的那个目录，查一下：

```bash
findmnt -o TARGET,PROPAGATION /path/to/dir
```

`PROPAGATION` 那列写着 `shared` 就说明已经开着，**整节跳过**。
精简系统里没有 `findmnt`，也可以直接看 `/proc`——输出里带 `shared:` 就是开着的：

```bash
grep ' / ' /proc/self/mountinfo
```

**这些系统默认就开着，查一下确认即可，别白改**：LibreELEC、CoreELEC、飞牛 fnOS。

#### 没开的话，按 Docker 的运行方式选一个

**选项 1 · Docker 以 systemd service 运行**

```bash
sudo mkdir -p /etc/systemd/system/docker.service.d/
sudo cat <<EOF > /etc/systemd/system/docker.service.d/clear_mount_propagation_flags.conf
[Service]
MountFlags=shared
EOF
sudo systemctl restart docker.service
```

**选项 2 · Docker 不是以 systemd service 运行**

在宿主机上直接给那个挂载点开共享（路径与上面查的是同一个）：

```bash
sudo mount --make-shared $(df -P /path/to/dir | tail -1 | awk '{ print $6 }')
```

**举个例子**：假如你把 mypan 装在 `/volume1/docker/mypan`，那 `df` 会告诉你它落在
`/volume1` 这个挂载点上，所以命令就是：

```bash
sudo mount --make-shared /volume1
```

> ⚠️ **选项 2 重启后会失效** —— `mount --make-shared` 只在当前运行的系统里生效。
> 想让 mypan 重启后自动可用，得把这条命令加进系统启动项。

### 二、启动容器

```yaml
services:
  litepan:
    image: desstg/mypan:latest
    container_name: mypan
    restart: unless-stopped
    ports:
      - "5211:5211"
      # Emby / 飞牛影视反代的监听端口。反代监听在容器内部，
      # 不在这里发布出去，播放器就连不上（表现只是「连不上服务器」）。
      # 端口号要与后台「代理工具 → 反代端口」一致；用不到反代可以删掉这行。
      - "18097:18097"
      # 内置 Magnet 的 TCP/uTP/DHT 监听端口，默认不开启；要用内置磁力下载时
      # 取消下面两行的注释（TCP 和 UDP 必须都给，只给 TCP 会导致 DHT 找不到节点）
      # - "42069:42069/tcp"
      # - "42069:42069/udp"
    environment:
      - TZ=Asia/Shanghai
    volumes:
      - ./data:/app/data
      - ./strm:/app/strm
      - ./mounts:/app/mounts:shared

      # 可选：将 FUSE 读缓存单独映射，建议放到更快的磁盘
      # - ./fuse_read_cache:/app/data/fuse_read_cache
    devices:
      - /dev/fuse:/dev/fuse
    pid: "host"
    privileged: true
    # 没有代理环境的，可以在下方配置tmdb的hosts
    # extra_hosts:
      # - "api.themoviedb.org:这里填写对应的ip"
      # - "image.tmdb.org:这里填写对应的ip"
    # 注意：也可以在程序内「目录整理 → TMDB 设置」填写反代主域名（自动补 /3 与 /t/p），与 hosts 二选一即可
```

### 三、打开界面

访问 `http://你的IP:5211`，默认管理员密码为 `admin`。

需要 FUSE 时请确保宿主机具备 `/dev/fuse` 权限。

### 四、推荐：自建一个盘搜，搜索能力会强很多

「TG 影片订阅」除了抓 Telegram 频道，还能拿片名去**外部网盘搜索服务**里搜资源。
程序内置的默认地址是一个公开演示站，**作者自己说过它被薅到返回 mock 数据、效果可忽略** ——
想要真正搜得到，建议自己起一个 **[PanSou](https://github.com/fish2018/pansou)**。

一条 Docker 命令，**不需要 Telegram 的 api_id / api_hash**：

```bash
docker run -d --name pansou -p 805:80 ghcr.io/fish2018/pansou-web
```

> 前后端一体版，起完 `http://你的IP:805` 就能打开搜索页。
> 想只要后端 API（MyPan 用到的就是 API）用 `ghcr.io/fish2018/pansou:latest`，
> 那个镜像默认监听 `8888`。

**然后填进 MyPan**：打开「影视订阅」页 → 右上角齿轮 → **推送设置** → **网盘搜索** →
把「搜索服务地址」填成你刚起的那个地址：

| 你起的容器 | 搜索服务地址填 |
|---|---|
| `-p 805:80`（前后端一体版） | `http://192.168.31.4:805` |
| `-p 8888:8888`（纯 API 版） | `http://192.168.31.4:8888` |

把 `192.168.31.4` 换成**跑 pansou 那台机器的局域网 IP**。三点注意：

1. **只填到端口，不要带 `/api/search`** —— 路径程序自己拼。
2. **不要写 `127.0.0.1`**：MyPan 在容器里，那指的是它自己。用宿主机的局域网 IP。
3. 地址是内网的话，同一页的「**走全局代理**」要**关掉**（默认就是关的）。

盘搜自己还要能连上 t.me 才拿得到 TG 频道那一半结果 —— 需要的话在**它的容器**里配
`HTTPS_PROXY`（同样写宿主机的局域网 IP）。

## 标签

| 标签 | 说明 |
|---|---|
| `latest` | 最新发布版 |
| `v2.7.10` | 固定版本 |

**默认只构建 `linux/amd64`。** 绝大多数 NAS（群晖、威联通、自建 x86）都是这个架构。
需要 ARM 版时请在 Actions 里手动触发并填写 `linux/amd64,linux/arm64` ——
arm64 走 QEMU 模拟，那一趟要一小时以上。

## 许可

见仓库的 LICENSE 文件。第三方组件与素材的说明见 ACKNOWLEDGEMENTS.md 与
THIRD_PARTY_NOTICES.md。

---

## 联系方式

- Telegram：[联系我](https://t.me/mypandesstg_bot)
- GitHub：[desstg/mypan](https://github.com/desstg/mypan)（源码与问题反馈）
