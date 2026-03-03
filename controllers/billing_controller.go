package controllers

import (
	"atm/models"
	"atm/odoo"
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm/clause"
)

type billingMonthlyEntry struct {
	PosName string  `json:"pos_name" binding:"required"`
	Nomina  float64 `json:"nomina"`
}

// SaveBillingMonthly upserts gastos y margen por local/mes (legacy, mantiene compatibilidad).
func SaveBillingMonthly(c *gin.Context) {
	var body struct {
		Year    int                   `json:"year" binding:"required"`
		Month   int                   `json:"month" binding:"required,min=1,max=12"`
		Entries []billingMonthlyEntry `json:"entries" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	now := time.Now()
	toSave := make([]models.BillingMonthly, 0, len(body.Entries))
	for _, e := range body.Entries {
		toSave = append(toSave, models.BillingMonthly{
			PosName:   e.PosName,
			Year:      body.Year,
			Month:     body.Month,
			Nomina:    e.Nomina,
			UpdatedAt: now,
		})
	}

	if err := DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "pos_name"}, {Name: "year"}, {Name: "month"}},
		DoUpdates: clause.AssignmentColumns([]string{"nomina", "updated_at"}),
	}).Create(&toSave).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok", "saved": len(toSave)})
}

func resolveFortnightSlot(payment models.NominaPayment) int {
	startUTC := payment.PeriodStart.UTC()
	endUTC := payment.PeriodEnd.UTC()

	if startUTC.Day() > 15 {
		return 2
	}
	// Si cruza mes o el fin del periodo supera el día 15, trátalo como 2da quincena.
	if startUTC.Month() != endUTC.Month() || endUTC.Day() > 15 {
		return 2
	}
	return 1
}

func pickCanonicalMonthlyPayments(payments []models.NominaPayment) []models.NominaPayment {
	type userFortnightKey struct {
		UserID uint
		Year   int
		Month  int
		Slot   int
	}

	chosen := make(map[userFortnightKey]models.NominaPayment)
	for _, p := range payments {
		startUTC := p.PeriodStart.UTC()
		key := userFortnightKey{
			UserID: p.UserID,
			Year:   startUTC.Year(),
			Month:  int(startUTC.Month()),
			Slot:   resolveFortnightSlot(p),
		}

		current, exists := chosen[key]
		if !exists {
			chosen[key] = p
			continue
		}

		if p.CreatedAt.After(current.CreatedAt) || (p.CreatedAt.Equal(current.CreatedAt) && p.ID > current.ID) {
			chosen[key] = p
		}
	}

	result := make([]models.NominaPayment, 0, len(chosen))
	for _, p := range chosen {
		result = append(result, p)
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].UserID != result[j].UserID {
			return result[i].UserID < result[j].UserID
		}
		slotI := resolveFortnightSlot(result[i])
		slotJ := resolveFortnightSlot(result[j])
		if slotI != slotJ {
			return slotI < slotJ
		}
		return result[i].ID < result[j].ID
	})

	return result
}

func paymentIDSet(payments []models.NominaPayment) map[uint]bool {
	result := make(map[uint]bool, len(payments))
	for _, p := range payments {
		result[p.ID] = true
	}
	return result
}

func cleanAssignmentsOutsidePaymentSet(assignments []models.BillingNominaAssignment, allowedPaymentIDs map[uint]bool) []models.BillingNominaAssignment {
	staleIDs := make([]uint, 0)
	filtered := make([]models.BillingNominaAssignment, 0, len(assignments))

	for _, a := range assignments {
		if !allowedPaymentIDs[a.PaymentID] {
			staleIDs = append(staleIDs, a.ID)
			continue
		}
		filtered = append(filtered, a)
	}

	if len(staleIDs) > 0 {
		DB.Where("id IN ?", staleIDs).Delete(&models.BillingNominaAssignment{})
	}

	return filtered
}

// isPOSIncludedInReports define si un POS debe incluirse en informes.
// Si no existe configuración explícita, se considera incluido por defecto.
func isPOSIncludedInReports(cfgMap map[string]models.BillingConfig, pos string) bool {
	cfg, ok := cfgMap[pos]
	if !ok || cfg.IncludeInReports == nil {
		return true
	}
	return *cfg.IncludeInReports
}

// normalizeBillingPOSName unifica nombres de local/POS para cruces en billing.
// Ej: "Bodega (Fabian Martin)" -> "Bodega"
func normalizeBillingPOSName(name string) string {
	clean := strings.TrimSpace(name)
	if idx := strings.Index(clean, " ("); idx != -1 {
		clean = clean[:idx]
	}
	return strings.TrimSpace(clean)
}

type fixedCostTotals struct {
	Arriendo  float64
	Servicios float64
}

type movementGastoEntry struct {
	Fecha   time.Time
	Motivo  string
	Monto   float64
	Usuario string
}

func billingExpenseFingerprint(fecha time.Time, monto float64, motivo, usuario string) string {
	return fmt.Sprintf("%d|%.2f|%s|%s",
		fecha.UTC().Unix(),
		monto,
		strings.ToLower(strings.TrimSpace(motivo)),
		strings.ToLower(strings.TrimSpace(usuario)),
	)
}

func billingExcludedExpenseKey(local, fingerprint string) string {
	return fmt.Sprintf("%s|%s",
		strings.ToLower(normalizeBillingPOSName(local)),
		strings.ToLower(strings.TrimSpace(fingerprint)),
	)
}

func loadBillingExclusionSet(year, month int) (map[string]struct{}, error) {
	var rows []models.BillingGastoExclusion
	if err := DB.Where("year = ? AND month = ?", year, month).Find(&rows).Error; err != nil {
		return nil, err
	}

	result := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		key := billingExcludedExpenseKey(row.Local, row.Fingerprint)
		result[key] = struct{}{}
	}
	return result, nil
}

// getCommonExpensesBetween construye gastos variables combinando:
// 1) gastos_locales existentes
// 2) transacciones de movements marcadas como gastos operativos, asignadas por defecto a Bodega
// evitando duplicados cuando ya existe un gasto_local equivalente.
func getCommonExpensesBetween(start, end time.Time) ([]models.GastoLocal, error) {
	year := start.Year()
	month := int(start.Month())

	excludedSet, err := loadBillingExclusionSet(year, month)
	if err != nil {
		return nil, err
	}

	var sourceGastos []models.GastoLocal
	if err := DB.Where("fecha >= ? AND fecha < ?", start, end).
		Order("local asc, fecha asc").
		Find(&sourceGastos).Error; err != nil {
		return nil, err
	}

	gastos := make([]models.GastoLocal, 0, len(sourceGastos))
	fingerprints := make(map[string]struct{}, len(sourceGastos))
	for _, g := range sourceGastos {
		fp := billingExpenseFingerprint(g.Fecha, g.Monto, g.Motivo, g.Usuario)
		excludedKey := billingExcludedExpenseKey(g.Local, fp)
		if _, excluded := excludedSet[excludedKey]; excluded {
			continue
		}
		gastos = append(gastos, g)
		fingerprints[fp] = struct{}{}
	}

	var movementGastos []movementGastoEntry
	if err := DB.Table("transacciones t").
		Select("t.fecha as fecha, t.descripcion as motivo, t.monto as monto, t.usuario as usuario").
		Joins("JOIN categorias c ON c.id = t.categoria_id").
		Where("t.fecha >= ? AND t.fecha < ?", start, end).
		Where("c.tipo = ? AND c.is_gasto_operativo = ?", "EGRESO", true).
		Order("t.fecha asc").
		Scan(&movementGastos).Error; err != nil {
		return nil, err
	}

	for _, mg := range movementGastos {
		key := billingExpenseFingerprint(mg.Fecha, mg.Monto, mg.Motivo, mg.Usuario)
		excludedKey := billingExcludedExpenseKey("Bodega", key)
		if _, excluded := excludedSet[excludedKey]; excluded {
			continue
		}
		if _, exists := fingerprints[key]; exists {
			continue
		}
		gastos = append(gastos, models.GastoLocal{
			Local:   "Bodega",
			Fecha:   mg.Fecha,
			Tipo:    "GASTO_OPERATIVO_MOVEMENT",
			Motivo:  mg.Motivo,
			Monto:   mg.Monto,
			Usuario: mg.Usuario,
		})
		fingerprints[key] = struct{}{}
	}

	sort.Slice(gastos, func(i, j int) bool {
		posI := strings.ToLower(normalizeBillingPOSName(gastos[i].Local))
		posJ := strings.ToLower(normalizeBillingPOSName(gastos[j].Local))
		if posI == posJ {
			return gastos[i].Fecha.Before(gastos[j].Fecha)
		}
		return posI < posJ
	})

	return gastos, nil
}

// getFixedCostTotalsByPOS suma los gastos fijos activos por POS.
// Regla de clasificación: si el nombre contiene "arriendo", va a Arriendo; el resto a Servicios.
func getFixedCostTotalsByPOS() map[string]fixedCostTotals {
	var costs []models.BillingFixedCost
	DB.Where("active = ?", true).Find(&costs)

	result := make(map[string]fixedCostTotals)
	for _, c := range costs {
		pos := strings.TrimSpace(c.PosName)
		if pos == "" {
			continue
		}
		totals := result[pos]
		name := strings.ToLower(strings.TrimSpace(c.Name))
		if strings.Contains(name, "arriendo") {
			totals.Arriendo += c.Amount
		} else {
			totals.Servicios += c.Amount
		}
		result[pos] = totals
	}

	return result
}

// getNominaPerPOS calcula nómina por POS desde asignaciones manuales (billing_nomina_assignments)
func getNominaPerPOS(year, month int) map[string]float64 {
	var assignments []models.BillingNominaAssignment
	DB.Where("year = ? AND month = ?", year, month).Find(&assignments)

	if len(assignments) == 0 {
		return make(map[string]float64)
	}

	start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	var payments []models.NominaPayment
	DB.Where("period_start >= ? AND period_start < ?", start, end).Find(&payments)
	payments = pickCanonicalMonthlyPayments(payments)

	validIDs := paymentIDSet(payments)
	paymentPaid := make(map[uint]int64)
	for _, p := range payments {
		paymentPaid[p.ID] = p.TotalPaid
	}

	assignments = cleanAssignmentsOutsidePaymentSet(assignments, validIDs)

	result := make(map[string]float64)
	for _, a := range assignments {
		if paid, ok := paymentPaid[a.PaymentID]; ok {
			result[a.PosName] += float64(paid)
		}
	}
	return result
}

// getNominaPerPOSBulk calcula nómina por POS para todos los meses de un año en sólo 2 queries.
// Retorna map[month]map[posName]total
func getNominaPerPOSBulk(year int) map[int]map[string]float64 {
	result := make(map[int]map[string]float64)

	// 1 query: todas las asignaciones del año
	var assignments []models.BillingNominaAssignment
	DB.Where("year = ?", year).Find(&assignments)

	if len(assignments) == 0 {
		return result
	}

	// 1 query: todos los pagos del año y normalización a máximo 2 quincenas por empleado/mes
	start := time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(year+1, time.January, 1, 0, 0, 0, 0, time.UTC)
	var payments []models.NominaPayment
	DB.Where("period_start >= ? AND period_start < ?", start, end).Find(&payments)
	payments = pickCanonicalMonthlyPayments(payments)

	paymentMap := make(map[uint]int64)
	for _, p := range payments {
		paymentMap[p.ID] = p.TotalPaid
	}

	validIDs := paymentIDSet(payments)
	assignments = cleanAssignmentsOutsidePaymentSet(assignments, validIDs)

	// Agrupar por mes + POS
	for _, a := range assignments {
		if paid, ok := paymentMap[a.PaymentID]; ok {
			if result[a.Month] == nil {
				result[a.Month] = make(map[string]float64)
			}
			result[a.Month][a.PosName] += float64(paid)
		}
	}
	return result
}

// GetNominaByPOS devuelve desglose de nómina asignada por POS con detalle de empleados (agrupado)
func GetNominaByPOS(c *gin.Context) {
	yearStr := c.Query("year")
	monthStr := c.Query("month")
	year, err := strconv.Atoi(yearStr)
	if err != nil || year < 2000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "year inválido"})
		return
	}
	month, err := strconv.Atoi(monthStr)
	if err != nil || month < 1 || month > 12 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "month inválido"})
		return
	}

	var assignments []models.BillingNominaAssignment
	DB.Where("year = ? AND month = ?", year, month).Find(&assignments)

	if len(assignments) == 0 {
		c.JSON(http.StatusOK, gin.H{})
		return
	}

	start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	var payments []models.NominaPayment
	DB.Preload("User").Where("period_start >= ? AND period_start < ?", start, end).Find(&payments)
	payments = pickCanonicalMonthlyPayments(payments)

	paymentMap := make(map[uint]models.NominaPayment)
	for _, p := range payments {
		paymentMap[p.ID] = p
	}

	validIDs := paymentIDSet(payments)
	assignments = cleanAssignmentsOutsidePaymentSet(assignments, validIDs)

	type employeeEntry struct {
		UserID    uint   `json:"user_id"`
		Name      string `json:"name"`
		TotalPaid int64  `json:"total_paid"`
		Count     int    `json:"count"` // quincenas asignadas
	}
	type posNomina struct {
		Employees []employeeEntry `json:"employees"`
		Total     int64           `json:"total"`
	}

	// Agrupar por POS + UserID para sumar quincenas del mismo empleado
	type posUserKey struct {
		PosName string
		UserID  uint
	}
	empAgg := make(map[posUserKey]*employeeEntry)
	posAgg := make(map[string]*posNomina)

	for _, a := range assignments {
		p, found := paymentMap[a.PaymentID]
		if !found {
			continue
		}
		key := posUserKey{a.PosName, a.UserID}
		emp, exists := empAgg[key]
		if !exists {
			name := p.User.Name
			if name == "" {
				name = p.User.FullName
			}
			if name == "" {
				name = p.User.Username
			}
			emp = &employeeEntry{
				UserID: a.UserID,
				Name:   name,
			}
			empAgg[key] = emp
		}
		emp.TotalPaid += p.TotalPaid
		emp.Count++

		if posAgg[a.PosName] == nil {
			posAgg[a.PosName] = &posNomina{}
		}
		posAgg[a.PosName].Total += p.TotalPaid
	}

	result := make(map[string]posNomina)
	for posName, pn := range posAgg {
		entry := posNomina{Total: pn.Total}
		for key, emp := range empAgg {
			if key.PosName == posName {
				entry.Employees = append(entry.Employees, *emp)
			}
		}
		result[posName] = entry
	}

	c.JSON(http.StatusOK, result)
}

// GetAvailableNominaPayments devuelve nóminas del mes agrupadas por empleado (suma de quincenas)
func GetAvailableNominaPayments(c *gin.Context) {
	yearStr := c.Query("year")
	monthStr := c.Query("month")
	year, err := strconv.Atoi(yearStr)
	if err != nil || year < 2000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "year inválido"})
		return
	}
	month, err := strconv.Atoi(monthStr)
	if err != nil || month < 1 || month > 12 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "month inválido"})
		return
	}

	// Usar rango amplio: desde el día 1 00:00:00 hasta el primer día del mes siguiente 00:00:00
	// Se usa time.UTC porque el frontend envía fechas en UTC y GORM con loc=Local las convierte
	// al escribir/leer. Usar UTC aquí asegura que la conversión sea consistente.
	start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)

	// Obtener todos los pagos del mes
	var payments []models.NominaPayment
	DB.Preload("User").
		Where("period_start >= ? AND period_start < ?", start, end).
		Find(&payments)
	payments = pickCanonicalMonthlyPayments(payments)
	validPaymentIDs := paymentIDSet(payments)

	// Obtener pagos ya asignados este mes y limpiar asignaciones fuera del set canónico
	var assigned []models.BillingNominaAssignment
	DB.Where("year = ? AND month = ?", year, month).Find(&assigned)
	assigned = cleanAssignmentsOutsidePaymentSet(assigned, validPaymentIDs)

	assignedUserIDs := make(map[uint]string) // user_id -> pos_name
	for _, a := range assigned {
		assignedUserIDs[a.UserID] = a.PosName
	}

	// Agrupar por user_id: sumar total_paid de todas las quincenas
	type employeeAgg struct {
		UserID     uint
		Name       string
		TotalPaid  int64
		PaymentIDs []uint
		Count      int // número de quincenas
	}
	aggMap := make(map[uint]*employeeAgg)
	for _, p := range payments {
		agg, exists := aggMap[p.UserID]
		if !exists {
			name := p.User.Name
			if name == "" {
				name = p.User.FullName
			}
			if name == "" {
				name = p.User.Username
			}
			agg = &employeeAgg{
				UserID: p.UserID,
				Name:   name,
			}
			aggMap[p.UserID] = agg
		}
		agg.TotalPaid += p.TotalPaid
		agg.PaymentIDs = append(agg.PaymentIDs, p.ID)
		agg.Count++
	}

	type employeeEntry struct {
		UserID     uint   `json:"user_id"`
		Name       string `json:"name"`
		TotalPaid  int64  `json:"total_paid"`
		PaymentIDs []uint `json:"payment_ids"`
		Count      int    `json:"count"`       // quincenas (1 o 2)
		AssignedTo string `json:"assigned_to"` // POS name si ya está asignado, "" si disponible
	}

	var result []employeeEntry
	for _, agg := range aggMap {
		assignedTo := ""
		if pos, ok := assignedUserIDs[agg.UserID]; ok {
			assignedTo = pos
		}
		result = append(result, employeeEntry{
			UserID:     agg.UserID,
			Name:       agg.Name,
			TotalPaid:  agg.TotalPaid,
			PaymentIDs: agg.PaymentIDs,
			Count:      agg.Count,
			AssignedTo: assignedTo,
		})
	}

	c.JSON(http.StatusOK, result)
}

// AssignNominaToPOS asigna todos los pagos de nómina de un empleado a un POS para el billing
func AssignNominaToPOS(c *gin.Context) {
	var input struct {
		Year    int    `json:"year" binding:"required"`
		Month   int    `json:"month" binding:"required,min=1,max=12"`
		PosName string `json:"pos_name" binding:"required"`
		UserID  uint   `json:"user_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Verificar que este empleado no esté ya asignado a otro POS este mes
	var existing models.BillingNominaAssignment
	if err := DB.Where("year = ? AND month = ? AND user_id = ?", input.Year, input.Month, input.UserID).First(&existing).Error; err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "Este empleado ya está asignado a " + existing.PosName})
		return
	}

	// Obtener todos los pagos del mes para este empleado (UTC para coincidir con frontend)
	start := time.Date(input.Year, time.Month(input.Month), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	var payments []models.NominaPayment
	DB.Where("user_id = ? AND period_start >= ? AND period_start < ?", input.UserID, start, end).Find(&payments)
	payments = pickCanonicalMonthlyPayments(payments)

	if len(payments) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "No hay pagos de nómina para este empleado en este mes"})
		return
	}

	// Crear una asignación por cada pago
	var created []models.BillingNominaAssignment
	for _, p := range payments {
		assignment := models.BillingNominaAssignment{
			Year:      input.Year,
			Month:     input.Month,
			PosName:   input.PosName,
			PaymentID: p.ID,
			UserID:    input.UserID,
		}
		if err := DB.Create(&assignment).Error; err != nil {
			// Rollback: eliminar las que ya se crearon
			for _, a := range created {
				DB.Delete(&a)
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Error guardando asignación"})
			return
		}
		created = append(created, assignment)
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok", "assigned": len(created)})
}

// RemoveNominaFromPOS elimina todas las asignaciones de nómina de un empleado de un POS
func RemoveNominaFromPOS(c *gin.Context) {
	var input struct {
		Year   int  `json:"year" binding:"required"`
		Month  int  `json:"month" binding:"required,min=1,max=12"`
		UserID uint `json:"user_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	result := DB.Where("year = ? AND month = ? AND user_id = ?", input.Year, input.Month, input.UserID).
		Delete(&models.BillingNominaAssignment{})
	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Asignación no encontrada"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// ResetNominaAssignmentsForMonth elimina todas las asignaciones de nómina del mes para todos los POS.
// Se usa al reiniciar un informe para dejar empleados/pagos sin asignar.
func ResetNominaAssignmentsForMonth(c *gin.Context) {
	var input struct {
		Year  int `json:"year" binding:"required"`
		Month int `json:"month" binding:"required,min=1,max=12"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	result := DB.Where("year = ? AND month = ?", input.Year, input.Month).
		Delete(&models.BillingNominaAssignment{})
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error reseteando asignaciones de nómina"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"removed": result.RowsAffected,
	})
}

