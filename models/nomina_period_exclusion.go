package models

import "time"

// NominaPeriodExclusion define empleados excluidos de una quincena específica.
// Si no existe registro para (year,month,period,user), se asume incluido.
type NominaPeriodExclusion struct {
	ID     uint `gorm:"primaryKey" json:"id"`
	Year   int  `gorm:"uniqueIndex:idx_nomina_period_exclusion" json:"year"`
	Month  int  `gorm:"uniqueIndex:idx_nomina_period_exclusion" json:"month"`
	Period int  `gorm:"uniqueIndex:idx_nomina_period_exclusion" json:"period"` // 1 o 2
	UserID uint `gorm:"uniqueIndex:idx_nomina_period_exclusion;index" json:"user_id"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (NominaPeriodExclusion) TableName() string {
	return "nomina_period_exclusions"
}
