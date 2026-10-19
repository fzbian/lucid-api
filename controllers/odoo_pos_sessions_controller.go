package controllers

import (
	"atm/odoo"
	"github.com/gin-gonic/gin"
	"net/http"
	"strings"
	"time"
)

// parseHoursBoundary interprets calendar dates in Colombia, independent of the
// API host's timezone. Calendar 'to' includes the entire selected date.
func parseHoursBoundary(raw string, end bool) (time.Time, error) {
	if len(raw) == 10 {
		date, err := time.ParseInLocation("2006-01-02", raw, odoo.ClosingLocation)
		if err == nil && end {
			date = date.AddDate(0, 0, 1)
		}
		return date, err
	}
	return time.Parse(time.RFC3339, raw)
}

// OdooPOSSessionsOverview returns complete daily opening-hour analytics.
func OdooPOSSessionsOverview(c *gin.Context) {
	now := time.Now().In(odoo.ClosingLocation)
	fromRaw, toRaw := strings.TrimSpace(c.Query("from")), strings.TrimSpace(c.Query("to"))
	if fromRaw == "" {
		fromRaw = now.AddDate(0, 0, -(int(now.Weekday())+6)%7).Format("2006-01-02")
	}
	if toRaw == "" {
		toRaw = now.Format("2006-01-02")
	}
	from, err := parseHoursBoundary(fromRaw, false)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Seleccione una fecha de inicio válida."})
		return
	}
	to, err := parseHoursBoundary(toRaw, true)
	if err != nil || !to.After(from) || to.Sub(from) > 366*24*time.Hour || from.After(now) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Seleccione un período válido de hasta 366 días, sin iniciar en el futuro."})
		return
	}
	client, err := odoo.NewFromEnv()
	if err == nil {
		err = client.Authenticate()
	}
	if err != nil {
		_ = c.Error(err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "No pudimos conectar con Odoo. Intente actualizar."})
		return
	}
	result, err := client.GetPOSSessionsHoursOverview(odoo.POSSessionHoursQuery{From: from, To: to, Local: strings.TrimSpace(c.Query("local"))})
	if err != nil {
		_ = c.Error(err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "No pudimos cargar todos los horarios. Intente nuevamente."})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"filters": gin.H{"from": fromRaw, "to": toRaw}, "timezone": "America/Bogota", "generated_at": result.GeneratedAt, "totals": result.Totals, "data": result.Data})
}
