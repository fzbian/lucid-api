package models

import (
	"strings"
	"time"

	"gorm.io/gorm"
)

const DefaultGastoImageURL = "https://rrimg.chinatownlogistic.com/public/uploads/384f24a9c175ff85b0504a03c6129c5d.jpg"

type GastoLocal struct {
	ID        int32     `json:"id" gorm:"primaryKey;autoIncrement"`
	Local     string    `json:"local"`
	Fecha     time.Time `json:"fecha"`
	Tipo      string    `json:"tipo"` // e.g., "GASTO_OPERATIVO"
	Motivo    string    `json:"motivo"`
	Monto     float64   `json:"monto"`
	ImagenURL string    `json:"imagen_url"`
	Usuario   string    `json:"usuario"`
}

func (GastoLocal) TableName() string {
	return "gastos_locales"
}

func NormalizeGastoImageURL(url string) string {
	safe := strings.TrimSpace(url)
	if safe == "" {
		return DefaultGastoImageURL
	}
	return safe
}

func (g *GastoLocal) EnsureDefaultImage() {
	g.ImagenURL = NormalizeGastoImageURL(g.ImagenURL)
}

func (g *GastoLocal) BeforeCreate(_ *gorm.DB) error {
	g.EnsureDefaultImage()
	return nil
}

func (g *GastoLocal) BeforeSave(_ *gorm.DB) error {
	g.EnsureDefaultImage()
	return nil
}
