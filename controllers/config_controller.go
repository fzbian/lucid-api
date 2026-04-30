package controllers

import (
	"atm/models"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
)

var rolePattern = regexp.MustCompile(`^[a-z0-9._-]{2,50}$`)

func RegisterConfigRoutes(r *gin.RouterGroup) {
	cfg := r.Group("/config")
	{
		cfg.GET("/roles", GetRoleConfigs)
		cfg.POST("/roles", SaveRoleConfig)
	}
}

func GetRoleConfigs(c *gin.Context) {
	var configs []models.RoleConfig
	if err := DB.Find(&configs).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error loading configs"})
		return
	}

	for i := range configs {
		parsed, err := parseStoredViews(configs[i].Views)
		if err != nil {
			configs[i].Views = "[]"
			continue
		}

		canonical := models.EnsureMandatoryRoleViews(configs[i].Role, parsed)
		payload, _ := json.Marshal(canonical)
		configs[i].Views = string(payload)
	}

	c.JSON(http.StatusOK, configs)
}

func SaveRoleConfig(c *gin.Context) {
	var payload struct {
		Role  string          `json:"role"`
		Views json.RawMessage `json:"views"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON"})
		return
	}

	role := strings.ToLower(strings.TrimSpace(payload.Role))
	if role == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "role is required"})
		return
	}

	if !rolePattern.MatchString(role) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid role format: use lowercase letters, numbers, dot, dash or underscore (2-50 chars)"})
		return
	}

	views, err := parseViewsPayload(payload.Views)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	clean := make([]string, 0, len(views))
	for _, view := range views {
		safe := strings.TrimSpace(view)
		if safe == "" {
			continue
		}
		if !models.IsAllowedRoleView(safe) {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("unknown view id: %s", safe)})
			return
		}
		clean = append(clean, safe)
	}

	clean = models.EnsureMandatoryRoleViews(role, clean)
	encodedViews, _ := json.Marshal(clean)

	record := models.RoleConfig{
		Role:  role,
		Views: string(encodedViews),
	}

	if err := DB.Save(&record).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error saving config"})
		return
	}

	c.JSON(http.StatusOK, record)
}

func parseViewsPayload(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("views is required")
	}

	var direct []string
	if err := json.Unmarshal(raw, &direct); err == nil {
		return direct, nil
	}

	var encoded string
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, fmt.Errorf("views must be an array of strings")
	}

	return parseStoredViews(encoded)
}

func parseStoredViews(raw string) ([]string, error) {
	safe := strings.TrimSpace(raw)
	if safe == "" {
		return []string{}, nil
	}

	var views []string
	if err := json.Unmarshal([]byte(safe), &views); err == nil {
		return views, nil
	}

	var nested string
	if err := json.Unmarshal([]byte(safe), &nested); err != nil {
		return nil, fmt.Errorf("views must be valid JSON array")
	}

	if strings.TrimSpace(nested) == "" {
		return []string{}, nil
	}

	if err := json.Unmarshal([]byte(nested), &views); err != nil {
		return nil, fmt.Errorf("views must be valid JSON array")
	}

	return views, nil
}
