package controllers

import (
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
