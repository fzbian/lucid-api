package models

import "time"

// BillingConfig guarda costos fijos por local.
type BillingConfig struct {
	ID               uint      `json:"id" gorm:"primaryKey"`
	PosName          string    `json:"pos_name" gorm:"size:191"`
	IncludeInReports *bool     `json:"include_in_reports" gorm:"default:true"`
	Arriendo         float64   `json:"arriendo"`
	Internet         float64   `json:"internet"`
	Luz              float64   `json:"luz"`
	LuzAplica        bool      `json:"luz_aplica"`
	Gas              float64   `json:"gas"`
	GasAplica        bool      `json:"gas_aplica"`
	Agua             float64   `json:"agua"`
	AguaAplica       bool      `json:"agua_aplica"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (BillingConfig) TableName() string {
	return "pos_billing_configs"
}
