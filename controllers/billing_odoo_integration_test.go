package controllers

import (
	"atm/models"
	"atm/odoo"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Exercise actual reconciliation, HTTP handlers and Odoo RPC against isolated fixtures.
func billingTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "billing.sqlite")), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true, IgnoreRelationshipsWhenMigrating: true,
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	err = db.AutoMigrate(&models.BillingConfig{}, &models.BillingPOSAlias{}, &models.BillingFixedCost{},
		&models.GastoLocal{}, &models.BillingMonthly{}, &models.EmployeePOSAssignment{},
		&models.BillingNominaAssignment{}, &models.BillingGastoExclusion{}, &models.NominaPayment{},
		&models.NominaPeriodExclusion{}, &models.Transaccion{}, &models.User{})
	if err != nil {
		t.Fatal(err)
	}
	// SQLite has no MySQL ENUM type; use its equivalent text column in this fixture.
	if err := db.Exec("CREATE TABLE categorias (id INTEGER PRIMARY KEY, nombre TEXT, tipo TEXT, is_gasto_operativo BOOLEAN, is_cartera_clientes BOOLEAN)").Error; err != nil {
		t.Fatal(err)
	}
	previous := DB
	DB = db
	invalidateBillingPOSAliasCache()
	odoo.ClearAllCache()
	t.Cleanup(func() {
		DB = previous
		invalidateBillingPOSAliasCache()
		odoo.ClearAllCache()
		conn, _ := db.DB()
		_ = conn.Close()
	})
	return db
}

