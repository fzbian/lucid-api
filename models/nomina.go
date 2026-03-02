package models

import "time"

// NominaConfig guarda configuraciones globales de la nómina
type NominaConfig struct {
	ID                uint      `gorm:"primaryKey" json:"id"`
	AuxilioTransporte int64     `json:"auxilio_transporte"` // Valor mensual
	ValorDominical    int64     `json:"valor_dominical"`    // Deprecated: Use S1/S2
	ValorDominicalS1  int64     `json:"valor_dominical_s1"` // Valor Semestre 1 (Ene-Jun)
	ValorDominicalS2  int64     `json:"valor_dominical_s2"` // Valor Semestre 2 (Jul-Dic)
	ValorMadrugon     int64     `json:"valor_madrugon"`     // Valor por hora
	PorcentajeSalud   float64   `json:"porcentaje_salud"`   // % (e.g. 4.0)
	PorcentajePension float64   `json:"porcentaje_pension"` // % (e.g. 4.0)
	SalarioMinimo     int64     `json:"salario_minimo"`     // Referencia (opcional)
	CompanyName       string    `json:"company_name"`       // Nombre Empresa para recibos
	NIT               string    `json:"nit"`                // NIT Empresa para recibos
	UpdatedAt         time.Time `json:"updated_at"`
}

func (NominaConfig) TableName() string { return "nomina_configs" }

// UserPayroll guarda información específica de nómina por empleado
type UserPayroll struct {
	UserID      uint      `gorm:"primaryKey" json:"user_id"`
	BaseSalary  int64     `json:"base_salary"`                   // Salario base mensual (usado cuando pay_type=fixed)
	DailyRate   int64     `json:"daily_rate"`                    // Valor por día (usado cuando pay_type=daily)
	HasSecurity *bool     `json:"has_security"`                  // Si paga seguridad social
	PayType     string    `gorm:"default:fixed" json:"pay_type"` // "fixed" = salario fijo, "daily" = pago por días trabajados
	UpdatedAt   time.Time `json:"updated_at"`
}

func (UserPayroll) TableName() string { return "user_payrolls" }

// NominaPayment guarda el histórico de pagos generados
type NominaPayment struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	UserID      uint      `gorm:"index" json:"user_id"`
	User        User      `gorm:"foreignKey:UserID" json:"user,omitempty"`
	PeriodStart time.Time `gorm:"index" json:"period_start"`
	PeriodEnd   time.Time `gorm:"index" json:"period_end"`

	// Valores calculados y guardados
	BaseSalary      int64   `json:"base_salary"`   // Salario base (mensual) snapshot
	DailyRate       int64   `json:"daily_rate"`    // Valor por día snapshot (solo para pay_type=daily)
	PayType         string  `json:"pay_type"`      // "fixed" o "daily" snapshot
	DaysWorked      int     `json:"days_worked"`   // Días trabajados (solo para pay_type=daily)
	PaidBase        int64   `json:"paid_base"`     // (Base / 2) o (DailyRate * days)
	TransportAid    int64   `json:"transport_aid"` // (Auxilio / 2)
	SundaysQty      int     `json:"sundays_qty"`
	SundaysTotal    int64   `json:"sundays_total"`    // (Valor * Qty)
	MadrugonesQty   float64 `json:"madrugones_qty"`   // Horas
	MadrugonesTotal int64   `json:"madrugones_total"` // Valor total madrugones

	IncludesTransportAid bool  `json:"includes_transport_aid" gorm:"default:true"` // Si incluyó auxilio transporte
	IncludesSecurity     bool  `json:"includes_security"`                          // Si incluyó seguridad social
	Health               int64 `json:"health"`                                     // Deducción 4%
	Pension              int64 `json:"pension"`                                    // Deducción 4%
	Advance              int64 `json:"advance"`                                    // Adelanto descontado
	Commission           int64 `json:"commission"`                                 // Comisión por administración de POS (solo 2da quincena)
	IsPartial            bool  `json:"is_partial" gorm:"default:false"`            // True = pago parcial, pendiente de comisión

	// JSON fields for extensibility
	Aditions   string `gorm:"type:json" json:"aditions"`   // []{Label, Value}
	Deductions string `gorm:"type:json" json:"deductions"` // []{Label, Value}

	TotalPaid int64 `json:"total_paid"` // El neto pagado

	Notes      string `gorm:"size:255" json:"notes"`
	CreatedBy  string `json:"created_by"`  // Usuario que generó el pago
	IsSigned   bool   `json:"is_signed"`   // Si el contrato está firmado
	SignedFile string `json:"signed_file"` // Ruta al archivo del contrato (PDF)
	// Firma electrónica por enlace (token + cédula + firma dibujada)
	SignatureTokenHash      string     `gorm:"size:64;index" json:"-"`
	SignatureTokenExpiresAt *time.Time `json:"signature_token_expires_at,omitempty"`
	SignatureLinkSentAt     *time.Time `json:"signature_link_sent_at,omitempty"`
	SignatureRequestedBy    string     `gorm:"size:100" json:"signature_requested_by,omitempty"`
	SignedAt                *time.Time `json:"signed_at,omitempty"`
	SignedIP                string     `gorm:"size:64" json:"signed_ip,omitempty"`
	SignedUserAgent         string     `gorm:"size:255" json:"signed_user_agent,omitempty"`
	SignatureMethod         string     `gorm:"size:80" json:"signature_method,omitempty"`
	CreatedAt               time.Time  `json:"created_at"`
}

func (NominaPayment) TableName() string { return "nomina_payments" }

// EmployeePOSAssignment vincula un empleado con un punto de venta y su % de comisión
type EmployeePOSAssignment struct {
	ID                   uint    `gorm:"primaryKey" json:"id"`
	UserID               uint    `gorm:"uniqueIndex:idx_user_pos" json:"user_id"`
	PosName              string  `gorm:"size:191;uniqueIndex:idx_user_pos" json:"pos_name"`
	CommissionPercentage float64 `json:"commission_percentage"` // ej: 5.0 = 5%
}

func (EmployeePOSAssignment) TableName() string { return "employee_pos_assignments" }

// BillingNominaAssignment vincula un pago de nómina con un local para el billing de gastos variables
// El usuario asigna manualmente qué pagos van a cada local
type BillingNominaAssignment struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	Year      int    `gorm:"uniqueIndex:idx_billing_nomina" json:"year"`
	Month     int    `gorm:"uniqueIndex:idx_billing_nomina" json:"month"`
	PosName   string `gorm:"size:191;uniqueIndex:idx_billing_nomina" json:"pos_name"`
	PaymentID uint   `gorm:"uniqueIndex:idx_billing_nomina" json:"payment_id"`
	UserID    uint   `json:"user_id"`
}

func (BillingNominaAssignment) TableName() string { return "billing_nomina_assignments" }
