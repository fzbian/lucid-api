package odoo

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// billingCacheKey genera la key de cache para billing por año/mes
func billingCacheKey(year, month int) string {
	return fmt.Sprintf("billing_%d_%d", year, month)
}

// billingMarginCacheEntry almacena sales y margins juntos
type billingMarginCacheEntry struct {
	sales   map[string]map[string]float64
	margins map[string]map[string]float64
}

// BillingEntry represents a single row from read_group
type BillingEntry struct {
	PosName    string  `json:"pos_name"`
	Month      string  `json:"month"` // e.g. "January 2024"
	Total      float64 `json:"total"`
	ConfigName string  `json:"config_name"`
}

// GetMonthlyBilling fecthes billing data grouped by POS and Month for a given year
func GetMonthlyBilling(ctx context.Context, odooURL, db, user, password string, year int) (map[string]map[string]float64, error) {
	uid, err := odooLogin(ctx, odooURL, db, user, password)
	if err != nil || uid == 0 {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("login fallido")
	}

	// Domain: Year range
	startDate := fmt.Sprintf("%d-01-01", year)
	endDate := fmt.Sprintf("%d-12-31", year)
	domain := []any{
		[]any{"date_order", ">=", startDate},
		[]any{"date_order", "<=", endDate},
		[]any{"state", "in", []string{"paid", "done", "invoiced"}}, // Only completed orders
	}

	// Algunos Odoo marcan config_id como no almacenado, haciendo fallar read_group.
	// Vamos directo al fallback con search_read para evitar errores ruidosos.
	fallback, fbErr := fallbackBillingFromOrders(ctx, odooURL, db, uid, password, domain)
	if fbErr != nil {
		return nil, fmt.Errorf("Odoo billing error (search_read fallback): %v", fbErr)
	}
	return fallback, nil
}

// GetMonthlyBillingWithMargin devuelve ventas y margen por local/mes (cache TTL: 5 min por año).
func GetMonthlyBillingWithMargin(ctx context.Context, odooURL, db, user, password string, year int) (map[string]map[string]float64, map[string]map[string]float64, error) {
	// Check cache first (TTL: 5 min, keyed by year — month=0 means whole year)
	cacheKey := billingCacheKey(year, 0)
	if cached, ok := getCached(cacheKey); ok {
		if entry, ok := cached.(billingMarginCacheEntry); ok {
			return entry.sales, entry.margins, nil
		}
	}

	uid, err := odooLogin(ctx, odooURL, db, user, password)
	if err != nil || uid == 0 {
		if err != nil {
			return nil, nil, err
		}
		return nil, nil, fmt.Errorf("login fallido")
	}

	// Domain: Year range
	startDate := fmt.Sprintf("%d-01-01", year)
	endDate := fmt.Sprintf("%d-12-31", year)
	domain := []any{
		[]any{"date_order", ">=", startDate},
		[]any{"date_order", "<=", endDate},
		[]any{"state", "in", []string{"paid", "done", "invoiced"}},
	}

	sales, margins, fbErr := fallbackBillingFromOrdersWithMargin(ctx, odooURL, db, uid, password, domain)
	if fbErr != nil {
		return nil, nil, fmt.Errorf("Odoo billing margin error: %v", fbErr)
	}

	// Store in cache
	setCache(cacheKey, billingMarginCacheEntry{sales: sales, margins: margins}, 5*time.Minute)
	return sales, margins, nil
}

// parseReadGroupBilling converts read_group response into {POS: {Month: Total}}
func parseReadGroupBilling(rows []map[string]any) map[string]map[string]float64 {
	billingData := make(map[string]map[string]float64)

	for _, row := range rows {
		// POS Name (strip anything in parentheses)
		posName := "Unknown"
		if config, ok := row["config_id"].([]any); ok && len(config) > 1 {
			if name, ok := config[1].(string); ok {
				posName = cleanPOSName(name)
			}
		}

		// Month string (first token e.g. "January" or "Enero")
		monthStr := ""
		if m, ok := row["date_order:month"].(string); ok {
			parts := strings.Split(m, " ")
			if len(parts) > 0 {
				monthStr = parts[0]
			}
		}

		// Total
		total := 0.0
		if t, ok := row["amount_total"].(float64); ok {
			total = t
		}

		if billingData[posName] == nil {
			billingData[posName] = make(map[string]float64)
		}
		billingData[posName][monthStr] = total
	}

	return billingData
}

