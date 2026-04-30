package models

import "time"

// TransaccionLog registra cambios en las transacciones para auditoría
type TransaccionLog struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	TransaccionID int32     `gorm:"column:transaccion_id;index" json:"transaccion_id"`
	Accion        string    `gorm:"type:varchar(20);not null" json:"accion"` // INSERT, UPDATE, DELETE
	Usuario       string    `gorm:"type:varchar(100)" json:"usuario"`
	Detalle       string    `gorm:"type:text" json:"detalle"` // JSON o texto describiendo el cambio
	SaldoAntes    float64   `gorm:"type:decimal(15,2);default:0" json:"saldo_antes"`
	SaldoDespues  float64   `gorm:"type:decimal(15,2);default:0" json:"saldo_despues"`
	Fecha         time.Time `gorm:"column:fecha;autoCreateTime" json:"fecha"`
}

func (TransaccionLog) TableName() string { return "transacciones_log" }
