package controllers

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHoursCalendarBoundaries(t *testing.T) {
	from, err := parseHoursBoundary("2026-10-19", false)
	if err != nil || from.UTC().Format("2006-01-02 15:04:05") != "2026-10-19 05:00:00" {
		t.Fatalf("wrong Colombian start %s %v", from, err)
	}
	to, err := parseHoursBoundary("2026-10-19", true)
	if err != nil || to.UTC().Format("2006-01-02 15:04:05") != "2026-10-20 05:00:00" {
		t.Fatalf("wrong exclusive end %s %v", to, err)
	}
}
func TestHoursRejectsInvalidAndUnboundedRanges(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, query := range []string{"from=2026-02-30&to=2026-03-01", "from=2026-10-19&to=2026-10-18", "from=2024-01-01&to=2026-01-01", "from=2099-01-01&to=2099-01-02"} {
		router := gin.New()
		RegisterOdooRoutes(router.Group("/api"))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/odoo/pos-sessions?"+query, nil))
		if response.Code != 400 {
			t.Errorf("%s returned %d", query, response.Code)
		}
	}
}
