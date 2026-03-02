package controllers

import (
	"atm/models"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm/clause"
)

type billingConfigEntry struct {
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
	var cfgs []models.BillingConfig
	if err := DB.Find(&cfgs).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, cfgs)
}

func SaveBillingConfigs(c *gin.Context) {
	var body struct {
		Entries []billingConfigEntry `json:"entries" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	toSave := make([]models.BillingConfig, 0, len(body.Entries))
	for _, e := range body.Entries {
		includeInReports := true
		if e.IncludeInReports != nil {
			includeInReports = *e.IncludeInReports
		}
		cfg := models.BillingConfig{
			PosName:          e.PosName,
			IncludeInReports: &includeInReports,
			Arriendo:         e.Arriendo,
			Internet:         e.Internet,
			Luz:              e.Luz,
			LuzAplica:        e.LuzAplica,
			Gas:              e.Gas,
			GasAplica:        e.GasAplica,
			Agua:             e.Agua,
			AguaAplica:       e.AguaAplica,
		}
		toSave = append(toSave, cfg)
	}

	if err := DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "pos_name"}},
		DoUpdates: clause.AssignmentColumns([]string{"include_in_reports", "arriendo", "internet", "luz", "luz_aplica", "gas", "gas_aplica", "agua", "agua_aplica", "updated_at"}),
	}).Create(&toSave).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok", "saved": len(toSave)})
}

// ---- Fixed Costs CRUD ----

func GetFixedCosts(c *gin.Context) {
	pos := c.Query("pos")
	var costs []models.BillingFixedCost
	q := DB.Order("sort_order, id")
	if pos != "" {
		q = q.Where("pos_name = ?", pos)
	}
	if err := q.Find(&costs).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, costs)
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
