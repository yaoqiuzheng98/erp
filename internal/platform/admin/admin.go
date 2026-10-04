// Package admin 租户管理区 handler：员工/权限角色/门诊设置/审计。
package admin

import (
	"net/http"
	"strconv"

	"erp/internal/platform/audit"
	"erp/internal/platform/auth"
	"erp/internal/platform/env"
	mw "erp/internal/platform/middleware"
	"erp/internal/platform/rbac"
	"erp/internal/platform/web"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type Handler struct {
	e *env.Env
}

func New(e *env.Env) *Handler {
	return &Handler{e: e}
}

func (h *Handler) Register(g *gin.RouterGroup) {
	g.GET("/users", mw.RequirePerm("admin.users"), h.usersPage)
	g.POST("/users", mw.RequirePerm("admin.users"), h.createUser)
	g.POST("/users/:id", mw.RequirePerm("admin.users"), h.updateUser)
	g.POST("/users/:id/password", mw.RequirePerm("admin.users"), h.resetPassword)

	g.GET("/roles", mw.RequirePerm("admin.roles"), h.rolesPage)
	g.POST("/roles", mw.RequirePerm("admin.roles"), h.createRole)
	g.POST("/roles/:id/delete", mw.RequirePerm("admin.roles"), h.deleteRole)

	g.GET("/settings", mw.RequirePerm("admin.settings"), h.settingsPage)
	g.POST("/settings", mw.RequirePerm("admin.settings"), h.saveSettings)

	g.GET("/audit", mw.RequirePerm("admin.audit"), h.auditPage)
}

func (h *Handler) audit(c *gin.Context, action, target, detail string) {
	u := mw.User(c)
	h.e.Audit.Log(c.Request.Context(), audit.Entry{
		TenantID: mw.TenantID(c), UserID: u.ID, Username: u.Phone,
		Action: action, Target: target, Detail: detail, IP: c.ClientIP(),
	})
}

func roleIDsOf(c *gin.Context) []bson.ObjectID {
	var out []bson.ObjectID
	for _, id := range c.PostFormArray("role_ids") {
		if oid, err := bson.ObjectIDFromHex(id); err == nil {
			out = append(out, oid)
		}
	}
	return out
}

// ---------- 员工 ----------

func (h *Handler) usersPage(c *gin.Context) {
	ctx := c.Request.Context()
	tid := mw.TenantID(c)
	users, _ := h.e.Auth.List(ctx, tid)
	roles, _ := h.e.RBAC.List(ctx, tid)
	web.Render(c, h.e, "admin/users", gin.H{"Users": users, "Roles": roles})
}

func (h *Handler) createUser(c *gin.Context) {
	u := &auth.User{
		Name:        c.PostForm("name"),
		Phone:       c.PostForm("phone"),
		RoleIDs:     roleIDsOf(c),
		CanPractice: c.PostForm("can_practice") == "on",
		IsTenantAdm: c.PostForm("is_admin") == "on",
	}
	if err := h.e.Auth.Create(c.Request.Context(), mw.TenantID(c), u, c.PostForm("password")); err != nil {
		web.SetFlash(c, "创建失败: "+err.Error())
	} else {
		h.audit(c, "user.create", u.Name+" "+u.Phone, "")
		web.SetFlash(c, "员工已创建: " + u.Name)
	}
	c.Redirect(http.StatusFound, "/admin/users")
}

func (h *Handler) updateUser(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	status := c.PostForm("status")
	if status != "active" && status != "disabled" {
		status = "active"
	}
	set := bson.M{
		"name":         c.PostForm("name"),
		"phone":        c.PostForm("phone"),
		"role_ids":     roleIDsOf(c),
		"can_practice": c.PostForm("can_practice") == "on",
		"status":       status,
	}
	if err := h.e.Auth.Update(c.Request.Context(), mw.TenantID(c), id, set); err != nil {
		web.SetFlash(c, "保存失败: "+err.Error())
	} else {
		h.audit(c, "user.update", c.PostForm("name"), "")
		web.SetFlash(c, "员工已保存")
	}
	c.Redirect(http.StatusFound, "/admin/users")
}

func (h *Handler) resetPassword(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	ctx := c.Request.Context()
	u, err := h.e.Auth.UserByID(ctx, mw.TenantID(c), id)
	if err != nil {
		web.SetFlash(c, "员工不存在")
		c.Redirect(http.StatusFound, "/admin/users")
		return
	}
	if err := h.e.Auth.SetPassword(ctx, mw.TenantID(c), id, c.PostForm("password")); err != nil {
		web.SetFlash(c, "重置失败: "+err.Error())
	} else {
		h.audit(c, "user.reset_password", u.Name+" "+u.Phone, "")
		web.SetFlash(c, "已重置 " + u.Name + " 的密码")
	}
	c.Redirect(http.StatusFound, "/admin/users")
}

// ---------- 权限角色 ----------

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
	ctx := c.Request.Context()
	tid := mw.TenantID(c)
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	n, _ := h.e.Auth.CountByRole(ctx, tid, id)
	if n > 0 {
		web.SetFlash(c, "仍有员工使用该角色，请先调整员工")
		c.Redirect(http.StatusFound, "/admin/roles")
		return
	}
	if err := h.e.RBAC.Delete(ctx, tid, id); err == nil {
		h.audit(c, "role.delete", id.Hex(), "")
		web.SetFlash(c, "角色已删除")
	} else {
		web.SetFlash(c, "删除失败: "+err.Error())
	}
	c.Redirect(http.StatusFound, "/admin/roles")
}

