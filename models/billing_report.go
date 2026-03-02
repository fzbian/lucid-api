package models

import "time"

// BillingMonthly almacena gastos y margen por local y mes.
type BillingMonthly struct {
	ID            uint      `json:"id" gorm:"primaryKey"`
	PosName       string    `json:"pos_name" gorm:"size:191;index:idx_pos_month,unique"`
	Year          int       `json:"year" gorm:"index:idx_pos_month,unique"`
	Month         int       `json:"month" gorm:"index:idx_pos_month,unique"` // 1-12
	GastosComunes float64   `json:"gastos_comunes"`
	Servicios     float64   `json:"servicios"`
	Nomina        float64   `json:"nomina"`
	Arriendo      float64   `json:"arriendo"`
	Margen        float64   `json:"margen"`

	// Campos de confirmación — se congelan al confirmar el informe
	Confirmed          bool       `json:"confirmed" gorm:"default:false"`
	ConfirmedAt        *time.Time `json:"confirmed_at"`
	Venta              float64    `json:"venta"`
	TotalGastos        float64    `json:"total_gastos"`
	UtilidadBruta      float64    `json:"utilidad_bruta"`
	ComisionPorcentaje float64    `json:"comision_porcentaje"` // % global usado al confirmar

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (BillingMonthly) TableName() string {
	return "billing_monthlies"
}
