# ERP 系统项目文档

一个面向中小企业的多端租户 ERP 系统。采用 **单体 + 服务端渲染** 架构：核心平台提供通用基础能力，业务功能以**插件**形式实现，不同企业（租户）可按需启用各自的插件组合。

## 1. 技术栈

| 层级 | 选型 | 说明 |
|------|------|------|
| 语言 | Go 1.26 | 单二进制交付，编译期插件注册 |
| HTTP 框架 | Gin（`github.com/gin-gonic/gin`） | 路由分组天然契合插件挂载；中间件链成熟 |
| 页面渲染 | `html/template` + `embed.FS` | 服务端渲染 |
| CSS | Bootstrap 5 | 组件齐全，vendor 进 embed，零前端构建 |
| JS 交互 | HTMX + Bootstrap bundle | 局部刷新返回 HTML 片段；组件行为用 Bootstrap 自带 JS |
| 数据库 | MongoDB 7+（`go.mongodb.org/mongo-driver/v2`） | 文档模型适合插件化、字段可扩展的业务数据 |
| 会话 | Cookie + MongoDB 会话存储 | 自研轻量 Session 管理器，透明于插件 |
| 密码 | `golang.org/x/crypto/bcrypt` | |
| 配置 | `config.toml`（`pelletier/go-toml/v2`） | 集中式配置文件，启动时解析进 Config 结构体 |
| 任务调度 | `go-co-op/gocron/v2` | 插件注册周期任务，命名任务/标签/防重入/执行钩子 |

设计取向：少依赖、单二进制部署（模板与静态资源全部 `embed`）、插件编译进二进制。

前端依赖文件统一 vendor 到 `platform/web/static/vendor/`（`bootstrap.min.css`、`bootstrap.bundle.min.js`、`htmx.min.js`），不走 CDN，离线/内网可部署。

## 2. 总体架构

```
┌─────────────────────────────────────────────────────────┐
│  Browser (SSR HTML)                                      │
│  ├─ /sysadmin/*  系统管理后台（平台超管）                  │
│  └─ /admin/* + /app/*  租户后台（租户管理区 + 业务插件页）  │
├─────────────────────────────────────────────────────────┤
│  HTTP Server (gin.Engine)                                │
│  └─ Middleware 链: Recovery → Logger → Session →         │
│       TenantResolver → CSRF → Auth → PluginGuard         │
├─────────────────────────────────────────────────────────┤
│  核心平台 (platform)                                     │
│  ├─ 租户/企业   ├─ 用户/认证   ├─ 角色权限 RBAC           │
│  ├─ 部门组织   ├─ 字典/参数   ├─ 单据编号器               │
│  ├─ 审计日志   ├─ 附件文件    ├─ 站内通知                 │
│  ├─ 菜单/导航  ├─ 插件注册中心 ├─ 后台任务                │
├─────────────────────────────────────────────────────────┤
│  插件层 (plugins) —— 按租户启用                           │
│  ├─ base-data 基础资料  ├─ sales 销售    ├─ purchase 采购 │
│  ├─ inventory 库存     ├─ finance 财务  ├─ mrp 生产       │
│  ├─ hr 人事考勤        ├─ flow 审批流   ├─ crm 客户       │
├─────────────────────────────────────────────────────────┤
│  Repository 层（强制 tenant_id 隔离） → MongoDB           │
└─────────────────────────────────────────────────────────┘
```

### 分层约定

- `handler`：HTTP 层，解析请求、调用 service、渲染模板，不含业务逻辑。
- `service`：业务逻辑与事务边界（MongoDB transaction）。
- `repository`：数据访问，所有查询自动注入 `tenant_id` 过滤。
- `model`：BSON 结构体与领域枚举。

### 2.1 设计模式应用约定

代码中统一采用以下设计模式，新增模块应优先复用这些既定模式而非另起风格：

