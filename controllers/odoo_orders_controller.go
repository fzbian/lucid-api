package controllers

import (
	"atm/odoo"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// OdooListInvoicedOrders godoc
// @Summary Listar pedidos facturados de Odoo
// @Description Devuelve pedidos en estado facturado (`invoiced`) con filtros por local, fechas y sesión.
// @Produce json
// @Param from query string false "Fecha inicio (RFC3339 o YYYY-MM-DD)"
// @Param to query string false "Fecha fin (RFC3339 o YYYY-MM-DD)"
// @Param local query string false "Nombre del local/POS (búsqueda parcial)"
// @Param session_id query int false "ID de sesión POS"
// @Param limit query int false "Máximo de resultados (default 200, max 1000)"
// @Param offset query int false "Desplazamiento de resultados (default 0)"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/odoo/orders [get]
func OdooListInvoicedOrders(c *gin.Context) {
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

	sessionID := int64(0)
	sessionRaw := strings.TrimSpace(c.Query("session_id"))
	if sessionRaw != "" {
		parsed, err := strconv.ParseInt(sessionRaw, 10, 64)
		if err != nil || parsed <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "session_id inválido"})
			return
		}
		sessionID = parsed
	}

	limit := 200
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limit inválido"})
			return
		}
		limit = parsed
	}

	offset := 0
	if raw := strings.TrimSpace(c.Query("offset")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "offset inválido"})
			return
		}
		offset = parsed
	}

	local := strings.TrimSpace(c.Query("local"))

	client, err := odoo.NewFromEnv()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := client.Authenticate(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Odoo Auth: " + err.Error()})
		return
	}

	result, err := client.GetPOSInvoicedOrders(odoo.POSOrdersQuery{
		From:      from,
		To:        to,
		Local:     local,
		SessionID: sessionID,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"filters": gin.H{
			"from":       fromRaw,
			"to":         toRaw,
			"local":      local,
			"session_id": sessionID,
			"limit":      result.Limit,
			"offset":     result.Offset,
		},
		"total":              result.Total,
		"summary":            result.Summary,
		"available_locales":  result.AvailableLocales,
		"available_sessions": result.AvailableSessions,
		"returned_fields":    result.ReturnedFields,
		"field_catalog":      result.FieldCatalog,
		"data":               result.Data,
	})
}

type odooRefundOrderBody struct {
	Confirm bool `json:"confirm"`
}

// OdooRefundOrderFull godoc
// @Summary Reembolsar pedido POS completo
// @Description Genera un reembolso completo del pedido validando sesión abierta y estado pagado.
// @Accept json
// @Produce json
// @Param id path int true "ID del pedido POS a reembolsar"
// @Param body body map[string]bool true "{ \"confirm\": true }"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/odoo/orders/{id}/refund [post]
func OdooRefundOrderFull(c *gin.Context) {
	orderIDRaw := strings.TrimSpace(c.Param("id"))
	orderID, err := strconv.ParseInt(orderIDRaw, 10, 64)
	if err != nil || orderID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id de pedido inválido"})
		return
	}

	var body odooRefundOrderBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "payload inválido, envía {\"confirm\": true}"})
		return
	}
	if !body.Confirm {
		c.JSON(http.StatusBadRequest, gin.H{"error": "confirmación requerida para ejecutar el reembolso"})
		return
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

	result, err := client.CreateFullOrderRefund(orderID)
	if err != nil {
		if odoo.IsPOSRefundValidationError(err) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		var syntaxErr *strconv.NumError
		if errors.As(err, &syntaxErr) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "error de validación en datos del pedido"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":   true,
		"data": result,
	})
}

func parseDateFilter(raw string, endOfDay bool) (time.Time, string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}, "", nil
	}

	layouts := []string{time.RFC3339, "2006-01-02"}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, trimmed); err == nil {
			if layout == "2006-01-02" && endOfDay {
				parsed = parsed.Add(24*time.Hour - time.Second)
			}
			return parsed, trimmed, nil
		}
	}

	return time.Time{}, "", strconv.ErrSyntax
}
