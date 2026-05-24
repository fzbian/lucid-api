package controllers

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"atm/models"
	"atm/notify"
)

const (
	carteraEstadoPendiente = "PENDIENTE"
	carteraEstadoParcial   = "PARCIAL"
	carteraEstadoPagada    = "PAGADA"
	carteraSupportMaxSize  = 8 << 20
	carteraPDFMaxSize      = 10 << 20
)

type carteraClienteResumenResponse struct {
	models.CarteraCliente
	TotalFacturado          float64 `json:"total_facturado"`
	TotalAbonado            float64 `json:"total_abonado"`
	TotalPendiente          float64 `json:"total_pendiente"`
	FacturasCount           int64   `json:"facturas_count"`
	FacturasPendientes      int64   `json:"facturas_pendientes"`
	IngresosPendientes      int64   `json:"ingresos_pendientes_count"`
	TotalIngresosPendientes float64 `json:"ingresos_pendientes_total"`
}

type carteraSoporteResponse struct {
	Nombre string `json:"nombre"`
	Path   string `json:"path"`
	URL    string `json:"url"`
}

type carteraFacturaMiniResponse struct {
	ID             uint    `json:"id"`
	Op             string  `json:"op"`
	Concepto       string  `json:"concepto"`
	Estado         string  `json:"estado"`
	ValorTotal     float64 `json:"valor_total"`
	ValorAbonado   float64 `json:"valor_abonado"`
	ValorPendiente float64 `json:"valor_pendiente"`
}

type carteraDistribucionResponse struct {
	FacturaID uint                        `json:"factura_id"`
	Valor     float64                     `json:"valor"`
	Factura   *carteraFacturaMiniResponse `json:"factura,omitempty"`
}

type carteraAbonoResponse struct {
	ID                  uint                          `json:"id"`
	ClienteID           uint                          `json:"cliente_id"`
	MetodoPago          string                        `json:"metodo_pago"`
	MontoTotal          float64                       `json:"monto_total"`
	Referencia          string                        `json:"referencia"`
	Observaciones       string                        `json:"observaciones"`
	FechaPago           time.Time                     `json:"fecha_pago"`
	CreatedAt           time.Time                     `json:"created_at"`
	UpdatedAt           time.Time                     `json:"updated_at"`
	ValorAplicado       float64                       `json:"valor_aplicado,omitempty"`
	OrigenTransaccionID *int32                        `json:"origen_transaccion_id,omitempty"`
	Soporte             *carteraSoporteResponse       `json:"soporte,omitempty"`
	Distribucion        []carteraDistribucionResponse `json:"distribucion"`
	Cliente             *models.CarteraCliente        `json:"cliente,omitempty"`
}

type carteraClienteResumenRow struct {
	ClienteID          uint
	TotalFacturado     float64
	TotalAbonado       float64
	TotalPendiente     float64
	FacturasCount      int64
	FacturasPendientes int64
}

type carteraPendienteIngresoResumenRow struct {
	ClienteID       uint
	CountPendientes int64
	TotalPendiente  float64
}

type carteraPendingIngresoResponse struct {
	ID          int32     `json:"id"`
	ClienteID   uint      `json:"cliente_id"`
	Monto       float64   `json:"monto"`
	Descripcion string    `json:"descripcion"`
	Fecha       time.Time `json:"fecha"`
	CajaID      int32     `json:"caja_id"`
	Usuario     string    `json:"usuario"`
}

type carteraEstadoCuentaNotifyInput struct {
	PDFBase64 string `json:"pdf_base64" binding:"required"`
	PDFNombre string `json:"pdf_nombre"`
}

func ListCarteraClientes(c *gin.Context) {
	search := strings.TrimSpace(c.Query("search"))

	var clientes []models.CarteraCliente
	query := DB.Model(&models.CarteraCliente{})
	if search != "" {
		like := "%" + search + "%"
		query = query.Where(
			"nombre LIKE ? OR documento LIKE ? OR celular LIKE ? OR email LIKE ?",
			like, like, like, like,
		)
	}

	if err := query.Order("updated_at desc, nombre asc").Find(&clientes).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudieron cargar los clientes"})
		return
	}

	summaryByClient, err := buildCarteraResumenPorCliente(DB, extractCarteraClienteIDs(clientes))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudieron calcular los saldos de cartera"})
		return
	}
	pendingByClient, err := buildPendingIngresosSummaryByClient(DB, extractCarteraClienteIDs(clientes))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudieron calcular los ingresos pendientes de cartera"})
		return
	}

	resp := make([]carteraClienteResumenResponse, 0, len(clientes))
	for _, cliente := range clientes {
		row := summaryByClient[cliente.ID]
		pending := pendingByClient[cliente.ID]
		resp = append(resp, carteraClienteResumenResponse{
			CarteraCliente:          cliente,
			TotalFacturado:          roundMoney(row.TotalFacturado),
			TotalAbonado:            roundMoney(row.TotalAbonado),
			TotalPendiente:          roundMoney(row.TotalPendiente),
			FacturasCount:           row.FacturasCount,
			FacturasPendientes:      row.FacturasPendientes,
			IngresosPendientes:      pending.CountPendientes,
			TotalIngresosPendientes: roundMoney(pending.TotalPendiente),
		})
	}

	c.JSON(http.StatusOK, resp)
}

func GetCarteraCliente(c *gin.Context) {
	clienteID, ok := parseUintParam(c, "id")
	if !ok {
		return
	}

	var cliente models.CarteraCliente
	if err := DB.First(&cliente, clienteID).Error; err != nil {
		handleCarteraNotFound(c, err, "cliente no encontrado")
		return
	}

	summaryByClient, err := buildCarteraResumenPorCliente(DB, []uint{cliente.ID})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudo calcular el resumen del cliente"})
		return
	}
	pendingByClient, err := buildPendingIngresosSummaryByClient(DB, []uint{cliente.ID})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudieron calcular los ingresos pendientes del cliente"})
		return
	}

	row := summaryByClient[cliente.ID]
	pending := pendingByClient[cliente.ID]
	c.JSON(http.StatusOK, carteraClienteResumenResponse{
		CarteraCliente:          cliente,
		TotalFacturado:          roundMoney(row.TotalFacturado),
		TotalAbonado:            roundMoney(row.TotalAbonado),
		TotalPendiente:          roundMoney(row.TotalPendiente),
		FacturasCount:           row.FacturasCount,
		FacturasPendientes:      row.FacturasPendientes,
		IngresosPendientes:      pending.CountPendientes,
		TotalIngresosPendientes: roundMoney(pending.TotalPendiente),
	})
}

