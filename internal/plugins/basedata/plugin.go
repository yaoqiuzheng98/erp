// Package basedata 基础资料插件：商品/仓库/客户/供应商主数据，并对其他插件暴露主数据服务。
package basedata

import (
	"context"
	"embed"
	"io/fs"

	"erp/internal/platform/contract"
	"erp/internal/platform/env"
	"erp/internal/platform/menu"
	"erp/internal/platform/plugin"
	"erp/internal/platform/rbac"
	"erp/internal/plugins/basedata/handler"
	"erp/internal/plugins/basedata/service"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
)

//go:embed templates
var tplFS embed.FS

type Plugin struct {
	plugin.Base
}

func init() { plugin.Register(&Plugin{}) }

func (Plugin) ID() string       { return "basedata" }
func (Plugin) Name() string     { return "基础资料" }
func (Plugin) Version() string  { return "0.2.0" }
func (Plugin) Templates() fs.FS { return tplFS }

func (Plugin) RegisterRoutes(g *gin.RouterGroup, e *env.Env) {
	handler.New(e).Register(g)
}

// RegisterServices 向服务定位器注册主数据 API（contract.MasterDataAPI）。
func (Plugin) RegisterServices(e *env.Env) {
	e.Provide(contract.MasterDataService, service.New(e.DB.Database))
}

func (Plugin) Menus() []menu.Item {
	return []menu.Item{{
		ID: "basedata", Title: "基础资料", Icon: "▦",
		Children: []menu.Item{
			{ID: "basedata.products", Title: "商品", Path: "/app/basedata/products", Perm: "basedata.product.read"},
			{ID: "basedata.warehouses", Title: "仓库", Path: "/app/basedata/warehouses", Perm: "basedata.master.read"},
			{ID: "basedata.customers", Title: "客户", Path: "/app/basedata/customers", Perm: "basedata.master.read"},
			{ID: "basedata.suppliers", Title: "供应商", Path: "/app/basedata/suppliers", Perm: "basedata.master.read"},
		},
	}}
}

func (Plugin) Permissions() []rbac.PermissionDef {
	return []rbac.PermissionDef{
		{Code: "basedata.product.read", Desc: "商品查看"},
		{Code: "basedata.product.write", Desc: "商品维护"},
		{Code: "basedata.master.read", Desc: "主数据查看"},
		{Code: "basedata.master.write", Desc: "主数据维护"},
	}
}

// OnInstall 首次启用时建索引。
func (Plugin) OnInstall(ctx context.Context, e *env.Env, tenantID bson.ObjectID) error {
	return service.New(e.DB.Database).EnsureIndexes(ctx)
}
