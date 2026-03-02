package controllers

import (
	"atm/odoo"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// OdooOrdersOverviewByPOS godoc
// @Summary Resumen de pedidos por POS y sesiones
// @Description Devuelve tarjetas por punto de venta con listado de sesiones ordenadas, optimizado para UI con una sola petición.
// @Produce json
// @Param from query string false "Fecha inicio (RFC3339 o YYYY-MM-DD)"
// @Param to query string false "Fecha fin (RFC3339 o YYYY-MM-DD)"
// @Param local query string false "Nombre del local/POS (búsqueda parcial)"
// @Param limit query int false "Máximo de pedidos fuente a leer en Odoo (default 12000, max 30000)"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/odoo/orders/overview [get]
func OdooOrdersOverviewByPOS(c *gin.Context) {
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
	limit := 12000
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

	overview, err := client.GetPOSSessionsOverview(odoo.POSSessionOverviewQuery{
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
		"totals": overview.Totals,
		"data":   overview.Data,
	})
}
