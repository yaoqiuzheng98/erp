package portal

import (
	"net/http/httptest"
	"testing"

	"erp/internal/dental/appointment"

	"github.com/gin-gonic/gin"
)

func TestBookingSource(t *testing.T) {
	gin.SetMode(gin.TestMode)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/api/p/x/appointments", nil)
	if got := bookingSource(c); got != appointment.SourcePortal {
		t.Fatalf("无 appid 头来源 = %q, want %q", got, appointment.SourcePortal)
	}

	c.Request.Header.Set("X-Wx-AppId", "wx6ec6f6ba380885ba")
	if got := bookingSource(c); got != appointment.SourceWxapp {
		t.Fatalf("带 appid 头来源 = %q, want %q", got, appointment.SourceWxapp)
	}
}
