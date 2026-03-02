package models

import "time"

// Categoria representa la tabla categorias
type Categoria struct {
	ID               int32     `gorm:"primaryKey;type:int" json:"id"`
	Nombre           string    `gorm:"size:100;not null" json:"nombre"`
	Tipo             string    `gorm:"type:enum('INGRESO','EGRESO');not null" json:"tipo"`
	IsGastoOperativo bool      `gorm:"column:is_gasto_operativo;default:false" json:"is_gasto_operativo"`
	CreatedAt        time.Time `json:"created_at" gorm:"-"`
}

// Input para crear categoria
type CategoriaCreateInput struct {
	Nombre string `json:"nombre" binding:"required"`
	Tipo   string `json:"tipo" binding:"required"`
}

// Input para actualizar categoria (parcial)
type CategoriaUpdateInput struct {
	Nombre *string `json:"nombre,omitempty"`
	Tipo   *string `json:"tipo,omitempty"`
}

func (Categoria) TableName() string { return "categorias" }