| 模式 | 应用位置 | 说明 |
|------|----------|------|
| 注册表 Registry | `plugin.Register` 全局插件注册表、菜单/权限/任务目录 | 插件 `init()` 自注册，平台零硬编码 |
| 工厂 Factory | `repo.NewTenantRepo[T]`、各 service 构造函数 | 统一构造，注入 db/租户上下文等依赖 |
| 依赖注入 DI | `Env` 结构体（db、session、event、task、config）经构造函数逐级注入 | 禁止包级全局单例，便于测试替换 |
| 模板方法 Template Method | `TenantRepo` 基类封装 CRUD 骨架（自动注入 `tenant_id`）；插件生命周期 `OnInstall→OnEnable` | 骨架固定、差异点下放给子类/插件 |
| 策略 Strategy | 单据编号规则、文件存储后端（本地磁盘/GridFS）、会话存储 | 接口抽象 + 实现可替换，配置决定实例 |
| 观察者 Observer | `platform/event` 事件总线 | 插件订阅事件跨模块联动（销售审核→库存扣减→财务应收），彼此不 import |
| 责任链 Chain | Gin 中间件链 | Recovery→Logger→Session→Tenant→CSRF→Auth→PluginGuard→RequirePerm |
| 装饰器 Decorator | `RequirePerm`、`PluginGuard` 包装 handler | 横切关注点与业务代码分离 |
| 外观 Facade | service 层对 handler 屏蔽 repo/事务/事件细节 | handler 只面向 service 接口 |
| 适配器 Adapter | `Render` 助手封装 `c.HTML`、mongo driver 的 repo 封装 | 第三方细节收敛到一处 |
| 状态 State | 单据状态机（draft→submitted→approved→done） | 迁移合法性集中校验，禁止散落的 status 赋值 |
| 单例 Singleton | `gocron.Scheduler`、插件注册表 | 仅进程级基础设施允许单例 |

原则：模式为解决真实问题服务，不为模式而模式——能用简单函数说清的，不抽象接口。

## 3. 多租户模型与两类后台

### 3.1 前端入口拆分

系统有两套独立的后台界面，登录体系、会话、菜单、布局均分开：

| | 系统管理后台 `/sysadmin/` | 租户后台 `/admin/` + `/app/` |
|---|---|---|
| 使用者 | 平台运维超管（`sys_admins`） | 企业员工与租户管理员（`users`） |
| 登录 | `/sysadmin/login`，独立会话 cookie | `/login`，租户会话 cookie |
| 功能 | 租户 CRUD/停用、全局插件目录管理、全局审计、系统监控 | 业务插件页面（`/app/{plugin}/`）+ 租户管理区 `/admin/`（本企业用户/角色/部门/字典/参数/插件启用） |
| 菜单 | 固定菜单 | 管理区固定项 + 按启用插件动态生成 |
| 布局 | `layout/sysadmin` | `layout/app`（侧边栏+顶栏） |
| 数据范围 | 跨租户 | 强制 `tenant_id` 隔离 |

### 3.2 租户隔离

- 一套部署服务多家企业，企业即 **租户（tenant）**。
- **共享集合 + `tenant_id` 字段** 隔离：所有业务文档携带 `tenant_id`，Repository 基类强制注入过滤条件与写入字段；关键集合建 `{tenant_id: 1, ...}` 复合索引。
- 系统级超管（`sys_admin`，无 `tenant_id`）可管理租户与全局插件目录；租户管理员管理本企业用户与插件启用。
- **行业标识**：租户带 `industry` 字段（系统后台创建/修改），插件经 `Industries()` 声明适用行业——行业插件只对匹配租户出现在插件列表中（如理发店插件对便利店租户不可见），留空插件为通用。启用时服务端同样校验适用性。
- 中间件分区：`/sysadmin/*` 走 `SysAuth`（无租户上下文）；`/admin/*`、`/app/*` 走 `TenantResolver + Auth`，`PluginGuard` 只作用于 `/app/{plugin}/`。

## 4. 插件系统设计（核心）

### 4.1 设计原则

- **编译期注册、运行期启用**：插件是内部 Go 包，编译进同一二进制；每个租户在数据库中维护启用列表，避免 Go `.so` 动态插件的跨平台与版本兼容问题。
- **插件自描述**：路由、模板、菜单、权限、安装钩子均由插件接口声明，核心平台不硬编码任何业务插件。
- **强隔离**：插件只能访问自己的集合（命名前缀约束），跨插件数据通过核心平台公开的 Service 接口或事件总线协作。

### 4.2 插件接口

