package models

import "time"

// Transaccion representa la tabla transacciones
// Ahora incluye el campo usuario
type Transaccion struct {
	ID          int32     `gorm:"primaryKey;type:int" json:"id"`
	CategoriaID int32     `gorm:"column:categoria_id;type:int;not null" json:"categoria_id"`
	CajaID      int32     `gorm:"column:caja_id;type:int;not null" json:"caja_id"`
	Monto       float64   `gorm:"type:decimal(15,2);not null" json:"monto"`
	Fecha       time.Time `gorm:"column:fecha;autoCreateTime" json:"fecha"`
	Descripcion string    `gorm:"type:text;not null" json:"descripcion"`
	Usuario     string    `gorm:"type:varchar(100);" json:"usuario"`
}

// TransaccionCreateInput limita los campos permitidos al crear una transacción (id y fecha se generan automáticamente)
// Ahora usuario es obligatorio
type TransaccionCreateInput struct {
	CategoriaID int32   `json:"categoria_id" binding:"required"`
	CajaID      int32   `json:"caja_id" binding:"required"`
	Monto       float64 `json:"monto" binding:"required"`
	Descripcion string  `json:"descripcion" binding:"required"`
	Usuario     string  `json:"usuario" binding:"required"`
	Local       string  `json:"local"` // opcional: POS name para gastos operativos
}

// TransaccionUpdateInput ahora usa punteros para distinguir campos omitidos
// (solo se actualizan los que lleguen no nulos)
type TransaccionUpdateInput struct {
	CategoriaID *int32   `json:"categoria_id,omitempty"`
	CajaID      *int32   `json:"caja_id,omitempty"`
	Monto       *float64 `json:"monto,omitempty"`
	Descripcion *string  `json:"descripcion,omitempty"`
}

func (Transaccion) TableName() string { return "transacciones" }
