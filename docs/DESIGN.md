# StarStack — 本地私有云盘设计文档

一个自托管、多用户、简洁实用、高性能的本地私有云盘。后端 Go，前端 SPA，Docker 一键部署，宿主机目录通过 volume 挂载为共享盘。

## 1. 目标与取舍

- **核心目标**：自托管、多用户、挂载本地盘共享、响应快。
- **已被你确认的决策**：
  - 访问方式：浏览器 Web 界面 + **分享链接**（不做 WebDAV/S3）。
  - 存储：文件**直接落盘**，元数据用 **SQLite**。
  - 用户体系：每用户个人空间 + **共享区/公共盘**。
- **非目标**（保持简洁）：版本历史、全文检索、实时协同编辑、复杂 ACE 权限。后续按需增量添加。

## 2. 总体架构

```
                      +---------------------- Docker 容器 ----------------------+
 用户浏览器 ──HTTPS──▶│  Nginx (静态资源 + gzip + 反代)                          │
                      │        │                                              │
                      │        ▼                                              │
                      │   Go 服务 (单个二进制)                                  │
                      │   ├─ HTTP API (REST)                                   │
                      │   ├─ 认证 / JWT                                        │
                      │   ├─ 文件上传/下载/预览 (流式, 断点续传)                   │
                      │   └─ 分享链接                                            │
                      │        │                                              │
                      │        ▼                                              │
                      │   SQLite (元数据)     宿主机挂载盘 (实际文件)             │
                      └────────────────────────┬──────────────────────────────┘
                                               │ Docker volume (-v /data:/share)
                             宿主机目录: /data/ 被挂载为共享区
```

**挂载模型**：宿主机把目录（如 `/data/cloud`) 通过 `-v` 挂载进容器。指定某目录映射为 **共享区（公共盘）**，每个用户还能有独立的**个人空间**。

| 区域 | 说明 | 可见性 |
|------|------|--------|
| 个人空间 | 每个用户自己的根目录 | 仅本人 |
| 共享区/公共盘 | 宿主机挂载进来的目录 | 全体用户 |

## 3. 技术栈

**后端 (Go)**
- **HTTP 框架**：标准库 `net/http` + `chi`（轻量路由中间件，性能好无魔法），性能敏感路径直接用 `net/http` handler。
- **路由**：`go-chi/chi`。
- **鉴权**：JWT (`github.com/golang-jwt/jwt`)，AccessToken 短时 + RefreshToken 轮换。
- **元数据存储**：`SQLite` + `modernc.org/sqlite`（纯 Go 驱动，无 CGO，便于静态编译与多平台 Docker）。
- **密码**：`golang.org/x/crypto/bcrypt`。
- **上传**：流式分块，服务端计算内容校验（可校验 md5/sha256）。
- **预览/缓存**：缩略图 `golang.org/x/image`；LRU `hashicorp/golang-lru`；视频 HLS / Office→PDF 通过 `ffmpeg` / `libreoffice`（容器内捆绑，限制并发）；并发生成用 `golang.org/x/sync` singleflight。

**前端 (SPA)**
- **框架**：**React + Vite + TS**（生态大、性能好、便于后续扩展）。备选：Preact/Solid 更轻量。
- **UI 库**：Shadcn/ui + Tailwind（简洁、可控、快速）。
- **状态**：TanStack Query（缓存/请求）+ Zustand（全局状态）。
- **文件列表**：虚拟滚动（`@tanstack/react-virtual`）应对大目录。
- **上传/下载**：浏览器 fetch 流式处理，支持并发与进度。
- **构建产物**：静态文件由容器内 Nginx 直接托管。

**部署**
- 单容器：Go 二进制 + Nginx 静态托管 + SQLite 数据卷。
- `docker-compose`：一条命令起服务。
- 反向代理（可选）：容器对外暴露 8080，前面可再接 Nginx/Caddy 做 TLS。
- **日志**：文件日志用 `lumberjack` 自动轮转（按大小/时间、保留份数/天数、可压缩）；Nginx access/error 同样走轮转。

