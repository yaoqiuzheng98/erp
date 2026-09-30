package handler

import (
	"net/http"

	"erp/internal/dental/patient"
	mw "erp/internal/platform/middleware"
	"erp/internal/platform/web"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func (h *Handler) registerPatient(g *gin.RouterGroup) {
	g.GET("/patients", mw.RequirePerm("patient.read"), h.patients)
	g.POST("/patients", mw.RequirePerm("patient.write"), h.createPatient)
	g.GET("/patients/:id", mw.RequirePerm("patient.read"), h.patient)
	g.POST("/patients/:id/tooth", mw.RequirePerm("patient.write"), h.setTooth)
	g.POST("/patients/:id/attach", mw.RequirePerm("patient.write"), h.uploadAttach)
	g.POST("/patients/:id/password", mw.RequirePerm("patient.write"), h.setPassword)
}

func (h *Handler) patients(c *gin.Context) {
	tenantID := mw.TenantID(c)
	skip, limit, pager := web.ParsePager(c, 30)
	q := c.Query("q")
	list, total, err := h.pats.List(c.Request.Context(), tenantID, q, skip, limit)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	pager.Total = total
	web.Render(c, h.e, "dental/patients", gin.H{"Rows": list, "Pager": pager, "Q": q})
}

func (h *Handler) createPatient(c *gin.Context) {
	p := &patient.Patient{
		Name: c.PostForm("name"), Phone: c.PostForm("phone"),
		Gender: c.PostForm("gender"), Birth: c.PostForm("birth"),
		Allergy: c.PostForm("allergy"), History: c.PostForm("history"),
	}
	if err := h.pats.Create(c.Request.Context(), mw.TenantID(c), p); err != nil {
		web.SetFlash(c, "创建失败: "+err.Error())
	} else {
		web.SetFlash(c, "患者已建档")
	}
	c.Redirect(http.StatusFound, "/app/patients")
}

func (h *Handler) patient(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	tid := mw.TenantID(c)
	p, err := h.pats.ByID(c.Request.Context(), tid, id)
	if err != nil {
		c.String(http.StatusNotFound, "患者不存在")
		return
	}
	appts, _ := h.appts.OfPatient(c.Request.Context(), tid, id)
	files, _ := h.e.Attach.ListByOwner(c.Request.Context(), tid, "patient", id)
	web.Render(c, h.e, "dental/patient", gin.H{
		"P": p, "Appts": appts, "Files": files,
		"Quadrants": quadrants(), "ToothNames": patient.ToothStatuses,
	})
}

// quadrants FDI 四象限，按牙位图显示顺序。
func quadrants() [][]string {
	return [][]string{
		{"18", "17", "16", "15", "14", "13", "12", "11"},
		{"21", "22", "23", "24", "25", "26", "27", "28"},
		{"48", "47", "46", "45", "44", "43", "42", "41"},
		{"31", "32", "33", "34", "35", "36", "37", "38"},
	}
}

// setTooth 牙位状态更新（fetch POST）。
func (h *Handler) setTooth(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	tooth, status := c.PostForm("tooth"), c.PostForm("status")
	if len(tooth) != 2 || tooth[0] < '1' || tooth[0] > '4' || tooth[1] < '1' || tooth[1] > '8' {
		c.String(http.StatusBadRequest, "牙位无效")
		return
	}
	if status != "" {
		if _, ok := patient.ToothStatuses[status]; !ok {
			c.String(http.StatusBadRequest, "状态无效")
			return
		}
	}
	if err := h.pats.SetTooth(c.Request.Context(), mw.TenantID(c), id, tooth, status); err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	c.Status(http.StatusNoContent)
}

// setPassword 配置患者端登录密码。
func (h *Handler) setPassword(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	if err := h.pats.SetPassword(c.Request.Context(), mw.TenantID(c), id, c.PostForm("password")); err != nil {
		web.SetFlash(c, "设置失败: "+err.Error())
	} else {
		web.SetFlash(c, "患者端密码已设置")
	}
	c.Redirect(http.StatusFound, "/app/patients/"+id.Hex())
}

// uploadAttach 患者档案附件（牙片/口内照）：multipart file 字段。
func (h *Handler) uploadAttach(c *gin.Context) {
	id, _ := bson.ObjectIDFromHex(c.Param("id"))
	tid := mw.TenantID(c)
	if _, err := h.pats.ByID(c.Request.Context(), tid, id); err != nil {
		web.SetFlash(c, "患者不存在")
		c.Redirect(http.StatusFound, "/app/patients")
		return
	}
	fh, err := c.FormFile("file")
	if err != nil {
		web.SetFlash(c, "请选择文件")
		c.Redirect(http.StatusFound, "/app/patients/"+id.Hex())
		return
	}
	f, err := fh.Open()
	if err != nil {
		web.SetFlash(c, "文件打开失败")
		c.Redirect(http.StatusFound, "/app/patients/"+id.Hex())
		return
	}
	defer f.Close()
	if _, err := h.e.Attach.Save(c.Request.Context(), tid,
		"patient", id, fh.Filename, fh.Header.Get("Content-Type"), f,
		mw.User(c).Name); err != nil {
		web.SetFlash(c, "上传失败: "+err.Error())
	} else {
		web.SetFlash(c, "影像已上传")
	}
	c.Redirect(http.StatusFound, "/app/patients/"+id.Hex())
}