// GetBillingMonthly devuelve ventas Odoo + gastos/margen para un mes/año.
func GetBillingMonthly(c *gin.Context) {
	yearStr := c.DefaultQuery("year", strconv.Itoa(time.Now().Year()))
	year, err := strconv.Atoi(yearStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Año inválido"})
		return
	}
	monthStr := c.Query("month")
	var month int
	if monthStr != "" {
		month, err = strconv.Atoi(monthStr)
		if err != nil || month < 1 || month > 12 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Mes inválido"})
			return
		}
	}

	// Ventas y margen por local/mes desde Odoo
	ventas, margenOdoo, err := odoo.GetMonthlyBillingWithMargin(context.Background(),
		os.Getenv("ODOO_URL"), os.Getenv("ODOO_DB"), os.Getenv("ODOO_USER"), os.Getenv("ODOO_PASSWORD"), year)
	if err != nil {
		fmt.Printf("[billing] error obteniendo ventas/margen Odoo: %v\n", err)
	}

	// Gastos/margen guardados
	var rows []models.BillingMonthly
	tx := DB.Where("year = ?", year)
	if month > 0 {
		tx = tx.Where("month = ?", month)
	}
	if err := tx.Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Unir datos
	type respEntry struct {
		PosName            string     `json:"pos_name"`
		Year               int        `json:"year"`
		Month              int        `json:"month"`
		Venta              float64    `json:"venta"`
		Margen             float64    `json:"margen"`
		GastosComunes      float64    `json:"gastos_comunes"`
		Servicios          float64    `json:"servicios"`
		Nomina             float64    `json:"nomina"`
		NominaAuto         float64    `json:"nomina_auto"` // Calculada automáticamente desde pagos
		Arriendo           float64    `json:"arriendo"`
		UtilidadBruta      float64    `json:"utilidad_bruta"`
		ComisionAdmin      float64    `json:"comision_admin"`
		ComisionPorcentaje float64    `json:"comision_porcentaje"`
		UtilidadNeta       float64    `json:"utilidad_neta"`
		Confirmed          bool       `json:"confirmed"`
		ConfirmedAt        *time.Time `json:"confirmed_at"`
	}

	entries := []respEntry{}

	getVenta := func(pos string, m int) float64 {
		if ventas == nil {
			return 0
		}
		for monthName, val := range ventas[pos] {
			if monthNumberFromLabel(monthName) == m {
				return val
			}
		}
		return 0
	}

	getMargen := func(pos string, m int) float64 {
		if margenOdoo == nil {
			return 0
		}
		for monthName, val := range margenOdoo[pos] {
			if monthNumberFromLabel(monthName) == m {
				return val
			}
		}
		return 0
	}

	// Config de inclusión por POS
	var cfgs []models.BillingConfig
	if err := DB.Find(&cfgs).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	cfgMap := make(map[string]models.BillingConfig)
	for _, cfg := range cfgs {
		cfgMap[cfg.PosName] = cfg
	}
	fixedCostMap := getFixedCostTotalsByPOS()

	// Comisión: sumar % de EmployeePOSAssignment por POS
	var posAssignments []models.EmployeePOSAssignment
	DB.Find(&posAssignments)
	comisionPctMap := make(map[string]float64) // pos_name -> sum of commission %
	for _, a := range posAssignments {
		comisionPctMap[a.PosName] += a.CommissionPercentage
	}

	// Gastos comunes (gastos_locales + movimientos operativos detectados desde movements)
	start := time.Date(year, 1, 1, 0, 0, 0, 0, time.Local)
	end := time.Date(year+1, 1, 1, 0, 0, 0, 0, time.Local)
	gastosMap := make(map[string]map[int]float64)
	if gastos, err := getCommonExpensesBetween(start, end); err == nil {
		for _, g := range gastos {
			pos := normalizeBillingPOSName(g.Local)
			if pos == "" {
				continue
			}
			monthNum := int(g.Fecha.In(time.Local).Month())
			if gastosMap[pos] == nil {
				gastosMap[pos] = make(map[int]float64)
			}
			gastosMap[pos][monthNum] += g.Monto
		}
	}

	// Nómina por POS desde asignaciones de empleados + pagos parciales
	var nominaByPosMap map[int]map[string]float64
	if month > 0 {
		nominaByPosMap = map[int]map[string]float64{
			month: getNominaPerPOS(year, month),
		}
	} else {
		// Bulk: 2 queries en vez de 24 para todo el año
		nominaByPosMap = getNominaPerPOSBulk(year)
	}

	// index existing rows
	rowKey := func(pos string, y, m int) string { return fmt.Sprintf("%s-%d-%d", pos, y, m) }
	rowMap := make(map[string]models.BillingMonthly)
	for _, r := range rows {
		rowMap[rowKey(r.PosName, r.Year, r.Month)] = r
	}

	// gather all pos/month present either in ventas or rows
	posMonths := make(map[string]struct{})
	for pos, months := range ventas {
		for label := range months {
			mn := monthNumberFromLabel(label)
			if month > 0 && mn != month {
				continue
			}
			posMonths[rowKey(pos, year, mn)] = struct{}{}
		}
	}
	for _, r := range rows {
		if month > 0 && r.Month != month {
			continue
		}
		posMonths[rowKey(r.PosName, r.Year, r.Month)] = struct{}{}
	}
	for pos := range fixedCostMap {
		if month > 0 {
			posMonths[rowKey(pos, year, month)] = struct{}{}
			continue
		}
		for m := 1; m <= 12; m++ {
			posMonths[rowKey(pos, year, m)] = struct{}{}
		}
	}
	for pos, monthValues := range gastosMap {
		if month > 0 {
			if monthValues[month] != 0 {
				posMonths[rowKey(pos, year, month)] = struct{}{}
			}
			continue
		}
		for m, val := range monthValues {
			if val == 0 {
				continue
			}
			posMonths[rowKey(pos, year, m)] = struct{}{}
		}
	}
	for m, nominaByPos := range nominaByPosMap {
		if month > 0 && m != month {
			continue
		}
		for pos, val := range nominaByPos {
			if val == 0 {
				continue
			}
			posMonths[rowKey(pos, year, m)] = struct{}{}
		}
	}

	for key := range posMonths {
		parts := strings.Split(key, "-")
		if len(parts) < 3 {
			continue
		}
		// pos name might contain dashes, so rejoin all but last 2 parts
		pos := strings.Join(parts[:len(parts)-2], "-")
		m, _ := strconv.Atoi(parts[len(parts)-1])

		if !isPOSIncludedInReports(cfgMap, pos) {
			continue
		}

		venta := getVenta(pos, m)
		row := rowMap[key]
		rowMargen := row.Margen
		if rowMargen == 0 {
			rowMargen = getMargen(pos, m)
		}
		fixedTotals := fixedCostMap[pos]
		serviciosTot := fixedTotals.Servicios
		arriendo := fixedTotals.Arriendo

		// Gastos comunes: desde gastos_locales + movimientos operativos detectados
		var gastosComunes float64
		if gastosMap[pos] != nil {
			gastosComunes = gastosMap[pos][m]
		}

		// Nómina por POS desde asignaciones de empleados
		var nominaPerPos float64
		if nominaByPos, ok := nominaByPosMap[m]; ok {
			nominaPerPos = nominaByPos[pos]
		}

		gastosTot := gastosComunes + serviciosTot + nominaPerPos + arriendo
		utilidadBruta := rowMargen - gastosTot

		comisionPct := comisionPctMap[pos]
		comision := comisionPct / 100.0 * utilidadBruta
		if comision < 0 {
			comision = 0
		}
		utilidadNeta := utilidadBruta - comision

		// Si el informe ya fue confirmado, usar datos congelados
		if row.Confirmed {
			entries = append(entries, respEntry{
				PosName:            pos,
				Year:               year,
				Month:              m,
				Venta:              row.Venta,
				Margen:             row.Margen,
				GastosComunes:      row.GastosComunes,
				Servicios:          row.Servicios,
				Nomina:             row.Nomina,
				NominaAuto:         nominaPerPos,
				Arriendo:           row.Arriendo,
				UtilidadBruta:      row.UtilidadBruta,
				ComisionAdmin:      row.ComisionPorcentaje / 100.0 * row.UtilidadBruta,
				ComisionPorcentaje: row.ComisionPorcentaje,
				UtilidadNeta:       row.UtilidadBruta - (row.ComisionPorcentaje / 100.0 * row.UtilidadBruta),
				Confirmed:          true,
				ConfirmedAt:        row.ConfirmedAt,
			})
		} else {
			entries = append(entries, respEntry{
				PosName:            pos,
				Year:               year,
				Month:              m,
				Venta:              venta,
				Margen:             rowMargen,
				GastosComunes:      gastosComunes,
				Servicios:          serviciosTot,
				Nomina:             nominaPerPos,
				NominaAuto:         nominaPerPos,
				Arriendo:           arriendo,
				UtilidadBruta:      utilidadBruta,
				ComisionAdmin:      comision,
				ComisionPorcentaje: comisionPct,
				UtilidadNeta:       utilidadNeta,
				Confirmed:          false,
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"year":   year,
		"month":  month,
		"data":   entries,
		"source": "db+odoo",
	})
}

// ConfirmBillingMonthly confirma y congela el informe de un mes.
func ConfirmBillingMonthly(c *gin.Context) {
	var body struct {
		Year  int `json:"year" binding:"required"`
		Month int `json:"month" binding:"required,min=1,max=12"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Obtener ventas/margen de Odoo
	ventas, margenOdoo, err := odoo.GetMonthlyBillingWithMargin(context.Background(),
		os.Getenv("ODOO_URL"), os.Getenv("ODOO_DB"), os.Getenv("ODOO_USER"), os.Getenv("ODOO_PASSWORD"), body.Year)
	if err != nil {
		fmt.Printf("[billing] error obteniendo ventas/margen Odoo: %v\n", err)
	}

	getVenta := func(pos string) float64 {
		if ventas == nil {
			return 0
		}
		for monthName, val := range ventas[pos] {
			if monthNumberFromLabel(monthName) == body.Month {
				return val
			}
		}
		return 0
	}
	getMargen := func(pos string) float64 {
		if margenOdoo == nil {
			return 0
		}
		for monthName, val := range margenOdoo[pos] {
			if monthNumberFromLabel(monthName) == body.Month {
				return val
			}
		}
		return 0
	}

	// Configs
	var cfgs []models.BillingConfig
	DB.Find(&cfgs)
	cfgMap := make(map[string]models.BillingConfig)
	for _, cfg := range cfgs {
		cfgMap[cfg.PosName] = cfg
	}
	fixedCostMap := getFixedCostTotalsByPOS()

	// Gastos comunes (gastos_locales + movimientos operativos detectados)
	startMonth := time.Date(body.Year, time.Month(body.Month), 1, 0, 0, 0, 0, time.Local)
	endMonth := startMonth.AddDate(0, 1, 0)
	gastosMap := make(map[string]float64)
	if gastos, err := getCommonExpensesBetween(startMonth, endMonth); err == nil {
		for _, g := range gastos {
			pos := normalizeBillingPOSName(g.Local)
			if pos == "" {
				continue
			}
			gastosMap[pos] += g.Monto
		}
	}

	// Nómina por POS desde asignaciones de empleados + pagos parciales
	nominaByPos := getNominaPerPOS(body.Year, body.Month)

	// Recolectar todos los POS
	allPOS := make(map[string]struct{})
	if ventas != nil {
		for pos, months := range ventas {
			for label := range months {
				if monthNumberFromLabel(label) == body.Month {
					allPOS[pos] = struct{}{}
				}
			}
		}
	}
	for _, cfg := range cfgs {
		allPOS[cfg.PosName] = struct{}{}
	}
	for pos := range fixedCostMap {
		allPOS[pos] = struct{}{}
	}
	for pos := range gastosMap {
		allPOS[pos] = struct{}{}
	}
	for pos := range nominaByPos {
		allPOS[pos] = struct{}{}
	}

	// Comisión: sumar % de EmployeePOSAssignment por POS
	var posAssignments []models.EmployeePOSAssignment
	DB.Find(&posAssignments)
	comisionPctMap := make(map[string]float64)
	for _, a := range posAssignments {
		comisionPctMap[a.PosName] += a.CommissionPercentage
	}

	now := time.Now()
	var confirmed []models.BillingMonthly

	for pos := range allPOS {
		if !isPOSIncludedInReports(cfgMap, pos) {
			continue
		}
		venta := getVenta(pos)
		margen := getMargen(pos)
		fixedTotals := fixedCostMap[pos]
		serviciosTot := fixedTotals.Servicios
		arriendo := fixedTotals.Arriendo

		// Gastos comunes: desde gastos_locales + movimientos operativos detectados
		gastosComunes := gastosMap[pos]
		nominaForPos := nominaByPos[pos]
		gastosTot := gastosComunes + serviciosTot + nominaForPos + arriendo
		utilidadBruta := margen - gastosTot

		comisionPct := comisionPctMap[pos]

		row := models.BillingMonthly{
			PosName:            pos,
			Year:               body.Year,
			Month:              body.Month,
			GastosComunes:      gastosComunes,
			Servicios:          serviciosTot,
			Nomina:             nominaForPos,
			Arriendo:           arriendo,
			Margen:             margen,
			Confirmed:          true,
			ConfirmedAt:        &now,
			Venta:              venta,
			TotalGastos:        gastosTot,
			UtilidadBruta:      utilidadBruta,
			ComisionPorcentaje: comisionPct,
			UpdatedAt:          now,
		}

		if err := DB.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "pos_name"}, {Name: "year"}, {Name: "month"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"gastos_comunes", "servicios", "nomina", "arriendo", "margen",
				"confirmed", "confirmed_at", "venta", "total_gastos", "utilidad_bruta",
				"comision_porcentaje", "updated_at",
			}),
		}).Create(&row).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Error confirmando informe: " + err.Error()})
			return
		}

		confirmed = append(confirmed, row)
	}

	// Invalidar cache de billing Odoo para forzar datos frescos en próxima consulta
	odoo.InvalidateBillingCache(body.Year, 0)

	c.JSON(http.StatusOK, gin.H{"status": "ok", "confirmed": len(confirmed), "data": confirmed})
}

// DeleteBillingReport elimina un informe confirmado y revierte comisiones de nómina.
func DeleteBillingReport(c *gin.Context) {
	yearStr := c.Query("year")
	monthStr := c.Query("month")
	year, err := strconv.Atoi(yearStr)
	if err != nil || year < 2000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "year inválido"})
		return
	}
	month, err := strconv.Atoi(monthStr)
	if err != nil || month < 1 || month > 12 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "month inválido (1-12)"})
		return
	}

	// Buscar informes confirmados para este mes
	var reports []models.BillingMonthly
	if err := DB.Where("year = ? AND month = ? AND confirmed = ?", year, month, true).Find(&reports).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error buscando informes"})
		return
	}
	if len(reports) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "No hay informe confirmado para este mes"})
		return
	}

	// Revertir NominaPayments que recibieron comisión en la 2da quincena de este mes
	// 2da quincena: period_start entre día 16 y fin del mes
	secondFortnightStart := time.Date(year, time.Month(month), 16, 0, 0, 0, 0, time.UTC)
	secondFortnightEnd := time.Date(year, time.Month(month)+1, 1, 0, 0, 0, 0, time.UTC)

	var paymentsToRevert []models.NominaPayment
	DB.Where("period_start >= ? AND period_start < ? AND commission > 0 AND is_partial = ?",
		secondFortnightStart, secondFortnightEnd, false).Find(&paymentsToRevert)

	revertedCount := 0
	for _, p := range paymentsToRevert {
		p.TotalPaid -= p.Commission
		p.Commission = 0
		p.IsPartial = true
		if err := DB.Save(&p).Error; err != nil {
			fmt.Printf("[billing] error revirtiendo pago %d: %v\n", p.ID, err)
			continue
		}
		revertedCount++
	}

	// Eliminar los BillingMonthly rows
	deletedCount := len(reports)
	if err := DB.Where("year = ? AND month = ?", year, month).Delete(&models.BillingMonthly{}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error eliminando informes: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":            "ok",
		"deleted":           deletedCount,
		"payments_reverted": revertedCount,
	})
}

// GetEmployeeCommission calcula la comisión de un empleado para un mes confirmado.
func GetEmployeeCommission(c *gin.Context) {
	yearStr := c.Query("year")
	monthStr := c.Query("month")
	userIDStr := c.Query("user_id")

	year, _ := strconv.Atoi(yearStr)
	month, _ := strconv.Atoi(monthStr)
	userID, _ := strconv.Atoi(userIDStr)

	if year == 0 || month == 0 || userID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "year, month, user_id requeridos"})
		return
	}

	// Buscar asignaciones POS del empleado
	var assignments []models.EmployeePOSAssignment
	DB.Where("user_id = ?", userID).Find(&assignments)

	if len(assignments) == 0 {
		c.JSON(http.StatusOK, gin.H{"total": 0, "details": []interface{}{}, "confirmed": false})
		return
	}

	// Buscar informes confirmados del mes
	posNames := make([]string, 0, len(assignments))
	assignMap := make(map[string]float64)
	for _, a := range assignments {
		posNames = append(posNames, a.PosName)
		assignMap[a.PosName] = a.CommissionPercentage
	}

	var reports []models.BillingMonthly
	DB.Where("pos_name IN ? AND year = ? AND month = ? AND confirmed = ?", posNames, year, month, true).Find(&reports)

	if len(reports) == 0 {
		c.JSON(http.StatusOK, gin.H{"total": 0, "details": []interface{}{}, "confirmed": false})
		return
	}

	type detail struct {
		PosName       string  `json:"pos_name"`
		UtilidadBruta float64 `json:"utilidad_bruta"`
		Percentage    float64 `json:"percentage"`
		Commission    float64 `json:"commission"`
	}

	var total float64
	var details []detail
	for _, r := range reports {
		pct := assignMap[r.PosName]
		comision := pct / 100.0 * r.UtilidadBruta
		if comision < 0 {
			comision = 0
		}
		total += comision
		details = append(details, detail{
			PosName:       r.PosName,
			UtilidadBruta: r.UtilidadBruta,
			Percentage:    pct,
			Commission:    comision,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"total":     total,
		"details":   details,
		"confirmed": true,
	})
}

// --- Billing Status ---

// GetBillingStatus retorna el estado de confirmación por mes para un año
func GetBillingStatus(c *gin.Context) {
	yearStr := c.DefaultQuery("year", strconv.Itoa(time.Now().Year()))
	year, _ := strconv.Atoi(yearStr)

	type monthStatus struct {
		Month       int        `json:"month"`
		Confirmed   bool       `json:"confirmed"`
		ConfirmedAt *time.Time `json:"confirmed_at"`
		PosCount    int        `json:"pos_count"`
	}

	type dbResult struct {
		Month       int
		ConfirmedAt *time.Time
		Cnt         int
	}

	var results []dbResult
	DB.Model(&models.BillingMonthly{}).
		Select("month, MAX(confirmed_at) as confirmed_at, COUNT(*) as cnt").
		Where("year = ? AND confirmed = ?", year, true).
		Group("month").Scan(&results)

	statuses := make([]monthStatus, 0, len(results))
	for _, r := range results {
		statuses = append(statuses, monthStatus{
			Month:       r.Month,
			Confirmed:   true,
			ConfirmedAt: r.ConfirmedAt,
			PosCount:    r.Cnt,
		})
	}

	c.JSON(http.StatusOK, statuses)
}

// --- POS Assignment endpoints ---

// GetEmployeePOSAssignments lista las asignaciones POS de un empleado
func GetEmployeePOSAssignments(c *gin.Context) {
	id := c.Param("id")
	var assignments []models.EmployeePOSAssignment
	DB.Where("user_id = ?", id).Find(&assignments)
	c.JSON(http.StatusOK, assignments)
}

// SaveEmployeePOSAssignments reemplaza las asignaciones POS de un empleado
func SaveEmployeePOSAssignments(c *gin.Context) {
	id := c.Param("id")
	userID, _ := strconv.Atoi(id)
	if userID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID inválido"})
		return
	}

	var body struct {
		Assignments []struct {
			PosName              string  `json:"pos_name" binding:"required"`
			CommissionPercentage float64 `json:"commission_percentage"`
		} `json:"assignments"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Delete existing
	DB.Where("user_id = ?", userID).Delete(&models.EmployeePOSAssignment{})

	// Create new
	for _, a := range body.Assignments {
		if a.PosName == "" {
			continue
		}
		DB.Create(&models.EmployeePOSAssignment{
			UserID:               uint(userID),
			PosName:              a.PosName,
			CommissionPercentage: a.CommissionPercentage,
		})
	}

	// Return updated list
	var assignments []models.EmployeePOSAssignment
	DB.Where("user_id = ?", userID).Find(&assignments)
	c.JSON(http.StatusOK, assignments)
}

// GetAllPOSAssignments lista TODAS las asignaciones
func GetAllPOSAssignments(c *gin.Context) {
	var assignments []models.EmployeePOSAssignment
	DB.Find(&assignments)
	c.JSON(http.StatusOK, assignments)
}

// --- Gastos ---

// GetBillingGastos lista gastos variables (gastos_locales + movimientos operativos detectados) por local/mes.
func GetBillingGastos(c *gin.Context) {
	local := normalizeBillingPOSName(c.Query("pos"))
	if local == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "pos requerido"})
		return
	}
	yearStr := c.DefaultQuery("year", strconv.Itoa(time.Now().Year()))
	monthStr := c.DefaultQuery("month", strconv.Itoa(int(time.Now().Month())))
	year, err := strconv.Atoi(yearStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Año inválido"})
		return
	}
	month, err := strconv.Atoi(monthStr)
	if err != nil || month < 1 || month > 12 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Mes inválido"})
		return
	}

	start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.Local)
	end := start.AddDate(0, 1, 0)

	gastos, err := getCommonExpensesBetween(start, end)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	filtered := make([]models.GastoLocal, 0, len(gastos))
	target := strings.ToLower(local)
	for _, g := range gastos {
		if strings.ToLower(normalizeBillingPOSName(g.Local)) == target {
			filtered = append(filtered, g)
		}
	}

	c.JSON(http.StatusOK, filtered)
}

