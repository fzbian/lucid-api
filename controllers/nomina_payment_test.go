package controllers

import (
	"atm/models"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestGeneratePaymentPreservesLongNotes(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "nomina.sqlite")), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
		IgnoreRelationshipsWhenMigrating:         true,
	})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.AutoMigrate(&models.NominaConfig{}, &models.UserPayroll{},
		&models.NominaPeriodExclusion{}, &models.NominaPayment{}); err != nil {
		t.Fatal(err)
	}
	previous := DB
	DB = db
	t.Cleanup(func() { DB = previous })
	if err := db.Create(&models.NominaConfig{
		ID: 1, AuxilioTransporte: 200000, ValorDominicalS2: 50000,
		ValorMadrugon: 10000, ValorSalud: 40000, ValorPension: 40000,
	}).Error; err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	RegisterNominaRoutes(router.Group("/api"))

	for i, tc := range []struct {
		payType  string
		paidBase int64
		total    int64
	}{
		{"fixed", 1000000, 1168000},
		{"daily", 480000, 478000},
		{"madrugones", 170000, 268000},
	} {
		t.Run(tc.payType, func(t *testing.T) {
			userID := uint(i + 1)
			if err := db.Create(&models.UserPayroll{
				UserID: userID, PayType: tc.payType, BaseSalary: 2000000,
				DailyRate: 60000, HourlyRate: 20000,
			}).Error; err != nil {
				t.Fatal(err)
			}
			// Exceeds both VARCHAR(255) and the MySQL TEXT byte limit.
			notes := strings.Repeat("Observación de nómina: áéíóú.\n", 3000)
			body, err := json.Marshal(map[string]any{
				"user_id": userID, "period_start": "2026-10-01T00:00:00Z",
				"period_end": "2026-10-15T00:00:00Z", "days_worked": 8,
				"hours_worked": 8.5, "sundays_qty": 1, "madrugones_qty": 2.0,
				"includes_security": true, "advance": 10000, "commission": 70000,
				"aditions":   `[{"label":"Bono","value":25000}]`,
				"deductions": `[{"label":"Descuento","value":7000}]`,
				"notes":      notes, "created_by": "test",
			})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/api/nomina/pay", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("POST /api/nomina/pay: status %d: %s", response.Code, response.Body.String())
			}
			var payment models.NominaPayment
			if err := json.Unmarshal(response.Body.Bytes(), &payment); err != nil {
				t.Fatal(err)
			}
			if payment.ID == 0 || payment.Notes != notes || payment.TotalPaid != tc.total || payment.PaidBase != tc.paidBase {
				t.Fatalf("unexpected response: id=%d, notes bytes=%d, total=%d, base=%d", payment.ID, len(payment.Notes), payment.TotalPaid, payment.PaidBase)
			}
			var saved models.NominaPayment
			if err := db.First(&saved, payment.ID).Error; err != nil {
				t.Fatal(err)
			}
			if saved.Notes != notes || saved.TotalPaid != tc.total || saved.PaidBase != tc.paidBase {
				t.Fatal("persisted notes or payroll amounts differ from the expected payment")
			}
		})
	}
}
