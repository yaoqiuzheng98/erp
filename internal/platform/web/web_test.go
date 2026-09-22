package web

import (
	"net/http/httptest"
	"testing"

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
