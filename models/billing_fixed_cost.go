package models

import "time"

// BillingFixedCost representa un gasto fijo configurable por POS.
type BillingFixedCost struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	PosName   string    `json:"pos_name" gorm:"size:191;index"`
	Name      string    `json:"name" gorm:"size:100"`
	Amount    float64   `json:"amount"`
	Active    bool      `json:"active" gorm:"default:true"`
	SortOrder int       `json:"sort_order" gorm:"default:0"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (BillingFixedCost) TableName() string {
	return "billing_fixed_costs"
}
