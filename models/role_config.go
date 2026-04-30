package models

type RoleConfig struct {
	Role  string `gorm:"primaryKey;size:50" json:"role"`
	Views string `gorm:"type:text" json:"views"` // JSON array of view IDs e.g. ["dashboard", "gastos"]
}

func (RoleConfig) TableName() string { return "role_configs" }
