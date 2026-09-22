# AGENTS.md

## 项目速览

Go + Gin + html/template + MongoDB 的多租户 ERP；插件编译期注册（`init()` + main.go blank import）、按租户启用。详见 `README.md`（架构）、`docs/plugin-dev.md`（插件开发）。

## 常用命令

```bash
go run . -config config.toml     # 本地启动（config.toml 不入库，参考 config.example.toml）
go build ./... && go vet ./...   # 编译与静态检查
go test ./...                    # 单元测试
scripts/backup.sh [config.toml]  # MongoDB 备份到 backups/
```

默认地址 `:8080`。种子账号：sysadmin `admin` / `admin123`；demo 租户 `admin` / `admin123`（见 config.toml `[seed]`）。

## 本地开发环境（WSL）

- MongoDB：Docker 容器 `erp-mongo`（`mongo:7`，端口映射 `27017`），`docker start erp-mongo`。
- 注意：WSL 中监听 `:8080` 的进程会通过 localhost 转发占用 Windows 侧 8080，GoLand debug 报 `bind: Only one usage...` 时先 `pgrep -af /tmp/erp` 清理。

## 生产环境

- 主机：`23.249.19.239`（hostname `AkZ1AFKZuVH.rfchost.com`，rfchost）
- 系统：Ubuntu，kernel 6.8.0-101-generic，x86_64
- 登录：用户 `root`；私钥 `D:\ssh_key\id_rsa`（Windows 侧）/ `~/.ssh/erp_prod_key`（WSL 内已复制，权限 600）

```bash
ssh -i ~/.ssh/erp_prod_key root@23.249.19.239
```

- 部署方式：Docker Compose，目录 `/opt/erp`；`erp` 应用 + `mongo:7` + `caddy`（erp.dokodemo.top 自动 HTTPS）。
- **服务器只有 961MB 内存且内核无 swap**：不能在上面编译 Go。部署用预构建方式——本地 `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o erp-linux .` 后 scp 上传，`docker compose -f compose.yaml -f compose.prebuilt.yaml up -d --build`；或本地 `docker build` + `docker save` 传镜像（`compose.image.yaml`）。
- 已部署（2026-09-22）：`https://erp.dokodemo.top` 在线，sysadmin `admin`（密码存于服务器 `config.prod.toml`）。
- 注意：服务器上另跑 `marzban-node` 容器；mongo 仅内网不暴露端口。

## 约定

- 插件之间禁止直接 import，跨插件走 `platform/contract` + `env.Provide/Service` + 事件总线。
- 所有业务集合 `plg_{id}_` 前缀，数据访问必须经 `repo.TenantRepo[T]`（强制 tenant_id）。
- 配置只走 `config.toml`（TOML），不使用环境变量；密钥类文件不入库。