```go
// platform/plugin/plugin.go
type Plugin interface {
    ID() string                      // 唯一标识，如 "inventory"，小写字母数字横线
    Name() string                    // 显示名，如 "库存管理"
    Version() string                 // 语义化版本
    Dependencies() []string          // 依赖的其他插件 ID（启用时校验）

    // 装配
    RegisterRoutes(g *gin.RouterGroup, env *Env) // g 已定位在 /app/{pluginID}/ 分组下
    Templates() fs.FS                            // embed 的模板文件系统
    Static() fs.FS                               // embed 的静态资源（可选）

    // 平台贡献
    Menus() []MenuItem               // 导航菜单项，绑定权限码
    Permissions() []PermissionDef    // 插件声明的权限码，如 inventory.item.write
    Tasks() []TaskDef                // 后台定时任务（可选），经 gocron 注册、按插件标签启停

    // 生命周期（按租户粒度回调）
    OnInstall(ctx context.Context, tc TenantContext) error  // 首次启用：建集合/索引/初始数据
    OnEnable(ctx context.Context, tc TenantContext) error
    OnDisable(ctx context.Context, tc TenantContext) error  // 禁用不删数据
}
```

### 4.3 注册机制

```go
// plugins/inventory/plugin.go
func init() { plugin.Register(&InventoryPlugin{}) }

// main.go —— blank import 触发 init 注册
import (
    _ "erp/plugins/basedata"
    _ "erp/plugins/inventory"
    _ "erp/plugins/sales"
)
```

平台维护 **全局插件目录**（编译进二进制的全部插件）与 **租户启用表**（`tenant_plugins` 集合）。管理后台可浏览目录并一键启用/禁用。

### 4.4 运行期隔离与拦截

- 路由约定：平台为每个插件创建 `engine.Group("/app/" + pluginID)` 与 `engine.Group("/api/plugins/" + pluginID)`，分组上挂 `PluginGuard` + `RequirePerm` 中间件后再交给插件注册路由。
- `PluginGuard`（gin.HandlerFunc）校验该租户已启用对应插件，否则 403/跳转。
- 禁用插件后其菜单、权限、路由立即对租户不可见；数据保留，便于再次启用。
- 插件数据集合命名约束为 `plg_{pluginID}_{entity}`（如 `plg_inventory_item`），由插件基类仓库强制。

### 4.5 模板组织

- 平台提供布局模板：`layout/base`、`layout/app`（含侧边栏、顶栏）、`partials/*`（分页、表单控件、消息提示）。
- 每个插件的模板文件在启动时统一解析进同一个 `*template.Template`，经 `engine.SetHTMLTemplate` 交给 Gin；块名加命名空间：`{{define "inventory/item_list"}}`，避免重名。
- 渲染助手 `Render(c *gin.Context, "inventory/item_list", data)` 封装 `c.HTML`，内部附带公共数据（当前用户、菜单树、CSRF token、flash 消息）。
- HTMX 约定：handler 检查 `HX-Request` 头 —— 是则只渲染内容模板块（如 `inventory/item_table` 片段），否则渲染完整页面（布局 + 内容块）。同一 handler 同时服务整页与局部刷新。
- 菜单由平台聚合「已启用插件的 `Menus()` × 当前用户权限」动态生成。

### 4.6 权限模型

- RBAC：`role` 包含权限码集合，用户可挂多角色；权限码约定 `{pluginID}.{resource}.{action}`，如 `inventory.item.write`。
- 启用插件时将其 `Permissions()` 写入租户权限目录供角色勾选；禁用后相关权限失效但保留角色配置。
- Handler 层用 `RequirePerm("inventory.item.write")`（gin.HandlerFunc）鉴权，可从 `c.MustGet("user")` 取上下文用户。

### 4.7 插件间协作

- 直接依赖：通过 `Dependencies()` 声明（如 `sales` 依赖 `base-data`、`inventory`），启用时拓扑校验。
- 松耦合：平台提供进程内 **事件总线**（如 `document.approved`、`stock.changed`），插件订阅感兴趣的事件实现跨模块联动（销售出库单审核 → 库存扣减 → 财务应收）。

## 5. 核心平台基础功能

