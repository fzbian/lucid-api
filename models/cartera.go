package models

import "time"

type CarteraCliente struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	Nombre        string    `gorm:"size:200;not null" json:"nombre"`
	Documento     string    `gorm:"size:60" json:"documento"`
	Celular       string    `gorm:"size:40" json:"celular"`
	Email         string    `gorm:"size:160" json:"email"`
	Direccion     string    `gorm:"size:255" json:"direccion"`
	Observaciones string    `gorm:"type:text" json:"observaciones"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (CarteraCliente) TableName() string { return "cartera_clientes" }

type CarteraFactura struct {
	ID                uint                  `gorm:"primaryKey" json:"id"`
	ClienteID         uint                  `gorm:"index;not null" json:"cliente_id"`
	Op                string                `gorm:"size:40;index" json:"op"`
	Concepto          string                `gorm:"size:255;not null" json:"concepto"`
	Observaciones     string                `gorm:"type:text" json:"observaciones"`
	Origen            string                `gorm:"size:30;not null;default:manual" json:"origen"`
	OdooPOSOrderID    *int64                `gorm:"column:odoo_pos_order_id;index" json:"odoo_pos_order_id,omitempty"`
	OdooPOSReference  string                `gorm:"size:120;index" json:"odoo_pos_reference"`
	OdooPOSName       string                `gorm:"size:160" json:"odoo_pos_name"`
	OdooClienteNombre string                `gorm:"size:200" json:"odoo_cliente_nombre"`
	ValorTotal        float64               `gorm:"type:decimal(15,2);not null" json:"valor_total"`
	ValorAbonado      float64               `gorm:"type:decimal(15,2);not null;default:0" json:"valor_abonado"`
	ValorPendiente    float64               `gorm:"type:decimal(15,2);not null;default:0" json:"valor_pendiente"`
	Estado            string                `gorm:"size:30;index;not null" json:"estado"`
	FechaEmision      time.Time             `gorm:"not null" json:"fecha_emision"`
	CreatedAt         time.Time             `json:"created_at"`
	UpdatedAt         time.Time             `json:"updated_at"`
	Cliente           CarteraCliente        `gorm:"foreignKey:ClienteID" json:"cliente,omitempty"`
	Lineas            []CarteraFacturaLinea `gorm:"foreignKey:FacturaID;constraint:OnDelete:CASCADE" json:"lineas,omitempty"`
}

func (CarteraFactura) TableName() string { return "cartera_facturas" }

type CarteraFacturaLinea struct {
	ID            uint           `gorm:"primaryKey" json:"id"`
	FacturaID     uint           `gorm:"index;not null" json:"factura_id"`
	Concepto      string         `gorm:"size:255;not null" json:"concepto"`
	Cantidad      float64        `gorm:"type:decimal(15,3);not null;default:1" json:"cantidad"`
	ValorUnitario float64        `gorm:"type:decimal(15,2);not null;default:0" json:"valor_unitario"`
	Valor         float64        `gorm:"type:decimal(15,2);not null" json:"valor"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	Factura       CarteraFactura `gorm:"foreignKey:FacturaID" json:"factura,omitempty"`
}

func (CarteraFacturaLinea) TableName() string { return "cartera_factura_lineas" }

type CarteraAbono struct {
	ID                  uint                     `gorm:"primaryKey" json:"id"`
	ClienteID           uint                     `gorm:"index;not null" json:"cliente_id"`
	MetodoPago          string                   `gorm:"size:60;not null" json:"metodo_pago"`
	MontoTotal          float64                  `gorm:"type:decimal(15,2);not null" json:"monto_total"`
	Referencia          string                   `gorm:"size:120" json:"referencia"`
	Observaciones       string                   `gorm:"type:text" json:"observaciones"`
	SoporteNombre       string                   `gorm:"size:255" json:"soporte_nombre"`
	SoportePath         string                   `gorm:"size:500" json:"soporte_path"`
	OrigenTransaccionID *int32                   `gorm:"column:origen_transaccion_id;index" json:"origen_transaccion_id,omitempty"`
	FechaPago           time.Time                `gorm:"not null" json:"fecha_pago"`
	CreatedAt           time.Time                `json:"created_at"`
	UpdatedAt           time.Time                `json:"updated_at"`
	Cliente             CarteraCliente           `gorm:"foreignKey:ClienteID" json:"cliente,omitempty"`
	Aplicaciones        []CarteraAbonoAplicacion `gorm:"foreignKey:AbonoID" json:"aplicaciones,omitempty"`
}

func (CarteraAbono) TableName() string { return "cartera_abonos" }

type CarteraAbonoAplicacion struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	AbonoID   uint           `gorm:"not null;index;uniqueIndex:uni_cartera_abono_factura" json:"abono_id"`
	FacturaID uint           `gorm:"not null;index;uniqueIndex:uni_cartera_abono_factura" json:"factura_id"`
	Valor     float64        `gorm:"type:decimal(15,2);not null" json:"valor"`
	Abono     CarteraAbono   `gorm:"foreignKey:AbonoID" json:"abono,omitempty"`
	Factura   CarteraFactura `gorm:"foreignKey:FacturaID" json:"factura,omitempty"`
}

func (CarteraAbonoAplicacion) TableName() string { return "cartera_abono_aplicaciones" }

type CarteraClienteInput struct {
	Nombre        string `json:"nombre" binding:"required"`
	Documento     string `json:"documento"`
	Celular       string `json:"celular"`
	Email         string `json:"email"`
	Direccion     string `json:"direccion"`
	Observaciones string `json:"observaciones"`
}

type CarteraFacturaInput struct {
	Concepto          string                     `json:"concepto" binding:"required"`
	ValorTotal        float64                    `json:"valor_total" binding:"required"`
	Observaciones     string                     `json:"observaciones"`
	FechaEmision      *time.Time                 `json:"fecha_emision"`
	Origen            string                     `json:"origen"`
	OdooPOSOrderID    *int64                     `json:"odoo_pos_order_id"`
	OdooPOSReference  string                     `json:"odoo_pos_reference"`
	OdooPOSName       string                     `json:"odoo_pos_name"`
	OdooClienteNombre string                     `json:"odoo_cliente_nombre"`
	Lineas            []CarteraFacturaLineaInput `json:"lineas"`
}

type CarteraFacturaLineaInput struct {
	Concepto      string  `json:"concepto"`
	Cantidad      float64 `json:"cantidad"`
	ValorUnitario float64 `json:"valor_unitario"`
	Valor         float64 `json:"valor"`
}

type CarteraAbonoDistribucionInput struct {
	FacturaID uint    `json:"factura_id" binding:"required"`
	Valor     float64 `json:"valor" binding:"required"`
}

type CarteraAbonoSoporteInput struct {
	Nombre string `json:"nombre"`
	Path   string `json:"path"`
	URL    string `json:"url"`
}

type CarteraAbonoInput struct {
	ClienteID     uint                            `json:"cliente_id" binding:"required"`
	MetodoPago    string                          `json:"metodo_pago" binding:"required"`
	MontoTotal    float64                         `json:"monto_total" binding:"required"`
	FechaPago     *time.Time                      `json:"fecha_pago"`
	Referencia    string                          `json:"referencia"`
	Observaciones string                          `json:"observaciones"`
	Soporte       *CarteraAbonoSoporteInput       `json:"soporte"`
	Distribucion  []CarteraAbonoDistribucionInput `json:"distribucion" binding:"required"`
	TransaccionID *int32                          `json:"transaccion_id,omitempty"`
}
