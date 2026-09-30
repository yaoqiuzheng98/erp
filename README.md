# 口腔门诊 SaaS

多门诊 SaaS，只做口腔门诊：患者档案、牙位图、预约排椅、诊疗价目、
医护花名册、完成收费直连财务应收。单垂直应用，无插件系统。

## 1. 技术栈

| 层级 | 选型 | 说明 |
|------|------|------|
| 语言 | Go 1.26 | 单二进制交付 |
| HTTP 框架 | Gin | 中间件链 + 路由分组 |
| 页面渲染 | `html/template` + `embed.FS` | 服务端渲染 |
| CSS | Bootstrap 5 | vendor 进 embed，零前端构建 |
| JS 交互 | Bootstrap bundle（弹窗等） | 无 HTMX、无打包 |
| 数据库 | MongoDB 7+（`go.mongodb.org/mongo-driver/v2`） | 文档模型 |
| 会话 | Cookie + MongoDB 会话存储 | 自研轻量 Session 管理器 |
| 密码 | `golang.org/x/crypto/bcrypt` | |
| 配置 | `config.toml`（`pelletier/go-toml/v2`） | `-config` 指定路径 |

设计取向：少依赖、单二进制部署（模板与静态资源全部 `embed`）。

## 2. 总体架构

```
┌─────────────────────────────────────────────────────────┐
│  Browser (SSR HTML)                                      │
│  ├─ /sysadmin/*  系统管理后台（平台超管：门诊管理/审计）   │
│  └─ /admin/* + /app/*  门诊后台（员工 + 管理区）           │
├─────────────────────────────────────────────────────────┤
│  HTTP Server (gin.Engine)                                │
│  └─ Middleware 链: Recovery → Logger → Session →         │
│       TenantResolver → CSRF → Auth → RequirePerm         │
├─────────────────────────────────────────────────────────┤
│  业务层（直连装配，无插件）                                │
│  ├─ dental  门诊：患者/预约/价目/员工/牙位                │
│  └─ billing 财务：应收/收款/费用/账簿汇总                 │
├─────────────────────────────────────────────────────────┤
│  平台底座 platform                                        │
│  ├─ 租户/企业   ├─ 用户/认证   ├─ 角色权限 RBAC           │
│  ├─ 单据编号器  ├─ 审计日志   ├─ 附件文件  ├─ 站内通知    │
│  └─ Repository 层（强制 tenant_id 隔离） → MongoDB       │
└─────────────────────────────────────────────────────────┘
```

### 分层约定

- `handler`：HTTP 层，解析请求、调用 service、渲染模板，不含业务逻辑。
- `service`：业务逻辑。`dental.Complete` 直接调 `billing.CreateAR`（同库直连，无事件中转）。
- `repository`：`TenantRepo[T]` 基类，所有查询自动注入 `tenant_id` 过滤。
- `model`：BSON 结构体与领域枚举。

### 两类后台

| | 系统管理后台 `/sysadmin/` | 门诊后台 `/admin/` + `/app/` | 患者端 `/p/{门诊ID}` |
|---|---|---|---|
| 使用者 | 平台运维超管（`sys_admins`） | 门诊员工与管理员（`users`） | 患者（凭手机号+密码，门诊后台配置） |
| 登录 | `/sysadmin/login`，独立会话 cookie | `/login`（门诊名+账号+密码） | `/p/{门诊ID}/login`，独立患者会话 |
| 功能 | 门诊 CRUD/停用、全局审计 | `/app/*` 业务页 + `/admin/` 用户/角色/审计 | 价目表（公开）、预约挂号、我的预约/账单 |
| 菜单 | 固定（概览/门诊管理/全局审计） | 固定（门诊/团队/财务/系统管理） | 无侧边栏，手机优先单列页 |
| 数据范围 | 跨租户 | 强制 `tenant_id` 隔离 | 强制隔离，只能看自己的单 |

## 3. 业务流程

```
预约（选患者+医生可选+价目明细；同医生同时段防重）
  → 签到（报手机号找到单；指定医生进其队列，无指定分空闲/最短队，取排号）
  → 叫号（完成上一位自动叫下一位，也可手动叫）
  → 完成（按明细重算，无明细用预约存量，再用手工额）
  → 生成收费流水 CH-xxx → 财务应收 → 收款核销（RC-xxx）
患者端实时看：候诊前面人数 / 正在就诊
```

- 患者建档只收姓名电话，按电话自动认领老档案（有则报错防重），无则新建。
- 价目是门诊主数据（名称租户内唯一，药品建分类=药品的条目，不走库存）。
- 预约必须选在职医生；医生离职不影响历史单（快照名）。
- 同医生同时段防重（已约/已到诊占位），前台与患者自助走同一入口。
- 患者端（`/p/{门诊ID}` H5 + `/api/p/{门诊ID}` JSON，小程序预留同一批接口）：
  手机号+密码登录（门诊后台给患者配密），会话与员工体系隔离
  （`patient` kind 会话；H5 走 cookie，小程序走 Bearer），公开接口限流。
- 挂号费：租户级配置（系统后台按门诊设，0=不收），预约时快照；
  有费则跳模拟支付页，支付即建应收并全额核销（`RG-`流水，方法 mock），
  真支付接入时只换这一处。

## 4. 数据模型（MongoDB 集合）