| 模块 | 功能要点 |
|------|----------|
| 租户管理 | 企业档案、套餐/插件配额、租户管理员初始化 |
| 用户与认证 | 登录/登出、会话管理、密码策略、登录日志、可选 TOTP 二步验证 |
| 角色权限 | 角色 CRUD、权限码勾选、数据范围（本人/本部门/全部） |
| 组织部门 | 部门树、员工归属、岗位 |
| 字典与参数 | 业务枚举字典（单位、币种、税率）、租户级系统参数 |
| 单据编号器 | `sequences` 集合按规则发号：`SO-2026-0001`，支持按租户/插件/规则分段 |
| 审计日志 | 登录、权限变更、单据增删改的操作留痕（who/when/what/diff） |
| 附件 | 文件元数据存 `attachments`，文件本体落本地磁盘或 GridFS，插件按 `owner_type/owner_id` 关联 |
| 站内通知 | 待办、预警消息推送（轮询或 SSE） |
| 菜单导航 | 租户后台按启用插件 + 用户权限动态构建；系统后台固定菜单 |
| 插件管理台 | 系统后台：全局插件目录维护；租户后台 `/admin/plugins`：本企业插件启用/禁用、租户级设置 |
| 后台任务 | 基于 gocron 的任务注册表（按插件标签管理）、`task_runs` 执行记录、失败告警 |

## 6. 业务插件清单

已实现（`internal/plugins/`）：

| 插件 ID | 名称 | 说明 | 依赖 |
|---------|------|------|------|
| `basedata` | 基础资料 | 商品、客户、供应商、仓库；对外提供主数据服务（contract.Master） | — |
| `inventory` | 库存管理 | 出入库/盘点单、库存余额、低库存预警任务 | `basedata` |
| `sales` | 销售管理 | 订单→审批→自动出库→应收 | `basedata`,`inventory` |
| `purchase` | 采购管理 | 订单→审批→自动入库→应付 | `basedata`,`inventory` |
| `flow` | 审批流 | 接收 `doc.submit` 生成审批单，结论经事件回写 | — |
| `finance` | 财务 | 应收应付核销、收付款、费用、账簿汇总 | `basedata` |
| `hr` | 人事考勤 | 员工档案、考勤、请假（可挂审批流） | — |
| `_example` | 骨架 | 插件开发模板（不参与编译） | — |

规划：`mrp` 生产制造、`crm` 客户关系。

不同企业的启用示例：贸易公司启用 `basedata + inventory + sales + purchase + finance`；服务型企业只开 `finance + flow + hr`。

## 7. 数据模型（MongoDB 集合）

平台集合（均含 `tenant_id`，除标注外）：

```
tenants          { _id, name, code, status, created_at }
users            { _id, tenant_id, username, password_hash, name, dept_id,
                   role_ids[], status, last_login_at }
sys_admins       { _id, username, password_hash }          // 系统级，无 tenant_id
roles            { _id, tenant_id, code, name, perm_codes[], data_scope }
departments      { _id, tenant_id, parent_id, name, sort }
dicts            { _id, tenant_id, type, code, label, sort, status }
params           { _id, tenant_id, key, value, desc }
sequences        { _id: "tenantID:rule", prefix, date_part, value }  // findOneAndUpdate 原子发号
sessions         { _id(token), user_id, tenant_id, expires_at, data }
audit_logs       { _id, tenant_id, user_id, action, target, detail, ip, at }
attachments      { _id, tenant_id, owner_type, owner_id, filename,
                   path, size, mime, uploaded_by }
notifications    { _id, tenant_id, user_id, type, title, link, read_at }
tenant_plugins   { _id, tenant_id, plugin_id, enabled, settings,
                   installed_at, enabled_at }
events           { _id, tenant_id, topic, payload, status, retries }  // 事件持久化(可选)
task_runs        { _id, plugin_id, job_name, started_at, finished_at,
                   status, error }                                   // 调度执行记录
plugin_catalog   { _id(plugin_id), name, version, description }       // 启动时同步注册表
```

插件集合命名 `plg_{pluginID}_{entity}`，例：

