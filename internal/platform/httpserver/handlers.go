package httpserver

import (
	"net/http"
	"net/url"

	"erp/internal/platform/attach"
	"erp/internal/platform/audit"
	"erp/internal/platform/env"
	mw "erp/internal/platform/middleware"
	"erp/internal/platform/session"
	"erp/internal/platform/web"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func loginTenant(e *env.Env) gin.HandlerFunc {
	return func(c *gin.Context) {
		u, err := e.Auth.LoginTenant(c.Request.Context(),
			c.PostForm("tenant"), c.PostForm("phone"), c.PostForm("password"))
		if err != nil {
			web.Render(c, e, "login", gin.H{"Err": "门诊名、手机号或密码错误"})
			return
		}
		s, err := e.Sessions.Create(c.Request.Context(), session.KindTenant, u.ID, u.TenantID)
		if err != nil {
			web.Render(c, e, "login", gin.H{"Err": "会话创建失败"})
			return
		}
		c.SetCookie(e.Cfg.Session.CookieName, s.ID, int(e.Cfg.Session.TTLHours)*3600, "/", "", e.Cfg.Session.Secure, true)
		e.Audit.Log(c.Request.Context(), audit.Entry{
			TenantID: u.TenantID, UserID: u.ID, Username: u.Phone,
			Action: audit.ActLogin, IP: c.ClientIP(),
		})
		c.Redirect(http.StatusFound, "/app/appointments")
	}
}

func loginSys(e *env.Env) gin.HandlerFunc {
	return func(c *gin.Context) {
		a, err := e.Auth.LoginSys(c.Request.Context(),
			c.PostForm("username"), c.PostForm("password"))
		if err != nil {
			web.Render(c, e, "sys/login", gin.H{"Err": "用户名或密码错误"})
			return
		}
		s, err := e.Sessions.Create(c.Request.Context(), session.KindSys, a.ID, bson.NilObjectID)
		if err != nil {
			web.Render(c, e, "sys/login", gin.H{"Err": "会话创建失败"})
			return
		}
		c.SetCookie(e.Cfg.Session.SysCookieName, s.ID, int(e.Cfg.Session.TTLHours)*3600, "/", "", e.Cfg.Session.Secure, true)
		e.Audit.Log(c.Request.Context(), audit.Entry{
			UserID: a.ID, Username: a.Username, Action: audit.ActSysLogin, IP: c.ClientIP(),
		})
		c.Redirect(http.StatusFound, "/sysadmin")
	}
}

func logout(e *env.Env, cookieName string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if token, err := c.Cookie(cookieName); err == nil {
			e.Sessions.Destroy(c.Request.Context(), token)
		}
		c.SetCookie(cookieName, "", -1, "/", "", e.Cfg.Session.Secure, true)
		if cookieName == e.Cfg.Session.SysCookieName {
			c.Redirect(http.StatusFound, "/sysadmin/login")
		} else {
			c.Redirect(http.StatusFound, "/login")
		}
	}
}

func notifications(e *env.Env) gin.HandlerFunc {
	return func(c *gin.Context) {
		t, u := mw.Tenant(c), mw.User(c)
		skip, limit, pager := web.ParsePager(c, 20)
		list, _ := e.Notify.List(c.Request.Context(), t.ID, u.ID, skip, limit)
		total, _ := e.Notify.Count(c.Request.Context(), t.ID, u.ID)
		pager.Total = total
		web.Render(c, e, "notifications", gin.H{"List": list, "Pager": pager})
	}
}

func notificationRead(e *env.Env) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, _ := bson.ObjectIDFromHex(c.Param("id"))
		e.Notify.MarkRead(c.Request.Context(), mw.TenantID(c), id)
		c.Redirect(http.StatusFound, "/app/notifications")
	}
}

func attachUpload(e *env.Env) gin.HandlerFunc {
	return func(c *gin.Context) {
		fh, err := c.FormFile("file")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing file"})
			return
		}
		ownerID, _ := bson.ObjectIDFromHex(c.PostForm("owner_id"))
		f, err := fh.Open()
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		defer f.Close()
		a, err := e.Attach.Save(c.Request.Context(), mw.TenantID(c),
			c.PostForm("owner_type"), ownerID, fh.Filename, fh.Header.Get("Content-Type"), f,
			mw.User(c).Name)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"id": a.ID.Hex(), "filename": a.Filename, "size": a.Size})
	}
}

func attachDownload(e *env.Env) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, _ := bson.ObjectIDFromHex(c.Param("id"))
		a, stream, err := e.Attach.Open(c.Request.Context(), mw.TenantID(c), id)
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		defer stream.Close()
		mime := a.Mime
		if mime == "" {
			mime = "application/octet-stream"
		}
		// 图片 inline 直接预览，其余走附件下载；文件名消毒 + UTF-8 双写，防断头与中文乱码
		disp := "attachment"
		if len(mime) >= 6 && mime[:6] == "image/" {
			disp = "inline"
		}
		name := attach.SafeFilename(a.Filename)
		c.Header("Content-Disposition", disp+"; filename=\""+name+"\"; filename*=UTF-8''"+url.PathEscape(name))
		c.DataFromReader(http.StatusOK, a.Size, mime, stream, nil)
	}
}

// ---------- 修改密码（本人） ----------

func passwordPage(e *env.Env) gin.HandlerFunc {
	return func(c *gin.Context) {
		web.Render(c, e, "app/password", nil)
	}
}

func passwordChange(e *env.Env) gin.HandlerFunc {
	return func(c *gin.Context) {
		oldPwd := c.PostForm("old_password")
		newPwd := c.PostForm("new_password")
		confirm := c.PostForm("confirm_password")
		if newPwd != confirm {
			web.SetFlash(c, "两次输入的新密码不一致")
			c.Redirect(http.StatusFound, "/app/password")
			return
		}
		u := mw.User(c)
		if err := e.Auth.ChangePassword(c.Request.Context(), u.TenantID, u.ID, oldPwd, newPwd); err != nil {
			web.SetFlash(c, "修改失败: "+err.Error())
		} else {
			e.Audit.Log(c.Request.Context(), audit.Entry{
				TenantID: u.TenantID, UserID: u.ID, Username: u.Phone,
				Action: audit.ActChangePassword, IP: c.ClientIP(),
			})
			web.SetFlash(c, "密码已修改")
		}
		c.Redirect(http.StatusFound, "/app/password")
	}
}