## 4. 核心功能清单（MVP）

### 4.1 认证与用户
- 注册/登录/登出（首个注册用户自动成为管理员）。
- JWT 鉴权 + RefreshToken。
- 管理员：创建/禁用用户、重置密码。

### 4.2 文件管理（个人空间）
- 目录树浏览：列表、排序（名称/大小/修改时间）、按类型过滤。
- 上传：拖拽/选择，流式上传，断点续传，多文件并发，进度显示。
- 下载：单文件流式下载，多选打包 zip。
- 操作：新建文件夹、重命名、移动、复制、删除（回收站）。
- 预览：图片、视频（HLS 流式）、音频、PDF、文本/代码高亮、Markdown。**所有预览资源动态按需加载 + 多级缓存**（见 §7 预览与缓存体系）。
- 回收站：软删除 + 定时或手动清空。

### 4.3 共享区 / 公共盘
- 独立于个人空间的顶层入口，展示宿主机挂载目录内容。
- 全体用户可读写（按后续需要可加"只读"开关）。
- 支持与个人空间同样的文件操作与分享。

### 4.4 分享链接
- 对任意文件/文件夹生成分享链接。
- 选项：过期时间（永久/指定日期）、访问密码、是否允许下载/上传（对文件夹）、最大访问次数。
- 分享链接走**独立的、无需登录**的访问路径（加签名防枚举），分享页为轻量只读视图。

### 4.5 管理与设置
- 管理员面板：用户管理、共享区路径配置、存储用量统计、系统信息。
- 个人设置：修改密码、头像（可选）。

## 5. 数据模型 (SQLite)

```sql
users          (id, username UNIQUE, password_hash, is_admin, storage_quota_bytes, created_at, disabled)
sessions       (token_hash, user_id, expires_at)              -- refresh token 黑名单/轮换
files          (id, parent_id?, name, is_dir, size, mime, created_at, updated_at, owner_id, deleted_at, sha256)
shares         (id, token UNIQUE, file_id, password?, expires_at, allow_download, max_uses, used, created_by)
system_config  (key PK, value)                                -- 共享区路径、默认配额等
```

- `files` 同时以 `parent_id` 表达树形结构（单表，含个人空间与共享区，用 `owner_id`/`is_public` 区分）。
- 目录路径与父 ID 冗余缓存；大目录用虚拟滚动，不做 DAG。
- 索引：`files(parent_id)`, `files(owner_id, deleted_at)`, `shares(token)`。

## 6. 性能设计

- **直连落盘**：文件不经任何对象存储中转，直接读写宿主机挂载目录，读写路径短。
- **流式传输**：上传/下载一律流式，杜绝把大文件载入内存；下载支持 `Range`，视频走分片。
- **静态资源**：前端构建产物由 Nginx 托管 + gzip/brotli + 强缓存哈希文件名。
- **SQLite 连接池**：单写多读，WAL 模式提升并发读性能。
- **目录缓存**：目录列表可加内存缓存 + 失效（后续）。
- **并发控制**：上传并发限流（信号量），避免拖垮磁盘与带宽。
- **分页**：列表接口分页/游标，避免一次性拉全量。

## 7. 预览与缓存体系（按需加载 + 缓存加速）

原则：**预览资源一律按需生成、懒加载，生成结果缓存复用**；不做启动时全量扫描/转码。

### 7.1 预览类型与加载策略

