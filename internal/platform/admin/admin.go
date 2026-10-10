// Package admin 租户管理区 handler：员工/权限角色/门诊设置/审计。
package admin

import (
	"net/http"
	"strconv"
	"strings"

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

	g.GET("/settings", mw.RequirePerm("admin.settings"), h.settingsPage)
	g.POST("/settings", mw.RequirePerm("admin.settings"), h.saveSettings)
	g.POST("/settings/gallery/:id/delete", mw.RequirePerm("admin.settings"), h.deleteGalleryPhoto)

	g.GET("/home", mw.RequirePerm("admin.settings"), h.homePage)
	g.POST("/home/profile", mw.RequirePerm("admin.settings"), h.saveHomeProfile)
	g.POST("/home/gallery", mw.RequirePerm("admin.settings"), h.uploadGallery)
	g.POST("/home/gallery/:id/delete", mw.RequirePerm("admin.settings"), h.deleteGalleryPhoto)
	g.POST("/home/staff/:id", mw.RequirePerm("admin.settings"), h.saveHomeStaff)
	g.POST("/home/staff/:id/top", mw.RequirePerm("admin.settings"), h.topHomeStaff)

	g.GET("/audit", mw.RequirePerm("admin.audit"), h.auditPage)
}

func (h *Handler) audit(c *gin.Context, action, target, detail string) {
	mw.Audit(c, h.e, action, target, detail)
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

// scopedRoleIDs 只保留本门诊的角色（防跨租户 role_id 污染入库；权限计算本就按租户过滤）。
func (h *Handler) scopedRoleIDs(c *gin.Context) []bson.ObjectID {
	roles, _ := h.e.RBAC.List(c.Request.Context(), mw.TenantID(c))
	valid := map[bson.ObjectID]bool{}
	for _, r := range roles {
		valid[r.ID] = true
	}
	var out []bson.ObjectID
	for _, id := range roleIDsOf(c) {
		if valid[id] {
			out = append(out, id)
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
		RoleIDs:     h.scopedRoleIDs(c),
		IsTenantAdm: c.PostForm("is_admin") == "on",
		Bio:         c.PostForm("bio"),
	}
	if err := h.e.Auth.Create(c.Request.Context(), mw.TenantID(c), u, c.PostForm("password")); err != nil {
		web.SetFlash(c, "创建失败: "+err.Error())
	} else {
		h.audit(c, audit.ActUserCreate, u.Name+" "+u.Phone, "")
		if msg := h.saveAvatar(c, u.ID, ""); msg != "" {
			web.SetFlash(c, "员工已创建，但"+msg)
		} else {
			web.SetFlash(c, "员工已创建: "+u.Name)
		}
	}
	c.Redirect(http.StatusFound, "/admin/users")
}

// saveAvatar 员工大头照（可选）：无上传返回空串；有上传则存 GridFS 并回写用户，
// 成功时删掉旧图。返回空串=成功或无事可做，否则为错误描述。
func (h *Handler) saveAvatar(c *gin.Context, id bson.ObjectID, oldAvatar string) string {
	fh, err := c.FormFile("avatar")
	if err != nil || fh == nil || fh.Filename == "" {
		return ""
	}
	ctx := c.Request.Context()
	tid := mw.TenantID(c)
	f, err := fh.Open()
	if err != nil {
		return "大头照打开失败"
	}
	defer f.Close()
	a, err := h.e.Attach.Save(ctx, tid, "user", id,
		fh.Filename, fh.Header.Get("Content-Type"), f, mw.User(c).Name)
	if err != nil {
		return "大头照上传失败: " + err.Error()
	}
	// 先落库再删旧图：落库失败用户仍指向旧图（可用），只多一个孤儿文件；
	// 反过来会先删旧图再失败，用户头像直接断链
	if err := h.e.Auth.Update(ctx, tid, id, bson.M{"avatar": a.ID.Hex()}); err != nil {
		return "大头照保存失败: " + err.Error()
	}
	if oldAvatar != "" {
		if oldID, err := bson.ObjectIDFromHex(oldAvatar); err == nil {
			_ = h.e.Attach.Delete(ctx, tid, oldID)
		}
	}
	return ""
}

func (h *Handler) updateUser(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	status := c.PostForm("status")
	if status != "active" && status != "disabled" {
		status = "active"
	}
	oldAvatar := ""
	if u, err := h.e.Auth.UserByID(c.Request.Context(), mw.TenantID(c), id); err == nil {
		oldAvatar = u.Avatar
	}
	isAdm := c.PostForm("is_admin") == "on"
	// 护栏：不能取消自己的管理员身份，也不能取消最后一个管理员
	if me := mw.User(c); me != nil && id == me.ID && !isAdm {
		web.SetFlash(c, "不能取消自己的管理员身份")
		c.Redirect(http.StatusFound, "/admin/users")
		return
	}
	if !isAdm {
		if n, _ := h.e.Auth.CountAdmins(c.Request.Context(), mw.TenantID(c)); n <= 1 {
			web.SetFlash(c, "至少保留一位门诊管理员")
			c.Redirect(http.StatusFound, "/admin/users")
			return
		}
	}
	set := bson.M{
		"name":            c.PostForm("name"),
		"phone":           c.PostForm("phone"),
		"bio":             c.PostForm("bio"),
		"role_ids":        h.scopedRoleIDs(c),
		"status":          status,
		"is_tenant_admin": isAdm,
	}
	if err := h.e.Auth.Update(c.Request.Context(), mw.TenantID(c), id, set); err != nil {
		web.SetFlash(c, "保存失败: "+err.Error())
	} else if msg := h.saveAvatar(c, id, oldAvatar); msg != "" {
		web.SetFlash(c, "员工已保存，但"+msg)
	} else {
		h.audit(c, audit.ActUserUpdate, c.PostForm("name"), "")
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
		h.audit(c, audit.ActUserResetPwd, u.Name+" "+u.Phone, "")
		web.SetFlash(c, "已重置 "+u.Name+" 的密码")
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
		h.audit(c, audit.ActRoleCreate, r.Name, r.Name)
		web.SetFlash(c, "角色已创建")
	}
	c.Redirect(http.StatusFound, "/admin/roles")
}

// ---------- 门诊设置 ----------

func (h *Handler) settingsPage(c *gin.Context) {
	t := mw.Tenant(c)
	mins, cap := t.SlotConfig()
	web.Render(c, h.e, "admin/settings", gin.H{"Tenant": t, "SlotMinutes": mins, "SlotCapacity": cap})
}

func (h *Handler) saveSettings(c *gin.Context) {
	slotMinutes, _ := strconv.Atoi(c.PostForm("slot_minutes"))
	slotCapacity, _ := strconv.Atoi(c.PostForm("slot_capacity"))
	ctx := c.Request.Context()
	if err := h.e.Tenants.SetSchedule(ctx, mw.TenantID(c), slotMinutes, slotCapacity); err != nil {
		web.SetFlash(c, "保存失败: "+err.Error())
	} else {
		h.audit(c, audit.ActTenantSettings, "schedule",
			c.PostForm("slot_minutes")+"min x"+c.PostForm("slot_capacity"))
		web.SetFlash(c, "门诊设置已保存")
	}
	c.Redirect(http.StatusFound, "/admin/settings")
}

// ---------- 首页配置（患者端首页所有内容统一在这里配） ----------

func (h *Handler) homePage(c *gin.Context) {
	ctx := c.Request.Context()
	tid := mw.TenantID(c)
	users, _ := h.e.Auth.List(ctx, tid)
	roles, _ := h.e.RBAC.List(ctx, tid)
	// 与患者端首页同序（比较器见 auth.SortByWeight），改完立即所见即所得。
	auth.SortUsersForHome(users)
	web.Render(c, h.e, "admin/home", gin.H{
		"Tenant": mw.Tenant(c), "Users": users, "Roles": roles,
	})
}

// saveHomeProfile 诊所介绍信息（介绍/公告/地址/电话/营业时间）。
func (h *Handler) saveHomeProfile(c *gin.Context) {
	ctx := c.Request.Context()
	tid := mw.TenantID(c)
	if err := h.e.Tenants.SetProfile(ctx, tid,
		c.PostForm("intro"), c.PostForm("address"), c.PostForm("phone"),
		c.PostForm("hours"), c.PostForm("notice")); err != nil {
		web.SetFlash(c, "保存失败: "+err.Error())
	} else {
		h.audit(c, audit.ActTenantHome, "profile", "")
		web.SetFlash(c, "诊所介绍已保存")
	}
	c.Redirect(http.StatusFound, "/admin/home")
}

// uploadGallery 门诊图多张批量上传。
func (h *Handler) uploadGallery(c *gin.Context) {
	ctx := c.Request.Context()
	tid := mw.TenantID(c)
	coverErr := ""
	n := 0
	if mf, err := c.MultipartForm(); err == nil && mf != nil {
		for _, fh := range mf.File["photos"] {
			if fh == nil || fh.Filename == "" {
				continue
			}
			func() {
				f, err := fh.Open()
				if err != nil {
					coverErr = "图片打开失败"
					return
				}
				defer f.Close()
				a, err := h.e.Attach.Save(ctx, tid, "tenant", tid,
					fh.Filename, fh.Header.Get("Content-Type"), f, mw.User(c).Name)
				if err != nil {
					coverErr = "图片上传失败: " + err.Error()
					return
				}
				if err := h.e.Tenants.AddGalleryPhoto(ctx, tid, a.ID.Hex()); err != nil {
					coverErr = "保存失败: " + err.Error()
					return
				}
				n++
			}()
			if coverErr != "" {
				break
			}
		}
	}
	if coverErr != "" {
		web.SetFlash(c, coverErr)
	} else if n == 0 {
		web.SetFlash(c, "请选择图片")
	} else {
		h.audit(c, audit.ActTenantHome, "gallery.add", strconv.Itoa(n))
		web.SetFlash(c, "门诊图已上传")
	}
	c.Redirect(http.StatusFound, "/admin/home")
}

// saveHomeStaff 首页人员配置：是否展示/排序（简介在员工页维护，这里只展示）。
func (h *Handler) saveHomeStaff(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	order, _ := strconv.Atoi(c.PostForm("home_order"))
	set := bson.M{
		"hide_home":  c.PostForm("hide_home") == "on",
		"home_order": order,
	}
	if err := h.e.Auth.Update(c.Request.Context(), mw.TenantID(c), id, set); err != nil {
		web.SetFlash(c, "保存失败: "+err.Error())
	} else {
		h.audit(c, audit.ActTenantHome, "staff", id.Hex())
		web.SetFlash(c, "人员展示已保存")
	}
	c.Redirect(http.StatusFound, "/admin/home")
}

// topHomeStaff 置顶：权重设为全门诊最大 +1（越大越前）。
func (h *Handler) topHomeStaff(c *gin.Context) {
	ctx := c.Request.Context()
	tid := mw.TenantID(c)
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	users, _ := h.e.Auth.List(ctx, tid)
	max := 0
	for _, u := range users {
		if u.HomeOrder > max {
			max = u.HomeOrder
		}
	}
	if err := h.e.Auth.Update(ctx, tid, id, bson.M{"home_order": max + 1}); err != nil {
		web.SetFlash(c, "置顶失败: "+err.Error())
	} else {
		h.audit(c, audit.ActTenantHome, "staff.top", id.Hex())
		web.SetFlash(c, "已置顶")
	}
	c.Redirect(http.StatusFound, "/admin/home")
}

// deleteGalleryPhoto 门诊图库删除一张（库记录 + GridFS 文件一起删）。
func (h *Handler) deleteGalleryPhoto(c *gin.Context) {
	ctx := c.Request.Context()
	tid := mw.TenantID(c)
	fid := c.Param("id")
	if oid, err := bson.ObjectIDFromHex(fid); err == nil {
		if err := h.e.Attach.Delete(ctx, tid, oid); err != nil {
			web.SetFlash(c, "删除图片失败: "+err.Error())
			c.Redirect(http.StatusFound, "/admin/settings")
			return
		}
	}
	if err := h.e.Tenants.RemoveGalleryPhoto(ctx, tid, fid); err != nil {
		web.SetFlash(c, "删除失败: "+err.Error())
	} else {
		web.SetFlash(c, "门诊图已删除")
	}
	// 两个入口复用：首页配置来的回首页，门诊设置来的回设置
	back := "/admin/settings"
	if strings.Contains(c.Request.URL.Path, "/home/gallery/") {
		back = "/admin/home"
	}
	c.Redirect(http.StatusFound, back)
}

// ---------- 审计 ----------

func (h *Handler) auditPage(c *gin.Context) {
	skip, limit, pager := web.ParsePager(c, 50)
	filter := bson.M{"tenant_id": mw.TenantID(c)}
	list, _ := h.e.Audit.List(c.Request.Context(), filter, skip, limit)
	total, _ := h.e.Audit.Count(c.Request.Context(), filter)
	pager.Total = total
	web.Render(c, h.e, "admin/audit", gin.H{"Logs": list, "Pager": pager})
}
