# 多阶段构建：前端构建 → Go 编译 → 精简运行镜像
# 注意：依赖根目录 .dockerignore 排除 data/（含凭据）、.git、node_modules 等，
# 请勿删除该文件，否则敏感数据会进入构建上下文与 builder 层缓存。

# ---- 前端构建 ----
# Node 22 LTS（Node 20 已于 2026-04 EOL；Vite 7 要求 node ^20.19.0 || >=22.12.0）
FROM node:22-alpine AS frontend
WORKDIR /src
COPY frontend/package.json frontend/package-lock.json ./
# npm ci 严格按 lockfile 安装，保证可复现构建
RUN npm ci
COPY frontend/ ./
RUN npm run build
# 产物输出到 /src/cmd/web/dist（由 vite.config.ts 的 outDir 决定）

# ---- Go 编译 ----
FROM golang:1.25-alpine AS backend
WORKDIR /src
# 依赖层（利用缓存：仅 go.mod/go.sum 变化时重新下载）
COPY go.mod go.sum ./
RUN go mod download
# 源码 + 前端产物（覆盖本地可能存在的旧 dist）
COPY . .
COPY --from=frontend /src/cmd/web/dist ./cmd/web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /audiobook-web ./cmd/web

# ---- 运行镜像 ----
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=backend /audiobook-web ./audiobook-web
# 数据目录（数据库 / 加密凭据 / 配置），建议挂载持久化
VOLUME ["/data"]
EXPOSE 8080
ENV AUDIOBOOK_DATA_DIR=/data
# 容器内自检（busybox wget）
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/api/health || exit 1
ENTRYPOINT ["./audiobook-web"]
