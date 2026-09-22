// Package plugin 插件框架：编译期注册、按租户运行期启用。
package plugin

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"sort"
	"strings"
	"time"

	"erp/internal/platform/env"
	"erp/internal/platform/event"
	"erp/internal/platform/menu"
	"erp/internal/platform/rbac"
	"erp/internal/platform/task"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// Plugin 业务插件接口。所有装配点经 Env 注入，插件间禁止直接 import。
type Plugin interface {
	ID() string
	Name() string
	Version() string
	Dependencies() []string

	// RegisterRoutes 在 /app/{id} 分组上注册页面路由（已挂守卫中间件）。
	RegisterRoutes(g *gin.RouterGroup, e *env.Env)
	// RegisterAPI 在 /api/plugins/{id} 分组上注册 API 路由（已挂守卫中间件）。
	RegisterAPI(g *gin.RouterGroup, e *env.Env)
	// Templates 返回 embed FS，其中模板块名必须带 "{id}/" 前缀。
	Templates() fs.FS
	// Static 可选，返回静态资源 FS，挂载到 /static/plugins/{id}/。
	Static() fs.FS

	Menus() []menu.Item
	Permissions() []rbac.PermissionDef
	Tasks(e *env.Env) []task.TaskDef
	// SubscribeEvents 订阅事件总线；handler 内应自行判断该租户是否已启用本插件。
	SubscribeEvents(b *event.Bus, e *env.Env)
	// RegisterServices 启动时向 Env 注册本插件对外服务（Service Locator）。
	RegisterServices(e *env.Env)

	OnInstall(ctx context.Context, e *env.Env, tenantID bson.ObjectID) error
	OnEnable(ctx context.Context, e *env.Env, tenantID bson.ObjectID) error
	OnDisable(ctx context.Context, e *env.Env, tenantID bson.ObjectID) error
}

// Base 空实现（Null Object 模式）：插件内嵌后只需覆盖关心的方法。
type Base struct{}

func (Base) Dependencies() []string                                   { return nil }
func (Base) RegisterRoutes(g *gin.RouterGroup, e *env.Env)            {}
func (Base) RegisterAPI(g *gin.RouterGroup, e *env.Env)               {}
func (Base) Templates() fs.FS                                         { return nil }
func (Base) Static() fs.FS                                            { return nil }
func (Base) Menus() []menu.Item                                       { return nil }
func (Base) Permissions() []rbac.PermissionDef                        { return nil }
func (Base) Tasks(e *env.Env) []task.TaskDef                          { return nil }
func (Base) SubscribeEvents(b *event.Bus, e *env.Env)                 {}
func (Base) RegisterServices(e *env.Env)                              {}
func (Base) OnInstall(context.Context, *env.Env, bson.ObjectID) error { return nil }
func (Base) OnEnable(context.Context, *env.Env, bson.ObjectID) error  { return nil }
func (Base) OnDisable(context.Context, *env.Env, bson.ObjectID) error { return nil }

// 全局注册表（Registry 模式）。
var registry = map[string]Plugin{}

// Register 在插件 init() 中调用；重复 ID 直接 panic。
func Register(p Plugin) {
	id := p.ID()
	if id == "" {
		panic("plugin: empty ID")
	}
	if _, dup := registry[id]; dup {
		panic("plugin: duplicated ID " + id)
	}
	registry[id] = p
}

func Get(id string) (Plugin, bool) {
	p, ok := registry[id]
	return p, ok
}

func All() []Plugin {
	out := make([]Plugin, 0, len(registry))
	for _, p := range registry {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}

// TopoSorted 按 Dependencies 拓扑排序（依赖在前），循环依赖退化为 ID 序。
func TopoSorted() []Plugin {
	all := All()
	byID := map[string]Plugin{}
	for _, p := range all {
		byID[p.ID()] = p
	}
	seen := map[string]int{} // 0 未访问 1 访问中 2 完成
	var out []Plugin
	var visit func(p Plugin)
	visit = func(p Plugin) {
		switch seen[p.ID()] {
		case 2:
			return
		case 1:
			out = append(out, p) // 循环依赖，按当前顺序输出
			seen[p.ID()] = 2
			return
		}
		seen[p.ID()] = 1
		for _, dep := range p.Dependencies() {
			if d, ok := byID[dep]; ok {
				visit(d)
			}
		}
		seen[p.ID()] = 2
		out = append(out, p)
	}
	for _, p := range all {
		visit(p)
	}
	return out
}

// CollectPermissions 汇总全部插件声明的权限码。
func CollectPermissions() []rbac.PermissionDef {
	var out []rbac.PermissionDef
	for _, p := range All() {
		out = append(out, p.Permissions()...)
	}
	return out
}

// ParseTemplates 把各插件模板解析进同一个 template（块名前缀隔离）。
func ParseTemplates(t *template.Template) (*template.Template, error) {
	for _, p := range All() {
		fsys := p.Templates()
		if fsys == nil {
			continue
		}
		err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".html") {
				return err
			}
			var err2 error
			t, err2 = t.ParseFS(fsys, path)
			return err2
		})
		if err != nil {
			return nil, fmt.Errorf("parse templates of plugin %s: %w", p.ID(), err)
		}
	}
	return t, nil
}

