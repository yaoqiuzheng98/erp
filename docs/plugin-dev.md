# 插件开发指南

业务功能以插件形式实现：**编译期注册、按租户运行期启用**。骨架见 `internal/plugins/_example/`（可直接复制改包名）。

## 1. 目录结构

```
internal/plugins/{id}/
├── plugin.go            # Plugin 定义 + init() 注册 + 生命周期
├── model/model.go       # 文档结构（嵌入 model.Doc 获得通用字段）
├── service/service.go   # 业务逻辑、状态机、事件发布
├── handler/handler.go   # Gin handler（薄层：解析参数→service→Render）
└── templates/*.html     # 模板块名必须 {id}/ 前缀
```

## 2. 注册与装配

```go
type Plugin struct{ plugin.Base }   // Null Object：只覆盖需要的方法
func init() { plugin.Register(&Plugin{}) }
```

`main.go` 加 blank import：`_ "erp/internal/plugins/{id}"`。启动时按 `Dependencies()` 拓扑序调用 `RegisterServices`，随后注册任务与事件订阅。

## 3. 数据访问

- 集合名前缀 `plg_{id}_`（例：`plg_sales_order`）。
- 必须经 `repo.NewTenantRepo[T](e.DB.Database, name)`——写入自动注入 `tenant_id`，查询自动过滤，禁止直接用裸 collection 漏过滤。
- `model.Doc` 提供 `_id/tenant_id/created_at/created_by/updated_at/updated_by/status/remark`。

## 4. 路由与权限

- `RegisterRoutes` 挂到 `/app/{id}`，`RegisterAPI` 挂到 `/api/plugins/{id}`；`Session/TenantResolver/CSRF/Auth/PluginGuard` 中间件已预装。
- handler 内 `mw.RequirePerm("{id}.{res}.{action}")`；权限码在 `Permissions()` 声明后出现在租户角色管理页。
- 获取上下文：`mw.TenantID(c)`、`mw.User(c)`、`mw.Perms(c)`。

## 5. 模板

- `templates/` 下 `.html` 递归解析；块名必须 `{id}/` 前缀防冲突。
- 整页模板手动组合 partial：`partial/head` `partial/appnav` `partial/apptop` `partial/flash` `partial/pager` `partial/foot`。
- HTMX：handler 用 `web.IsHTMX(c)` 判断，返回 `web.RenderFrag` 渲染行/片段块。
- 分页：`skip, limit, pager := web.ParsePager(c, 20)`，查出 total 后 `pager.Total = total`。

## 6. 跨插件协作（禁止插件互 import）

- **事件**：`e.Events.Publish(ctx, event.Event{Topic, TenantID, Payload})`；payload 类型定义在 `internal/platform/contract`。订阅方在 `SubscribeEvents` 中先 `e.Gate.IsEnabled` 判断本插件是否启用。
  - 已定义：`doc.submit`（→flow 生成审批）、`flow.approved/rejected`、`sales.order.approved`（→inventory 出库、finance 应收）、`purchase.order.approved`（→inventory 入库、finance 应付）、`inventory.stock.low`（→通知）。
- **服务**：提供方 `RegisterServices` 里 `e.Provide(name, svc)`；消费方经 `contract` 中接口取用（见 `contract.Master(e)` 取主数据服务）。
- **编号**：`e.Seq.Next(ctx, tenantID, "SO")` → `SO-202609-0001`。

## 7. 单据状态机与审批

- 状态常量集中在 model 包；流转只在 service 方法中校验（`draft→submitted→approved/rejected`）。
- 接审批流：提交时若 `e.Gate.IsEnabled(ctx, tid, "flow")` 则置 `submitted` 并发 `doc.submit`，否则直接审结；同时订阅 `flow.approved/rejected` 按 `TargetType` 回写状态（参考 hr 插件）。

## 8. 生命周期

- `OnInstall`：首次启用，建索引/初始数据。
- `OnEnable`/`OnDisable`：启停钩子；禁用不删数据。
- 依赖约束：启用前依赖插件须已启用；禁用前无已启用插件依赖它（Manager 强制校验）。

## 8.1 行业适用范围（可选）

- `Industries() []string`：声明插件适用的租户行业码；**留空 = 通用插件**，所有行业可见可启用。
- 行业码采用 **GB/T 4754-2017 国民经济行业分类**（门类字母 / 大类 2 位 / 中类 3 位 / 小类 4 位），全量目录种子在 `industries` 集合，解析自国家统计局官方文档。
- 可声明任意层级：声明大类 `80`（居民服务业）即覆盖其下 804 理发、805 洗浴保健等全部中类小类；声明小类 `8040` 则只精确匹配理发店。
- 匹配语义：租户行业码的祖先链（path）包含声明码即适用——`plugin.AppliesTo` 强制校验，行业插件不出现在不匹配租户的 `/admin/plugins` 列表，`Enable` 直接拒绝（`ErrNotApplicable`）。
- 租户行业修改时若存在"已启用但不适用新行业"的插件，修改会被拒绝，需先禁用。

## 9. 检查清单

- [ ] ID 全局唯一；模板/权限/集合/事件 payload 均带 `{id}` 前缀
- [ ] 所有 Mongo 访问经 TenantRepo
- [ ] 非 GET 表单带 `_csrf` 隐藏域（layout 表单已示范）
- [ ] 依赖在 `Dependencies()` 声明，跨插件调用只经 contract
- [ ] 行业限定插件在 `Industries()` 声明 GB/T 4754 行业码（留空 = 通用）
- [ ] `go build ./... && go vet ./...` 干净，启动无模板解析错误