// ---------- 门诊设置 ----------

func (h *Handler) settingsPage(c *gin.Context) {
	t := mw.Tenant(c)
	web.Render(c, h.e, "admin/settings", gin.H{"Tenant": t})
}

func (h *Handler) saveSettings(c *gin.Context) {
	ctx := c.Request.Context()
	tid := mw.TenantID(c)
	oldCover := ""
	if t := mw.Tenant(c); t != nil {
		oldCover = t.CoverID
	}
	fee, _ := strconv.ParseFloat(c.PostForm("reg_fee"), 64)
	if err := h.e.Tenants.SetFee(ctx, tid, fee); err != nil {
		web.SetFlash(c, "保存失败: "+err.Error())
		c.Redirect(http.StatusFound, "/admin/settings")
		return
	}
	if err := h.e.Tenants.SetProfile(ctx, tid,
		c.PostForm("intro"), c.PostForm("address"), c.PostForm("phone"),
		c.PostForm("hours"), c.PostForm("notice")); err != nil {
		web.SetFlash(c, "保存失败: "+err.Error())
		c.Redirect(http.StatusFound, "/admin/settings")
		return
	}
	// 封面图（可选）：有上传才替换，旧图删掉防孤儿
	coverErr := ""
	if fh, err := c.FormFile("cover"); err == nil && fh != nil && fh.Filename != "" {
		if f, err := fh.Open(); err == nil {
			func() {
				defer f.Close()
				a, err := h.e.Attach.Save(ctx, tid, "tenant", tid,
					fh.Filename, fh.Header.Get("Content-Type"), f, mw.User(c).Name)
				if err != nil {
					coverErr = "封面上传失败: " + err.Error()
					return
				}
				if oldCover != "" {
					if oldID, err := bson.ObjectIDFromHex(oldCover); err == nil {
						_ = h.e.Attach.Delete(ctx, tid, oldID)
					}
				}
				if err := h.e.Tenants.SetCover(ctx, tid, a.ID.Hex()); err != nil {
					coverErr = "保存失败: " + err.Error()
					return
				}
			}()
		} else {
			coverErr = "封面打开失败"
		}
	}
	if coverErr != "" {
		web.SetFlash(c, coverErr)
	} else {
		h.audit(c, "tenant.settings", "reg_fee+profile", c.PostForm("reg_fee"))
		web.SetFlash(c, "门诊设置已保存")
	}
	c.Redirect(http.StatusFound, "/admin/settings")
}

// ---------- 审计 ----------

func (h *Handler) auditPage(c *gin.Context) {
	list, _ := h.e.Audit.List(c.Request.Context(), bson.M{"tenant_id": mw.TenantID(c)}, 200)
	web.Render(c, h.e, "admin/audit", gin.H{"Logs": list})
}