```
tenants      { _id, name, status, created_at }
users        { _id, tenant_id, username, password_hash, name,
               role_ids[], status, is_tenant_admin, last_login_at }
sys_admins   { _id, username, password_hash }          // 系统级，无 tenant_id
roles        { _id, tenant_id, name, perm_codes[] }
sessions     { _id(token), kind[tenant/sys/patient], user_id, tenant_id, expires_at, data }
sequences    { _id: "tenantID:rule", prefix, date_part, value }
audit_logs   { _id, tenant_id, user_id, action, target, detail, ip, at }
attachments  { _id, tenant_id, owner_type, owner_id, filename,
               path, size, mime, uploaded_by }
notifications{ _id, tenant_id, user_id, type, title, link, read_at }
patients     { _id, tenant_id, name, phone, password_hash, gender, birth,
               allergy, history, note, teeth{} }
appointments { _id, tenant_id, patient_id, patient_name, doctor_id, doctor,
               chair, date, slot, item, items[], status, charge, charge_no }
service_items{ _id, tenant_id, name, category, price, unit, status }
staff        { _id, tenant_id, name, role[doctor/nurse/front/assistant],
               phone, status }
bills        { _id, tenant_id, patient_id, patient_name, doc_no, amount,
               lines[], paid_amount, status, ref_id }
payments     { _id, tenant_id, doc_no, bill_id, bill_doc_no,
               amount, method, paid_at }
expenses     { _id, tenant_id, title, amount, category, at }
```

权限码固定目录（`rbac.Catalog`）：`patient/appointment(catalog)/staff/billing` 各 `.read/.write`，
加 `admin.users/roles/audit`。租户管理员拥有全部权限。

## 5. 目录结构

```
erp/
├── main.go                   // 装配入口：配置→DB→服务直连→HTTP
├── config.toml               // 实际配置，gitignore 不入库
├── config.example.toml       // 配置样例
├── compose.yaml              // erp/erp-test/mongo/caddy 编排
├── deploy/                   // Caddyfile + config.*.example.toml
└── internal/
    ├── dental/               // 门诊：model/service/handler
    │                         // patient/appointment/serviceitem/staff
    ├── billing/              // 财务：model/service/handler
    └── platform/             // 底座：config/db/model/repo/session/
                              // auth/tenant/rbac/middleware/menu/web/
                              // httpserver/admin/sysadmin/
                              // seqno/audit/attach/notify/
```

## 6. 配置与部署

- `config.toml`：监听地址、Mongo URI/库名、会话 TTL、文件路径、密钥全集中于此。
- 本地运行：`cp config.example.toml config.toml`，启动 MongoDB（`docker start erp-mongo`），`go run . -config config.toml`；种子开启时自动创建系统超管（`admin/admin123`）。
- 验证：`go build ./...`、`go vet ./...`、`go test ./internal/...`。

### 部署方式（唯一）：本地打镜像 + `docker load`

```bash
# ① 本地构建测试镜像并打包
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

# ② 上传并加载启动（生产容器不动，它用 :latest）
scp -i ~/.ssh/erp_prod_key /tmp/erp-app-test.tar.gz root@23.249.19.239:/opt/erp/
ssh -i ~/.ssh/erp_prod_key root@23.249.19.239 \
  "cd /opt/erp && docker load < erp-app-test.tar.gz && rm -f erp-app-test.tar.gz && docker compose up -d erp-test"

# ③ 验证（系统后台登录）
curl -sk -c jar -d "username=admin&password=admin123" https://erp.test.dokodemo.top/sysadmin/login
curl -sk -b jar https://erp.test.dokodemo.top/sysadmin/tenants | head -c 200
```

```
/opt/erp/
├── compose.yaml              # erp/erp-test/mongo/caddy（引用镜像，无 build）
├── config.prod.toml          # 生产配置（不入库）
├── config.test.toml          # 测试配置（不入库，库 erp_test）
├── deploy/Caddyfile          # erp.dokodemo.top → erp:8080；erp.test → erp-test:8080
└── backups/
```

- Caddy 监听 80/443，两个域名首次访问自动签发证书，分别转发到 `erp:8080` / `erp-test:8080`。
- 日常生产部署用一键脚本 `scripts/deploy.sh`（打 `:latest` 镜像 → 上传 → `load` → `compose up -d`）。

### 生产配置检查单

- `[server].addr` 保持 `:8080`（compose 内网监听，由 caddy 反代）
- `[mongo].uri` 用 compose 服务名 `mongodb://mongo:27017`（mongo 不暴露端口）
- `[session].secret` 强随机值，HTTPS 下 `[session].secure = true`
- 首次可开 `[seed]` 建超管，建好后改为 `enabled = false`
- 测试环境 `[seed].enabled` 常开，含演示门诊

### 当前实例

- 主机：`23.249.19.239`（Ubuntu / x86_64），用户 `root`；私钥 `D:\ssh_key\id_rsa`（Windows 侧）/ `~/.ssh/erp_prod_key`（WSL 内副本，权限 600）

```bash
ssh -i ~/.ssh/erp_prod_key root@23.249.19.239
```

- 生产 `https://erp.dokodemo.top`、测试 `https://erp.test.dokodemo.top`；容器 `erp-app` / `erp-test` / `erp-mongo` / `erp-caddy`，同机另跑 `marzban-node`。
- sysadmin `admin`（密码在服务器 `config.prod.toml` / `config.test.toml`，不入库；测试站当前 `admin123`）。

## 7. 铁律

- 禁止在服务器上构建（961MB 内存，曾经 OOM）。只走本地打镜像 → `docker load`。
- `config.*.toml` 属主必须是 `10001:10001`（容器内 `erp` 用户），否则 `permission denied`。
- 改了 `deploy/Caddyfile` 后用 `docker exec erp-caddy caddy reload --config /etc/caddy/Caddyfile`，不要重建 caddy（零停机）。
- 种子幂等：改配置密码不影响已有账号；要重置先删对应集合文档再重启容器。
