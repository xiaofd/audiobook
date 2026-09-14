# 有声书播放器 (Audiobook Player)

自托管的有声书播放器，支持**桌面端（Wails）**与 **Web 服务端（浏览器 / 手机 PWA）**双端运行。基于 Go + React + TypeScript + Tailwind CSS。

> **开发方式说明**：本项目采用 AI 辅助开发（AI-Assisted Development）模式构建——架构设计、功能规划、代码实现与问题修复均在 AI 编程助手（Trae / Claude）协作下完成，并经过人工审阅、静态检查（go vet / tsc）与运行时验证（API 冒烟测试、流式播放字节级校验）。请在使用前自行评估并验证代码的适用性。

## 功能特性

- 🎧 流式播放：章节导航、倍速（0.5x–3.0x）、进度记忆、断点续播、连播开关、睡眠定时器（15/30/60/90 分钟）
- 🔊 全局迷你播放条：浏览书库/管理页时底部常驻控制，不打断播放
- 📚 书库刮削：自动识别书名/作者/封面/分集，支持 "作者/书名" 两级目录、中文数字排序（"第十章" < "第二十一章"）
- 📁 多书源：**本地存储 / WebDAV / OneDrive / 中国移动云盘**（驱动可插拔架构，借鉴 OpenList）
- 🔀 双流模式：每存储实例可配 `auto` / `proxy`（中转 + Range 透传）/ `redirect`（302 直链）
- 📊 播放进度：每集进度记录、书库聚合进度条（单请求）、点击直接续播、导出/导入迁移
- 👥 多账户：管理员 + 普通用户，JWT 认证，修改密码 / 管理员重置密码 / 注册开关
- 🛡️ 安全：登录限流（5 次失败锁 5 分钟）、网盘凭据 AES-GCM 加密落盘、令牌轮换即时持久化、本地驱动路径穿越防护
- 📱 移动端：响应式布局 + Media Session 锁屏控制 + PWA（添加到主屏幕、静态资源离线缓存）

## 快速开始

### 方式一：Web 服务端（推荐部署形态）

```bash
# 1. 构建前端
cd frontend && npm install && npm run build && cd ..

# 2. 构建并启动（单文件二进制，自带前端与 API）
go build -o audiobook-web ./cmd/web
./audiobook-web
```

浏览器访问 `http://localhost:8080`。支持的环境变量与命令行参数：

| 参数 / 环境变量 | 说明 | 默认值 |
|---|---|---|
| `-host` / `AUDIOBOOK_HOST` | 监听地址 | `0.0.0.0` |
| `-port` / `AUDIOBOOK_PORT`（或 `PORT`） | 监听端口 | `8080` |
| `-data-dir` / `AUDIOBOOK_DATA_DIR` | 数据目录 | exe 同目录 `data/` |
| `AUDIOBOOK_SECRET_KEY` | 凭据加密密钥（跨机迁移用） | 自动生成 `.machine.key` |
| `AUDIOBOOK_TRUST_PROXY` | 部署于可信反向代理之后时设为 `1`，登录限流才会解析 `X-Forwarded-For`（直连部署保持关闭，防伪造头绕过限流） | 关闭 |

### 方式二：Wails 桌面端

```bash
wails dev            # 开发模式（热重载）
wails build -clean -s  # 打包单文件 EXE → build/bin/audiobook.exe
```

桌面端与 Web 端共用同一套 `/api/*` handler（Wails AssetServer Middleware 转发），行为完全一致。

### 方式三：Docker 部署

```bash
docker compose up -d        # 构建并启动，访问 http://localhost:8080
```

镜像为多阶段构建（node:22 前端 → golang:1.25 编译 → alpine 运行），内置健康检查（`/api/health`），数据持久化于 `./data`。构建上下文由 `.dockerignore` 排除敏感数据（`data/` 含凭据与数据库，严禁进入镜像）。

## 默认管理员

- 用户名：`admin`　密码：`admin123`

首次启动自动创建，**登录后请立即通过右上角 🔑 按钮修改密码**。

## 账户体系

- **自助注册默认关闭**（公网部署安全考虑）；管理员可在「管理 → 用户管理」开启，或直接创建用户
- 修改密码：导航栏右上角 🔑；管理员重置：「管理 → 用户管理 → 重置密码」
- 登录限流：同一 用户名+IP 连续失败 5 次锁定 5 分钟

## 使用流程

1. **登录**：`admin` / `admin123`（首次登录后改密）
2. **添加书源**：「管理 → 书源存储 → 添加存储」→ 填配置 → 「保存并选择目录」浏览远端目录
3. **扫描书库**：点击「扫描」（后台异步执行，实时进度；列目录失败会计数并警告）
4. **听书**：书库点击书籍 → 断点续播或进入目录选章节

