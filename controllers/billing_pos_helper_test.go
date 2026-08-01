package controllers

import (
	"atm/models"
	"testing"
	"time"
)

func boolPointer(value bool) *bool {
	return &value
}

func int64Pointer(value int64) *int64 {
	return &value
}

func TestMergeBillingConfigsUsesOdooDisplayNameCaseInsensitively(t *testing.T) {
	cfgs := []models.BillingConfig{
		{
			ID:               17,
			PosName:          " internet ",
			IncludeInReports: boolPointer(false),
			UpdatedAt:        time.Now(),
		},
	}

	merged, cfgMap := mergeBillingConfigsWithPOSNames(cfgs, []string{"Internet", "INTERNET "})
	if len(merged) != 1 {
		t.Fatalf("expected one Odoo POS, got %d", len(merged))
	}
	if merged[0].PosName != "Internet" {
		t.Fatalf("expected Odoo display name Internet, got %q", merged[0].PosName)
	}
	if merged[0].IncludeInReports == nil || *merged[0].IncludeInReports {
		t.Fatal("expected the saved exclusion to be preserved")
	}
	if isPOSIncludedInReports(cfgMap, " internet ") {
		t.Fatal("expected case-insensitive lookup to preserve exclusion")
	}
}

func TestMergeBillingConfigsIncludesNewOdooPOSByDefault(t *testing.T) {
	merged, cfgMap := mergeBillingConfigsWithPOSNames(nil, []string{"Internet"})
	if len(merged) != 1 {
		t.Fatalf("expected one Odoo POS, got %d", len(merged))
	}
	if merged[0].IncludeInReports == nil || !*merged[0].IncludeInReports {
		t.Fatal("expected a new Odoo POS to be included by default")
	}
	if !isPOSIncludedInReports(cfgMap, "INTERNET") {
		t.Fatal("expected Internet to be included regardless of casing")
	}
}

func TestBillingPOSNameMapKeepsCurrentOdooName(t *testing.T) {
	nameMap := billingPOSNameMap([]string{"Internet", " internet ", "San Fason"})
	if len(nameMap) != 2 {
		t.Fatalf("expected two unique POS names, got %d", len(nameMap))
	}
	if got := nameMap[billingPOSKey("INTERNET")]; got != "Internet" {
		t.Fatalf("expected current Odoo name Internet, got %q", got)
	}
}

func TestAlignBillingMetricMapUsesOnlySelectedOdooPOS(t *testing.T) {
	selection := billingPOSSelection{
		SelectedNames: []string{"Internet"},
		SelectedNameByKey: map[string]string{
			billingPOSKey("Internet"): "Internet",
		},
	}
	values := map[string]map[string]float64{
		" internet ": {"January": 1250},
		"Otro local": {"January": 500},
	}

	aligned := alignBillingMetricMapToSelection(values, selection)
	if len(aligned) != 1 {
		t.Fatalf("expected only the selected Odoo POS, got %d", len(aligned))
	}
	if got := aligned["Internet"]["January"]; got != 1250 {
		t.Fatalf("expected Internet billing 1250, got %.2f", got)
	}
}

func TestVerifyBillingConfigSelectionUsesOdooID(t *testing.T) {
	configs := []models.BillingConfig{
		{OdooPOSID: int64Pointer(42), PosName: "Internet", IncludeInReports: boolPointer(true)},
	}
	expected := map[int64]billingConfigEntry{
		42: {OdooPOSID: 42, PosName: "Internet", IncludeInReports: boolPointer(true)},
	}
	if err := verifyBillingConfigSelection(configs, expected); err != nil {
		t.Fatalf("expected verified Odoo selection: %v", err)
	}

	configs[0].IncludeInReports = boolPointer(false)
	if err := verifyBillingConfigSelection(configs, expected); err == nil {
		t.Fatal("expected verification to detect an inclusion mismatch")
	}
}

func TestMergeBillingConfigValuesPreservesHistoricalCosts(t *testing.T) {
	primary := models.BillingConfig{PosName: "San Fason", IncludeInReports: boolPointer(true)}
	legacy := models.BillingConfig{
		PosName:    "Titanium",
		Arriendo:   100,
		Internet:   200,
		Luz:        300,
		LuzAplica:  true,
		Gas:        400,
		GasAplica:  true,
		Agua:       500,
		AguaAplica: true,
	}
	merged := mergeBillingConfigValues(primary, legacy)
	if merged.Arriendo != 100 || merged.Internet != 200 || merged.Luz != 300 || merged.Gas != 400 || merged.Agua != 500 {
		t.Fatal("expected historical fixed-cost configuration to be preserved")
	}
	if !merged.LuzAplica || !merged.GasAplica || !merged.AguaAplica {
		t.Fatal("expected historical service flags to be preserved")
	}
}
