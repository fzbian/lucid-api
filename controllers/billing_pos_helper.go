package controllers

import (
	"atm/models"
	"atm/odoo"
	"context"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	billingPOSAliasMu       sync.RWMutex
	billingPOSAliasCache    map[string]string
	billingPOSAliasCacheExp time.Time
)

var defaultBillingPOSAliases = map[string]string{
	"titanium": "San Fason",
}

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

func baseBillingPOSName(name string) string {
	clean := strings.TrimSpace(name)
	if idx := strings.Index(clean, " ("); idx != -1 {
		clean = clean[:idx]
	}
	return strings.TrimSpace(clean)
}

func invalidateBillingPOSAliasCache() {
	billingPOSAliasMu.Lock()
	defer billingPOSAliasMu.Unlock()
	billingPOSAliasCache = nil
	billingPOSAliasCacheExp = time.Time{}
}

func loadBillingPOSAliasMap() map[string]string {
	now := time.Now()
	billingPOSAliasMu.RLock()
	if billingPOSAliasCache != nil && now.Before(billingPOSAliasCacheExp) {
		cached := make(map[string]string, len(billingPOSAliasCache))
		for k, v := range billingPOSAliasCache {
			cached[k] = v
		}
		billingPOSAliasMu.RUnlock()
		return cached
	}
	billingPOSAliasMu.RUnlock()

	aliases := make(map[string]string, len(defaultBillingPOSAliases))
	for oldName, currentName := range defaultBillingPOSAliases {
		old := strings.ToLower(baseBillingPOSName(oldName))
		current := baseBillingPOSName(currentName)
		if old == "" || current == "" {
			continue
		}
		aliases[old] = current
	}

	if DB != nil {
		var rows []models.BillingPOSAlias
		if err := DB.Where("active = ?", true).Find(&rows).Error; err == nil {
			for _, row := range rows {
				old := strings.ToLower(baseBillingPOSName(row.OldPosName))
				current := baseBillingPOSName(row.CurrentPosName)
				if old == "" || current == "" {
					continue
				}
				aliases[old] = current
			}
		}
	}

	billingPOSAliasMu.Lock()
	billingPOSAliasCache = make(map[string]string, len(aliases))
	for k, v := range aliases {
		billingPOSAliasCache[k] = v
	}
	billingPOSAliasCacheExp = now.Add(30 * time.Second)
	billingPOSAliasMu.Unlock()

	return aliases
}

func resolveBillingPOSAlias(name string, aliases map[string]string) string {
	current := baseBillingPOSName(name)
	seen := make(map[string]struct{})
	for current != "" {
		key := strings.ToLower(current)
		next, ok := aliases[key]
		if !ok {
			return current
		}
		if _, loop := seen[key]; loop {
			return current
		}
		seen[key] = struct{}{}
		current = baseBillingPOSName(next)
	}
	return current
}

func buildBillingConfigMap(cfgs []models.BillingConfig) map[string]models.BillingConfig {
	cfgMap := make(map[string]models.BillingConfig, len(cfgs))
	cfgIsCurrent := make(map[string]bool, len(cfgs))
	for _, cfg := range cfgs {
		originalPosName := baseBillingPOSName(cfg.PosName)
		canonicalPosName := normalizeBillingPOSName(cfg.PosName)
		if canonicalPosName == "" {
			continue
		}
		isCurrentName := originalPosName == canonicalPosName
		cfg.PosName = canonicalPosName
		if existing, exists := cfgMap[canonicalPosName]; exists {
			_ = existing
			// Si existen la configuración antigua y la actual, mantener la actual.
			if cfgIsCurrent[canonicalPosName] || !isCurrentName {
				continue
			}
		}
		cfgMap[canonicalPosName] = cfg
		cfgIsCurrent[canonicalPosName] = isCurrentName
	}
	return cfgMap
}

func mergeBillingMonthlyRow(existing, incoming models.BillingMonthly, canonicalPosName string) models.BillingMonthly {
	existingOriginal := baseBillingPOSName(existing.PosName)
	incomingOriginal := baseBillingPOSName(incoming.PosName)

	if incomingOriginal == canonicalPosName && existingOriginal != canonicalPosName {
		if !incoming.VentaManual && existing.VentaManual {
			incoming.Venta = existing.Venta
			incoming.VentaManual = true
		}
		if !incoming.MargenManual && existing.MargenManual {
			incoming.Margen = existing.Margen
			incoming.MargenManual = true
		}
		return incoming
	}

	if existingOriginal == canonicalPosName {
		if !existing.VentaManual && incoming.VentaManual {
			existing.Venta = incoming.Venta
			existing.VentaManual = true
		}
		if !existing.MargenManual && incoming.MargenManual {
			existing.Margen = incoming.Margen
			existing.MargenManual = true
		}
		return existing
	}

	if incoming.UpdatedAt.After(existing.UpdatedAt) {
		if !incoming.VentaManual && existing.VentaManual {
			incoming.Venta = existing.Venta
			incoming.VentaManual = true
		}
		if !incoming.MargenManual && existing.MargenManual {
			incoming.Margen = existing.Margen
			incoming.MargenManual = true
		}
		return incoming
	}

	if !existing.VentaManual && incoming.VentaManual {
		existing.Venta = incoming.Venta
		existing.VentaManual = true
	}
	if !existing.MargenManual && incoming.MargenManual {
		existing.Margen = incoming.Margen
		existing.MargenManual = true
	}
	return existing
}

func canonicalizeBillingMetricMap(values map[string]map[string]float64) map[string]map[string]float64 {
	if values == nil {
		return nil
	}
	result := make(map[string]map[string]float64, len(values))
	for pos, months := range values {
		canonicalPos := normalizeBillingPOSName(pos)
		if canonicalPos == "" {
			continue
		}
		if result[canonicalPos] == nil {
			result[canonicalPos] = make(map[string]float64, len(months))
		}
		for label, value := range months {
			result[canonicalPos][label] += value
		}
	}
	return result
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
