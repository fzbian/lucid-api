package controllers

import (
	"atm/models"
	"testing"
	"time"
)

func boolPointer(value bool) *bool {
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
