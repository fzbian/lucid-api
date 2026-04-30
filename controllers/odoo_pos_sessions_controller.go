package controllers

import (
	"atm/odoo"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// OdooPOSSessionsOverview godoc
// @Summary Resumen de horarios de sesiones POS
// @Description Devuelve las sesiones POS agrupadas por punto de venta para consultar aperturas y cierres por rango.
// @Produce json
// @Param from query string false "Fecha inicio (RFC3339 o YYYY-MM-DD)"
// @Param to query string false "Fecha fin (RFC3339 o YYYY-MM-DD)"
// @Param local query string false "Nombre del local/POS (búsqueda parcial)"
// @Param limit query int false "Máximo de sesiones a leer en Odoo (default 5000, max 20000)"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/odoo/pos-sessions [get]
func OdooPOSSessionsOverview(c *gin.Context) {
	from, fromRaw, err := parseDateFilter(c.Query("from"), false)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "formato de 'from' inválido. Use RFC3339 o YYYY-MM-DD"})
		return
	}
	to, toRaw, err := parseDateFilter(c.Query("to"), true)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "formato de 'to' inválido. Use RFC3339 o YYYY-MM-DD"})
		return
	}
	if !from.IsZero() && !to.IsZero() && to.Before(from) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "'to' no puede ser menor que 'from'"})
		return
	}

	local := strings.TrimSpace(c.Query("local"))
	limit := 5000
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limit inválido"})
			return
		}
		limit = parsed
	}

	client, err := odoo.NewFromEnv()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := client.Authenticate(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Odoo Auth: " + err.Error()})
		return
	}

	result, err := client.GetPOSSessionsHoursOverview(odoo.POSSessionHoursQuery{
		From:  from,
		To:    to,
		Local: local,
		Limit: limit,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"filters": gin.H{
			"from":  fromRaw,
			"to":    toRaw,
			"local": local,
			"limit": limit,
		},
		"totals": result.Totals,
		"data":   result.Data,
	})
}
