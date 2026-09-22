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

（截至 2026-09-22 尚未部署 ERP / MongoDB，待实施。）

- 部署方式：Docker Compose（`erp` 应用 + `mongo:7` + `caddy`），域名 `erp.dokodemo.top` 已解析到服务器，Caddy 自动 HTTPS；部署目录 `/opt/erp`，生产配置 `config.prod.toml`（样例 `deploy/config.prod.example.toml`）。

## 约定

- 插件之间禁止直接 import，跨插件走 `platform/contract` + `env.Provide/Service` + 事件总线。
- 所有业务集合 `plg_{id}_` 前缀，数据访问必须经 `repo.TenantRepo[T]`（强制 tenant_id）。
- 配置只走 `config.toml`（TOML），不使用环境变量；密钥类文件不入库。
