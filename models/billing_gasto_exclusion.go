package models

import "time"

// BillingGastoExclusion marca un gasto como excluido para un informe mensual
// sin eliminar el registro original de la base.
type BillingGastoExclusion struct {
	ID uint `json:"id" gorm:"primaryKey"`

	Year  int    `json:"year" gorm:"index;uniqueIndex:idx_billing_gasto_exclusion,priority:1"`
	Month int    `json:"month" gorm:"index;uniqueIndex:idx_billing_gasto_exclusion,priority:2"` // 1-12
	Local string `json:"local" gorm:"size:191;uniqueIndex:idx_billing_gasto_exclusion,priority:3"`

	// Fingerprint del gasto (fecha+monto+motivo+usuario)
	Fingerprint string `json:"fingerprint" gorm:"size:255;uniqueIndex:idx_billing_gasto_exclusion,priority:4"`

	Source       string `json:"source" gorm:"size:64"` // ej: GASTO_COMUN / GASTO_OPERATIVO_MOVEMENT
	GastoLocalID *int32 `json:"gasto_local_id,omitempty" gorm:"index"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (BillingGastoExclusion) TableName() string {
	return "billing_gasto_exclusions"
}
