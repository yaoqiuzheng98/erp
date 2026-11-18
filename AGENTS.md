# 协作流程（测试先行，验证通过才提交）

口腔门诊 SaaS，单垂直应用，无插件。后续所有开发默认走这个流程，
不直接提交未验证的代码。

## 0. 写代码前先想模式

动手前问自己：这段逻辑有没有现成的设计模式能让它更优雅？

- 状态分支（switch 状态→文案/颜色/动作）→ 状态元数据表（一处定义，多处复用）
- 三个地方写同样代码 → 收敛到一处（Facade/比较器/helper），调用方只剩一行
- 先查后改的状态流转 → 条件更新一步到位（`UpdateWhere` 命中为 0 即报状态错）
- 字符串魔法值（审计动作/权限码/单号前缀）→ 常量，拼错在编译期或 Code Review 暴露
- 跨模块横切关注点（审计/限流）→ 中间件或统一入口，不在 handler 里散写

反面清单：不要为用模式而用模式。简单 CRUD 不套策略模式；
只有两处且不太可能再增的重复，抽常量即可，不必上泛型。

## 1. 本地开发与自测

```bash
go build ./... && go vet ./... && go test ./internal/...
```

模板大改后，用临时 `main.go`（放仓库内如 `./tmpcheck/main.go`，跑完删除）
调 `web.Build()` 全量解析 + 关键页面真实渲染，防止"字段已删、
模板还在引用"的运行时错误。

涉及 Mongo 逻辑改动时，连本地 `erp-mongo`（`mongodb://localhost:27017`）
跑集成验证：唯一索引、种子幂等、收费→应收链路，用完 `Drop` 测试库。
单测里连 mongo 的集成测试默认连 `127.0.0.1:27018`（`MONGO_TEST_URL` 可覆盖），
连不上自动 Skip；本地起一个 `docker run -d -p 27018:27017 mongo:7` 即可跑全量。

## 分支流程（铁律）

`main` 是唯一生产来源；`test` 只做集成测试，可能包含已经废弃的功能。

```text
main
  └── feat/xxx        特性分支从 main 拉
        ├── test      合到 test 联调/测试
        └── main      测试通过后，只把 feat/xxx 合到 main
```

- 新建特性：`git switch -c feat/xxx main`
- 合测试：`git switch test && git merge --no-ff feat/xxx`
- 上线：`git switch main && scripts/release-feature.sh feat/xxx`
- **严禁** `git merge test` 到 `main`，包括 fast-forward。
- 不要把 `test` 合进特性分支；需要同步主线时合 `main`，否则会把 `test` 的废弃功能带进特性分支。
- `test` 变脏（积累废弃功能）：`git switch test && git reset --hard main`，再合入当前活跃特性分支重新验证。
- 本地启用合并保护：`git config core.hooksPath .githooks`（一次性）。`pre-merge-commit` 拦截非快进的 test → main，`pre-push` 拦截 main 快进到 test 顶点；`scripts/release-feature.sh` 会拒绝 test，并检查特性分支历史里有没有 test 合并提交。
- 生产只从 `main` 构建。

## 2. 发测试环境

先切到 `test` 分支（特性分支已合入 test）：

```bash
git switch test
```

测试站：`https://erp.test.yayan.site`（广州生产机 `81.71.69.44`，独立 `erp-test` 容器 + `erp_test` 库，复用 `erp-mongo`/`erp-caddy`）。
Caddy 自动签发/续期 Let's Encrypt 证书；`erp.yayan.site/test` 保留为备用路径。

```bash
# ① 本地打测试镜像（test 分支代码）
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o erp-linux .
docker build -t erp-app:test -f - . <<'DOCKER'
FROM alpine:3.21
ENV TZ=Asia/Shanghai
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 erp
WORKDIR /opt/erp
COPY erp-linux /opt/erp/erp
USER erp
EXPOSE 8080
ENTRYPOINT ["/opt/erp/erp", "-config", "/opt/erp/config.toml"]
DOCKER
docker save erp-app:test | gzip > /tmp/erp-app-test.tar.gz

# ② 上传并加载启动（生产容器不动，它用 :latest；改了 compose.yaml/Caddyfile 一起 scp）
scp -i ~/.ssh/erp_prod_key /tmp/erp-app-test.tar.gz ubuntu@81.71.69.44:/tmp/
ssh -i ~/.ssh/erp_prod_key ubuntu@81.71.69.44 "cd /opt/erp && docker load < /tmp/erp-app-test.tar.gz && rm -f /tmp/erp-app-test.tar.gz && docker compose up -d --force-recreate erp-test"

# ③ 验证：测试小程序 API（生产容器不动）
curl -sk https://erp.test.yayan.site/api/p/6abceb30a79db9b892b1649e/clinic | grep "parking_image"
```

