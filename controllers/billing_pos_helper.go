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
		key := billingPOSKey(normalized)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, normalized)
	}

	sort.Strings(out)
	return out, nil
}

func billingPOSKey(name string) string {
	return strings.ToLower(normalizeBillingPOSName(name))
}

func billingPOSNameMap(posNames []string) map[string]string {
	result := make(map[string]string, len(posNames))
	for _, posName := range posNames {
		normalized := normalizeBillingPOSName(posName)
		key := billingPOSKey(normalized)
		if key == "" {
			continue
		}
		if _, exists := result[key]; !exists {
			result[key] = normalized
		}
	}
	return result
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
	cfgByKey := make(map[string]models.BillingConfig, len(cfgs))
	cfgIsCurrent := make(map[string]bool, len(cfgs))
	for _, cfg := range cfgs {
		originalPosName := baseBillingPOSName(cfg.PosName)
		canonicalPosName := normalizeBillingPOSName(cfg.PosName)
		key := billingPOSKey(canonicalPosName)
		if key == "" {
			continue
		}
		isCurrentName := strings.EqualFold(originalPosName, canonicalPosName)
		cfg.PosName = canonicalPosName
		if existing, exists := cfgByKey[key]; exists {
			// Si existen la configuración antigua y la actual, mantener la actual.
			if cfgIsCurrent[key] || !isCurrentName {
				if cfgIsCurrent[key] == isCurrentName && cfg.UpdatedAt.After(existing.UpdatedAt) {
					cfgByKey[key] = cfg
				}
				continue
			}
		}
		cfgByKey[key] = cfg
		cfgIsCurrent[key] = isCurrentName
	}

	cfgMap := make(map[string]models.BillingConfig, len(cfgByKey))
	for _, cfg := range cfgByKey {
		cfgMap[cfg.PosName] = cfg
	}
	return cfgMap
}

func findBillingConfig(cfgMap map[string]models.BillingConfig, posName string) (models.BillingConfig, bool) {
	normalized := normalizeBillingPOSName(posName)
	if cfg, ok := cfgMap[normalized]; ok {
		return cfg, true
	}

	key := billingPOSKey(normalized)
	for candidateName, cfg := range cfgMap {
		if billingPOSKey(candidateName) == key {
			return cfg, true
		}
	}
	return models.BillingConfig{}, false
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
	seen := make(map[string]struct{}, len(posNames))

	for _, pos := range posNames {
		normalized := normalizeBillingPOSName(pos)
		key := billingPOSKey(normalized)
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}

		cfg, ok := findBillingConfig(cfgByPos, normalized)
		if !ok {
			includeInReports := true
			cfg = models.BillingConfig{
				PosName:          normalized,
				IncludeInReports: &includeInReports,
			}
		} else {
			// El nombre visible siempre debe ser el nombre vigente enviado por Odoo.
			cfg.PosName = normalized
		}

		merged = append(merged, cfg)
		mergedMap[normalized] = cfg
	}

	sort.Slice(merged, func(i, j int) bool {
		return merged[i].PosName < merged[j].PosName
	})

	return merged, mergedMap
}
