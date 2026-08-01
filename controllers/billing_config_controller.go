package controllers

import (
	"atm/models"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type billingConfigEntry struct {
	OdooPOSID        *int64  `json:"odoo_pos_id"`
	PosName          string  `json:"pos_name" binding:"required"`
	IncludeInReports *bool   `json:"include_in_reports"`
	Arriendo         float64 `json:"arriendo"`
	Internet         float64 `json:"internet"`
	Luz              float64 `json:"luz"`
	LuzAplica        bool    `json:"luz_aplica"`
	Gas              float64 `json:"gas"`
	GasAplica        bool    `json:"gas_aplica"`
	Agua             float64 `json:"agua"`
	AguaAplica       bool    `json:"agua_aplica"`
}

func GetBillingConfigs(c *gin.Context) {
	forceRefresh := c.Query("refresh") == "1" || strings.EqualFold(c.Query("refresh"), "true")
	requireOdoo := c.Query("require_odoo") == "1" || strings.EqualFold(c.Query("require_odoo"), "true")
	selection, err := loadBillingPOSSelection(forceRefresh, requireOdoo)
	if err != nil {
		status := http.StatusInternalServerError
		if requireOdoo {
			status = http.StatusBadGateway
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.Header("X-Billing-POS-Source", selection.Source)
	c.JSON(http.StatusOK, selection.Configs)
}

func SaveBillingConfigs(c *gin.Context) {
	var body struct {
		Entries []billingConfigEntry `json:"entries" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	currentSelection, err := loadBillingPOSSelection(true, false)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo cargar el catálogo de puntos de venta", "detalle": err.Error()})
		return
	}
	if len(currentSelection.Configs) != len(billingReportPOSCatalog) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "El catálogo de informes está incompleto"})
		return
	}

	currentByKey := make(map[string]models.BillingConfig, len(currentSelection.Configs))
	for _, cfg := range currentSelection.Configs {
		currentByKey[billingPOSKey(cfg.PosName)] = cfg
	}
	entriesByKey := make(map[string]billingConfigEntry, len(body.Entries))
	duplicateNames := make([]string, 0)
	unknownNames := make([]string, 0)
	for _, e := range body.Entries {
		key := billingPOSKey(e.PosName)
		current, existsInCatalog := currentByKey[key]
		if !existsInCatalog {
			unknownNames = append(unknownNames, strings.TrimSpace(e.PosName))
			continue
		}
		if _, duplicate := entriesByKey[key]; duplicate {
			duplicateNames = append(duplicateNames, current.PosName)
			continue
		}
		e.PosName = current.PosName
		entriesByKey[key] = e
	}

	missingNames := make([]string, 0)
	for key, cfg := range currentByKey {
		if _, submitted := entriesByKey[key]; !submitted {
			missingNames = append(missingNames, cfg.PosName)
		}
	}
	if len(duplicateNames) > 0 || len(unknownNames) > 0 || len(missingNames) > 0 {
		sort.Strings(duplicateNames)
		sort.Strings(unknownNames)
		sort.Strings(missingNames)
		c.JSON(http.StatusConflict, gin.H{
			"error":           "La lista enviada no coincide con el catálogo completo de informes.",
			"duplicate_names": duplicateNames,
			"unknown_names":   unknownNames,
			"missing_names":   missingNames,
			"configs":         currentSelection.Configs,
		})
		return
	}

	if err := DB.Transaction(func(tx *gorm.DB) error {
		dbIDs := make([]uint, 0, len(currentByKey))
		for _, cfg := range currentByKey {
			if cfg.ID > 0 {
				dbIDs = append(dbIDs, cfg.ID)
			}
		}
		var locked []models.BillingConfig
		if len(dbIDs) > 0 {
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ?", dbIDs).Find(&locked).Error; err != nil {
				return err
			}
			if len(locked) != len(dbIDs) {
				return fmt.Errorf("la configuración cambió durante el guardado: esperados=%d encontrados=%d", len(dbIDs), len(locked))
			}
		}
		lockedByID := make(map[uint]models.BillingConfig, len(locked))
		for _, cfg := range locked {
			lockedByID[cfg.ID] = cfg
		}

		persisted := make([]models.BillingConfig, 0, len(currentSelection.Configs))
		for _, current := range currentSelection.Configs {
			entry := entriesByKey[billingPOSKey(current.PosName)]
			include := entry.IncludeInReports == nil || *entry.IncludeInReports
			if current.ID == 0 {
				current.IncludeInReports = &include
				current.Arriendo = entry.Arriendo
				current.Internet = entry.Internet
				current.Luz = entry.Luz
				current.LuzAplica = entry.LuzAplica
				current.Gas = entry.Gas
				current.GasAplica = entry.GasAplica
				current.Agua = entry.Agua
				current.AguaAplica = entry.AguaAplica
				if err := tx.Create(&current).Error; err != nil {
					return err
				}
				persisted = append(persisted, current)
				continue
			}

			cfg, found := lockedByID[current.ID]
			if !found {
				return fmt.Errorf("configuración %s no encontrada durante el guardado", current.PosName)
			}
			if err := tx.Model(&cfg).Updates(map[string]interface{}{
				"pos_name":           current.PosName,
				"include_in_reports": include,
				"arriendo":           entry.Arriendo,
				"internet":           entry.Internet,
				"luz":                entry.Luz,
				"luz_aplica":         entry.LuzAplica,
				"gas":                entry.Gas,
				"gas_aplica":         entry.GasAplica,
				"agua":               entry.Agua,
				"agua_aplica":        entry.AguaAplica,
			}).Error; err != nil {
				return err
			}
			cfg.PosName = current.PosName
			cfg.IncludeInReports = &include
			persisted = append(persisted, cfg)
		}
		return verifyBillingConfigSelection(persisted, entriesByKey)
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	verifiedSelection, err := loadBillingPOSSelection(false, false)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := verifyBillingConfigSelection(verifiedSelection.Configs, entriesByKey); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "La configuración se guardó pero no superó la verificación final", "detalle": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":             "ok",
		"saved":              len(entriesByKey),
		"verified":           true,
		"configs":            verifiedSelection.Configs,
		"selected_pos_ids":   verifiedSelection.SelectedIDs,
		"selected_pos_names": verifiedSelection.SelectedNames,
	})
}

func verifyBillingConfigSelection(configs []models.BillingConfig, expected map[string]billingConfigEntry) error {
	if len(configs) != len(expected) {
		return fmt.Errorf("cantidad distinta: esperados=%d encontrados=%d", len(expected), len(configs))
	}
	seen := make(map[string]struct{}, len(configs))
	for _, cfg := range configs {
		key := billingPOSKey(cfg.PosName)
		entry, ok := expected[key]
		if !ok {
			return fmt.Errorf("POS inesperado: %s", cfg.PosName)
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("POS duplicado: %s", cfg.PosName)
		}
		seen[key] = struct{}{}
		expectedIncluded := entry.IncludeInReports == nil || *entry.IncludeInReports
		actualIncluded := cfg.IncludeInReports == nil || *cfg.IncludeInReports
		if expectedIncluded != actualIncluded {
			return fmt.Errorf("selección no persistida para %s", cfg.PosName)
		}
	}
	return nil
}

// ---- Fixed Costs CRUD ----

func GetFixedCosts(c *gin.Context) {
	pos := normalizeBillingPOSName(c.Query("pos"))
	selection, err := loadBillingPOSSelection(false, false)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	var costs []models.BillingFixedCost
	q := DB.Order("sort_order, id")
	if err := q.Find(&costs).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	costs = normalizeFixedCostsForResponse(costs)
	filtered := make([]models.BillingFixedCost, 0, len(costs))
	for _, cost := range costs {
		currentName, included := selection.selectedName(cost.PosName)
		if !included {
			continue
		}
		if pos != "" && billingPOSKey(pos) != billingPOSKey(currentName) {
			continue
		}
		cost.PosName = currentName
		filtered = append(filtered, cost)
	}
	c.JSON(http.StatusOK, filtered)
}

func normalizeFixedCostsForResponse(costs []models.BillingFixedCost) []models.BillingFixedCost {
	result := make([]models.BillingFixedCost, 0, len(costs))
	seen := make(map[string]int, len(costs))

	for _, cost := range costs {
		canonicalPos := normalizeBillingPOSName(cost.PosName)
		if canonicalPos == "" {
			continue
		}
		cost.PosName = canonicalPos

		key := fmt.Sprintf("%s|%s|%.2f|%t",
			strings.ToLower(canonicalPos),
			strings.ToLower(strings.TrimSpace(cost.Name)),
			cost.Amount,
			cost.Active,
		)
		if _, exists := seen[key]; exists {
			continue
		}

		seen[key] = len(result)
		result = append(result, cost)
	}

	return result
}

func CreateFixedCost(c *gin.Context) {
	var fc models.BillingFixedCost
	if err := c.ShouldBindJSON(&fc); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if fc.PosName == "" || fc.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "pos_name y name son requeridos"})
		return
	}
	selection, err := loadBillingPOSSelection(false, false)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	currentName, included := selection.selectedName(fc.PosName)
	if !included {
		c.JSON(http.StatusConflict, gin.H{"error": "El punto de venta no está seleccionado para informes"})
		return
	}
	fc.PosName = currentName
	if err := DB.Create(&fc).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, fc)
}

func UpdateFixedCost(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id inválido"})
		return
	}
	var fc models.BillingFixedCost
	if err := DB.First(&fc, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "No encontrado"})
		return
	}
	var patch map[string]interface{}
	if err := c.BindJSON(&patch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Bad JSON"})
		return
	}
	// Only allow safe fields
	allowed := map[string]bool{"name": true, "amount": true, "active": true, "sort_order": true}
	updates := make(map[string]interface{})
	for k, v := range patch {
		if allowed[k] {
			updates[k] = v
		}
	}
	DB.Model(&fc).Updates(updates)
	c.JSON(http.StatusOK, fc)
}

func DeleteFixedCost(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id inválido"})
		return
	}
	if err := DB.Delete(&models.BillingFixedCost{}, id).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// SyncBillingPOSAlias consolida datos guardados con un nombre antiguo de POS hacia el nombre actual.
func SyncBillingPOSAlias(c *gin.Context) {
	var body struct {
		OldPosName     string `json:"old_pos_name" binding:"required"`
		CurrentPosName string `json:"current_pos_name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	oldPos := baseBillingPOSName(body.OldPosName)
	currentPos := baseBillingPOSName(body.CurrentPosName)
	if oldPos == "" || currentPos == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "old_pos_name y current_pos_name son requeridos"})
		return
	}
	if strings.EqualFold(oldPos, currentPos) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "El nombre antiguo y el actual no pueden ser iguales"})
		return
	}

	now := time.Now()
	type syncCounts struct {
		BillingConfigs           int64 `json:"billing_configs"`
		BillingFixedCosts        int64 `json:"billing_fixed_costs"`
		BillingFixedCostArchived int64 `json:"billing_fixed_costs_archived_duplicates"`
		GastosLocales            int64 `json:"gastos_locales"`
		BillingMonthlies         int64 `json:"billing_monthlies"`
		EmployeePOSAssignments   int64 `json:"employee_pos_assignments"`
		BillingNominaAssignments int64 `json:"billing_nomina_assignments"`
		BillingGastoExclusions   int64 `json:"billing_gasto_exclusions"`
	}
	counts := syncCounts{}

	if err := DB.Transaction(func(tx *gorm.DB) error {
		alias := models.BillingPOSAlias{
			OldPosName:     oldPos,
			CurrentPosName: currentPos,
			Active:         true,
			LastSyncedAt:   &now,
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "old_pos_name"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"current_pos_name": currentPos,
				"active":           true,
				"last_synced_at":   &now,
				"updated_at":       now,
			}),
		}).Create(&alias).Error; err != nil {
			return err
		}

		if err := syncBillingConfigPOSName(tx, oldPos, currentPos, &counts.BillingConfigs); err != nil {
			return err
		}
		if err := syncBillingFixedCostsPOSName(tx, oldPos, currentPos, &counts.BillingFixedCosts, &counts.BillingFixedCostArchived); err != nil {
			return err
		}
		if result := tx.Model(&models.GastoLocal{}).Where("local = ?", oldPos).Update("local", currentPos); result.Error != nil {
			return result.Error
		} else {
			counts.GastosLocales = result.RowsAffected
		}
		if err := syncBillingMonthliesPOSName(tx, oldPos, currentPos, &counts.BillingMonthlies); err != nil {
			return err
		}
		if err := syncEmployeePOSAssignmentsPOSName(tx, oldPos, currentPos, &counts.EmployeePOSAssignments); err != nil {
			return err
		}
		if err := syncBillingNominaAssignmentsPOSName(tx, oldPos, currentPos, &counts.BillingNominaAssignments); err != nil {
			return err
		}
		if err := syncBillingGastoExclusionsPOSName(tx, oldPos, currentPos, &counts.BillingGastoExclusions); err != nil {
			return err
		}
		return nil
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo sincronizar el POS", "detalle": err.Error()})
		return
	}

	invalidateBillingPOSAliasCache()
	c.JSON(http.StatusOK, gin.H{
		"status":           "ok",
		"old_pos_name":     oldPos,
		"current_pos_name": currentPos,
		"updated":          counts,
	})
}