```
plg_basedata_product   { _id, tenant_id, code, name, unit, category, price, min_stock }
plg_basedata_warehouse / _customer / _supplier
plg_inventory_doc      { _id, tenant_id, doc_no, type[in|out|check], lines[], status }
plg_inventory_balance  { _id, tenant_id, product_id, warehouse_id, qty }
plg_sales_order        { _id, tenant_id, doc_no, customer_id, lines[], status, total }
plg_purchase_order     { _id, tenant_id, doc_no, supplier_id, lines[], status, total }
plg_flow_approval      { _id, tenant_id, target_type, target_id, status, approver }
plg_finance_bill       { _id, tenant_id, type[ar|ap], doc_no, amount, paid_amount, status }
plg_finance_payment    { _id, tenant_id, doc_no, type[receipt|payment], bill_id, amount }
plg_finance_expense    { _id, tenant_id, title, amount, category, at }
plg_hr_employee / _attend / _leave
```

通用字段约定：`tenant_id`、`created_at/by`、`updated_at/by`、`status`、`remark`；金额用 `Decimal128`；关键查询建 `{tenant_id, ...}` 复合索引。

## 8. 目录结构

```
erp/
├── main.go                     // 装配入口：配置→DB→平台→blank import 插件→HTTP
├── config.toml                 // 实际配置，gitignore 不入库
├── config.example.toml         // 配置样例，入库供参考
└── internal/
    ├── platform/               // 核心平台（插件可依赖，插件之间不可互相 import）
    │   ├── config/  db/  model/  repo/    // TOML 配置、Mongo 连接、文档基座、TenantRepo[T]
    │   ├── session/ auth/  rbac/  tenant/  org/  dict/
    │   ├── seqno/  audit/  attach/  notify/
    │   ├── menu/  plugin/  env/           // 插件接口+注册表+启停管理、Env 聚合
    │   ├── event/  task/                  // 事件总线、gocron 封装
    │   ├── middleware/                    // Session/Tenant/CSRF/Auth/PluginGuard/RequirePerm
    │   ├── httpserver/                    // gin.Engine 装配、登录/工作台/附件 handler
    │   ├── admin/  sysadmin/              // 租户管理区、系统后台 handler
    │   └── web/
    │       ├── templates/  partial|pages/{app,admin,sysadmin}
    │       └── static/vendor/             // bootstrap / htmx
    └── plugins/
        ├── basedata/   inventory/  sales/  purchase/
        ├── finance/    flow/       hr/
        └── _example/             // 插件骨架（_ 前缀不参与编译，复制即用）
            ├── plugin.go         // 全方法注释模板
            └── templates/        // embed，块名 {id}/*
```

## 9. 关键流程

### 9.1 请求生命周期

```
租户后台 /admin/*、/app/*:
请求 → Recover → RequestLog → Session(解析租户 cookie，加载用户)
     → TenantResolver(用户→租户，校验租户状态)
     → CSRF(非 GET 校验 token)
     → Auth(未登录跳 /login)
     → PluginGuard(仅 /app/{plugin}/，校验租户已启用)
     → RequirePerm(权限码校验)
     → handler → service(→事务) → repo(注入 tenant_id) → MongoDB
     → Render(layout/app + 插件模板，注入菜单/flash/CSRF)

系统后台 /sysadmin/*:
请求 → Recover → RequestLog → Session(解析超管 cookie)
     → CSRF → SysAuth(未登录跳 /sysadmin/login)
     → handler → service → repo(可跨租户，操作仍记审计)
     → Render(layout/sysadmin)
```

### 9.2 租户启用插件

```
管理员在插件台点击启用
  → 校验依赖（递归启用或提示先启用依赖）
  → OnInstall（首次：建索引、写初始字典）
  → OnEnable
  → 写入 tenant_plugins{enabled:true} + 同步权限目录
  → 菜单即刻生效；事件订阅注册
```

## 10. 安全要点

- 密码 bcrypt(cost≥12)；会话 token 加密随机、HttpOnly+Secure+SameSite Cookie。
- 所有表单 POST 走 CSRF token；`html/template` 默认转义防 XSS。
- Repository 层强制 `tenant_id`：写路径注入、读路径过滤，单元测试覆盖越租户访问。
- 审计敏感操作；限流登录接口；Mongo 注入面小但禁止把用户输入直接拼进 `$where`/聚合表达式。
- 备份：`mongodump` 定时任务 + 租户级导出。

## 11. 配置与部署

