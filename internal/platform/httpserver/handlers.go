package httpserver

import (
	"net/http"

	"erp/internal/platform/audit"
	"erp/internal/platform/env"
	mw "erp/internal/platform/middleware"
	"erp/internal/platform/plugin"
	"erp/internal/platform/session"
	"erp/internal/platform/web"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func loginTenant(e *env.Env) gin.HandlerFunc {
	return func(c *gin.Context) {
		u, err := e.Auth.LoginTenant(c.Request.Context(),
			c.PostForm("tenant"), c.PostForm("username"), c.PostForm("password"))
		if err != nil {
			web.Render(c, e, "login", gin.H{"Err": "企业名、用户名或密码错误"})
			return
		}
		s, err := e.Sessions.Create(c.Request.Context(), session.KindTenant, u.ID, u.TenantID)
		if err != nil {
			web.Render(c, e, "login", gin.H{"Err": "会话创建失败"})
			return
		}
		c.SetCookie(e.Cfg.Session.CookieName, s.ID, int(e.Cfg.Session.TTLHours)*3600, "/", "", e.Cfg.Session.Secure, true)
		e.Audit.Log(c.Request.Context(), audit.Entry{
			TenantID: u.TenantID, UserID: u.ID, Username: u.Username,
			Action: "login", IP: c.ClientIP(),
		})
		c.Redirect(http.StatusFound, "/app")
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
			UserID: a.ID, Username: a.Username, Action: "sys.login", IP: c.ClientIP(),
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

func dashboard(e *env.Env) gin.HandlerFunc {
	return func(c *gin.Context) {
		t := mw.Tenant(c)
		enabled := e.Gate.EnabledSet(c.Request.Context(), t.ID)
		type card struct{ ID, Name, Path string }
		var cards []card
		for _, p := range plugin.All() {
			if !enabled[p.ID()] {
				continue
			}
			path := "/app/" + p.ID()
			for _, m := range p.Menus() {
				if len(m.Children) > 0 && m.Children[0].Path != "" {
					path = m.Children[0].Path
				}
			}
			cards = append(cards, card{ID: p.ID(), Name: p.Name(), Path: path})
		}
		web.Render(c, e, "dashboard", gin.H{"Cards": cards})
	}
}

func notifications(e *env.Env) gin.HandlerFunc {
	return func(c *gin.Context) {
		t, u := mw.Tenant(c), mw.User(c)
		list, _ := e.Notify.List(c.Request.Context(), t.ID, u.ID, 100)
		web.Render(c, e, "notifications", gin.H{"List": list})
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
			mw.User(c).Username)
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
		a, path, err := e.Attach.Get(c.Request.Context(), mw.TenantID(c), id)
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		c.Header("Content-Disposition", "attachment; filename=\""+a.Filename+"\"")
		c.File(path)
	}
}