func syncBillingConfigPOSName(tx *gorm.DB, oldPos, currentPos string, count *int64) error {
	var oldCfg models.BillingConfig
	err := tx.Where("pos_name = ?", oldPos).First(&oldCfg).Error
	if err == gorm.ErrRecordNotFound {
		return nil
	}
	if err != nil {
		return err
	}

	var currentCfg models.BillingConfig
	err = tx.Where("pos_name = ?", currentPos).First(&currentCfg).Error
	if err == gorm.ErrRecordNotFound {
		result := tx.Model(&models.BillingConfig{}).Where("id = ?", oldCfg.ID).Update("pos_name", currentPos)
		*count += result.RowsAffected
		return result.Error
	}
	if err != nil {
		return err
	}

	updates := map[string]interface{}{}
	if currentCfg.IncludeInReports == nil && oldCfg.IncludeInReports != nil {
		updates["include_in_reports"] = *oldCfg.IncludeInReports
	}
	if currentCfg.Arriendo == 0 && oldCfg.Arriendo != 0 {
		updates["arriendo"] = oldCfg.Arriendo
	}
	if currentCfg.Internet == 0 && oldCfg.Internet != 0 {
		updates["internet"] = oldCfg.Internet
	}
	if currentCfg.Luz == 0 && oldCfg.Luz != 0 {
		updates["luz"] = oldCfg.Luz
	}
	if !currentCfg.LuzAplica && oldCfg.LuzAplica {
		updates["luz_aplica"] = oldCfg.LuzAplica
	}
	if currentCfg.Gas == 0 && oldCfg.Gas != 0 {
		updates["gas"] = oldCfg.Gas
	}
	if !currentCfg.GasAplica && oldCfg.GasAplica {
		updates["gas_aplica"] = oldCfg.GasAplica
	}
	if currentCfg.Agua == 0 && oldCfg.Agua != 0 {
		updates["agua"] = oldCfg.Agua
	}
	if !currentCfg.AguaAplica && oldCfg.AguaAplica {
		updates["agua_aplica"] = oldCfg.AguaAplica
	}
	if len(updates) > 0 {
		if err := tx.Model(&currentCfg).Updates(updates).Error; err != nil {
			return err
		}
	}

	result := tx.Delete(&oldCfg)
	*count += result.RowsAffected
	return result.Error
}