func NotifyCarteraEstadoCuenta(c *gin.Context) {
	clienteID, ok := parseUintParam(c, "id")
	if !ok {
		return
	}

	var cliente models.CarteraCliente
	if err := DB.First(&cliente, clienteID).Error; err != nil {
		handleCarteraNotFound(c, err, "cliente no encontrado")
		return
	}

	number := normalizeWhatsAppNumber(cliente.Celular)
	if number == "" {
		if strings.TrimSpace(cliente.Celular) == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "el cliente no tiene un celular registrado para WhatsApp"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "el celular del cliente no es válido para WhatsApp"})
		return
	}

	var facturasCount int64
	if err := DB.Model(&models.CarteraFactura{}).Where("cliente_id = ?", clienteID).Count(&facturasCount).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudo validar el estado de cuenta del cliente"})
		return
	}
	if facturasCount == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "este cliente no tiene facturas para enviar en el estado de cuenta"})
		return
	}

	var input carteraEstadoCuentaNotifyInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "debes enviar un PDF válido del estado de cuenta"})
		return
	}

	pdfBytes, err := decodeCarteraPDFPayload(input.PDFBase64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	pdfName := sanitizeCarteraPDFFileName(strings.TrimSpace(input.PDFNombre), cliente.ID)
	caption, message := buildCarteraEstadoCuentaDeliveryMessage(cliente.Nombre)
	if err := notify.SendPDFToNumber(number, pdfBytes, pdfName, caption, message); err != nil {
		fmt.Printf("[CARTERA] no se pudo enviar estado de cuenta cliente=%d numero=%s: %v\n", cliente.ID, number, err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "no se pudo enviar el estado de cuenta por WhatsApp"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":     "whatsapp_sent",
		"numero":     number,
		"pdf_nombre": pdfName,
	})
}

func NotifyCarteraFacturaAbonos(c *gin.Context) {
	facturaID, ok := parseUintParam(c, "id")
	if !ok {
		return
	}

	var factura models.CarteraFactura
	if err := DB.Preload("Cliente").
		Preload("Lineas", func(db *gorm.DB) *gorm.DB { return db.Order("id asc") }).
		First(&factura, facturaID).Error; err != nil {
		handleCarteraNotFound(c, err, "factura no encontrada")
		return
	}

	number := normalizeWhatsAppNumber(factura.Cliente.Celular)
	if number == "" {
		if strings.TrimSpace(factura.Cliente.Celular) == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "el cliente de esta factura no tiene un celular registrado para WhatsApp"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "el celular del cliente de esta factura no es válido para WhatsApp"})
		return
	}

	var abonosCount int64
	if err := DB.Model(&models.CarteraAbonoAplicacion{}).Where("factura_id = ?", facturaID).Count(&abonosCount).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudo validar el detalle de abonos de la factura"})
		return
	}
	if abonosCount == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "esta factura no tiene abonos para enviar"})
		return
	}

	var input carteraEstadoCuentaNotifyInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "debes enviar un PDF válido de los abonos de la factura"})
		return
	}

	pdfBytes, err := decodeCarteraPDFPayload(input.PDFBase64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	pdfName := sanitizeCarteraPDFFileName(strings.TrimSpace(input.PDFNombre), factura.ID)
	caption, message := buildCarteraFacturaAbonosDeliveryMessage(factura.Cliente.Nombre, factura.Op)
	if err := notify.SendPDFToNumber(number, pdfBytes, pdfName, caption, message); err != nil {
		fmt.Printf("[CARTERA] no se pudo enviar detalle de abonos factura=%d numero=%s: %v\n", factura.ID, number, err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "no se pudo enviar el detalle de abonos por WhatsApp"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":      "whatsapp_sent",
		"numero":      number,
		"pdf_nombre":  pdfName,
		"factura_id":  factura.ID,
		"factura_op":  factura.Op,
		"cliente_id":  factura.ClienteID,
		"cliente_nom": factura.Cliente.Nombre,
	})
}

func CreateCarteraCliente(c *gin.Context) {
	var input models.CarteraClienteInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "datos de cliente inválidos"})
		return
	}

	cliente := models.CarteraCliente{
		Nombre:        strings.TrimSpace(input.Nombre),
		Documento:     strings.TrimSpace(input.Documento),
		Celular:       strings.TrimSpace(input.Celular),
		Email:         strings.TrimSpace(input.Email),
		Direccion:     strings.TrimSpace(input.Direccion),
		Observaciones: strings.TrimSpace(input.Observaciones),
	}
	if cliente.Nombre == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "el nombre del cliente es obligatorio"})
		return
	}

	if err := DB.Create(&cliente).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudo crear el cliente"})
		return
	}

	c.JSON(http.StatusCreated, carteraClienteResumenResponse{CarteraCliente: cliente})
}

func UpdateCarteraCliente(c *gin.Context) {
	clienteID, ok := parseUintParam(c, "id")
	if !ok {
		return
	}

	var cliente models.CarteraCliente
	if err := DB.First(&cliente, clienteID).Error; err != nil {
		handleCarteraNotFound(c, err, "cliente no encontrado")
		return
	}

	var input models.CarteraClienteInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "datos de cliente inválidos"})
		return
	}

	nombre := strings.TrimSpace(input.Nombre)
	if nombre == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "el nombre del cliente es obligatorio"})
		return
	}

	cliente.Nombre = nombre
	cliente.Documento = strings.TrimSpace(input.Documento)
	cliente.Celular = strings.TrimSpace(input.Celular)
	cliente.Email = strings.TrimSpace(input.Email)
	cliente.Direccion = strings.TrimSpace(input.Direccion)
	cliente.Observaciones = strings.TrimSpace(input.Observaciones)

	if err := DB.Save(&cliente).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudo actualizar el cliente"})
		return
	}

	summaryByClient, err := buildCarteraResumenPorCliente(DB, []uint{cliente.ID})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "el cliente fue actualizado, pero no se pudo recalcular su resumen"})
		return
	}
	pendingByClient, err := buildPendingIngresosSummaryByClient(DB, []uint{cliente.ID})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "el cliente fue actualizado, pero no se pudieron recalcular sus ingresos pendientes"})
		return
	}
	row := summaryByClient[cliente.ID]
	pending := pendingByClient[cliente.ID]
	c.JSON(http.StatusOK, carteraClienteResumenResponse{
		CarteraCliente:          cliente,
		TotalFacturado:          roundMoney(row.TotalFacturado),
		TotalAbonado:            roundMoney(row.TotalAbonado),
		TotalPendiente:          roundMoney(row.TotalPendiente),
		FacturasCount:           row.FacturasCount,
		FacturasPendientes:      row.FacturasPendientes,
		IngresosPendientes:      pending.CountPendientes,
		TotalIngresosPendientes: roundMoney(pending.TotalPendiente),
	})
}