// CreateBillingGasto crea un gasto común sin imagen para un local/mes.
func CreateBillingGasto(c *gin.Context) {
	var body struct {
		Pos    string  `json:"pos" binding:"required"`
		Year   int     `json:"year" binding:"required"`
		Month  int     `json:"month" binding:"required,min=1,max=12"`
		Motivo string  `json:"motivo" binding:"required"`
		Monto  float64 `json:"monto" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	fecha := time.Date(body.Year, time.Month(body.Month), 1, 0, 0, 0, 0, time.Local)
	gasto := models.GastoLocal{
		Local:   body.Pos,
		Fecha:   fecha,
		Tipo:    "GASTO_COMUN",
		Motivo:  body.Motivo,
		Monto:   body.Monto,
		Usuario: "sistema",
	}
	if err := DB.Create(&gasto).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gasto)
}

// ExcludeBillingGasto marca un gasto como excluido del informe mensual (sin borrarlo físicamente).
func ExcludeBillingGasto(c *gin.Context) {
	var body struct {
		Pos   string `json:"pos" binding:"required"`
		Year  int    `json:"year" binding:"required"`
		Month int    `json:"month" binding:"required,min=1,max=12"`
		Gasto struct {
			ID      *int32    `json:"id"`
			Local   string    `json:"local"`
			Tipo    string    `json:"tipo"`
			Motivo  string    `json:"motivo" binding:"required"`
			Monto   float64   `json:"monto" binding:"required"`
			Fecha   time.Time `json:"fecha" binding:"required"`
			Usuario string    `json:"usuario"`
		} `json:"gasto" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	local := normalizeBillingPOSName(body.Pos)
	if local == "" {
		local = normalizeBillingPOSName(body.Gasto.Local)
	}
	if local == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "pos inválido"})
		return
	}

	fingerprint := billingExpenseFingerprint(body.Gasto.Fecha, body.Gasto.Monto, body.Gasto.Motivo, body.Gasto.Usuario)
	if strings.TrimSpace(fingerprint) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "gasto inválido"})
		return
	}

	var gastoLocalID *int32
	if body.Gasto.ID != nil && *body.Gasto.ID > 0 {
		gastoLocalID = body.Gasto.ID
	}

	exclusion := models.BillingGastoExclusion{
		Year:         body.Year,
		Month:        body.Month,
		Local:        local,
		Fingerprint:  fingerprint,
		Source:       strings.TrimSpace(body.Gasto.Tipo),
		GastoLocalID: gastoLocalID,
	}

	if err := DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "year"},
			{Name: "month"},
			{Name: "local"},
			{Name: "fingerprint"},
		},
		DoNothing: true,
	}).Create(&exclusion).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo excluir el gasto", "detalle": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "excluded"})
}