func syncBillingFixedCostsPOSName(tx *gorm.DB, oldPos, currentPos string, updated, archived *int64) error {
	var oldCosts []models.BillingFixedCost
	if err := tx.Where("pos_name = ?", oldPos).Find(&oldCosts).Error; err != nil {
		return err
	}
	for _, cost := range oldCosts {
		var duplicate models.BillingFixedCost
		err := tx.Where("pos_name = ? AND LOWER(name) = ? AND amount = ? AND active = ?",
			currentPos, strings.ToLower(strings.TrimSpace(cost.Name)), cost.Amount, cost.Active).First(&duplicate).Error
		if err == nil {
			result := tx.Model(&cost).Updates(map[string]interface{}{"active": false, "sort_order": cost.SortOrder})
			if result.Error != nil {
				return result.Error
			}
			*archived += result.RowsAffected
			continue
		}
		if err != gorm.ErrRecordNotFound {
			return err
		}
		result := tx.Model(&cost).Update("pos_name", currentPos)
		if result.Error != nil {
			return result.Error
		}
		*updated += result.RowsAffected
	}
	return nil
}

func syncBillingMonthliesPOSName(tx *gorm.DB, oldPos, currentPos string, count *int64) error {
	var oldRows []models.BillingMonthly
	if err := tx.Where("pos_name = ?", oldPos).Find(&oldRows).Error; err != nil {
		return err
	}
	for _, oldRow := range oldRows {
		var currentRow models.BillingMonthly
		err := tx.Where("pos_name = ? AND year = ? AND month = ?", currentPos, oldRow.Year, oldRow.Month).First(&currentRow).Error
		if err == gorm.ErrRecordNotFound {
			result := tx.Model(&oldRow).Update("pos_name", currentPos)
			if result.Error != nil {
				return result.Error
			}
			*count += result.RowsAffected
			continue
		}
		if err != nil {
			return err
		}
		merged := mergeBillingMonthlyRow(currentRow, oldRow, currentPos)
		if err := tx.Save(&merged).Error; err != nil {
			return err
		}
		if err := tx.Delete(&oldRow).Error; err != nil {
			return err
		}
		*count++
	}
	return nil
}