- `config.toml`：监听地址、Mongo URI/库名、会话 TTL、文件存储路径、日志级别、密钥（session 加密 key 等）全部集中于此；`-config` 启动参数指定路径，默认 `./config.toml`。
- 样例：

```toml
[server]
addr = ":8080"

[mongo]
uri = "mongodb://localhost:27017"
database = "erp"

[session]
ttl_hours = 12
cookie_name = "erp_sid"
sys_cookie_name = "erp_sys"
secret = "change-me"          # 会话 cookie 签名密钥
secure = false                # HTTPS 部署置 true，cookie 加 Secure 标记

[storage]
upload_dir = "./data/uploads"

[log]
level = "info"

[seed]
enabled = true                # 首次启动写入种子数据，生产建好后改 false
sysadmin_user = "admin"
sysadmin_pass = "admin123"
demo_tenant = true            # 生产环境建议 false
demo_admin_user = "admin"
demo_admin_pass = "admin123"
```

- 安全：`config.toml` 含密钥，加入 `.gitignore` 不入库；仓库只提交 `config.example.toml`；文件权限建议 `chmod 600`。
- 本地运行：`cp config.example.toml config.toml`，启动 MongoDB，然后 `go run . -config config.toml`；种子开启时自动创建系统超管与演示租户（默认 `admin/admin123`）。
- 验证：`go build ./...`、`go vet ./...`、`go test ./...`；备份 `scripts/backup.sh [config.toml]` 导出到 `backups/`。
- 索引初始化：启动时平台与已注册插件各自 `ensureIndexes`。
- 本地开发（WSL）：MongoDB 用 Docker 容器 `erp-mongo`（`mongo:7`，端口映射 `27017`，`docker start erp-mongo`）。注意 WSL 中监听 `:8080` 的进程会经 localhost 转发占用 Windows 侧端口，GoLand debug 报 `bind: Only one usage...` 时先 `pgrep -af /tmp/erp` 清理遗留进程。

### 11.1 生产部署（本地打镜像 + docker load）

生产环境用 `compose.yaml` 编排三个服务：`erp`（`erp-app:latest` 镜像）、`mongo:7`（仅内部网络）、`caddy`（反向代理 + 自动 HTTPS）。

**部署方式只有一种**：镜像在本地 `docker build` 构建后打包上传，服务器 `docker load` 直接运行——服务器不做任何构建（内存小的机器尤其需要）。本仓库 `Dockerfile` 多阶段构建在本地完成。

```bash
# ① 本地构建镜像并打包
docker build -t erp-app:latest .
docker save erp-app:latest | gzip > erp-app.tar.gz

# ② 上传
scp -i ~/.ssh/erp_prod_key erp-app.tar.gz root@<host>:/opt/erp/

# ③ 服务器：加载镜像并启动（mongo/caddy 首次会从 Docker Hub 拉取）
cd /opt/erp
docker load < erp-app.tar.gz
cp deploy/config.prod.example.toml config.prod.toml   # 首次：改密钥
docker compose up -d
docker compose logs -f erp
```

部署目录约定 `/opt/erp`：

```
/opt/erp/
├── compose.yaml              # 三个服务编排（erp 引用 erp-app:latest，无 build）
├── config.prod.toml          # 生产配置（不入库，见 deploy/config.prod.example.toml）
├── deploy/Caddyfile          # 域名反代（erp.dokodemo.top → erp:8080）
└── backups/                  # mongodump 输出目录（挂载进容器）
                              # （erp-app.tar.gz 镜像包 docker load 后自动删除）
```

Caddy 监听 80/443，`erp.dokodemo.top` 首次访问自动签发证书（域名已解析到服务器即可），转发到 `erp:8080`；应用只监听容器内网。

> 日常部署直接用一键脚本 `scripts/deploy.sh`（编译→打镜像→上传→load→compose up），可用 `ERP_HOST`/`ERP_SSH_KEY`/`ERP_DIR` 环境变量覆盖默认值。

### 11.2 域名与 HTTPS

`deploy/Caddyfile`：

```
erp.dokodemo.top {
	reverse_proxy erp:8080
	encode gzip
	request_body { max_size 64MB }
}
```