// fallbackBillingFromOrders uses search_read and aggregates in Go when read_group is unavailable.
func fallbackBillingFromOrders(ctx context.Context, baseURL, db string, uid int, password string, domain []any) (map[string]map[string]float64, error) {
	fields := []string{"amount_total", "config_id", "date_order"}
	kwargs := map[string]any{"fields": fields, "domain": domain, "limit": 8000}

	var orders []map[string]any
	if err := odooExecuteKW(ctx, baseURL, db, uid, password, "pos.order", "search_read", []any{}, kwargs, &orders); err != nil {
		return nil, err
	}

	agg := make(map[string]map[string]float64)

	for _, row := range orders {
		// POS name
		posName := "Unknown"
		if cfg, ok := row["config_id"].([]any); ok && len(cfg) > 1 {
			if name, ok := cfg[1].(string); ok {
				posName = cleanPOSName(name)
			}
		}

		// Date parsing
		monthStr := ""
		if ds, ok := row["date_order"].(string); ok && ds != "" {
			// Odoo usually returns "YYYY-MM-DD HH:MM:SS" (UTC). Try that first.
			layouts := []string{"2006-01-02 15:04:05", time.RFC3339}
			for _, layout := range layouts {
				if t, parseErr := time.Parse(layout, ds); parseErr == nil {
					monthStr = t.Month().String() // English month; frontend handles ES/EN ordering
					break
				}
			}
		}

		total := 0.0
		if t, ok := row["amount_total"].(float64); ok {
			total = t
		}

		if agg[posName] == nil {
			agg[posName] = make(map[string]float64)
		}
		if monthStr == "" {
			monthStr = "Sin Mes"
		}
		agg[posName][monthStr] += total
	}

	return agg, nil
}

// fallbackBillingFromOrdersWithMargin agrega ventas y margen (si existe campo margin en pos.order).
func fallbackBillingFromOrdersWithMargin(ctx context.Context, baseURL, db string, uid int, password string, domain []any) (map[string]map[string]float64, map[string]map[string]float64, error) {
	fields := []string{"amount_total", "margin", "config_id", "date_order"}
	kwargs := map[string]any{"fields": fields, "domain": domain, "limit": 8000}

	var orders []map[string]any
	if err := odooExecuteKW(ctx, baseURL, db, uid, password, "pos.order", "search_read", []any{}, kwargs, &orders); err != nil {
		return nil, nil, err
	}

	sales := make(map[string]map[string]float64)
	margins := make(map[string]map[string]float64)

	for _, row := range orders {
		posName := "Unknown"
		if cfg, ok := row["config_id"].([]any); ok && len(cfg) > 1 {
			if name, ok := cfg[1].(string); ok {
				posName = cleanPOSName(name)
			}
		}

		monthStr := ""
		if ds, ok := row["date_order"].(string); ok && ds != "" {
			layouts := []string{"2006-01-02 15:04:05", time.RFC3339}
			for _, layout := range layouts {
				if t, parseErr := time.Parse(layout, ds); parseErr == nil {
					monthStr = t.Month().String()
					break
				}
			}
		}
		if monthStr == "" {
			monthStr = "Sin Mes"
		}

		total := 0.0
		if t, ok := row["amount_total"].(float64); ok {
			total = t
		}
		marginVal := 0.0
		if m, ok := row["margin"].(float64); ok {
			marginVal = m
		}

		if sales[posName] == nil {
			sales[posName] = make(map[string]float64)
		}
		if margins[posName] == nil {
			margins[posName] = make(map[string]float64)
		}
		sales[posName][monthStr] += total
		margins[posName][monthStr] += marginVal
	}

	return sales, margins, nil
}

// cleanPOSName trims anything in parentheses to keep the store/base name.
// Example: "Bodega (Fabian Martin)" -> "Bodega"
func cleanPOSName(name string) string {
	if idx := strings.Index(name, " ("); idx != -1 {
		return strings.TrimSpace(name[:idx])
	}
	return strings.TrimSpace(name)
}
