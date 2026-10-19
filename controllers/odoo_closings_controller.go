package controllers

import (
	"atm/odoo"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// OdooClosings returns complete closed sessions for a Colombian calendar day.
func OdooClosings(c *gin.Context) {
	day, err := time.ParseInLocation("2006-01-02", c.Query("date"), odoo.ClosingLocation)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Seleccione una fecha válida (YYYY-MM-DD)"})
		return
	}
	client, err := odoo.NewFromEnv()
	if err == nil {
		err = client.Authenticate()
	}
	if err != nil {
		_ = c.Error(err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "No se pudo conectar con Odoo. Intente actualizar en unos momentos."})
		return
	}
	data, err := client.GetClosings(day)
	if err != nil {
		_ = c.Error(err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "No se pudo obtener el reporte completo de Odoo. Intente nuevamente."})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"date": day.Format("2006-01-02"), "timezone": "America/Bogota", "data": data})
}