测试账号：系统后台 `admin` 密码见服务器 `/opt/erp/config.test.toml`
（当前 `admin123`，保留账号密码登录）。门诊后台已改手机号+密码登录：
门诊管理员在系统后台建门诊时设手机号/密码，其他员工由门诊管理员在
门诊后台「系统管理→员工」添加，不固定。
门诊通用超管：手机号 `15889862321`，测试密码 `123456`；启动时/新门诊创建时
按 `[clinic_admin]` 同步到每个门诊，员工列表隐藏，所有门诊后台通用。

## 3. 验证通过才提交推送

```bash
# ① 特性分支提交
git switch feat/xxx
git add -A && git commit -m "<中文一句话：做了什么>"

# ② 合 test 验证（test 只做集成分支，不作为生产来源）
git switch test && git merge --no-ff feat/xxx

# ③ 验证通过后合 main 上线（禁止 git merge test）
git switch main && scripts/release-feature.sh feat/xxx
git push origin main
```

WSL 侧 key 可直推 GitHub；Windows 侧 GoLand 推不动时，把
`~/.ssh/id_rsa(.pub)` 拷到 `C:\Users\yaoqi\.ssh\` 并收紧 ACL。

## 4. 生产升级（确认测试没问题后，另行指令才做）

生产只从 `main` 构建；不要用 `test` 分支或 test 构建产物。

走 `scripts/deploy.sh`（打 `:latest` 镜像 → 上传 → `docker load` → `compose up -d`）。

生产机 `ubuntu@81.71.69.44`（密钥同测试 `~/.ssh/erp_prod_key`）：Ubuntu 22.04 / 2 核 / 2G 内存 / 50G 盘；
Docker 29.9.1 + compose v5.6.0（apt 阿里源；`daemon.json` 配腾讯镜像加速；预拉 `mongo:7`、`caddy:2-alpine`）。
已上线（`https://erp.yayan.site`，Caddy 自动 HTTPS，证书有效）；
compose 服务 `mongo/erp-prod/caddy`（见 `deploy/Caddyfile.prod`），库 `erp`；
生产门诊：雅言口腔 `6ae4751d390dca5039774216`（绑小程序 `wx6ec6f6ba380885ba`），门诊管理员 13827001351。
门诊通用超管：手机号 `15889862321`，生产密码见 `/opt/erp/.clinic-admin-initial-password`（root 600）；`[clinic_admin]` 配置在 `config.toml`。
生产系统超管 `admin` 已改为强随机密码，密码只存在服务器 `/opt/erp/.sysadmin-initial-password`（root 600），不要写入仓库。

## 铁律

- 禁止在服务器上构建（961MB 内存，曾经 OOM）。只走本地打镜像 → `docker load`。
- 分支铁律：禁止 `test` → `main`；上线只合并特性分支（`scripts/release-feature.sh`），生产只从 `main` 构建。
- `config.*.toml` 属主必须是 `10001:10001`（容器内 `erp` 用户），否则 `permission denied`。
- 改了 `deploy/Caddyfile` 后用 `docker exec erp-caddy caddy reload --config /etc/caddy/Caddyfile`，不要重建 caddy（零停机）。
- 种子幂等：改 `config.*.toml` 的密码不影响已有账号；要重置先删对应集合文档再重启容器。
- 业务服务直连装配（`main.go` 构造注入），不许加全局单例、不许跨模块直连 Mongo（走 service/repo）。
- 并发按单实例设计：`compose.yaml` 只起 1 个 app 副本，钱路/约号用进程内条带锁串行化；
  扩到多副本前必须换 Redis 分布式锁，否则重单。单实例内也优先用条件更新（`UpdateWhere`），
  锁只兜"先查后建"（约号查重、挂号费查单）这种索引表达不了的间隙。
- 临时会话只删自己建的（按 `_id` 精确删），不许按 `user_id` 批量删（会踢掉用户登录）。
- 经 ssh 取 Mongo `_id` 会带 `ObjectId('...')`，用 `sed "s/ObjectId('//;s/')//;s/[^0-9a-f]//g"` 剥掉；远端命令用单引号，`$set` 等要写成 `\$set` 防远端 shell 展开。
- 加唯一索引前先查全库有无重复；改索引选项（加 `SetUnique`）必须先 `DropOne` 旧索引，否则 `IndexOptionsConflict` 起不来。
- `gofmt -l` 报的旧文件（没动过的对齐差异）不动，只管自己改过的文件。
- 浏览器验证走 WSL 调 Windows Chrome（`127.0.0.1:9222`），临时 cookie 设完要清，会话删掉，页面回 `about:blank`，手机模拟完把 viewport 调回桌面。