// TenantPlugin 租户插件启用记录。
type TenantPlugin struct {
	ID          bson.ObjectID `bson:"_id,omitempty"`
	TenantID    bson.ObjectID `bson:"tenant_id"`
	PluginID    string        `bson:"plugin_id"`
	Enabled     bool          `bson:"enabled"`
	Settings    bson.M        `bson:"settings,omitempty"`
	InstalledAt time.Time     `bson:"installed_at"`
	EnabledAt   time.Time     `bson:"enabled_at,omitempty"`
}

var (
	ErrNotRegistered = errors.New("插件未注册")
	ErrDepMissing    = errors.New("依赖插件未启用")
	ErrDepended      = errors.New("存在依赖本插件的已启用插件")
)

// Manager 按租户管理插件启停，实现 env.PluginGate。
type Manager struct {
	col *mongo.Collection
}

func NewManager(db *mongo.Database) *Manager {
	return &Manager{col: db.Collection("tenant_plugins")}
}

func (m *Manager) IsEnabled(ctx context.Context, tenantID bson.ObjectID, pluginID string) bool {
	n, _ := m.col.CountDocuments(ctx, bson.M{
		"tenant_id": tenantID, "plugin_id": pluginID, "enabled": true,
	})
	return n > 0
}

func (m *Manager) EnabledSet(ctx context.Context, tenantID bson.ObjectID) map[string]bool {
	cur, err := m.col.Find(ctx, bson.M{"tenant_id": tenantID, "enabled": true})
	if err != nil {
		return map[string]bool{}
	}
	var rows []TenantPlugin
	if err := cur.All(ctx, &rows); err != nil {
		return map[string]bool{}
	}
	set := map[string]bool{}
	for _, r := range rows {
		set[r.PluginID] = true
	}
	return set
}

// ListWithStatus 返回全部注册插件及该租户启用状态。
func (m *Manager) ListWithStatus(ctx context.Context, tenantID bson.ObjectID) ([]map[string]any, error) {
	enabled := m.EnabledSet(ctx, tenantID)
	var out []map[string]any
	for _, p := range All() {
		out = append(out, map[string]any{
			"ID": p.ID(), "Name": p.Name(), "Version": p.Version(),
			"Dependencies": p.Dependencies(), "Enabled": enabled[p.ID()],
		})
	}
	return out, nil
}

// Enable 启用插件：校验依赖已启用 → 首次 OnInstall → OnEnable → 记录。
func (m *Manager) Enable(ctx context.Context, e *env.Env, tenantID bson.ObjectID, pluginID string) error {
	p, ok := Get(pluginID)
	if !ok {
		return ErrNotRegistered
	}
	for _, dep := range p.Dependencies() {
		if !m.IsEnabled(ctx, tenantID, dep) {
			return fmt.Errorf("%w: %s", ErrDepMissing, dep)
		}
	}
	var rec TenantPlugin
	err := m.col.FindOne(ctx, bson.M{"tenant_id": tenantID, "plugin_id": pluginID}).Decode(&rec)
	firstInstall := errors.Is(err, mongo.ErrNoDocuments)
	if err != nil && !firstInstall {
		return err
	}
	if !firstInstall && rec.Enabled {
		return nil
	}
	if firstInstall {
		if err := p.OnInstall(ctx, e, tenantID); err != nil {
			return fmt.Errorf("OnInstall: %w", err)
		}
	}
	if err := p.OnEnable(ctx, e, tenantID); err != nil {
		return fmt.Errorf("OnEnable: %w", err)
	}
	now := time.Now()
	if firstInstall {
		rec = TenantPlugin{
			TenantID: tenantID, PluginID: pluginID,
			Enabled: true, InstalledAt: now, EnabledAt: now,
		}
		_, err = m.col.InsertOne(ctx, &rec)
		return err
	}
	_, err = m.col.UpdateOne(ctx,
		bson.M{"tenant_id": tenantID, "plugin_id": pluginID},
		bson.M{"$set": bson.M{"enabled": true, "enabled_at": now}},
	)
	return err
}

// Disable 禁用插件：校验无已启用插件依赖它，数据保留。
func (m *Manager) Disable(ctx context.Context, e *env.Env, tenantID bson.ObjectID, pluginID string) error {
	enabled := m.EnabledSet(ctx, tenantID)
	for _, p := range All() {
		if !enabled[p.ID()] {
			continue
		}
		for _, dep := range p.Dependencies() {
			if dep == pluginID {
				return fmt.Errorf("%w: %s", ErrDepended, p.ID())
			}
		}
	}
	p, ok := Get(pluginID)
	if !ok {
		return ErrNotRegistered
	}
	if err := p.OnDisable(ctx, e, tenantID); err != nil {
		return fmt.Errorf("OnDisable: %w", err)
	}
	_, err := m.col.UpdateOne(ctx,
		bson.M{"tenant_id": tenantID, "plugin_id": pluginID},
		bson.M{"$set": bson.M{"enabled": false}},
	)
	return err
}

// MenusOf 聚合租户已启用插件的菜单。
func MenusOf(enabled map[string]bool) []menu.Item {
	var out []menu.Item
	for _, p := range All() {
		if enabled[p.ID()] {
			out = append(out, p.Menus()...)
		}
	}
	return out
}

// PermissionsOf 聚合租户已启用插件的权限目录。
func PermissionsOf(enabled map[string]bool) []rbac.PermissionDef {
	var out []rbac.PermissionDef
	for _, p := range All() {
		if enabled[p.ID()] {
			out = append(out, p.Permissions()...)
		}
	}
	return out
}