func billingTestOdoo(t *testing.T, configs *[]odoo.POSConfigRef) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     any            `json:"id"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "invalid request", 400)
			return
		}
		var result any
		switch r.URL.Path {
		case "/web/session/authenticate":
			result = map[string]any{"uid": 1}
		case "/web/dataset/call_kw":
			if request.Params["model"] != "pos.config" {
				t.Errorf("unexpected model: %v", request.Params)
				return
			}
			rows := []map[string]any{}
			for _, cfg := range *configs {
				rows = append(rows, map[string]any{"id": cfg.ID, "name": cfg.Name})
			}
			result = rows
		case "/jsonrpc":
			if request.Params["service"] == "common" {
				result = 1
			} else {
				rows := []map[string]any{}
				for _, cfg := range *configs {
					rows = append(rows, map[string]any{"config_id": []any{cfg.ID, cfg.Name}, "date_order": "2026-01-15 12:00:00", "amount_total": float64(cfg.ID) * 100, "margin": float64(cfg.ID) * 10})
				}
				result = rows
			}
		default:
			t.Errorf("unexpected Odoo endpoint %s", r.URL.Path)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}))
	t.Cleanup(server.Close)
	t.Setenv("ODOO_URL", server.URL)
	t.Setenv("ODOO_DB", "test")
	t.Setenv("ODOO_USER", "test")
	t.Setenv("ODOO_PASSWORD", "test")
}

func billingRequest(t *testing.T, handler gin.HandlerFunc, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Handle(method, "/test", handler)
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, "/test"+target, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

func TestBillingDiscoversGranSanAndKeepsRenamedConfigurationAndHistory(t *testing.T) {
	db := billingTestDB(t)
	original := models.BillingConfig{OdooPOSID: int64Pointer(42), PosName: "Nombre anterior", IncludeInReports: boolPointer(true), Arriendo: 100}
	legacy := models.BillingConfig{PosName: "Internet", IncludeInReports: boolPointer(false)}
	fixtures := []any{
		&original, &legacy,
		&models.BillingFixedCost{PosName: "Nombre anterior", Name: "Alquiler", Amount: 25, Active: true},
		&models.BillingMonthly{PosName: "Nombre anterior", Year: 2026, Month: 1, Venta: 999, VentaManual: true},
		&models.GastoLocal{Local: "Nombre anterior", Tipo: "GASTO_OPERATIVO", Monto: 15},
		&models.EmployeePOSAssignment{UserID: 1, PosName: "Nombre anterior", CommissionPercentage: 2},
		&models.BillingNominaAssignment{Year: 2026, Month: 1, PosName: "Nombre anterior", PaymentID: 1},
		&models.BillingGastoExclusion{Year: 2026, Month: 1, Local: "Nombre anterior", Fingerprint: "test"},
	}
	for _, fixture := range fixtures {
		if err := db.Create(fixture).Error; err != nil {
			t.Fatal(err)
		}
	}
	configs := []odoo.POSConfigRef{{ID: 42, Name: "Local renombrado"}, {ID: 43, Name: "Internet"}, {ID: 44, Name: "Gran San"}}
	billingTestOdoo(t, &configs)

	selection, err := loadBillingPOSSelection(true, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(selection.Configs) != 3 {
		t.Fatalf("missing Odoo POS: %#v", selection.Configs)
	}
	var renamed, granSan models.BillingConfig
	for _, cfg := range selection.Configs {
		switch *cfg.OdooPOSID {
		case 42:
			renamed = cfg
		case 43:
			if *cfg.IncludeInReports {
				t.Fatal("legacy exclusion was reset")
			}
		case 44:
			granSan = cfg
		}
	}
	if renamed.ID != original.ID || renamed.Arriendo != 100 || !*renamed.IncludeInReports {
		t.Fatalf("rename lost existing config: %#v", renamed)
	}
	if granSan.ID == 0 || *granSan.IncludeInReports {
		t.Fatal("new POS must be available but not auto-selected")
	}
	for _, fixture := range []struct{ table, column string }{
		{"billing_fixed_costs", "pos_name"}, {"billing_monthlies", "pos_name"}, {"gastos_locales", "local"},
		{"employee_pos_assignments", "pos_name"}, {"billing_nomina_assignments", "pos_name"}, {"billing_gasto_exclusions", "local"},
	} {
		var count int64
		if err := db.Table(fixture.table).Where(fixture.column+" = ?", "Local renombrado").Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("history detached in %s", fixture.table)
		}
	}
	entries := []billingConfigEntry{}
	for _, cfg := range selection.Configs {
		included := *cfg.OdooPOSID != 43
		name := cfg.PosName
		if *cfg.OdooPOSID == 42 {
			name = "Nombre anterior"
		} // A stale browser name must still save by ID.
		entries = append(entries, billingConfigEntry{OdooPOSID: cfg.OdooPOSID, PosName: name, IncludeInReports: &included, Arriendo: cfg.Arriendo})
	}
	saved := billingRequest(t, SaveBillingConfigs, http.MethodPost, "", map[string]any{"entries": entries})
	if saved.Code != 200 {
		t.Fatalf("save failed: %d %s", saved.Code, saved.Body.String())
	}
	var savedPayload struct {
		Verified bool
		Configs  []models.BillingConfig
	}
	if err := json.Unmarshal(saved.Body.Bytes(), &savedPayload); err != nil {
		t.Fatal(err)
	}
	if !savedPayload.Verified {
		t.Fatal("save was not verified")
	}
	expected := map[string]billingConfigEntry{}
	for _, entry := range entries {
		expected[billingConfigIdentity(entry.OdooPOSID, entry.PosName)] = entry
	}
	if err := verifyBillingConfigSelection(savedPayload.Configs, expected); err != nil {
		t.Fatal(err)
	}

	response := billingRequest(t, OdooGetBilling, http.MethodGet, "?year=2026", nil)
	if response.Code != 200 {
		t.Fatalf("billing failed: %s", response.Body.String())
	}
	var sales map[string]map[string]float64
	if err := json.Unmarshal(response.Body.Bytes(), &sales); err != nil {
		t.Fatal(err)
	}
	if len(sales) != 2 || sales["Gran San"]["January"] != 4400 || sales["Local renombrado"]["January"] != 4200 {
		t.Fatalf("wrong enabled sales: %#v", sales)
	}
	monthly := billingRequest(t, GetBillingMonthly, http.MethodGet, "?year=2026&month=1", nil)
	if monthly.Code != 200 {
		t.Fatalf("monthly failed: %s", monthly.Body.String())
	}
	var report struct {
		Data []struct {
			ID    int64   `json:"odoo_pos_id"`
			Name  string  `json:"pos_name"`
			Venta float64 `json:"venta"`
		}
		SelectedIDs []int64 `json:"selected_pos_ids"`
	}
	if err := json.Unmarshal(monthly.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Data) != 2 || len(report.SelectedIDs) != 2 {
		t.Fatalf("wrong monthly selection: %s", monthly.Body.String())
	}
	for _, row := range report.Data {
		if row.ID == 42 && row.Venta != 999 {
			t.Fatal("manual historical sale lost after rename")
		}
		if row.ID == 44 && row.Venta != 4400 {
			t.Fatal("Gran San sale missing")
		}
		if row.ID == 43 {
			t.Fatal("disabled Internet included")
		}
	}
	// Cached metrics must stay attached by ID after another rename.
	configs[0].Name = "Segundo nombre"
	selection, err = loadBillingPOSSelection(true, true)
	if err != nil {
		t.Fatal(err)
	}
	monthly = billingRequest(t, GetBillingMonthly, http.MethodGet, "?year=2026&month=1", nil)
	if monthly.Code != 200 || !bytes.Contains(monthly.Body.Bytes(), []byte("Segundo nombre")) {
		t.Fatalf("second rename failed: %s", monthly.Body.String())
	}
	configs[0].Name = "Local renombrado"
	selection, err = loadBillingPOSSelection(true, true)
	if err != nil {
		t.Fatal(err)
	}
	if normalizeBillingPOSName("Segundo nombre") != "Local renombrado" ||
		normalizeBillingPOSName("Local renombrado") != "Local renombrado" {
		t.Fatal("renaming back created an alias cycle")
	}
}

func TestBillingCatalogChangeReturnsConflictAndOdooFailureBlocksSave(t *testing.T) {
	db := billingTestDB(t)
	configs := []odoo.POSConfigRef{{ID: 42, Name: "Gran San"}}
	billingTestOdoo(t, &configs)
	selection, err := loadBillingPOSSelection(true, true)
	if err != nil {
		t.Fatal(err)
	}
	configs = append(configs, odoo.POSConfigRef{ID: 43, Name: "Otro nuevo"})
	response := billingRequest(t, SaveBillingConfigs, http.MethodPost, "", map[string]any{"entries": []billingConfigEntry{
		{OdooPOSID: selection.Configs[0].OdooPOSID, PosName: "Gran San", IncludeInReports: boolPointer(true)},
	}})
	if response.Code != 409 {
		t.Fatalf("expected catalog conflict, got %d %s", response.Code, response.Body.String())
	}
	var cfg models.BillingConfig
	if err := db.Where("odoo_pos_id = ?", 42).First(&cfg).Error; err != nil {
		t.Fatal(err)
	}
	if *cfg.IncludeInReports {
		t.Fatal("conflicting save altered selection")
	}
	t.Setenv("ODOO_URL", "")
	if _, err := loadBillingPOSSelection(true, true); err == nil {
		t.Fatal("required Odoo failure swallowed")
	}
	fallback, err := loadBillingPOSSelection(false, false)
	if err != nil || len(fallback.Configs) != 2 || fallback.Source != "saved-config-fallback" {
		t.Fatalf("wrong fallback: %#v %v", fallback, err)
	}
}