func DeleteCarteraCliente(c *gin.Context) {
	clienteID, ok := parseUintParam(c, "id")
	if !ok {
		return
	}

	var cliente models.CarteraCliente
	if err := DB.First(&cliente, clienteID).Error; err != nil {
		handleCarteraNotFound(c, err, "cliente no encontrado")
		return
	}

	if err := DB.Transaction(func(tx *gorm.DB) error {
		var linkedTransactions int64
		if err := tx.Model(&models.Transaccion{}).
			Where("cartera_cliente_id = ?", clienteID).
			Count(&linkedTransactions).Error; err != nil {
			return err
		}
		if linkedTransactions > 0 {
			return fmt.Errorf("cliente_con_movimientos_cartera")
		}

		var invoiceIDs []uint
		if err := tx.Model(&models.CarteraFactura{}).Where("cliente_id = ?", clienteID).Pluck("id", &invoiceIDs).Error; err != nil {
			return err
		}

		var abonoIDs []uint
		if err := tx.Model(&models.CarteraAbono{}).Where("cliente_id = ?", clienteID).Pluck("id", &abonoIDs).Error; err != nil {
			return err
		}

		if len(invoiceIDs) > 0 {
			if err := tx.Where("factura_id IN ?", invoiceIDs).Delete(&models.CarteraFacturaLinea{}).Error; err != nil {
				return err
			}
			if err := tx.Where("factura_id IN ?", invoiceIDs).Delete(&models.CarteraAbonoAplicacion{}).Error; err != nil {
				return err
			}
			if err := tx.Where("cliente_id = ?", clienteID).Delete(&models.CarteraFactura{}).Error; err != nil {
				return err
			}
		}

		if len(abonoIDs) > 0 {
			if err := tx.Where("abono_id IN ?", abonoIDs).Delete(&models.CarteraAbonoAplicacion{}).Error; err != nil {
				return err
			}
			if err := tx.Where("cliente_id = ?", clienteID).Delete(&models.CarteraAbono{}).Error; err != nil {
				return err
			}
		}

		return tx.Delete(&models.CarteraCliente{}, clienteID).Error
	}); err != nil {
		if err.Error() == "cliente_con_movimientos_cartera" {
			c.JSON(http.StatusConflict, gin.H{"error": "no puedes eliminar este cliente porque tiene movimientos registrados en caja para cartera"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudo eliminar el cliente"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"id": cliente.ID, "message": "cliente eliminado"})
}

func ListCarteraPendingIngresosByClient(c *gin.Context) {
	clienteID, ok := parseUintParam(c, "id")
	if !ok {
		return
	}

	var cliente models.CarteraCliente
	if err := DB.First(&cliente, clienteID).Error; err != nil {
		handleCarteraNotFound(c, err, "cliente no encontrado")
		return
	}

	ingresos, err := listPendingIngresosByClient(DB, clienteID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudieron cargar los ingresos pendientes"})
		return
	}

	c.JSON(http.StatusOK, ingresos)
}

func GetCarteraPendingIngreso(c *gin.Context) {
	transaccionID, ok := parseInt32Param(c, "id")
	if !ok {
		return
	}

	ingreso, err := loadPendingIngresoByID(DB, transaccionID)
	if err != nil {
		handleCarteraNotFound(c, err, "ingreso pendiente no encontrado")
		return
	}

	c.JSON(http.StatusOK, ingreso)
}

func ListCarteraFacturasPorCliente(c *gin.Context) {
	clienteID, ok := parseUintParam(c, "id")
	if !ok {
		return
	}

	var cliente models.CarteraCliente
	if err := DB.First(&cliente, clienteID).Error; err != nil {
		handleCarteraNotFound(c, err, "cliente no encontrado")
		return
	}

	var facturas []models.CarteraFactura
	if err := DB.Where("cliente_id = ?", clienteID).
		Preload("Cliente").
		Preload("Lineas", func(db *gorm.DB) *gorm.DB { return db.Order("id asc") }).
		Order("fecha_emision desc, id desc").
		Find(&facturas).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudieron cargar las facturas"})
		return
	}

	for i := range facturas {
		facturas[i].Cliente = cliente
	}

	c.JSON(http.StatusOK, facturas)
}

func GetCarteraFactura(c *gin.Context) {
	facturaID, ok := parseUintParam(c, "id")
	if !ok {
		return
	}

	var factura models.CarteraFactura
	if err := DB.Preload("Cliente").
		Preload("Lineas", func(db *gorm.DB) *gorm.DB { return db.Order("id asc") }).
		First(&factura, facturaID).Error; err != nil {
		handleCarteraNotFound(c, err, "factura no encontrada")
		return
	}

	c.JSON(http.StatusOK, factura)
}

func CreateCarteraFactura(c *gin.Context) {
	clienteID, ok := parseUintParam(c, "id")
	if !ok {
		return
	}

	var cliente models.CarteraCliente
	if err := DB.First(&cliente, clienteID).Error; err != nil {
		handleCarteraNotFound(c, err, "cliente no encontrado")
		return
	}

	var input models.CarteraFacturaInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "datos de factura inválidos"})
		return
	}

	concepto := strings.TrimSpace(input.Concepto)
	if concepto == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "el concepto de la factura es obligatorio"})
		return
	}
	lineas, lineasTotal, err := normalizeCarteraFacturaLineas(input.Lineas)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	valorTotal := resolveCarteraFacturaValorTotal(input.ValorTotal, lineasTotal, len(lineas) > 0)
	if valorTotal <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "el valor de la factura debe ser mayor a 0"})
		return
	}

	fechaEmision := time.Now()
	if input.FechaEmision != nil && !input.FechaEmision.IsZero() {
		fechaEmision = input.FechaEmision.In(time.Local)
	}

	factura := models.CarteraFactura{
		ClienteID:      clienteID,
		Concepto:       concepto,
		Observaciones:  strings.TrimSpace(input.Observaciones),
		Origen:         "manual",
		ValorTotal:     valorTotal,
		ValorAbonado:   0,
		ValorPendiente: valorTotal,
		Estado:         carteraEstadoPendiente,
		FechaEmision:   fechaEmision,
	}
	assignCarteraFacturaSource(&factura, input)

	if err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&factura).Error; err != nil {
			return err
		}
		factura.Op = fmt.Sprintf("CAR-%06d", factura.ID)
		if err := tx.Model(&factura).Update("op", factura.Op).Error; err != nil {
			return err
		}
		if len(lineas) == 0 {
			return nil
		}
		for i := range lineas {
			lineas[i].FacturaID = factura.ID
		}
		return tx.Create(&lineas).Error
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudo crear la factura"})
		return
	}

	if err := DB.Preload("Cliente").
		Preload("Lineas", func(db *gorm.DB) *gorm.DB { return db.Order("id asc") }).
		First(&factura, factura.ID).Error; err != nil {
		factura.Cliente = cliente
		factura.Lineas = lineas
	}
	c.JSON(http.StatusCreated, factura)
}

func UpdateCarteraFactura(c *gin.Context) {
	facturaID, ok := parseUintParam(c, "id")
	if !ok {
		return
	}

	var factura models.CarteraFactura
	if err := DB.Preload("Cliente").
		Preload("Lineas", func(db *gorm.DB) *gorm.DB { return db.Order("id asc") }).
		First(&factura, facturaID).Error; err != nil {
		handleCarteraNotFound(c, err, "factura no encontrada")
		return
	}

	var input models.CarteraFacturaInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "datos de factura inválidos"})
		return
	}

	concepto := strings.TrimSpace(input.Concepto)
	if concepto == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "el concepto de la factura es obligatorio"})
		return
	}
	lineas, lineasTotal, err := normalizeCarteraFacturaLineas(input.Lineas)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	valorTotal := resolveCarteraFacturaValorTotal(input.ValorTotal, lineasTotal, len(lineas) > 0)
	if valorTotal <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "el valor de la factura debe ser mayor a 0"})
		return
	}
	if valorTotal+0.009 < factura.ValorAbonado {
		c.JSON(http.StatusBadRequest, gin.H{"error": "el valor total no puede ser menor a lo ya abonado"})
		return
	}

	factura.Concepto = concepto
	factura.Observaciones = strings.TrimSpace(input.Observaciones)
	assignCarteraFacturaSource(&factura, input)
	factura.ValorTotal = valorTotal
	factura.ValorPendiente = roundMoney(math.Max(0, factura.ValorTotal-factura.ValorAbonado))
	factura.Estado = carteraEstadoFromValues(factura.ValorTotal, factura.ValorAbonado)
	if input.FechaEmision != nil && !input.FechaEmision.IsZero() {
		factura.FechaEmision = input.FechaEmision.In(time.Local)
	}

	if err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&factura).Error; err != nil {
			return err
		}
		if err := tx.Where("factura_id = ?", factura.ID).Delete(&models.CarteraFacturaLinea{}).Error; err != nil {
			return err
		}
		if len(lineas) == 0 {
			return nil
		}
		for i := range lineas {
			lineas[i].FacturaID = factura.ID
		}
		return tx.Create(&lineas).Error
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudo actualizar la factura"})
		return
	}

	if err := DB.Preload("Cliente").
		Preload("Lineas", func(db *gorm.DB) *gorm.DB { return db.Order("id asc") }).
		First(&factura, factura.ID).Error; err != nil {
		factura.Lineas = lineas
	}
	c.JSON(http.StatusOK, factura)
}

