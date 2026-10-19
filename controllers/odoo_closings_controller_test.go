package controllers

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOdooClosingsInvalidDate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, date := range []string{"", "2026-02-30", "2026-10-19T00:00:00Z", "invalid"} {
		router := gin.New()
		RegisterOdooRoutes(router.Group("/api"))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/odoo/closings?date="+date, nil))
		if response.Code != http.StatusBadRequest {
			t.Errorf("date %q returned %d", date, response.Code)
		}
	}
}

func TestOdooClosingsEmptyDayAndUpstreamFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty day", true: "Odoo unavailable"}[fail], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if fail {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if strings.HasSuffix(r.URL.Path, "authenticate") {
					_, _ = w.Write([]byte(`{"result":{"uid":1}}`))
					return
				}
				_, _ = w.Write([]byte(`{"result":[]}`))
			}))
			defer server.Close()
			t.Setenv("ODOO_URL", server.URL)
			t.Setenv("ODOO_DB", "test")
			t.Setenv("ODOO_USER", "test")
			t.Setenv("ODOO_PASSWORD", "test")
			router := gin.New()
			RegisterOdooRoutes(router.Group("/api"))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/odoo/closings?date=2026-10-19", nil))
			if fail {
				if response.Code != http.StatusBadGateway {
					t.Fatalf("failure returned %d", response.Code)
				}
				return
			}
			if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("unexpected response %d %s", response.Code, response.Body.String())
			}
			var body struct {
				Date     string `json:"date"`
				Timezone string `json:"timezone"`
				Data     []any  `json:"data"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Date != "2026-10-19" || body.Timezone != "America/Bogota" || body.Data == nil || len(body.Data) != 0 {
				t.Fatalf("invalid empty response %+v", body)
			}
		})
	}
}
