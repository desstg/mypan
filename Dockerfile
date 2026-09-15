# ============================================================================
#  LitePan Go 版 · 多阶段构建，最终产物 = scratch + 单个静态二进制
#
#  本地构建：
#    docker build -t desstg/mypan:beta .
#  带版本号构建（会写进「关于」页的 buildinfo.Version）：
#    docker build --build-arg VERSION=v1.0.1 -t desstg/mypan:v1.0.1 .
#  多架构构建并推送：
#    docker buildx build --platform linux/amd64,linux/arm64 \
#      --build-arg VERSION=v1.0.1 -t desstg/mypan:beta --push .
#
#  ⚠ 兼容性：本文件刻意只使用 legacy builder 也支持的语法，不要引入
#    RUN --mount=type=cache 之类的 BuildKit 专属指令。
#    群晖 Container Manager（以及部分 NAS / 老版本 docker）没有打包 buildx
#    插件，docker build 会静默回退到 legacy builder，然后在这一行直接报
#    「the --mount option requires BuildKit」中断整个构建。
#    缓存改为靠 COPY 顺序保证：依赖清单单独一层，源码在后，包没变就命中缓存。
#    如果环境确实有 buildx，加 DOCKER_BUILDKIT=1 前缀即可，本文件同样适用。
# ============================================================================


# ------------------------------- 阶段 1 / 前端 ------------------------------
# 必须用 Node 22.18+ / 24+：package-lock 里锁了 @babel/generator@8（engines 要求
# ^22.18.0 || >=24.11.0），直接依赖 pdfjs-dist@6 要求 >=22.13.0 || >=24。
# 本地开发环境是 Node 24，用 node:20 构建会在 npm ci 阶段刷一屏 EBADENGINE，
# 且不保证后续构建能跑通。Node 20 已于 2026-04 EOL，这里跟本地对齐用 24。
FROM node:24-bookworm-slim AS web

WORKDIR /src/web

# 依赖清单单独一层：package-lock 没变时 npm ci 这层直接命中构建缓存
COPY web/package.json web/package-lock.json ./
RUN npm config set registry https://registry.npmmirror.com \
    && npm ci

COPY web/ ./
# vue-tsc 类型检查 → vite 构建 → gzip 预压缩，产物直接落到 internal/api/web
RUN npm run build


# --------------------------- 阶段 2 / Go 静态编译 ---------------------------
FROM golang:1.26.6-bookworm AS build

WORKDIR /src

# CGO_ENABLED=0 是能上 scratch 的前提：
#   - sqlite 走 modernc.org/sqlite（纯 Go 实现，不需要 libsqlite3）
#   - go-fuse 直接调 mount(2)/umount2(2) 系统调用，不需要 libfuse
# 因此二进制完全静态，无任何动态链接依赖。
ENV GOTOOLCHAIN=local \
    CGO_ENABLED=0 \
    GOPROXY=https://goproxy.cn,direct

ARG BUILD_TAGS=fuse
ARG VERSION=v1.0.1

# 同上：go.mod/go.sum 单独一层，依赖不变就能复用 go mod download 的缓存
COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=web /src/internal/api/web /src/internal/api/web

# -trimpath    抹掉构建机路径，保证可复现
# -buildvcs=false  构建上下文不是 git 仓库（.dockerignore 也排除了 .git），关掉探测
# -s -w        去符号表与 DWARF 调试信息
RUN go build -tags "${BUILD_TAGS}" \
        -trimpath -buildvcs=false \
        -ldflags="-s -w -X litepan/internal/buildinfo.Version=${VERSION}" \
        -o /out/litepan ./cmd/litepan


# ------------------ 阶段 3 / 组装 scratch 用的最小 rootfs -------------------
# scratch 里没法 RUN，所有目录结构和证书都得先在构建阶段摆好再整个 COPY 过去。
FROM build AS rootfs

# 构建环境没有 tty，debconf 会退化成 Teletype 并刷一屏
# "unable to initialize frontend: Dialog/Readline" 的噪音，压制掉。
# 用 ARG 而不是 ENV：它只在本次 RUN 生效，不会被写进镜像。
ARG DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && update-ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# /app/data/log 等目录给「不挂载卷」的场景兜底；挂载时会被宿主机目录覆盖
RUN mkdir -p /rootfs/app/data/log \
             /rootfs/app/strm \
             /rootfs/app/mounts \
             /rootfs/tmp \
             /rootfs/etc/ssl/certs

# 云盘全是 HTTPS，根证书是硬依赖，漏了会在运行时才炸
RUN cp /out/litepan /rootfs/app/litepan \
 && cp /etc/ssl/certs/ca-certificates.crt /rootfs/etc/ssl/certs/ca-certificates.crt


# ------------------------------ 阶段 4 / 运行 -------------------------------
FROM scratch AS runtime

COPY --from=rootfs /rootfs/ /

WORKDIR /app

# 时区由二进制内嵌的 time/tzdata 提供（见 cmd/litepan/main.go 的空白导入），
# scratch 里没有 /usr/share/zoneinfo，不内嵌的话 TZ 会静默退化成 UTC。
ENV LITEPAN_DATA_DIR=/app/data \
    LITEPAN_STRM_DIR=/app/strm \
    LITEPAN_LISTEN=:5211 \
    LITEPAN_LOG_LEVEL=info \
    TZ=Asia/Shanghai

EXPOSE 5211 42069/tcp 42069/udp

VOLUME ["/app/data", "/app/strm", "/app/mounts"]

# 运行要求（与 compose 一致）：
#   privileged: true + /dev/fuse   —— FUSE 挂载走 mount(2)，无需 fusermount3
#   pid: "host"                    —— 保证卸载时能正确识别挂载点
# 注意：scratch 没有 shell，docker exec 进不去；排障请用 /app/data/log 下的日志。
ENTRYPOINT ["/app/litepan"]