func DeleteCarteraFactura(c *gin.Context) {
	facturaID, ok := parseUintParam(c, "id")
	if !ok {
		return
	}

	var factura models.CarteraFactura
	if err := DB.Preload("Cliente").
		Preload("Lineas", func(db *gorm.DB) *gorm.DB { return db.Order("id asc") }).
		First(&factura, facturaID).Error; err != nil {
		handleCarteraNotFound(c, err, "factura no encontrada")
		return
	}

	if err := DB.Transaction(func(tx *gorm.DB) error {
		var abonoIDs []uint
		if err := tx.Model(&models.CarteraAbonoAplicacion{}).
			Where("factura_id = ?", facturaID).
			Pluck("abono_id", &abonoIDs).Error; err != nil {
			return err
		}
		if len(abonoIDs) > 0 {
			var linkedCount int64
			if err := tx.Model(&models.CarteraAbono{}).
				Where("id IN ? AND origen_transaccion_id IS NOT NULL", uniqueUintIDs(abonoIDs)).
				Count(&linkedCount).Error; err != nil {
				return err
			}
			if linkedCount > 0 {
				return fmt.Errorf("factura_con_ingreso_caja_asignado")
			}
		}

		if err := tx.Where("factura_id = ?", facturaID).Delete(&models.CarteraAbonoAplicacion{}).Error; err != nil {
			return err
		}

		if err := tx.Where("factura_id = ?", facturaID).Delete(&models.CarteraFacturaLinea{}).Error; err != nil {
			return err
		}

		if err := tx.Delete(&models.CarteraFactura{}, facturaID).Error; err != nil {
			return err
		}

		return syncAbonoTotalsWithApplications(tx, abonoIDs)
	}); err != nil {
		if err.Error() == "factura_con_ingreso_caja_asignado" {
			c.JSON(http.StatusConflict, gin.H{"error": "esta factura tiene un abono ligado a un ingreso de caja. Primero elimina o reasigna ese abono."})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudo eliminar la factura"})
		return
	}

	c.JSON(http.StatusOK, factura)
}

func UploadCarteraSoporte(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "archivo no enviado"})
		return
	}
	if file.Size <= 0 || file.Size > carteraSupportMaxSize {
		c.JSON(http.StatusBadRequest, gin.H{"error": "el archivo debe ser menor o igual a 8MB"})
		return
	}

	ext := strings.ToLower(filepath.Ext(file.Filename))
	allowed := map[string]bool{
		".jpg":  true,
		".jpeg": true,
		".png":  true,
		".webp": true,
		".gif":  true,
	}
	if !allowed[ext] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "solo se permiten imágenes jpg, png, webp o gif"})
		return
	}

	if err := os.MkdirAll(filepath.Join("uploads", "cartera"), 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudo preparar el almacenamiento"})
		return
	}

	filename := fmt.Sprintf("cartera_%d%s", time.Now().UnixNano(), ext)
	dst := filepath.Join("uploads", "cartera", filename)
	if err := c.SaveUploadedFile(file, dst); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudo guardar el soporte"})
		return
	}

	relPath := "/uploads/cartera/" + filename
	c.JSON(http.StatusCreated, gin.H{
		"url":     relPath,
		"fullUrl": carteraAbsoluteURL(c, relPath),
		"nombre":  file.Filename,
	})
}

func ListCarteraAbonosPorFactura(c *gin.Context) {
	facturaID, ok := parseUintParam(c, "id")
	if !ok {
		return
	}

	var factura models.CarteraFactura
	if err := DB.Preload("Cliente").
		Preload("Lineas", func(db *gorm.DB) *gorm.DB { return db.Order("id asc") }).
		First(&factura, facturaID).Error; err != nil {
		handleCarteraNotFound(c, err, "factura no encontrada")
		return
	}

	var aplicaciones []models.CarteraAbonoAplicacion
	if err := DB.Where("factura_id = ?", facturaID).
		Order("id desc").
		Preload("Abono.Cliente").
		Preload("Abono.Aplicaciones.Factura").
		Find(&aplicaciones).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudieron cargar los abonos"})
		return
	}

	resp := make([]carteraAbonoResponse, 0, len(aplicaciones))
	for _, aplicacion := range aplicaciones {
		abono := aplicacion.Abono
		resp = append(resp, serializeCarteraAbono(c, &abono, facturaID))
	}

	c.JSON(http.StatusOK, gin.H{
		"factura": factura,
		"abonos":  resp,
	})
}

func GetCarteraAbono(c *gin.Context) {
	abonoID, ok := parseUintParam(c, "id")
	if !ok {
		return
	}

	abono, err := loadCarteraAbonoWithRelations(DB, abonoID)
	if err != nil {
		handleCarteraNotFound(c, err, "abono no encontrado")
		return
	}

	c.JSON(http.StatusOK, serializeCarteraAbono(c, &abono, 0))
}

func CreateCarteraAbono(c *gin.Context) {
	var input models.CarteraAbonoInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "datos de abono inválidos"})
		return
	}

	distribucion, totalDistribuido, err := normalizeDistribucionCartera(input.Distribucion)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if roundMoney(input.MontoTotal) != totalDistribuido {
		c.JSON(http.StatusBadRequest, gin.H{"error": "el monto total debe coincidir exactamente con la distribución"})
		return
	}

	var abono models.CarteraAbono
	if err := DB.Transaction(func(tx *gorm.DB) error {
		if err := validateCarteraAbonoInput(tx, input.ClienteID, distribucion, nil); err != nil {
			return err
		}
		sourceTx, err := validateSourceTransactionForAbono(tx, input.ClienteID, input.TransaccionID, totalDistribuido, nil)
		if err != nil {
			return err
		}

		soporteNombre, soportePath := normalizeCarteraSoporte(input.Soporte)
		abono = models.CarteraAbono{
			ClienteID:           input.ClienteID,
			MetodoPago:          strings.ToUpper(strings.TrimSpace(input.MetodoPago)),
			MontoTotal:          totalDistribuido,
			Referencia:          strings.TrimSpace(input.Referencia),
			Observaciones:       strings.TrimSpace(input.Observaciones),
			SoporteNombre:       soporteNombre,
			SoportePath:         soportePath,
			OrigenTransaccionID: input.TransaccionID,
			FechaPago:           resolveCarteraFechaPago(input.FechaPago, sourceTransactionTime(sourceTx, time.Now())),
		}
		if abono.MetodoPago == "" {
			return fmt.Errorf("el método de pago es obligatorio")
		}
		if err := tx.Create(&abono).Error; err != nil {
			return err
		}

		aplicaciones := make([]models.CarteraAbonoAplicacion, 0, len(distribucion))
		invoiceIDs := make([]uint, 0, len(distribucion))
		for _, item := range distribucion {
			aplicaciones = append(aplicaciones, models.CarteraAbonoAplicacion{
				AbonoID:   abono.ID,
				FacturaID: item.FacturaID,
				Valor:     item.Valor,
			})
			invoiceIDs = append(invoiceIDs, item.FacturaID)
		}
		if err := tx.Create(&aplicaciones).Error; err != nil {
			return err
		}

		if err := recalculateInvoiceBalances(tx, invoiceIDs); err != nil {
			return err
		}
		if sourceTx != nil {
			if err := tx.Model(&models.Transaccion{}).
				Where("id = ?", sourceTx.ID).
				Update("cartera_abono_id", abono.ID).Error; err != nil {
				return err
			}
		}

		return nil
	}); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	abono, err = loadCarteraAbonoWithRelations(DB, abono.ID)
	if err != nil {
		c.JSON(http.StatusCreated, gin.H{"id": abono.ID, "message": "abono creado"})
		return
	}

	c.JSON(http.StatusCreated, serializeCarteraAbono(c, &abono, 0))
}