func syncEmployeePOSAssignmentsPOSName(tx *gorm.DB, oldPos, currentPos string, count *int64) error {
	var oldRows []models.EmployeePOSAssignment
	if err := tx.Where("pos_name = ?", oldPos).Find(&oldRows).Error; err != nil {
		return err
	}
	for _, oldRow := range oldRows {
		var currentRow models.EmployeePOSAssignment
		err := tx.Where("user_id = ? AND pos_name = ?", oldRow.UserID, currentPos).First(&currentRow).Error
		if err == gorm.ErrRecordNotFound {
			result := tx.Model(&oldRow).Update("pos_name", currentPos)
			if result.Error != nil {
				return result.Error
			}
			*count += result.RowsAffected
			continue
		}
		if err != nil {
			return err
		}
		currentRow.CommissionPercentage += oldRow.CommissionPercentage
		if err := tx.Save(&currentRow).Error; err != nil {
			return err
		}
		if err := tx.Delete(&oldRow).Error; err != nil {
			return err
		}
		*count++
	}
	return nil
}

func syncBillingNominaAssignmentsPOSName(tx *gorm.DB, oldPos, currentPos string, count *int64) error {
	var oldRows []models.BillingNominaAssignment
	if err := tx.Where("pos_name = ?", oldPos).Find(&oldRows).Error; err != nil {
		return err
	}
	for _, oldRow := range oldRows {
		var currentRow models.BillingNominaAssignment
		err := tx.Where("year = ? AND month = ? AND pos_name = ? AND payment_id = ?",
			oldRow.Year, oldRow.Month, currentPos, oldRow.PaymentID).First(&currentRow).Error
		if err == gorm.ErrRecordNotFound {
			result := tx.Model(&oldRow).Update("pos_name", currentPos)
			if result.Error != nil {
				return result.Error
			}
			*count += result.RowsAffected
			continue
		}
		if err != nil {
			return err
		}
		if err := tx.Delete(&oldRow).Error; err != nil {
			return err
		}
		*count++
	}
	return nil
}

func syncBillingGastoExclusionsPOSName(tx *gorm.DB, oldPos, currentPos string, count *int64) error {
	var oldRows []models.BillingGastoExclusion
	if err := tx.Where("local = ?", oldPos).Find(&oldRows).Error; err != nil {
		return err
	}
	for _, oldRow := range oldRows {
		var currentRow models.BillingGastoExclusion
		err := tx.Where("year = ? AND month = ? AND local = ? AND fingerprint = ?",
			oldRow.Year, oldRow.Month, currentPos, oldRow.Fingerprint).First(&currentRow).Error
		if err == gorm.ErrRecordNotFound {
			result := tx.Model(&oldRow).Update("local", currentPos)
			if result.Error != nil {
				return result.Error
			}
			*count += result.RowsAffected
			continue
		}
		if err != nil {
			return err
		}
		if err := tx.Delete(&oldRow).Error; err != nil {
			return err
		}
		*count++
	}
	return nil
}
