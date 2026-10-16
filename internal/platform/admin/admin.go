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
	"erp/internal/platform/model"
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

	g.GET("/home", mw.RequirePerm("admin.settings"), h.homePage)
	g.POST("/home/profile", mw.RequirePerm("admin.settings"), h.saveHomeProfile)
	g.POST("/home/image/:kind", mw.RequirePerm("admin.settings"), h.uploadHomeImage)
	g.POST("/home/image/:kind/delete", mw.RequirePerm("admin.settings"), h.deleteHomeImage)
	g.POST("/home/staff", mw.RequirePerm("admin.settings"), h.saveHomeStaff)

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
	years, _ := strconv.Atoi(c.PostForm("years"))
	u := &auth.User{
		Name:    c.PostForm("name"),
		Phone:   c.PostForm("phone"),
		RoleIDs: h.scopedRoleIDs(c),
		Bio:     c.PostForm("bio"),
		Title:   strings.TrimSpace(c.PostForm("title")),
		Years:   years,
		Treat:   c.PostForm("treat") != "",
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
	years, _ := strconv.Atoi(c.PostForm("years"))
	set := bson.M{
		"name":     c.PostForm("name"),
		"phone":    c.PostForm("phone"),
		"bio":      c.PostForm("bio"),
		"title":    strings.TrimSpace(c.PostForm("title")),
		"years":    years,
		"role_ids": h.scopedRoleIDs(c),
		"status":   status,
		"treat":    c.PostForm("treat") != "",
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
	segs := t.BusinessHours()
	var am, pm model.HourSeg
	if len(segs) > 0 {
		am = segs[0]
	}
	if len(segs) > 1 {
		pm = segs[1]
	}
	// 每周休息日勾选项（0=周日..6=周六，展示按周一到周日排）。
	names := [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}
	restOn := map[int]bool{}
	for _, d := range t.RestWeekdays {
		restOn[d] = true
	}
	weekdays := make([]gin.H, 0, 7)
	for _, d := range []int{1, 2, 3, 4, 5, 6, 0} {
		weekdays = append(weekdays, gin.H{"Value": d, "Label": names[d], "On": restOn[d]})
	}
	ssegs := t.SlotSegs
	if len(ssegs) == 0 && (t.SlotFrom != "" || t.SlotTo != "") {
		ssegs = []model.HourSeg{{Open: t.SlotFrom}}
	}
	var sam, spm model.HourSeg
	if len(ssegs) > 0 {
		sam = ssegs[0]
	}
	if len(ssegs) > 1 {
		spm = ssegs[1]
	}
	web.Render(c, h.e, "admin/settings", gin.H{
		"Tenant": t, "SlotMinutes": mins, "SlotCapacity": cap,
		"SlotAMOpen": sam.Open, "SlotAMClose": sam.Close, "SlotPMOpen": spm.Open, "SlotPMClose": spm.Close,
		"AMOpen": am.Open, "AMClose": am.Close, "PMOpen": pm.Open, "PMClose": pm.Close,
		"Weekdays": weekdays, "RestDates": strings.Join(t.RestDates, "\n"),
	})
}

func (h *Handler) saveSettings(c *gin.Context) {
	slotMinutes, _ := strconv.Atoi(c.PostForm("slot_minutes"))
	// 每档人数已废弃（现为每医生每档 1 单）：表单不再提交该字段，保留旧值语义=1。
	slotCapacity := 1
	ctx := c.Request.Context()
	tid := mw.TenantID(c)
	if err := h.e.Tenants.SetSchedule(ctx, tid, slotMinutes, slotCapacity); err != nil {
		web.SetFlash(c, "保存失败: "+err.Error())
		c.Redirect(http.StatusFound, "/admin/settings")
		return
	}
	segs := []model.HourSeg{
		{Open: c.PostForm("biz_am_open"), Close: c.PostForm("biz_am_close")},
		{Open: c.PostForm("biz_pm_open"), Close: c.PostForm("biz_pm_close")},
	}
	if err := h.e.Tenants.SetBusinessHours(ctx, tid, segs); err != nil {
		web.SetFlash(c, "放号已保存，营业时间保存失败: "+err.Error())
		c.Redirect(http.StatusFound, "/admin/settings")
		return
	}
	if err := h.e.Tenants.SetSlotSegs(ctx, tid, []model.HourSeg{
		{Open: c.PostForm("slot_am_open"), Close: c.PostForm("slot_am_close")},
		{Open: c.PostForm("slot_pm_open"), Close: c.PostForm("slot_pm_close")},
	}); err != nil {
		web.SetFlash(c, "放号/营业时间已保存，放号时间范围保存失败: "+err.Error())
		c.Redirect(http.StatusFound, "/admin/settings")
		return
	}
	var wds []int
	for _, s := range c.PostFormArray("rest_weekdays") {
		if n, err := strconv.Atoi(s); err == nil {
			wds = append(wds, n)
		}
	}
	if err := h.e.Tenants.SetRest(ctx, tid, wds, strings.Split(c.PostForm("rest_dates"), "\n")); err != nil {
		web.SetFlash(c, "放号/营业时间已保存，休息日保存失败: "+err.Error())
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

// saveHomeProfile 诊所地址/联系方式 + 首页公告（患者端首页展示）。
func (h *Handler) saveHomeProfile(c *gin.Context) {
	ctx := c.Request.Context()
	tid := mw.TenantID(c)
	if err := h.e.Tenants.SetProfile(ctx, tid, c.PostForm("address"), c.PostForm("contact")); err != nil {
		web.SetFlash(c, "保存失败: "+err.Error())
		c.Redirect(http.StatusFound, "/admin/home")
		return
	}
	if err := h.e.Tenants.SetNotice(ctx, tid,
		c.PostForm("notice"), c.PostForm("notice_from"), c.PostForm("notice_to")); err != nil {
		web.SetFlash(c, "地址/联系方式已保存，公告保存失败: "+err.Error())
	} else {
		h.audit(c, audit.ActTenantHome, "profile", "")
		web.SetFlash(c, "诊所介绍已保存")
	}
	c.Redirect(http.StatusFound, "/admin/home")
}

// uploadHomeImage 首页图位上传（kind=hero 顶部大图 / clinic 门诊卡图）：单张，换图即删旧图。
func (h *Handler) uploadHomeImage(c *gin.Context) {
	ctx := c.Request.Context()
	tid := mw.TenantID(c)
	kind := c.Param("kind")
	fh, err := c.FormFile("file")
	if err != nil || fh == nil || fh.Filename == "" {
		web.SetFlash(c, "请选择图片")
		c.Redirect(http.StatusFound, "/admin/home")
		return
	}
	f, err := fh.Open()
	if err != nil {
		web.SetFlash(c, "图片打开失败")
		c.Redirect(http.StatusFound, "/admin/home")
		return
	}
	defer f.Close()
	a, err := h.e.Attach.Save(ctx, tid, "tenant", tid, fh.Filename, fh.Header.Get("Content-Type"), f, mw.User(c).Name)
	if err != nil {
		web.SetFlash(c, "上传失败: "+err.Error())
		c.Redirect(http.StatusFound, "/admin/home")
		return
	}
	t, _ := h.e.Tenants.ByID(ctx, tid)
	old := ""
	if t != nil {
		if kind == "hero" {
			old = t.HeroImage
		} else if kind == "clinic" {
			old = t.ClinicImage
		}
	}
	if err := h.e.Tenants.SetHomeImage(ctx, tid, kind, a.ID.Hex()); err != nil {
		web.SetFlash(c, "保存失败: "+err.Error())
		c.Redirect(http.StatusFound, "/admin/home")
		return
	}
	if old != "" {
		if oldID, err := bson.ObjectIDFromHex(old); err == nil {
			_ = h.e.Attach.Delete(ctx, tid, oldID)
		}
	}
	h.audit(c, audit.ActTenantHome, "image."+kind, a.ID.Hex())
	web.SetFlash(c, "图片已更新")
	c.Redirect(http.StatusFound, "/admin/home")
}

// deleteHomeImage 删除首页图位。
func (h *Handler) deleteHomeImage(c *gin.Context) {
	ctx := c.Request.Context()
	tid := mw.TenantID(c)
	kind := c.Param("kind")
	t, err := h.e.Tenants.ByID(ctx, tid)
	if err != nil {
		web.SetFlash(c, "门诊不存在")
		c.Redirect(http.StatusFound, "/admin/home")
		return
	}
	cur := ""
	if kind == "hero" {
		cur = t.HeroImage
	} else if kind == "clinic" {
		cur = t.ClinicImage
	}
	if cur != "" {
		if oid, err := bson.ObjectIDFromHex(cur); err == nil {
			_ = h.e.Attach.Delete(ctx, tid, oid)
		}
	}
	if err := h.e.Tenants.ClearHomeImage(ctx, tid, kind); err != nil {
		web.SetFlash(c, "删除失败: "+err.Error())
		c.Redirect(http.StatusFound, "/admin/home")
		return
	}
	h.audit(c, audit.ActTenantHome, "image."+kind+".delete", cur)
	web.SetFlash(c, "图片已删除")
	c.Redirect(http.StatusFound, "/admin/home")
}

// saveHomeStaff 首页人员批量配置：一次保存所有成员的展示/排序。
// 字段命名 order_<hex>/hide_<hex>，按当前员工名单逐个读表（没在表单里的忽略）。
func (h *Handler) saveHomeStaff(c *gin.Context) {
	ctx := c.Request.Context()
	tid := mw.TenantID(c)
	users, err := h.e.Auth.List(ctx, tid)
	if err != nil {
		web.SetFlash(c, "保存失败: "+err.Error())
		c.Redirect(http.StatusFound, "/admin/home")
		return
	}
	n := 0
	for _, u := range users {
		hex := u.ID.Hex()
		ordStr, hasOrd := c.GetPostForm("order_" + hex)
		hideStr, hasHide := c.GetPostForm("hide_" + hex)
		if !hasOrd && !hasHide {
			continue
		}
		order, _ := strconv.Atoi(strings.TrimSpace(ordStr))
		if err := h.e.Auth.Update(ctx, tid, u.ID, bson.M{
			"home_order": order,
			"hide_home":  hideStr == "1",
		}); err != nil {
			web.SetFlash(c, "保存失败("+u.Name+"): "+err.Error())
			c.Redirect(http.StatusFound, "/admin/home")
			return
		}
		n++
	}
	h.audit(c, audit.ActTenantHome, "staff-batch", strconv.Itoa(n))
	web.SetFlash(c, "团队展示已保存")
	c.Redirect(http.StatusFound, "/admin/home")
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