HTTPS 证书由 Caddy 自动申请续期；`config.prod.toml` 必须 `[session].secure = true`（cookie 加 Secure）。

### 11.3 备份与升级

- 备份：`scripts/backup.sh /opt/erp/config.prod.toml` 自动识别 `erp-mongo` 容器导出到 `backups/erp-<时间戳>/`；建议 crontab 每日执行并异地转储。
- 升级：本地重新 `docker build` + `docker save` 上传 → 服务器 `docker load` → `docker compose up -d`（compose 检测到镜像变化会重建 erp 容器）；插件 `OnInstall`/索引迁移在启动时自动执行，无停机脚本。

### 11.4 生产配置差异检查单

- `[server].addr` 保持 `:8080`（compose 内网监听，由 caddy 反代；应用不直接对公网）
- `[mongo].uri` 用 compose 服务名 `mongodb://mongo:27017`（mongo 不暴露端口）
- `[session].secret` 使用新生成的强随机值，勿与开发环境共用；HTTPS 下 `[session].secure = true`
- `[log].level` 用 `info`/`warn`
- 首次部署可开 `[seed]` 建超管（`demo_tenant` 建议 `false`），建好后改为 `enabled = false`

### 11.5 当前生产实例

- 主机：`23.249.19.239`（rfchost，Ubuntu / kernel 6.8 / x86_64），用户 `root`；私钥 `D:\ssh_key\id_rsa`（Windows 侧）/ `~/.ssh/erp_prod_key`（WSL 内副本，权限 600）

```bash
ssh -i ~/.ssh/erp_prod_key root@23.249.19.239
```

- 站点：`https://erp.dokodemo.top`（已上线）；部署目录 `/opt/erp`，三个容器 `erp-app` / `erp-mongo` / `erp-caddy`。
- **只有 961MB 内存且内核无 swap**：禁止在服务器上做任何构建（曾经 `compose up --build` 直接 OOM 重启）。只能走本地打镜像 → `docker load` 这条路，即 `scripts/deploy.sh`。
- 同机另跑 `marzban-node` 容器；mongo 仅 compose 内网可达，不暴露端口。
- sysadmin `admin`（密码在服务器 `config.prod.toml`，不入库）。

## 12. 里程碑

| 阶段 | 目标 |
|------|------|
| M1 平台底座 | 配置/DB/会话/登录/租户/RBAC/菜单/审计，插件注册表与启用台跑通 ✅ |
| M2 基础数据+库存 | `basedata`（商品/仓库/客户/供应商/主数据服务）+ `inventory`（出入库/余额/预警任务）✅ |
| M3 业务闭环 | `sales` + `purchase`，事件总线联动库存与应收应付 ✅ |
| M4 扩展 | `finance`（应收应付/收付款/费用/汇总）、`flow` 审批流、`hr`（员工/考勤/请假）✅ |
| M5 加固 | `_example` 骨架、`docs/plugin-dev.md`、`scripts/backup.sh`、分页组件、单元测试 ✅ |

## 12.1 已实现的事件链

```
订单 submit → doc.submit → flow 生成审批单
审批通过 → flow.approved → 订单 approved → {sales,purchase}.order.approved
  → inventory 自动生成并确认出/入库单（出库先校验库存，不足留草稿+通知）
  → finance 自动生成应收/应付单 → 核销生成收付款单
库存低于商品 min_stock → inventory.stock.low → 通知租户管理员
```

## 13. 插件开发规范（附录）

新插件开发步骤：

1. 复制 `internal/plugins/_example` 为 `internal/plugins/{id}`。
2. 实现 `Plugin` 接口：`ID/Name/Version/Dependencies`、路由、模板、菜单、权限、生命周期钩子。
3. 数据访问继承 `platform/repo` 的 `TenantRepo[T]`，集合名前缀 `plg_{id}_`。
4. 模板块名加 `{id}/` 前缀；路由全部挂在 `/app/{id}/` 下并用 `RequirePerm` 保护。
5. `main.go` 增加 blank import；启动后系统管理员在插件台为租户启用。
6. 需要跨模块联动时发布/订阅 `platform/event` 事件，不要直接 import 其他插件。
7. 遵循 2.1 节设计模式约定：service 用构造函数注入依赖、单据流转走状态机、可替换实现走策略接口。