// ClearOdooCache limpia todo el cache de Odoo (admin only)
func ClearOdooCache(c *gin.Context) {
	count := odoo.ClearAllCache()
	c.JSON(http.StatusOK, gin.H{"status": "ok", "cleared": count})
}

// GetBillingGastosBatch retorna todos los gastos variables (gastos_locales + movements) para un mes.
// GET /api/billing/gastos-batch?year=Y&month=M
func GetBillingGastosBatch(c *gin.Context) {
	yearStr := c.DefaultQuery("year", strconv.Itoa(time.Now().Year()))
	monthStr := c.DefaultQuery("month", strconv.Itoa(int(time.Now().Month())))
	year, err := strconv.Atoi(yearStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Año inválido"})
		return
	}
	month, err := strconv.Atoi(monthStr)
	if err != nil || month < 1 || month > 12 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Mes inválido"})
		return
	}

	start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.Local)
	end := start.AddDate(0, 1, 0)

	gastos, err := getCommonExpensesBetween(start, end)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Agrupar por local (POS name)
	result := make(map[string][]models.GastoLocal)
	for _, g := range gastos {
		pos := normalizeBillingPOSName(g.Local)
		if pos == "" {
			continue
		}
		result[pos] = append(result[pos], g)
	}

	c.JSON(http.StatusOK, result)
}

