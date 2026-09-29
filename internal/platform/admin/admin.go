// Package admin 租户管理区 handler：用户/角色/审计。
package admin

import (
	"net/http"

	"erp/internal/platform/audit"
	"erp/internal/platform/auth"
	"erp/internal/platform/env"
	mw "erp/internal/platform/middleware"
	"erp/internal/platform/rbac"
	"erp/internal/platform/web"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type Handler struct {
	e     *env.Env
	users *mongo.Collection
}

func New(e *env.Env) *Handler {
	return &Handler{e: e, users: e.DB.C("users")}
}

func (h *Handler) Register(g *gin.RouterGroup) {
	g.GET("/users", mw.RequirePerm("admin.users"), h.usersPage)
	g.POST("/users", mw.RequirePerm("admin.users"), h.createUser)
	g.POST("/users/:id/toggle", mw.RequirePerm("admin.users"), h.toggleUser)

	g.GET("/roles", mw.RequirePerm("admin.roles"), h.rolesPage)
	g.POST("/roles", mw.RequirePerm("admin.roles"), h.createRole)
	g.POST("/roles/:id/delete", mw.RequirePerm("admin.roles"), h.deleteRole)

	g.GET("/audit", mw.RequirePerm("admin.audit"), h.auditPage)
}

func (h *Handler) audit(c *gin.Context, action, target, detail string) {
	u := mw.User(c)
	h.e.Audit.Log(c.Request.Context(), audit.Entry{
		TenantID: mw.TenantID(c), UserID: u.ID, Username: u.Username,
		Action: action, Target: target, Detail: detail, IP: c.ClientIP(),
	})
}

// ---------- 用户 ----------

func (h *Handler) usersPage(c *gin.Context) {
	ctx := c.Request.Context()
	tid := mw.TenantID(c)
	cur, _ := h.users.Find(ctx, bson.M{"tenant_id": tid})
	var users []auth.User
	_ = cur.All(ctx, &users)
	roles, _ := h.e.RBAC.List(ctx, tid)
	roleName := map[string]string{}
	for _, r := range roles {
		roleName[r.ID.Hex()] = r.Name
	}
	web.Render(c, h.e, "admin/users", gin.H{
		"Users": users, "Roles": roles, "RoleName": roleName,
	})
}

func (h *Handler) createUser(c *gin.Context) {
	ctx := c.Request.Context()
	hash, err := auth.HashPassword(c.PostForm("password"))
	if err != nil {
		web.SetFlash(c, "密码加密失败")
		c.Redirect(http.StatusFound, "/admin/users")
		return
	}
	var roleIDs []bson.ObjectID
	for _, id := range c.PostFormArray("role_ids") {
		if oid, err := bson.ObjectIDFromHex(id); err == nil {
			roleIDs = append(roleIDs, oid)
		}
	}
	u := auth.User{
		TenantID:     mw.TenantID(c),
		Username:     c.PostForm("username"),
		Name:         c.PostForm("name"),
		PasswordHash: hash,
		RoleIDs:      roleIDs,
		Status:       "active",
		IsTenantAdm:  c.PostForm("is_admin") == "on",
	}
	var dup bson.M
	if err := h.users.FindOne(ctx, bson.M{"tenant_id": u.TenantID, "username": u.Username}).Decode(&dup); err == nil {
		web.SetFlash(c, "用户名已存在: "+u.Username)
		c.Redirect(http.StatusFound, "/admin/users")
		return
	}
	if _, err := h.users.InsertOne(ctx, &u); err != nil {
		web.SetFlash(c, "创建失败: "+err.Error())
	} else {
		h.audit(c, "user.create", u.Username, "")
		web.SetFlash(c, "用户已创建")
	}
	c.Redirect(http.StatusFound, "/admin/users")
}

func (h *Handler) toggleUser(c *gin.Context) {
	ctx := c.Request.Context()
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	var u auth.User
	if err := h.users.FindOne(ctx, bson.M{"_id": id, "tenant_id": mw.TenantID(c)}).Decode(&u); err != nil {
		c.Redirect(http.StatusFound, "/admin/users")
		return
	}
	status := "active"
	if u.Status == "active" {
		status = "disabled"
	}
	_, _ = h.users.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"status": status}})
	h.audit(c, "user.toggle", u.Username, status)
	c.Redirect(http.StatusFound, "/admin/users")
}

// ---------- 角色 ----------

func (h *Handler) rolesPage(c *gin.Context) {
	ctx := c.Request.Context()
	tid := mw.TenantID(c)
	roles, _ := h.e.RBAC.List(ctx, tid)
	web.Render(c, h.e, "admin/roles", gin.H{
		"Roles": roles, "Perms": rbac.Catalog(),
	})
}

func (h *Handler) createRole(c *gin.Context) {
	r := rbac.Role{
		TenantID:  mw.TenantID(c),
		Name:      c.PostForm("name"),
		PermCodes: c.PostFormArray("perm_codes"),
	}
	if r.Name == "" {
		web.SetFlash(c, "名称必填")
		c.Redirect(http.StatusFound, "/admin/roles")
		return
	}
	if err := h.e.RBAC.Create(c.Request.Context(), &r); err != nil {
		web.SetFlash(c, "创建失败: "+err.Error())
	} else {
		h.audit(c, "role.create", r.Name, r.Name)
		web.SetFlash(c, "角色已创建")
	}
	c.Redirect(http.StatusFound, "/admin/roles")
}

func (h *Handler) deleteRole(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	if err := h.e.RBAC.Delete(c.Request.Context(), mw.TenantID(c), id); err == nil {
		h.audit(c, "role.delete", id.Hex(), "")
	}
	c.Redirect(http.StatusFound, "/admin/roles")
}

// ---------- 审计 ----------

func (h *Handler) auditPage(c *gin.Context) {
	list, _ := h.e.Audit.List(c.Request.Context(), bson.M{"tenant_id": mw.TenantID(c)}, 200)
	web.Render(c, h.e, "admin/audit", gin.H{"Logs": list})
}