func UpdateCarteraAbono(c *gin.Context) {
	abonoID, ok := parseUintParam(c, "id")
	if !ok {
		return
	}

	var input models.CarteraAbonoInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "datos de abono inválidos"})
		return
	}

	distribucion, totalDistribuido, err := normalizeDistribucionCartera(input.Distribucion)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var abono models.CarteraAbono
	if err := DB.Transaction(func(tx *gorm.DB) error {
		current, err := loadCarteraAbonoWithRelations(tx, abonoID)
		if err != nil {
			return err
		}
		if input.ClienteID != 0 && input.ClienteID != current.ClienteID {
			return fmt.Errorf("no se puede mover un abono a otro cliente")
		}
		if roundMoney(input.MontoTotal) != totalDistribuido {
			return fmt.Errorf("el monto total debe coincidir exactamente con la distribución")
		}
		if _, err := validateSourceTransactionForAbono(tx, current.ClienteID, current.OrigenTransaccionID, totalDistribuido, &current.ID); err != nil {
			return err
		}

		extraAllowance := map[uint]float64{}
		affectedInvoiceIDs := make([]uint, 0, len(current.Aplicaciones)+len(distribucion))
		for _, item := range current.Aplicaciones {
			extraAllowance[item.FacturaID] = roundMoney(extraAllowance[item.FacturaID] + item.Valor)
			affectedInvoiceIDs = append(affectedInvoiceIDs, item.FacturaID)
		}

		if err := validateCarteraAbonoInput(tx, current.ClienteID, distribucion, extraAllowance); err != nil {
			return err
		}

		soporteNombre, soportePath := normalizeCarteraSoporte(input.Soporte)
		if soportePath == "" {
			soporteNombre = current.SoporteNombre
			soportePath = current.SoportePath
		}

		current.MetodoPago = strings.ToUpper(strings.TrimSpace(input.MetodoPago))
		current.MontoTotal = totalDistribuido
		current.FechaPago = resolveCarteraFechaPago(input.FechaPago, current.FechaPago)
		current.Referencia = strings.TrimSpace(input.Referencia)
		current.Observaciones = strings.TrimSpace(input.Observaciones)
		current.SoporteNombre = soporteNombre
		current.SoportePath = soportePath
		if current.MetodoPago == "" {
			return fmt.Errorf("el método de pago es obligatorio")
		}
		if err := tx.Save(&current).Error; err != nil {
			return err
		}

		if err := tx.Where("abono_id = ?", current.ID).Delete(&models.CarteraAbonoAplicacion{}).Error; err != nil {
			return err
		}

		aplicaciones := make([]models.CarteraAbonoAplicacion, 0, len(distribucion))
		for _, item := range distribucion {
			aplicaciones = append(aplicaciones, models.CarteraAbonoAplicacion{
				AbonoID:   current.ID,
				FacturaID: item.FacturaID,
				Valor:     item.Valor,
			})
			affectedInvoiceIDs = append(affectedInvoiceIDs, item.FacturaID)
		}
		if len(aplicaciones) > 0 {
			if err := tx.Create(&aplicaciones).Error; err != nil {
				return err
			}
		}

		if err := recalculateInvoiceBalances(tx, affectedInvoiceIDs); err != nil {
			return err
		}

		abono, err = loadCarteraAbonoWithRelations(tx, current.ID)
		return err
	}); err != nil {
		status := http.StatusBadRequest
		if errorsIsNotFound(err) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, serializeCarteraAbono(c, &abono, 0))
}