## 书源配置说明

| 类型 | 配置项 | 说明 |
|------|--------|------|
| 本地存储 | `root` | 本地目录路径，如 `D:\audiobooks` |
| WebDAV | `url` / `username` / `password` / `mountPath` | 标准协议，支持坚果云 / Nextcloud / 群晖 / AList；坚果云请使用应用专用密码；不支持 302 直链（走中转 + Range 透传） |
| OneDrive | `region` / `redirectUri` / `refreshToken` | Microsoft Graph API；可前往 [api.oplist.org](https://api.oplist.org/) 获取 Refresh Token，Client ID/Secret 可选；429 自动重试、令牌轮换即时落盘 |
| 中国移动云盘 | `authType` | `authorization`（推荐，填 yun.139.com 请求头 Basic 后内容）/ `cookie`（邮箱 Cookie）/ `password`（兼容性差，不推荐） |

## 持续集成与发布（GitHub Actions）

推送到 `main` 或提 PR 时自动编译验证；推送 `v*` 标签（如 `v1.0.0`）时自动构建全部产物并发布 GitHub Release：

```bash
git tag v1.0.0
git push origin v1.0.0
```

Release 产物：

| 产物 | 说明 |
|------|------|
| `audiobook-server-linux-amd64` / `linux-arm64` | Linux 服务端 |
| `audiobook-server-windows-amd64.exe` | Windows 服务端 |
| `audiobook-server-darwin-amd64` / `darwin-arm64` | macOS 服务端 |
| `audiobook.exe` | Windows 桌面端（Wails GUI）+ NSIS 安装包 |
| `checksums.txt` | 全部产物 SHA256 校验和 |

## 项目结构

```
audiobook/
├── frontend/                  # React + TS + Tailwind 前端
│   └── src/
│       ├── services/api.ts    # 统一 API 层（双端共用 fetch）
│       ├── hooks/             # usePlayer / useMediaSession
│       └── features/          # Library / BookDetail / Player / MiniPlayer / Admin / Login ...
├── internal/
│   ├── driver/                # 存储驱动接口（Driver + 可选 Writer/RangeReader/TokenRefresher/ConfigSaver）
│   ├── drivers/               # 驱动实现（每个驱动拆分为 driver/meta/types/util，借鉴 OpenList）
│   │   ├── local/             # 本地文件系统
│   │   ├── webdav/            # WebDAV
│   │   ├── onedrive/          # Microsoft Graph
│   │   └── mobilecloud/       # 中国移动云盘（139）
│   └── service/               # 业务层（纯 Go，双端共用）
│       ├── auth.go            # JWT / bcrypt / 密码管理
│       ├── library.go         # 扫描刮削 / 聚合进度
│       ├── stream.go          # 流式播放（302 / 中转 + Range）
│       ├── storage.go         # 存储实例与驱动生命周期 / 令牌刷新 cron
│       └── ...
├── main.go                    # Wails 桌面端入口
├── cmd/web/main.go            # Web 服务端入口（go:embed 前端）
├── Dockerfile / docker-compose.yml
└── build.bat                  # Windows 一键构建（Linux/Windows 服务端 + 桌面端）
```

## 数据存储

- `data/config.json`：全局配置（网盘凭据 AES-GCM 加密存储）
- `data/data.db`：SQLite（书籍 / 分集 / 进度 / 用户 / 偏好）
- `data/.machine.key`：加密密钥（跨机迁移请一并拷贝，或改用 `AUDIOBOOK_SECRET_KEY` 环境变量）
- `data/config.json.bak`：启动时检测到密钥不匹配或配置损坏，自动备份的原始配置（用于人工恢复）

## 公网部署安全建议

1. 修改默认管理员密码，保持自助注册关闭
2. 使用 Nginx / Caddy 反向代理提供 HTTPS
3. `data/` 目录含密钥与数据库，注意备份与文件权限
4. 定期备份：拷贝整个 `data/` 目录即可完成全量备份

## 致谢与许可

- **[OpenList](https://github.com/OpenListTeam/OpenList)**（AGPL-3.0）：本项目的存储驱动架构（Driver 接口 + 注册工厂 + `drivers/<name>/{driver,meta,types,util}.go` 模块拆分）借鉴了其设计思路；中国移动云盘驱动的签名算法为与其服务互操作所必需的协议实现。**本项目未复制 OpenList 的任何源代码**，为独立实现，不受 AGPL 传染。
- **[api.oplist.org](https://api.oplist.org/)**：OneDrive 免 Client ID/Secret 的令牌刷新中转服务（由 OpenList 社区维护）。
- 本项目自身尚未选择开源协议；如需开源发布，请自行选定并补充 LICENSE 文件。