| 类型 | 预览形式 | 按需点 | 缓存 |
|------|---------|--------|------|
| 图片 | 缩略图（grid 用）+ 原图（点开用） | 进入目录时只请求当前页的缩略图；点开才拉原图 | 缩略图落盘缓存 + `Cache-Control` |
| 视频 | 缩略图 poster + HLS 分片播放 | 点开才触发转码，按需切片（不整片转完） | 切片落盘缓存 + HTTP Range |
| 音频 | 播放器 + 歌词（可选） | 点开才加载 | Range 缓存 |
| PDF | 浏览器内嵌 viewer + 分页 | 首屏只加载前几页 | 浏览器缓存 |
| Office | 转 PDF 预览（可选 LibreOffice headless） | 点开才转换，结果缓存 | 转换结果落盘缓存 |
| 文本/代码 | 编辑器组件 + 懒加载内容，超限截断 | 仅读前 N KB，展开再全量 | ETag |
| Markdown | 前端渲染，不落库 | 内容按需拉取 | ETag |
| Unknown | 只给图标 + 下载 | — | — |

### 7.2 三级缓存

```
浏览器缓存 (immutable/ETag)  →  服务端内存缓存 (LRU)  →  磁盘缓存 (缓存目录)
                                                          ↕
                                                  源文件 / 实时生成
```

1. **浏览器层**：缩略图 URL 带内容指纹（`sha256` 或 mtime 签名），`Cache-Control: public, immutable`；预览 HTML/文本用 ETag/304。
2. **内存层**：热点缩略图 / 小文件用 LRU（如 `hashicorp/golang-lru` v2）二级缓存，命中时零磁盘 IO。
3. **磁盘层**：生成类产物（缩略图、视频切片、Office→PDF）存到 `DATA_DIR/cache/`，按 `类型/内容hash/参数` 命名，断电重启不丢（同于 WebP 转码缓存）。缓存目录大小上限可配 + 定期 LRU 淘汰。

### 7.3 动态按需生成流水线

- **缩略图**：请求 `/api/files/{id}/thumb?size=256` 时，若缓存未命中 → 生成（Go `image` + 特定格式库，如 `golang.org/x/image`）→ 落盘缓存 → 响应（本次直出后续走缓存）。图片超过尺寸阈值才缩。
- **视频 HLS**：请求 `/api/files/{id}/video/master.m3u8` 时：
  - 检查缓存目录是否有切片；没有则**边转边播**（ffmpeg 按需从当前时间点切 2~4s 粒度片段，只转请求到的段）。
  - 后台任务可异步把整片预转好（配置开关，避免占满 CPU）。
  - 转码进程池限并发（如 2 个 ffmpeg），带 CPU 配额环境变量。
- **Office/PDF**：同样按需转换 + 结果缓存 + 进程池限流。
- **去重击穿**：同 key 并发生成用 singleflight，只算一次。

### 7.4 鉴权与清理

- 预览接口全部走鉴权（排除分享链接的匿名分支专门设计）。
- 后台缓存清理 job：低峰扫描 cache 目录按 LRU 淘汰，超预算回收。

## 8. Docker 部署

```yaml
# docker-compose.yml
services:
  starstack:
    image: starstack:latest
    ports:
      - "8080:8080"
    volumes:
      - ./data:/app/data        # SQLite + 用户文件
      - /data/cloud:/share:rw   # 宿主机目录挂载为共享区
    restart: unless-stopped
```

- 环境变量：`SHARE_ROOT=/share`（共享区挂载点）、`DATA_DIR=/app/data`、`PUBLIC_URL`（生成分享链接用）、`SECRET`（JWT 密钥）。
- 镜像：多阶段构建 —— `golang:alpine` 编译（`CGO_ENABLED=0`）→ `nginx:alpine` 拷入静态资源与二进制。
- 非 root 运行，容器内挂载目录权限校验。

## 9. 目录结构（建议）

```
starstack/
├── cmd/starstack/main.go
├── internal/
│   ├── api/          # HTTP handlers, 中间件, 路由
│   ├── auth/         # JWT, session, bcrypt
│   ├── fs/           # 文件系统操作: 落盘、回收站、zip、range
│   ├── preview/      # 预览: 缩略图、HLS、Office→PDF、按需生成与落盘缓存
│   ├── share/        # 分享链接生成与访问
│   ├── store/        # SQLite 数据访问
│   └── config/
├── web/              # 前端 (React+Vite+TS)
│   ├── src/
│   └── dist/         # 构建产物, 由 Nginx 托管
├── deploy/           # Dockerfile, docker-compose, nginx.conf
└── docs/DESIGN.md
```

