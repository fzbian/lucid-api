package controllers

import (
	"atm/models"
	"atm/odoo"
	"context"
	"os"
	"sort"
)

func getAllBillingPOSNamesFromOdoo() ([]string, error) {
	names, err := odoo.ListPOSNames(
		context.Background(),
		os.Getenv("ODOO_URL"),
		os.Getenv("ODOO_DB"),
		os.Getenv("ODOO_USER"),
		os.Getenv("ODOO_PASSWORD"),
	)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, name := range names {
		normalized := normalizeBillingPOSName(name)
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}

	sort.Strings(out)
	return out, nil
}

func buildBillingConfigMap(cfgs []models.BillingConfig) map[string]models.BillingConfig {
	cfgMap := make(map[string]models.BillingConfig, len(cfgs))
	for _, cfg := range cfgs {
		cfg.PosName = normalizeBillingPOSName(cfg.PosName)
		if cfg.PosName == "" {
			continue
		}
		cfgMap[cfg.PosName] = cfg
	}
	return cfgMap
}

func mergeBillingConfigsWithPOSNames(cfgs []models.BillingConfig, posNames []string) ([]models.BillingConfig, map[string]models.BillingConfig) {
	cfgByPos := buildBillingConfigMap(cfgs)
	merged := make([]models.BillingConfig, 0, len(posNames))
	mergedMap := make(map[string]models.BillingConfig, len(posNames))

	for _, pos := range posNames {
		normalized := normalizeBillingPOSName(pos)
		if normalized == "" {
			continue
		}

		cfg, ok := cfgByPos[normalized]
		if !ok {
			includeInReports := true
			cfg = models.BillingConfig{
				PosName:          normalized,
				IncludeInReports: &includeInReports,
			}
		}

		merged = append(merged, cfg)
		mergedMap[normalized] = cfg
	}

	return merged, mergedMap
}
