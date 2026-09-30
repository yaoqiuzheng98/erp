# 协作流程（测试先行，验证通过才提交）

口腔门诊 SaaS，单垂直应用，无插件。后续所有开发默认走这个流程，
不直接提交未验证的代码。

## 1. 本地开发与自测

```bash
go build ./... && go vet ./... && go test ./internal/...
```

模板大改后，用临时 `main.go`（放仓库内如 `./tmpcheck/main.go`，跑完删除）
调 `web.Build()` 全量解析 + 关键页面真实渲染，防止"字段已删、
模板还在引用"的运行时错误。

涉及 Mongo 逻辑改动时，连本地 `erp-mongo`（`mongodb://localhost:27017`）
跑集成验证：唯一索引、种子幂等、收费→应收链路，用完 `Drop` 测试库。

## 2. 发测试环境

测试站：`https://erp.test.dokodemo.top`（与生产同机，复用 `erp-mongo`/`erp-caddy`）。

```bash
# ① 本地打测试镜像（当前工作区代码）
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o erp-linux .
docker build -t erp-app:test -f - . <<'DOCKER'
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 erp
WORKDIR /opt/erp
COPY erp-linux /opt/erp/erp
USER erp
EXPOSE 8080
ENTRYPOINT ["/opt/erp/erp", "-config", "/opt/erp/config.toml"]
DOCKER
docker save erp-app:test | gzip > /tmp/erp-app-test.tar.gz

# ② 上传并加载启动（生产容器不动，它用 :latest；改了 compose.yaml/Caddyfile 一起 scp）
scp -i ~/.ssh/erp_prod_key /tmp/erp-app-test.tar.gz root@23.249.19.239:/opt/erp/
ssh -i ~/.ssh/erp_prod_key root@23.249.19.239 "cd /opt/erp && docker load < erp-app-test.tar.gz && rm -f erp-app-test.tar.gz && docker compose up -d erp-test"

# ③ 验证：测试站登录 + 新功能页面（登录 POST 无需 CSRF）
curl -sk -c jar -d "username=admin&password=admin123" https://erp.test.dokodemo.top/sysadmin/login
curl -sk -b jar https://erp.test.dokodemo.top/sysadmin/tenants | grep "门诊"
```

测试账号：系统后台 `admin` 密码见服务器 `/opt/erp/config.test.toml`
（当前 `admin123`，保留账号密码登录）。门诊后台已改手机号+密码登录：
门诊管理员在系统后台建门诊时设手机号/密码，其他员工由门诊管理员在
门诊后台「系统管理→员工」添加，不固定。

## 3. 验证通过才提交推送

```bash
git add -A && git commit -m "<中文一句话：做了什么>" && git push origin main
```

WSL 侧 key 可直推 GitHub；Windows 侧 GoLand 推不动时，把
`~/.ssh/id_rsa(.pub)` 拷到 `C:\Users\yaoqi\.ssh\` 并收紧 ACL。

## 4. 生产升级（确认测试没问题后，另行指令才做）

走 `scripts/deploy.sh`（打 `:latest` 镜像 → 上传 → `docker load` → `compose up -d`）。

## 铁律

- 禁止在服务器上构建（961MB 内存，曾经 OOM）。只走本地打镜像 → `docker load`。
- `config.*.toml` 属主必须是 `10001:10001`（容器内 `erp` 用户），否则 `permission denied`。
- 改了 `deploy/Caddyfile` 后用 `docker exec erp-caddy caddy reload --config /etc/caddy/Caddyfile`，不要重建 caddy（零停机）。
- 种子幂等：改 `config.*.toml` 的密码不影响已有账号；要重置先删对应集合文档再重启容器。
- 业务服务直连装配（`main.go` 构造注入），不许加全局单例、不许跨模块直连 Mongo（走 service/repo）。