func DeleteCarteraAbono(c *gin.Context) {
	abonoID, ok := parseUintParam(c, "id")
	if !ok {
		return
	}

	abono, err := loadCarteraAbonoWithRelations(DB, abonoID)
	if err != nil {
		handleCarteraNotFound(c, err, "abono no encontrado")
		return
	}

	if err := DB.Transaction(func(tx *gorm.DB) error {
		affectedInvoiceIDs := make([]uint, 0, len(abono.Aplicaciones))
		for _, item := range abono.Aplicaciones {
			affectedInvoiceIDs = append(affectedInvoiceIDs, item.FacturaID)
		}

		if err := tx.Where("abono_id = ?", abonoID).Delete(&models.CarteraAbonoAplicacion{}).Error; err != nil {
			return err
		}
		if err := tx.Delete(&models.CarteraAbono{}, abonoID).Error; err != nil {
			return err
		}
		if abono.OrigenTransaccionID != nil {
			if err := tx.Model(&models.Transaccion{}).
				Where("id = ?", *abono.OrigenTransaccionID).
				Update("cartera_abono_id", nil).Error; err != nil {
				return err
			}
		}
		return recalculateInvoiceBalances(tx, affectedInvoiceIDs)
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudo eliminar el abono"})
		return
	}

	c.JSON(http.StatusOK, serializeCarteraAbono(c, &abono, 0))
}

func buildCarteraResumenPorCliente(tx *gorm.DB, clientIDs []uint) (map[uint]carteraClienteResumenRow, error) {
	resp := map[uint]carteraClienteResumenRow{}
	if len(clientIDs) == 0 {
		return resp, nil
	}

	var rows []carteraClienteResumenRow
	if err := tx.Model(&models.CarteraFactura{}).
		Select(`
			cliente_id,
			COALESCE(SUM(valor_total), 0) AS total_facturado,
			COALESCE(SUM(valor_abonado), 0) AS total_abonado,
			COALESCE(SUM(valor_pendiente), 0) AS total_pendiente,
			COUNT(*) AS facturas_count,
			COALESCE(SUM(CASE WHEN estado <> ? THEN 1 ELSE 0 END), 0) AS facturas_pendientes
		`, carteraEstadoPagada).
		Where("cliente_id IN ?", uniqueUintIDs(clientIDs)).
		Group("cliente_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}

	for _, row := range rows {
		resp[row.ClienteID] = row
	}
	return resp, nil
}

func buildPendingIngresosSummaryByClient(tx *gorm.DB, clientIDs []uint) (map[uint]carteraPendienteIngresoResumenRow, error) {
	resp := map[uint]carteraPendienteIngresoResumenRow{}
	if len(clientIDs) == 0 {
		return resp, nil
	}

	var rows []carteraPendienteIngresoResumenRow
	if err := tx.Model(&models.Transaccion{}).
		Joins("JOIN categorias c ON c.id = transacciones.categoria_id").
		Select(`
			transacciones.cartera_cliente_id AS cliente_id,
			COUNT(*) AS count_pendientes,
			COALESCE(SUM(transacciones.monto), 0) AS total_pendiente
		`).
		Where("transacciones.cartera_cliente_id IN ?", uniqueUintIDs(clientIDs)).
		Where("transacciones.cartera_abono_id IS NULL").
		Where("c.is_cartera_clientes = ? AND c.tipo = ?", true, "INGRESO").
		Group("transacciones.cartera_cliente_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}

	for _, row := range rows {
		resp[row.ClienteID] = row
	}
	return resp, nil
}

func listPendingIngresosByClient(tx *gorm.DB, clientID uint) ([]carteraPendingIngresoResponse, error) {
	if clientID == 0 {
		return []carteraPendingIngresoResponse{}, nil
	}

	var transacciones []models.Transaccion
	if err := tx.Model(&models.Transaccion{}).
		Joins("JOIN categorias c ON c.id = transacciones.categoria_id").
		Where("transacciones.cartera_cliente_id = ?", clientID).
		Where("transacciones.cartera_abono_id IS NULL").
		Where("c.is_cartera_clientes = ? AND c.tipo = ?", true, "INGRESO").
		Order("transacciones.fecha desc, transacciones.id desc").
		Find(&transacciones).Error; err != nil {
		return nil, err
	}

	resp := make([]carteraPendingIngresoResponse, 0, len(transacciones))
	for _, t := range transacciones {
		resp = append(resp, carteraPendingIngresoResponse{
			ID:          t.ID,
			ClienteID:   clientID,
			Monto:       roundMoney(t.Monto),
			Descripcion: t.Descripcion,
			Fecha:       t.Fecha,
			CajaID:      t.CajaID,
			Usuario:     t.Usuario,
		})
	}
	return resp, nil
}

func loadPendingIngresoByID(tx *gorm.DB, transaccionID int32) (carteraPendingIngresoResponse, error) {
	var transaccion models.Transaccion
	err := tx.Model(&models.Transaccion{}).
		Joins("JOIN categorias c ON c.id = transacciones.categoria_id").
		Where("transacciones.id = ?", transaccionID).
		Where("transacciones.cartera_abono_id IS NULL").
		Where("c.is_cartera_clientes = ? AND c.tipo = ?", true, "INGRESO").
		First(&transaccion).Error
	if err != nil {
		return carteraPendingIngresoResponse{}, err
	}
	if transaccion.CarteraClienteID == nil || *transaccion.CarteraClienteID == 0 {
		return carteraPendingIngresoResponse{}, gorm.ErrRecordNotFound
	}
	return carteraPendingIngresoResponse{
		ID:          transaccion.ID,
		ClienteID:   *transaccion.CarteraClienteID,
		Monto:       roundMoney(transaccion.Monto),
		Descripcion: transaccion.Descripcion,
		Fecha:       transaccion.Fecha,
		CajaID:      transaccion.CajaID,
		Usuario:     transaccion.Usuario,
	}, nil
}

func loadCarteraAbonoWithRelations(tx *gorm.DB, abonoID uint) (models.CarteraAbono, error) {
	var abono models.CarteraAbono
	err := tx.Preload("Cliente").
		Preload("Aplicaciones.Factura").
		First(&abono, abonoID).Error
	return abono, err
}

func recalculateInvoiceBalances(tx *gorm.DB, invoiceIDs []uint) error {
	ids := uniqueUintIDs(invoiceIDs)
	if len(ids) == 0 {
		return nil
	}

	var facturas []models.CarteraFactura
	if err := tx.Where("id IN ?", ids).Find(&facturas).Error; err != nil {
		return err
	}
	if len(facturas) == 0 {
		return nil
	}

	var sums []struct {
		FacturaID uint
		Total     float64
	}
	if err := tx.Model(&models.CarteraAbonoAplicacion{}).
		Select("factura_id, COALESCE(SUM(valor), 0) AS total").
		Where("factura_id IN ?", ids).
		Group("factura_id").
		Scan(&sums).Error; err != nil {
		return err
	}

	paidByInvoice := map[uint]float64{}
	for _, row := range sums {
		paidByInvoice[row.FacturaID] = roundMoney(row.Total)
	}

	for _, factura := range facturas {
		abonado := paidByInvoice[factura.ID]
		pendiente := roundMoney(factura.ValorTotal - abonado)
		if math.Abs(pendiente) < 0.009 {
			pendiente = 0
		}
		if pendiente < -0.009 {
			return fmt.Errorf("la factura %s quedó sobre abonada", factura.Op)
		}
		if err := tx.Model(&models.CarteraFactura{}).
			Where("id = ?", factura.ID).
			Updates(map[string]interface{}{
				"valor_abonado":   abonado,
				"valor_pendiente": math.Max(0, pendiente),
				"estado":          carteraEstadoFromValues(factura.ValorTotal, abonado),
			}).Error; err != nil {
			return err
		}
	}

	return nil
}

func syncAbonoTotalsWithApplications(tx *gorm.DB, abonoIDs []uint) error {
	ids := uniqueUintIDs(abonoIDs)
	if len(ids) == 0 {
		return nil
	}

	var abonos []models.CarteraAbono
	if err := tx.Where("id IN ?", ids).Find(&abonos).Error; err != nil {
		return err
	}
	abonoByID := map[uint]models.CarteraAbono{}
	for _, abono := range abonos {
		abonoByID[abono.ID] = abono
	}

	var sums []struct {
		AbonoID uint
		Total   float64
	}
	if err := tx.Model(&models.CarteraAbonoAplicacion{}).
		Select("abono_id, COALESCE(SUM(valor), 0) AS total").
		Where("abono_id IN ?", ids).
		Group("abono_id").
		Scan(&sums).Error; err != nil {
		return err
	}

	totalByAbono := map[uint]float64{}
	for _, row := range sums {
		totalByAbono[row.AbonoID] = roundMoney(row.Total)
	}

	for _, id := range ids {
		total := totalByAbono[id]
		abono := abonoByID[id]
		if total <= 0 {
			if abono.OrigenTransaccionID != nil {
				if err := tx.Model(&models.Transaccion{}).
					Where("id = ?", *abono.OrigenTransaccionID).
					Update("cartera_abono_id", nil).Error; err != nil {
					return err
				}
			}
			if err := tx.Delete(&models.CarteraAbono{}, id).Error; err != nil {
				return err
			}
			continue
		}
		if abono.OrigenTransaccionID != nil {
			var sourceTx models.Transaccion
			if err := tx.First(&sourceTx, *abono.OrigenTransaccionID).Error; err != nil {
				return err
			}
			if math.Abs(roundMoney(sourceTx.Monto)-total) > 0.009 {
				return fmt.Errorf("el abono ligado al ingreso de caja no puede cambiar de valor automáticamente")
			}
		}
		if err := tx.Model(&models.CarteraAbono{}).Where("id = ?", id).Update("monto_total", total).Error; err != nil {
			return err
		}
	}

	return nil
}

func validateSourceTransactionForAbono(tx *gorm.DB, clienteID uint, transaccionID *int32, totalDistribuido float64, allowedAbonoID *uint) (*models.Transaccion, error) {
	if transaccionID == nil || *transaccionID == 0 {
		return nil, nil
	}

	var transaccion models.Transaccion
	if err := tx.Model(&models.Transaccion{}).
		Joins("JOIN categorias c ON c.id = transacciones.categoria_id").
		Where("transacciones.id = ?", *transaccionID).
		Where("c.is_cartera_clientes = ? AND c.tipo = ?", true, "INGRESO").
		First(&transaccion).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("el ingreso de caja seleccionado ya no está disponible")
		}
		return nil, err
	}

	if transaccion.CarteraClienteID == nil || *transaccion.CarteraClienteID == 0 || *transaccion.CarteraClienteID != clienteID {
		return nil, fmt.Errorf("el ingreso seleccionado no pertenece a este cliente")
	}
	if transaccion.CarteraAbonoID != nil {
		if allowedAbonoID == nil || *transaccion.CarteraAbonoID != *allowedAbonoID {
			return nil, fmt.Errorf("el ingreso seleccionado ya fue asignado a un abono")
		}
	}
	if math.Abs(roundMoney(transaccion.Monto)-totalDistribuido) > 0.009 {
		return nil, fmt.Errorf("el valor del abono debe coincidir exactamente con el ingreso registrado en caja")
	}

	return &transaccion, nil
}

func validateCarteraAbonoInput(tx *gorm.DB, clienteID uint, distribucion []models.CarteraAbonoDistribucionInput, extraAllowance map[uint]float64) error {
	if clienteID == 0 {
		return fmt.Errorf("cliente requerido")
	}
	if len(distribucion) == 0 {
		return fmt.Errorf("debes distribuir el abono al menos sobre una factura")
	}

	var cliente models.CarteraCliente
	if err := tx.First(&cliente, clienteID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return fmt.Errorf("cliente no encontrado")
		}
		return err
	}

	invoiceIDs := make([]uint, 0, len(distribucion))
	for _, item := range distribucion {
		invoiceIDs = append(invoiceIDs, item.FacturaID)
	}

	var facturas []models.CarteraFactura
	if err := tx.Where("cliente_id = ? AND id IN ?", clienteID, uniqueUintIDs(invoiceIDs)).Find(&facturas).Error; err != nil {
		return err
	}

	if len(facturas) != len(uniqueUintIDs(invoiceIDs)) {
		return fmt.Errorf("una o más facturas no existen o no pertenecen al cliente")
	}

	facturaByID := map[uint]models.CarteraFactura{}
	for _, factura := range facturas {
		facturaByID[factura.ID] = factura
	}

	for _, item := range distribucion {
		factura := facturaByID[item.FacturaID]
		disponible := factura.ValorPendiente + extraAllowance[item.FacturaID]
		disponible = roundMoney(disponible)
		if item.Valor > disponible+0.009 {
			return fmt.Errorf("el valor aplicado supera el saldo pendiente de la factura %s", factura.Op)
		}
	}

	return nil
}

