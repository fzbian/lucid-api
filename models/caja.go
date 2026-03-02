package models

import "time"

// Caja representa la tabla caja
type Caja struct {
	ID                  int32     `gorm:"primaryKey;type:int" json:"id"`
	Saldo               float64   `gorm:"type:decimal(15,2);not null" json:"saldo"`
	UltimaActualizacion time.Time `gorm:"column:ultima_actualizacion;autoUpdateTime" json:"ultima_actualizacion"`
}

// CajaOdoo representa la respuesta agregada de saldos obtenidos desde Odoo
// swagger:model
type CajaOdoo struct {
	ID                  uint                      `json:"id"`
	SaldoCaja           float64                   `json:"saldo_caja"`
	SaldoCaja2          float64                   `json:"saldo_caja2"`
	Locales             map[string]POSLocalDetail `json:"locales"`
	TotalLocales        float64                   `json:"total_locales"`
	SaldoTotal          float64                   `json:"saldo_total"`
	UltimaActualizacion time.Time                 `json:"ultima_actualizacion"`
}

// POSLocalDetail representa el saldo y ventas por local POS
type POSLocalDetail struct {
	SaldoEnCaja  float64  `json:"saldo_en_caja"`
	Vendido      *float64 `json:"vendido,omitempty"`
	EstadoSesion string   `json:"estado_sesion"`
}

func (Caja) TableName() string { return "caja" }