## 10. 里程碑

1. **M1 认证 + 用户**：注册/登录/JWT/管理员。
2. **M2 文件 CRUD**：浏览、上传、下载、重命名、移动、删除、回收站。
3. **M3 共享区**：挂载目录读取与读写。
4. **M4 预览 + 分享**：缩略图/HLS/文档预览 + 三级缓存、分享链接。
5. **M5 打磨**：Docker 一键部署、压测、文档。

## 11. 风险与注意

- **权限校验**：所有文件操作必须二次校验归属（防越权访问他人空间），分享链接用随机 token 而非文件路径。
- **路径穿越**：严格校验并清理用户提交的相对路径，禁止 `..` 逃逸。
- **SQLite 并发**：WAL 模式 + 合理超时；若单实例写入压力大再考虑 PostgreSQL 或加缓存。
- **磁盘/配额**：可选给用户配置空间配额，防止占满共享区。
- **上传安全**：限制文件大小（配置）、类型扫描、文件名清洗。
## 12. 配置参数（最小化）

原则：**开箱即用**。只允许极少数**必填**项，其余全部内置合理默认值，没有配置文件也能跑。

```yaml
# config.yaml —— 可选；全部缺省也能直接启动
listen:  ":8080"                # 默认 :8080
data_dir: "/app/data"           # 默认 /app/data（SQLite、用户个人空间、缓存目录都在这）
share_root: "/share"            # 默认 /share（宿主机挂载进来的共享区）
secret: ""                      # 默认自动生成并持久化到 data_dir/secret
public_url: ""                  # 默认用请求的 Host 拼分享链接
log:                            # 全部默认
  level: "info"
  file: ""                      # 默认只打 stdout（docker logs 即可），留空即最简
```

### 12.1 默认值策略（用户无需配置的部分）

| 项 | 默认 |
|----|------|
| SQLite 数据库文件 | `<data_dir>/starstack.db` |
| 缓存目录 | `<data_dir>/cache/` |
| 上传大小上限 | 10 GiB |
| 缩略图尺寸 | 256px（可调 UI 参数, 不算配置项） |
| 视频转码并发 | min(2, CPU 核数/2)，CPU 配额自动探测 |
| 缓存清理 | 每 6 小时后台任务，磁盘预算默认 20% free 后开始 LRU 淘汰 |
| 日志轮转（若启用文件日志） | 100 MB/文件、保留 7 份/7 天、自动压缩 |

### 12.2 设计说明

- `-v` 挂载共享区到 `/share` 即生效，**不需要额外配置**。
- `secret` 若未设置：首次启动随机生成并写盘，避免重启后 JWT/分享链接失效。
- 进阶调优（缓存上限、HLS 预转开关、配额等）通过 `config.yaml` 可选覆盖，**不在基础最小配置清单里展开**，避免主页面放了太多字段。

## 13. 日志与轮转

**后端（Go）**
- 结构化 JSON 日志，用 `slog`（标准库），性能好无三方依赖。
- 默认只写 **stdout**，容器场景直接观察 `docker logs`，配合 logrotate/docer 的日志驱动即可；lumberjack 仅在显式配置 `log.file` 时启用。
- 若配置了文件日志：用 `natefinch/lumberjack` 轮转，参数齐全但默认即可：
  - `MaxSize: 100 MB` / `MaxBackups: 7` / `MaxAge: 7 days` / `Compress: true`
- 日志分两种级别：`info`（请求、分享生成、缓存命中未命中等关键事件）、`warn/error`（异常）。请求日志只记路径/method/status/耗时/用户 ID，**不记 body**，避免隐私泄露与放大日志体积。
- nginx access/error 同样按大小或时间轮转（若用文件日志）。

**容器**
- 默认 stdout，交给 Docker 日志驱动（`json-file` + `max-size=10m` + `max-file=3` 即可，开箱即用）。