func normalizeCarteraFacturaLineas(input []models.CarteraFacturaLineaInput) ([]models.CarteraFacturaLinea, float64, error) {
	lineas := make([]models.CarteraFacturaLinea, 0, len(input))
	total := 0.0

	for index, item := range input {
		concepto := strings.TrimSpace(item.Concepto)
		cantidad := roundQuantity(item.Cantidad)
		valor := roundMoney(item.Valor)

		if concepto == "" && cantidad == 0 && valor == 0 {
			continue
		}
		if concepto == "" {
			return nil, 0, fmt.Errorf("el concepto de la línea %d es obligatorio", index+1)
		}
		if len([]rune(concepto)) > 255 {
			return nil, 0, fmt.Errorf("el concepto de la línea %d no puede superar los 255 caracteres", index+1)
		}
		if cantidad <= 0 {
			return nil, 0, fmt.Errorf("la cantidad de la línea %d debe ser mayor a 0", index+1)
		}
		unitario := roundMoney(item.ValorUnitario)
		if unitario <= 0 {
			unitario = roundMoney(valor / cantidad)
		}
		if unitario <= 0 {
			return nil, 0, fmt.Errorf("el valor de la línea %d debe ser mayor a 0", index+1)
		}

		lineTotal := roundMoney(cantidad * unitario)
		if lineTotal <= 0 {
			return nil, 0, fmt.Errorf("el valor total de la línea %d debe ser mayor a 0", index+1)
		}

		lineas = append(lineas, models.CarteraFacturaLinea{
			Concepto:      concepto,
			Cantidad:      cantidad,
			ValorUnitario: unitario,
			Valor:         lineTotal,
		})
		total += lineTotal
	}

	return lineas, roundMoney(total), nil
}

func resolveCarteraFacturaValorTotal(inputTotal float64, lineasTotal float64, hasLineas bool) float64 {
	if hasLineas {
		return roundMoney(lineasTotal)
	}
	return roundMoney(inputTotal)
}

func assignCarteraFacturaSource(factura *models.CarteraFactura, input models.CarteraFacturaInput) {
	origen := strings.TrimSpace(input.Origen)
	if origen == "" {
		origen = "manual"
	}

	posReference := strings.TrimSpace(input.OdooPOSReference)
	posName := strings.TrimSpace(input.OdooPOSName)
	clienteNombre := strings.TrimSpace(input.OdooClienteNombre)

	if input.OdooPOSOrderID != nil || posReference != "" || posName != "" {
		origen = "odoo_bodega"
	}
	if origen != "odoo_bodega" {
		origen = "manual"
		input.OdooPOSOrderID = nil
		posReference = ""
		posName = ""
		clienteNombre = ""
	}

	factura.Origen = origen
	factura.OdooPOSOrderID = input.OdooPOSOrderID
	factura.OdooPOSReference = trimMax(posReference, 120)
	factura.OdooPOSName = trimMax(posName, 160)
	factura.OdooClienteNombre = trimMax(clienteNombre, 200)
}

func roundQuantity(value float64) float64 {
	return math.Round(value*1000) / 1000
}

func trimMax(value string, max int) string {
	safe := strings.TrimSpace(value)
	runes := []rune(safe)
	if max <= 0 || len(runes) <= max {
		return safe
	}
	return string(runes[:max])
}

func normalizeDistribucionCartera(input []models.CarteraAbonoDistribucionInput) ([]models.CarteraAbonoDistribucionInput, float64, error) {
	if len(input) == 0 {
		return nil, 0, fmt.Errorf("debes seleccionar al menos una factura")
	}

	merged := map[uint]float64{}
	for _, item := range input {
		if item.FacturaID == 0 {
			return nil, 0, fmt.Errorf("hay una factura inválida en la distribución")
		}
		valor := roundMoney(item.Valor)
		if valor <= 0 {
			return nil, 0, fmt.Errorf("todos los valores de la distribución deben ser mayores a 0")
		}
		merged[item.FacturaID] = roundMoney(merged[item.FacturaID] + valor)
	}

	resp := make([]models.CarteraAbonoDistribucionInput, 0, len(merged))
	total := 0.0
	for facturaID, valor := range merged {
		resp = append(resp, models.CarteraAbonoDistribucionInput{
			FacturaID: facturaID,
			Valor:     valor,
		})
		total += valor
	}

	sort.Slice(resp, func(i, j int) bool {
		return resp[i].FacturaID < resp[j].FacturaID
	})

	return resp, roundMoney(total), nil
}

