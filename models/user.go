package models

import "time"

// User representa un usuario del sistema sincronizado con Odoo
type User struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Username  string    `gorm:"size:100;uniqueIndex:uni_users_username;not null" json:"username"`
	Name      string    `gorm:"size:200" json:"name"`      // Display Name
	FullName  string    `gorm:"size:200" json:"full_name"` // Legal Name for Payroll
	Cedula    string    `gorm:"size:20" json:"cedula"`     // ID Document
	Celular   string    `gorm:"size:20" json:"celular"`    // Número para envío de recibos/firma
	PIN       string    `gorm:"size:200" json:"-"`         // PIN/Password (hashed or plain depending on Odoo sync, assumes Odoo PIN usage)
	Role      string    `gorm:"size:50;default:'user'" json:"role"`
	OdooID    int       `gorm:"uniqueIndex" json:"odoo_id"` // Link to hr.employee
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (User) TableName() string { return "users" }
