# StarStack

本地私有云盘：多用户、Go 后端、React 前端、Docker 一键部署、挂载本地盘共享。

## 功能（当前实现）

- 多用户认证（JWT + RefreshToken 轮换，首个用户自动为管理员）
- 个人空间 + 共享盘（宿主机目录挂载）
- 文件操作：上传（多文件/拖拽、流式）、下载（Range/断点续传）、
  新建目录、重命名、移动、删除（进回收站）
- 回收站：列表、恢复、彻底删除、清空
- 预览（按需 + 缓存）：图片缩略图（内存 LRU + 磁盘缓存）、
  图片/视频(HTML5+Range)/音频/PDF/文本 内联预览
- 分享链接：无需登录访问，目录列表/文件下载、可配过期/密码/次数上限/禁下载
- 管理面板：创建用户、停用/启用、重置密码

> 完整设计见 [docs/DESIGN.md](docs/DESIGN.md)（预览/缓存体系、最小化配置、日志轮转等）。

## 本地开发

```bash
# 常用命令（详见 make help）
make build        # 编译后端到 bin/
make run          # 本地启动后端（默认 :8290）
make web-dev      # 前端 HMR（/api 代理到 :8290）
make test         # Go 测试 + vet

# 或手动：
# 后端
LISTEN=:8290 DATA_DIR=./data SHARE_ROOT=./share go run ./cmd/starstack
# 前端（vite 代理 /api → :8290）
cd web && npm install && npm run dev
```

## Docker 部署

```bash
make up          # = docker compose up -d --build (deploy/docker-compose.yml)
make image       # 仅构建镜像
make logs        # 查看日志
make down        # 停止

# 或手动：
cd deploy && docker compose up -d --build
# 打开 http://localhost:8290 ，首个注册用户即管理员
```

挂载说明：`docker-compose.yml` 中 `/data/cloud:/share` 即宿主机目录→共享盘，
修改该卷路径即可指向任意本地目录。

环境变量（全部可选，见 docs/DESIGN.md §12）：

| 变量 | 默认 | 说明 |
|------|------|------|
| `LISTEN` | `:8290` | 后端监听 |
| `DATA_DIR` | `/app/data` | SQLite、个人空间、缓存 |
| `SHARE_ROOT` | `/share` | 共享盘挂载点 |
| `SECRET` | 自动生成 | JWT/分享签名密钥（留空随机生成并持久化） |
| `LOG_LEVEL` | `info` | `info` / `debug` |

## 测试

```bash
go test ./...          # 单元测试
go build ./... && go vet ./...
```