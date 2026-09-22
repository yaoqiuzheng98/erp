# 插件开发骨架

复制本目录为新插件的脚手架（`_` 前缀目录不参与编译，仅作模板参考）：

```bash
cp -r internal/plugins/_example internal/plugins/myplugin
# 改包名、ID()、模板命名空间 example/ → myplugin/
# 在 main.go 加 blank import：_ "erp/internal/plugins/myplugin"
```

## 约定速查

| 项 | 规则 |
|---|---|
| 路由 | 页面挂 `/app/{id}`（RegisterRoutes），API 挂 `/api/plugins/{id}`（RegisterAPI），守卫中间件已预装 |
| 集合 | 统一前缀 `plg_{id}_`，必须用 `repo.NewTenantRepo[T]` 操作（自动注入 tenant_id） |
| 模板 | `templates/` 下所有 `.html` 被递归解析，块名必须 `{id}/` 前缀，整页模板自行 include 布局 partial |
| 权限码 | `{id}.{resource}.{action}`，在 Permissions() 声明，handler 用 `mw.RequirePerm` 校验 |
| 菜单 | Menus() 返回树，Perm 过滤后交给菜单组件 |
| 事件 | 跨插件联动走 `e.Events` + `contract` 包 payload；handler 内先判 `e.Gate.IsEnabled` |
| 服务 | 对外能力 `RegisterServices` → `e.Provide(name, svc)`；使用方 `contract` 定义接口 + `e.Service(name)` |
| 任务 | Tasks() 声明 cron/duration，Singleton 防重入，执行记录落 `task_runs` |
| 生命周期 | OnInstall 建索引/初始数据；OnDisable 不删数据 |
| 单据 | `e.Seq.Next(ctx, tenantID, "RULE")` 发号；状态流转集中在 service，发布 `contract` 事件 |

详见 docs/plugin-dev.md。
