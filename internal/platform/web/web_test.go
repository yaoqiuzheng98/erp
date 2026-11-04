package web

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"

	"erp/internal/platform/tenant"

	"github.com/gin-gonic/gin"
)

func ctxOf(url string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", url, nil)
	return c
}

func TestParsePager(t *testing.T) {
	skip, limit, p := ParsePager(ctxOf("/x?page=3"), 20)
	if p.Page != 3 || skip != 40 || limit != 20 {
		t.Fatalf("bad pager: skip=%d limit=%d p=%+v", skip, limit, p)
	}
	skip, _, p = ParsePager(ctxOf("/x?page=-2"), 10)
	if p.Page != 1 || skip != 0 {
		t.Fatalf("negative page must clamp to 1: %+v", p)
	}
}

func TestPagerPages(t *testing.T) {
	if (Pager{Page: 1, Size: 20, Total: 41}).Pages() != 3 {
		t.Fatal("41/20 should be 3 pages")
	}
	if (Pager{Page: 1, Size: 20, Total: 0}).Pages() != 1 {
		t.Fatal("empty should be 1 page")
	}
}

// TestAdminHomeRendersParkingImage 首页配置页能渲染停车图上传/删除入口（防模板字段漏接）。
func TestAdminHomeRendersParkingImage(t *testing.T) {
	tpl, err := Build()
	if err != nil {
		t.Fatalf("build templates: %v", err)
	}
	var buf bytes.Buffer
	p := &Page{
		Data: gin.H{"Tenant": &tenant.Tenant{ParkingImage: "parking-file"}},
		CSRF: "csrf",
	}
	if err := tpl.ExecuteTemplate(&buf, "admin/home", p); err != nil {
		t.Fatalf("render admin/home: %v", err)
	}
	html := buf.String()
	for _, want := range []string{"停车指引图", "/admin/home/image/parking", "/admin/home/image/parking/delete"} {
		if !strings.Contains(html, want) {
			t.Fatalf("admin/home missing %q", want)
		}
	}
}

func TestFlashChineseRoundTrip(t *testing.T) {
	// 中文 flash 经 cookie 往返不丢（net/http 会剥非 ASCII，必须先转义）。
	c := ctxOf("/x")
	SetFlash(c, "门诊设置已保存")
	h := c.Writer.Header().Get("Set-Cookie")
	if h == "" {
		t.Fatal("no set-cookie")
	}
	c2 := ctxOf("/y")
	c2.Request.Header.Set("Cookie", h[strings.Index(h, "erp_flash="):strings.Index(h, ";")])
	if got := takeFlash(c2); got != "门诊设置已保存" {
		t.Fatalf("flash = %q", got)
	}
}
