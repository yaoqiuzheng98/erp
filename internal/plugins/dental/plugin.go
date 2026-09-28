// Package dental 口腔门诊插件：患者档案、牙位图、预约排椅、收费联动财务。
package dental

import (
	"context"
	"embed"
	"io/fs"
	"time"

	"erp/internal/platform/env"
	"erp/internal/platform/menu"
	"erp/internal/platform/plugin"
	"erp/internal/platform/rbac"
	"erp/internal/plugins/dental/handler"
	"erp/internal/plugins/dental/model"

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
func (Plugin) Version() string        { return "0.2.0" }
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
			{ID: "dental.services", Title: "价目表", Path: "/app/dental/services", Perm: "dental.read"},
		},
	}}
}

func (Plugin) Permissions() []rbac.PermissionDef {
	return []rbac.PermissionDef{
		{Code: "dental.read", Desc: "门诊查看"},
		{Code: "dental.write", Desc: "门诊操作"},
	}
}

// OnEnable 已启用租户同样幂等补索引与价目（覆盖升级前已启用的租户）。
func (Plugin) OnEnable(ctx context.Context, e *env.Env, tenantID bson.ObjectID) error {
	return ensureDental(ctx, e, tenantID)
}

// OnInstall 建索引并种子默认价目表（幂等：已有条目则跳过）。
func (Plugin) OnInstall(ctx context.Context, e *env.Env, tenantID bson.ObjectID) error {
	return ensureDental(ctx, e, tenantID)
}

func ensureDental(ctx context.Context, e *env.Env, tenantID bson.ObjectID) error {
	// 去编码迁移：删掉历史 {tenant_id, code} 唯一索引，清理存量 code 字段。
	_ = e.DB.C("plg_dental_patient").Indexes().DropOne(ctx, "tenant_id_1_code_1")
	_ = e.DB.C("plg_dental_service_item").Indexes().DropOne(ctx, "tenant_id_1_code_1")
	for _, col := range []string{"plg_dental_patient", "plg_dental_service_item"} {
		_, _ = e.DB.C(col).UpdateMany(ctx, bson.M{"code": bson.M{"$exists": true}},
			bson.M{"$unset": bson.M{"code": ""}})
	}
	if _, err := e.DB.C("plg_dental_appt").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "date", Value: 1}},
	}); err != nil {
		return err
	}
	return seedServiceItems(ctx, e, tenantID)
}

// seedServiceItems 默认诊疗价目（参考价，租户可改）；已有条目则跳过。
func seedServiceItems(ctx context.Context, e *env.Env, tenantID bson.ObjectID) error {
	n, err := e.DB.C("plg_dental_service_item").CountDocuments(ctx, bson.M{"tenant_id": tenantID})
	if err != nil || n > 0 {
		return err
	}
	defaults := []model.ServiceItem{
		{Name: "初诊检查", Category: "检查", Price: 50, Unit: "次", Status: "active"},
		{Name: "口腔拍片", Category: "检查", Price: 100, Unit: "次", Status: "active"},
		{Name: "超声洁治", Category: "洁治", Price: 300, Unit: "次", Status: "active"},
		{Name: "树脂补牙", Category: "充填", Price: 300, Unit: "颗", Status: "active"},
		{Name: "根管治疗", Category: "根管", Price: 1200, Unit: "颗", Status: "active"},
		{Name: "简单拔牙", Category: "拔牙", Price: 300, Unit: "颗", Status: "active"},
		{Name: "阻生智齿拔除", Category: "拔牙", Price: 1200, Unit: "颗", Status: "active"},
		{Name: "正畸复诊", Category: "正畸", Price: 200, Unit: "次", Status: "active"},
		{Name: "烤瓷冠修复", Category: "修复", Price: 1500, Unit: "颗", Status: "active"},
		{Name: "种植牙", Category: "种植", Price: 8000, Unit: "颗", Status: "active"},
	}
	docs := make([]any, 0, len(defaults))
	now := time.Now()
	for _, d := range defaults {
		d.TenantID, d.CreatedAt = tenantID, now
		docs = append(docs, d)
	}
	_, err = e.DB.C("plg_dental_service_item").InsertMany(ctx, docs)
	return err
}