func normalizeCarteraSoporte(input *models.CarteraAbonoSoporteInput) (string, string) {
	if input == nil {
		return "", ""
	}
	nombre := strings.TrimSpace(input.Nombre)
	path := strings.TrimSpace(input.Path)
	if path == "" {
		path = strings.TrimSpace(input.URL)
	}
	return nombre, path
}

func serializeCarteraAbono(c *gin.Context, abono *models.CarteraAbono, facturaID uint) carteraAbonoResponse {
	resp := carteraAbonoResponse{
		ID:                  abono.ID,
		ClienteID:           abono.ClienteID,
		MetodoPago:          abono.MetodoPago,
		MontoTotal:          roundMoney(abono.MontoTotal),
		Referencia:          abono.Referencia,
		Observaciones:       abono.Observaciones,
		FechaPago:           abono.FechaPago,
		CreatedAt:           abono.CreatedAt,
		UpdatedAt:           abono.UpdatedAt,
		OrigenTransaccionID: abono.OrigenTransaccionID,
		Distribucion:        make([]carteraDistribucionResponse, 0, len(abono.Aplicaciones)),
	}

	if abono.Cliente.ID != 0 {
		cliente := abono.Cliente
		resp.Cliente = &cliente
	}
	if abono.SoportePath != "" || abono.SoporteNombre != "" {
		resp.Soporte = &carteraSoporteResponse{
			Nombre: abono.SoporteNombre,
			Path:   abono.SoportePath,
			URL:    carteraAbsoluteURL(c, abono.SoportePath),
		}
	}

	for _, item := range abono.Aplicaciones {
		dist := carteraDistribucionResponse{
			FacturaID: item.FacturaID,
			Valor:     roundMoney(item.Valor),
		}
		if item.Factura.ID != 0 {
			dist.Factura = &carteraFacturaMiniResponse{
				ID:             item.Factura.ID,
				Op:             item.Factura.Op,
				Concepto:       item.Factura.Concepto,
				Estado:         item.Factura.Estado,
				ValorTotal:     roundMoney(item.Factura.ValorTotal),
				ValorAbonado:   roundMoney(item.Factura.ValorAbonado),
				ValorPendiente: roundMoney(item.Factura.ValorPendiente),
			}
		}
		if facturaID != 0 && item.FacturaID == facturaID {
			resp.ValorAplicado = roundMoney(item.Valor)
		}
		resp.Distribucion = append(resp.Distribucion, dist)
	}

	return resp
}

func carteraAbsoluteURL(c *gin.Context, rawPath string) string {
	safe := strings.TrimSpace(rawPath)
	if safe == "" {
		return ""
	}
	if strings.HasPrefix(safe, "http://") || strings.HasPrefix(safe, "https://") {
		return safe
	}

	scheme := strings.TrimSpace(c.GetHeader("X-Forwarded-Proto"))
	if scheme == "" {
		if c.Request.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}

	host := strings.TrimSpace(c.GetHeader("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(c.Request.Host)
	}
	if host == "" {
		return safe
	}

	if !strings.HasPrefix(safe, "/") {
		safe = "/" + safe
	}
	return fmt.Sprintf("%s://%s%s", scheme, host, safe)
}

func carteraEstadoFromValues(valorTotal float64, valorAbonado float64) string {
	if roundMoney(valorAbonado) <= 0 {
		return carteraEstadoPendiente
	}
	if roundMoney(valorAbonado) >= roundMoney(valorTotal)-0.009 {
		return carteraEstadoPagada
	}
	return carteraEstadoParcial
}

func buildCarteraEstadoCuentaDeliveryMessage(clientName string) (caption string, message string) {
	name := strings.TrimSpace(clientName)
	if name == "" {
		name = "cliente"
	}
	caption = "Estado de cuenta"
	message = fmt.Sprintf("Hola %s. Te compartimos tu estado de cuenta actualizado en PDF.", name)
	return caption, message
}

func buildCarteraFacturaAbonosDeliveryMessage(clientName, facturaOp string) (caption string, message string) {
	name := strings.TrimSpace(clientName)
	if name == "" {
		name = "cliente"
	}
	op := strings.TrimSpace(facturaOp)
	if op == "" {
		op = "sin referencia"
	}
	caption = fmt.Sprintf("Detalle de abonos %s", op)
	message = fmt.Sprintf("Hola %s. Te compartimos el detalle de abonos registrados para la factura %s.", name, op)
	return caption, message
}

func decodeCarteraPDFPayload(raw string) ([]byte, error) {
	pdfBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("no se pudo leer el PDF generado")
	}
	if len(pdfBytes) == 0 || len(pdfBytes) > carteraPDFMaxSize {
		return nil, fmt.Errorf("el PDF está vacío o supera el tamaño permitido")
	}
	if !bytes.HasPrefix(pdfBytes, []byte("%PDF")) {
		return nil, fmt.Errorf("el archivo generado no es un PDF válido")
	}
	return pdfBytes, nil
}

func resolveCarteraFechaPago(input *time.Time, fallback time.Time) time.Time {
	if input == nil || input.IsZero() {
		return fallback.In(time.Local)
	}
	return input.In(time.Local)
}

func sourceTransactionTime(tx *models.Transaccion, fallback time.Time) time.Time {
	if tx == nil || tx.Fecha.IsZero() {
		return fallback
	}
	return tx.Fecha
}

func sanitizeCarteraPDFFileName(raw string, clientID uint) string {
	name := strings.TrimSpace(raw)
	if name == "" {
		return fmt.Sprintf("Estado_Cuenta_%d.pdf", clientID)
	}
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	if !strings.HasSuffix(strings.ToLower(name), ".pdf") {
		name += ".pdf"
	}
	return name
}

func extractCarteraClienteIDs(clientes []models.CarteraCliente) []uint {
	ids := make([]uint, 0, len(clientes))
	for _, cliente := range clientes {
		ids = append(ids, cliente.ID)
	}
	return ids
}

func uniqueUintIDs(ids []uint) []uint {
	if len(ids) == 0 {
		return nil
	}
	seen := map[uint]struct{}{}
	out := make([]uint, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func roundMoney(v float64) float64 {
	return math.Round(v*100) / 100
}

func parseUintParam(c *gin.Context, key string) (uint, bool) {
	raw := strings.TrimSpace(c.Param(key))
	id64, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || id64 == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "identificador inválido"})
		return 0, false
	}
	return uint(id64), true
}

func parseInt32Param(c *gin.Context, key string) (int32, bool) {
	raw := strings.TrimSpace(c.Param(key))
	id64, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || id64 == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "identificador inválido"})
		return 0, false
	}
	return int32(id64), true
}

func handleCarteraNotFound(c *gin.Context, err error, message string) {
	if err == gorm.ErrRecordNotFound {
		c.JSON(http.StatusNotFound, gin.H{"error": message})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": "error interno de cartera"})
}

func errorsIsNotFound(err error) bool {
	return err == gorm.ErrRecordNotFound
}
