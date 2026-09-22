// Package admin 租户管理区 handler：用户/角色/部门/字典/插件/审计。
package admin

import (
	"net/http"
	"time"

	"erp/internal/platform/audit"
	"erp/internal/platform/auth"
	"erp/internal/platform/dict"
	"erp/internal/platform/env"
	mw "erp/internal/platform/middleware"
	"erp/internal/platform/org"
	"erp/internal/platform/plugin"
	"erp/internal/platform/rbac"
	"erp/internal/platform/web"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// Perms 平台管理区权限码目录。
var Perms = []rbac.PermissionDef{
	{Code: "admin.users", Desc: "用户管理"},
	{Code: "admin.roles", Desc: "角色管理"},
	{Code: "admin.depts", Desc: "部门管理"},
	{Code: "admin.dicts", Desc: "字典管理"},
	{Code: "admin.plugins", Desc: "插件启用"},
	{Code: "admin.audit", Desc: "审计查看"},
}

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

	g.GET("/depts", mw.RequirePerm("admin.depts"), h.deptsPage)
	g.POST("/depts", mw.RequirePerm("admin.depts"), h.createDept)
	g.POST("/depts/:id/delete", mw.RequirePerm("admin.depts"), h.deleteDept)

	g.GET("/dicts", mw.RequirePerm("admin.dicts"), h.dictsPage)
	g.POST("/dicts", mw.RequirePerm("admin.dicts"), h.createDict)
	g.POST("/dicts/:id/delete", mw.RequirePerm("admin.dicts"), h.deleteDict)

	g.GET("/plugins", mw.RequirePerm("admin.plugins"), h.pluginsPage)
	g.POST("/plugins/:id/enable", mw.RequirePerm("admin.plugins"), h.enablePlugin)
	g.POST("/plugins/:id/disable", mw.RequirePerm("admin.plugins"), h.disablePlugin)

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
	enabled := h.e.Gate.EnabledSet(ctx, tid)
	web.Render(c, h.e, "admin/roles", gin.H{
		"Roles": roles,
		"Perms": append(append([]rbac.PermissionDef{}, Perms...), plugin.PermissionsOf(enabled)...),
	})
}

func (h *Handler) createRole(c *gin.Context) {
	r := rbac.Role{
		TenantID:  mw.TenantID(c),
		Code:      c.PostForm("code"),
		Name:      c.PostForm("name"),
		PermCodes: c.PostFormArray("perm_codes"),
		DataScope: c.PostForm("data_scope"),
	}
	if r.DataScope == "" {
		r.DataScope = "self"
	}
	if err := h.e.RBAC.Create(c.Request.Context(), &r); err != nil {
		web.SetFlash(c, "创建失败: "+err.Error())
	} else {
		h.audit(c, "role.create", r.Code, r.Name)
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

// ---------- 部门 ----------

func (h *Handler) deptsPage(c *gin.Context) {
	list, _ := h.e.Org.List(c.Request.Context(), mw.TenantID(c))
	web.Render(c, h.e, "admin/depts", gin.H{"Depts": list})
}

func (h *Handler) createDept(c *gin.Context) {
	d := org.Department{Name: c.PostForm("name")}
	d.TenantID = mw.TenantID(c)
	d.CreatedAt = time.Now()
	if pid, err := bson.ObjectIDFromHex(c.PostForm("parent_id")); err == nil {
		d.ParentID = pid
	}
	if err := h.e.Org.Create(c.Request.Context(), &d); err != nil {
		web.SetFlash(c, "创建失败: "+err.Error())
	} else {
		web.SetFlash(c, "部门已创建")
	}
	c.Redirect(http.StatusFound, "/admin/depts")
}

func (h *Handler) deleteDept(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	_ = h.e.Org.Delete(c.Request.Context(), mw.TenantID(c), id)
	c.Redirect(http.StatusFound, "/admin/depts")
}

// ---------- 字典 ----------

func (h *Handler) dictsPage(c *gin.Context) {
	list, _ := h.e.Dict.List(c.Request.Context(), mw.TenantID(c), c.Query("type"))
	web.Render(c, h.e, "admin/dicts", gin.H{"Dicts": list, "Type": c.Query("type")})
}

func (h *Handler) createDict(c *gin.Context) {
	e := dict.Entry{
		Type: c.PostForm("type"), Code: c.PostForm("code"),
		Label: c.PostForm("label"), Status: "active",
	}
	e.TenantID = mw.TenantID(c)
	e.CreatedAt = time.Now()
	if err := h.e.Dict.Create(c.Request.Context(), &e); err != nil {
		web.SetFlash(c, "创建失败: "+err.Error())
	} else {
		web.SetFlash(c, "字典项已创建")
	}
	c.Redirect(http.StatusFound, "/admin/dicts?type="+e.Type)
}

func (h *Handler) deleteDict(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	_ = h.e.Dict.Delete(c.Request.Context(), mw.TenantID(c), id)
	c.Redirect(http.StatusFound, "/admin/dicts")
}

// ---------- 插件 ----------

func (h *Handler) pluginsPage(c *gin.Context) {
	list, _ := h.e.Gate.ListWithStatus(c.Request.Context(), mw.TenantID(c))
	web.Render(c, h.e, "admin/plugins", gin.H{"Plugins": list})
}

func (h *Handler) enablePlugin(c *gin.Context) {
	id := c.Param("id")
	if err := h.e.Gate.Enable(c.Request.Context(), h.e, mw.TenantID(c), id); err != nil {
		web.SetFlash(c, "启用失败: "+err.Error())
	} else {
		h.audit(c, "plugin.enable", id, "")
		web.SetFlash(c, "插件已启用")
	}
	c.Redirect(http.StatusFound, "/admin/plugins")
}

func (h *Handler) disablePlugin(c *gin.Context) {
	id := c.Param("id")
	if err := h.e.Gate.Disable(c.Request.Context(), h.e, mw.TenantID(c), id); err != nil {
		web.SetFlash(c, "禁用失败: "+err.Error())
	} else {
		h.audit(c, "plugin.disable", id, "")
		web.SetFlash(c, "插件已禁用")
	}
	c.Redirect(http.StatusFound, "/admin/plugins")
}

// ---------- 审计 ----------

func (h *Handler) auditPage(c *gin.Context) {
	list, _ := h.e.Audit.List(c.Request.Context(), bson.M{"tenant_id": mw.TenantID(c)}, 200)
	web.Render(c, h.e, "admin/audit", gin.H{"Logs": list})
}