// GetNominaSummary combina nomina-by-pos y nomina-available en una sola respuesta.
// GET /api/billing/nomina-summary?year=Y&month=M
func GetNominaSummary(c *gin.Context) {
	yearStr := c.Query("year")
	monthStr := c.Query("month")
	year, err := strconv.Atoi(yearStr)
	if err != nil || year < 2000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "year inválido"})
		return
	}
	month, err := strconv.Atoi(monthStr)
	if err != nil || month < 1 || month > 12 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "month inválido"})
		return
	}

	start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(year, time.Month(month)+1, 1, 0, 0, 0, 0, time.UTC)

	// 1. Obtener todos los pagos del mes (incluye empleados fixed y daily)
	var payments []models.NominaPayment
	DB.Preload("User").
		Where("period_start >= ? AND period_start < ?", start, end).
		Find(&payments)
	payments = pickCanonicalMonthlyPayments(payments)

	type userAgg struct {
		UserID    uint
		Name      string
		TotalPaid int64
		Count     int // quincenas/pagos del mes
	}
	paymentByID := make(map[uint]models.NominaPayment)
	validPaymentIDs := make(map[uint]bool)
	userMonthlyAgg := make(map[uint]*userAgg) // userID -> total mensual

	for _, p := range payments {
		paymentByID[p.ID] = p
		validPaymentIDs[p.ID] = true

		agg, exists := userMonthlyAgg[p.UserID]
		if !exists {
			name := p.User.Name
			if name == "" {
				name = p.User.FullName
			}
			if name == "" {
				name = p.User.Username
			}
			agg = &userAgg{
				UserID: p.UserID,
				Name:   name,
			}
			userMonthlyAgg[p.UserID] = agg
		}
		agg.TotalPaid += p.TotalPaid
		agg.Count++
	}

	// 2. Obtener asignaciones del mes
	var assignments []models.BillingNominaAssignment
	DB.Where("year = ? AND month = ?", year, month).Find(&assignments)

	// 3. Limpiar asignaciones fuera del set canónico de pagos válidos
	assignments = cleanAssignmentsOutsidePaymentSet(assignments, validPaymentIDs)

	// 4. Construir by_pos agrupado por empleado (total mensual por local)
	type employeeEntry struct {
		UserID    uint   `json:"user_id"`
		Name      string `json:"name"`
		TotalPaid int64  `json:"total_paid"`
		Count     int    `json:"count"`
	}
	type posNomina struct {
		Employees []employeeEntry `json:"employees"`
		Total     int64           `json:"total"`
	}

	byPos := make(map[string]*posNomina)
	type posUserKey struct {
		PosName string
		UserID  uint
	}
	employeeAggByPos := make(map[posUserKey]*employeeEntry)
	assignedUsers := make(map[uint]string) // userID -> posName

	for _, a := range assignments {
		p, found := paymentByID[a.PaymentID]
		if !found {
			continue
		}
		assignedUsers[a.UserID] = a.PosName
		if byPos[a.PosName] == nil {
			byPos[a.PosName] = &posNomina{Employees: []employeeEntry{}}
		}
		byPos[a.PosName].Total += p.TotalPaid

		key := posUserKey{PosName: a.PosName, UserID: a.UserID}
		empAgg, exists := employeeAggByPos[key]
		if !exists {
			name := p.User.Name
			if name == "" {
				name = p.User.FullName
			}
			if name == "" {
				name = p.User.Username
			}
			empAgg = &employeeEntry{
				UserID: a.UserID,
				Name:   name,
			}
			employeeAggByPos[key] = empAgg
		}
		empAgg.TotalPaid += p.TotalPaid
		empAgg.Count++
	}

	for posName, posData := range byPos {
		for key, emp := range employeeAggByPos {
			if key.PosName == posName {
				posData.Employees = append(posData.Employees, *emp)
			}
		}
	}

	// 5. Construir available agrupado por empleado (incluye asignados y no asignados)
	type availableEntry struct {
		UserID     uint   `json:"user_id"`
		Name       string `json:"name"`
		TotalPaid  int64  `json:"total_paid"`
		Count      int    `json:"count"`
		AssignedTo string `json:"assigned_to"` // POS si ya está asignado
	}

	available := make([]availableEntry, 0, len(userMonthlyAgg))
	for _, agg := range userMonthlyAgg {
		entry := availableEntry{
			UserID:    agg.UserID,
			Name:      agg.Name,
			TotalPaid: agg.TotalPaid,
			Count:     agg.Count,
		}
		if posName, ok := assignedUsers[agg.UserID]; ok {
			entry.AssignedTo = posName
		}
		available = append(available, entry)
	}

	c.JSON(http.StatusOK, gin.H{
		"by_pos":    byPos,
		"available": available,
	})
}

// monthNumberFromLabel acepta "January", "Enero", "January 2024", etc.
func monthNumberFromLabel(label string) int {
	l := strings.ToLower(label)
	names := []string{"enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"}
	namesEn := []string{"january", "february", "march", "april", "may", "june", "july", "august", "september", "october", "november", "december"}
	for i, n := range names {
		if strings.HasPrefix(l, n) {
			return i + 1
		}
	}
	for i, n := range namesEn {
		if strings.HasPrefix(l, n) {
			return i + 1
		}
	}
	return 0
}
