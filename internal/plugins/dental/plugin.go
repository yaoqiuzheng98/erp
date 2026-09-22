// Package dental 口腔门诊插件：患者档案、牙位图、预约排椅、收费联动财务。
package dental

import (
	"context"
	"embed"
	"io/fs"

	"erp/internal/platform/env"
	"erp/internal/platform/menu"
	"erp/internal/platform/plugin"
	"erp/internal/platform/rbac"
	"erp/internal/plugins/dental/handler"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

//go:embed templates
var tplFS embed.FS

type Plugin struct {
	plugin.Base
}

func init() { plugin.Register(&Plugin{}) }

func (Plugin) ID() string             { return "dental" }
func (Plugin) Name() string           { return "口腔门诊" }
func (Plugin) Version() string        { return "0.1.0" }
func (Plugin) Templates() fs.FS       { return tplFS }
func (Plugin) Dependencies() []string { return []string{"basedata", "finance"} }

// Industries 门诊部（所）与专科医院。
func (Plugin) Industries() []string { return []string{"8425", "8415"} }

func (Plugin) RegisterRoutes(g *gin.RouterGroup, e *env.Env) {
	handler.New(e).Register(g)
}

func (Plugin) Menus() []menu.Item {
	return []menu.Item{{
		ID: "dental", Title: "门诊", Icon: "✚",
		Children: []menu.Item{
			{ID: "dental.today", Title: "今日预约", Path: "/app/dental", Perm: "dental.read"},
			{ID: "dental.patients", Title: "患者", Path: "/app/dental/patients", Perm: "dental.read"},
			{ID: "dental.appts", Title: "预约", Path: "/app/dental/appointments", Perm: "dental.read"},
		},
	}}
}

func (Plugin) Permissions() []rbac.PermissionDef {
	return []rbac.PermissionDef{
		{Code: "dental.read", Desc: "门诊查看"},
		{Code: "dental.write", Desc: "门诊操作"},
	}
}

// OnInstall 建索引。
func (Plugin) OnInstall(ctx context.Context, e *env.Env, tenantID bson.ObjectID) error {
	_, err := e.DB.C("plg_dental_patient").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "code", Value: 1}},
	})
	if err != nil {
		return err
	}
	_, err = e.DB.C("plg_dental_appt").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "date", Value: 1}},
	})
	return err
}
