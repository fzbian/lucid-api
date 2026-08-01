package controllers

import (
	"atm/models"
	"atm/odoo"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	billingPOSAliasMu       sync.RWMutex
	billingPOSAliasCache    map[string]string
	billingPOSAliasCacheExp time.Time
)

var defaultBillingPOSAliases = map[string]string{
	"titanium": "San Fason",
}

func getAllBillingPOSConfigsFromOdoo(forceRefresh bool) ([]odoo.POSConfigRef, error) {
	if forceRefresh {
		odoo.InvalidatePOSConfigCache()
	}
	client, err := odoo.NewFromEnv()
	if err != nil {
		return nil, err
	}
	if err := client.Authenticate(); err != nil {
		return nil, fmt.Errorf("autenticación Odoo: %w", err)
	}
	configs, err := client.ListPOSConfigRefs()
	if err != nil {
		return nil, err
	}

	seenIDs := make(map[int64]struct{}, len(configs))
	out := make([]odoo.POSConfigRef, 0, len(configs))
	for _, config := range configs {
		normalized := normalizeBillingPOSName(config.Name)
		if config.ID <= 0 || normalized == "" {
			continue
		}
		if _, exists := seenIDs[config.ID]; exists {
			continue
		}
		seenIDs[config.ID] = struct{}{}
		out = append(out, odoo.POSConfigRef{ID: config.ID, Name: normalized})
	}

	sort.Slice(out, func(i, j int) bool {
		if strings.EqualFold(out[i].Name, out[j].Name) {
			return out[i].ID < out[j].ID
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

func getAllBillingPOSNamesFromOdoo() ([]string, error) {
	configs, err := getAllBillingPOSConfigsFromOdoo(false)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(configs))
	for _, config := range configs {
		names = append(names, config.Name)
	}
	return names, nil
}

type billingPOSSelection struct {
	Configs           []models.BillingConfig
	ConfigMap         map[string]models.BillingConfig
	SelectedNames     []string
	SelectedIDs       []int64
	SelectedNameByKey map[string]string
	Source            string
}

func loadBillingPOSSelection(forceRefresh, requireOdoo bool) (billingPOSSelection, error) {
	odooConfigs, odooErr := getAllBillingPOSConfigsFromOdoo(forceRefresh)
	if odooErr != nil && requireOdoo {
		return billingPOSSelection{}, odooErr
	}
	if odooErr == nil && len(odooConfigs) == 0 && requireOdoo {
		return billingPOSSelection{}, fmt.Errorf("Odoo respondió correctamente pero no devolvió registros de pos.config")
	}

	var configs []models.BillingConfig
	if odooErr == nil {
		if err := DB.Transaction(func(tx *gorm.DB) error {
			var err error
			configs, err = reconcileBillingConfigsWithOdoo(tx, odooConfigs)
			return err
		}); err != nil {
			return billingPOSSelection{}, err
		}
	} else {
		if err := DB.Find(&configs).Error; err != nil {
			return billingPOSSelection{}, err
		}
		identified := make([]models.BillingConfig, 0, len(configs))
		for _, cfg := range configs {
			if cfg.OdooPOSID != nil {
				identified = append(identified, cfg)
			}
		}
		if len(identified) > 0 {
			configs = billingConfigsFromMap(buildBillingConfigMap(identified))
		} else {
			configs = billingConfigsFromMap(buildBillingConfigMap(configs))
		}
	}

	selection := billingPOSSelection{
		Configs:           configs,
		ConfigMap:         buildBillingConfigMap(configs),
		SelectedNameByKey: make(map[string]string, len(configs)),
		Source:            "odoo",
	}
	if odooErr != nil {
		selection.Source = "saved-config-fallback"
	}
	for _, cfg := range configs {
		if cfg.IncludeInReports == nil || !*cfg.IncludeInReports {
			continue
		}
		selection.SelectedNames = append(selection.SelectedNames, cfg.PosName)
		selection.SelectedNameByKey[billingPOSKey(cfg.PosName)] = cfg.PosName
		if cfg.OdooPOSID != nil {
			selection.SelectedIDs = append(selection.SelectedIDs, *cfg.OdooPOSID)
		}
	}
	return selection, nil
}

func (selection billingPOSSelection) selectedName(posName string) (string, bool) {
	name, ok := selection.SelectedNameByKey[billingPOSKey(posName)]
	return name, ok
}

func alignBillingMetricMapToSelection(values map[string]map[string]float64, selection billingPOSSelection) map[string]map[string]float64 {
	result := make(map[string]map[string]float64, len(selection.SelectedNames))
	for posName, months := range values {
		selectedName, included := selection.selectedName(posName)
		if !included {
			continue
		}
		if result[selectedName] == nil {
			result[selectedName] = make(map[string]float64, len(months))
		}
		for month, value := range months {
			result[selectedName][month] += value
		}
	}
	return result
}

func billingConfigsFromMap(cfgMap map[string]models.BillingConfig) []models.BillingConfig {
	configs := make([]models.BillingConfig, 0, len(cfgMap))
	for _, cfg := range cfgMap {
		configs = append(configs, cfg)
	}
	sort.Slice(configs, func(i, j int) bool {
		return strings.ToLower(configs[i].PosName) < strings.ToLower(configs[j].PosName)
	})
	return configs
}

func reconcileBillingConfigsWithOdoo(tx *gorm.DB, odooConfigs []odoo.POSConfigRef) ([]models.BillingConfig, error) {
	var existing []models.BillingConfig
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Order("id asc").Find(&existing).Error; err != nil {
		return nil, err
	}

	result := make([]models.BillingConfig, 0, len(odooConfigs))
	consumed := make(map[uint]struct{})
	for _, odooConfig := range odooConfigs {
		candidateIndexes := make([]int, 0, 2)
		bestIndex := -1
		bestHasOdooID := false
		bestHasCurrentName := false
		for index, cfg := range existing {
			if _, alreadyConsumed := consumed[cfg.ID]; alreadyConsumed {
				continue
			}
			hasSameID := cfg.OdooPOSID != nil && *cfg.OdooPOSID == odooConfig.ID
			isLegacyNameMatch := cfg.OdooPOSID == nil && billingPOSKey(cfg.PosName) == billingPOSKey(odooConfig.Name)
			if !hasSameID && !isLegacyNameMatch {
				continue
			}
			hasCurrentName := strings.EqualFold(baseBillingPOSName(cfg.PosName), odooConfig.Name)
			candidateIndexes = append(candidateIndexes, index)
			if bestIndex == -1 ||
				(hasSameID && !bestHasOdooID) ||
				(hasSameID == bestHasOdooID && hasCurrentName && !bestHasCurrentName) ||
				(hasSameID == bestHasOdooID && hasCurrentName == bestHasCurrentName && cfg.UpdatedAt.After(existing[bestIndex].UpdatedAt)) {
				bestIndex = index
				bestHasOdooID = hasSameID
				bestHasCurrentName = hasCurrentName
			}
		}

		odooPOSID := odooConfig.ID
		if bestIndex == -1 {
			include := true
			created := models.BillingConfig{
				OdooPOSID:        &odooPOSID,
				PosName:          odooConfig.Name,
				IncludeInReports: &include,
			}
			if err := tx.Create(&created).Error; err != nil {
				return nil, err
			}
			result = append(result, created)
			continue
		}

		primary := existing[bestIndex]
		for _, index := range candidateIndexes {
			consumed[existing[index].ID] = struct{}{}
			if index != bestIndex {
				primary = mergeBillingConfigValues(primary, existing[index])
			}
		}
		primary.OdooPOSID = &odooPOSID
		primary.PosName = odooConfig.Name
		if primary.IncludeInReports == nil {
			include := true
			primary.IncludeInReports = &include
		}
		if err := tx.Save(&primary).Error; err != nil {
			return nil, err
		}

		duplicateIDs := make([]uint, 0, len(candidateIndexes)-1)
		for _, index := range candidateIndexes {
			if index != bestIndex {
				duplicateIDs = append(duplicateIDs, existing[index].ID)
			}
		}
		if len(duplicateIDs) > 0 {
			if err := tx.Where("id IN ?", duplicateIDs).Delete(&models.BillingConfig{}).Error; err != nil {
				return nil, err
			}
		}
		result = append(result, primary)
	}

	sort.Slice(result, func(i, j int) bool {
		return strings.ToLower(result[i].PosName) < strings.ToLower(result[j].PosName)
	})
	return result, nil
}

func mergeBillingConfigValues(primary, duplicate models.BillingConfig) models.BillingConfig {
	if primary.IncludeInReports == nil && duplicate.IncludeInReports != nil {
		primary.IncludeInReports = duplicate.IncludeInReports
	}
	if primary.Arriendo == 0 {
		primary.Arriendo = duplicate.Arriendo
	}
	if primary.Internet == 0 {
		primary.Internet = duplicate.Internet
	}
	if primary.Luz == 0 {
		primary.Luz = duplicate.Luz
	}
	if primary.Gas == 0 {
		primary.Gas = duplicate.Gas
	}
	if primary.Agua == 0 {
		primary.Agua = duplicate.Agua
	}
	primary.LuzAplica = primary.LuzAplica || duplicate.LuzAplica
	primary.GasAplica = primary.GasAplica || duplicate.GasAplica
	primary.AguaAplica = primary.AguaAplica || duplicate.AguaAplica
	return primary
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
