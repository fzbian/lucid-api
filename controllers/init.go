package controllers

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var DB *gorm.DB

// SetDB asigna la conexión de base de datos para los controladores
func SetDB(db *gorm.DB) {
	DB = db
}

// RegisterCategoriaRoutes registra rutas de categorias
func RegisterCategoriaRoutes(rg *gin.RouterGroup) {
	r := rg.Group("/categorias")
	{
		r.GET("", GetCategorias)
		r.POST("", CreateCategoria)
		r.GET(":id", GetCategoria)
		r.PUT(":id", UpdateCategoria)
		r.DELETE(":id", DeleteCategoria)
		r.POST(":id/set-gasto-operativo", SetGastoOperativo)
	}
}

// RegisterTransaccionRoutes registra rutas de transacciones
func RegisterTransaccionRoutes(rg *gin.RouterGroup) {
	r := rg.Group("/transacciones")
	{
		r.GET("", GetTransacciones)
		r.POST("", CreateTransaccion)
		r.GET(":id", GetTransaccion)
		r.PUT(":id", UpdateTransaccion)
		r.DELETE(":id", DeleteTransaccion)
	}
}

// RegisterCajaRoutes registra rutas de caja
func RegisterCajaRoutes(rg *gin.RouterGroup) {
	r := rg.Group("/caja")
	{
		r.GET("", GetCaja)
	}
}

// RegisterLogsRoutes registra rutas de logs
func RegisterLogsRoutes(rg *gin.RouterGroup) {
	r := rg.Group("/logs")
	{
		r.GET("", GetTransaccionesLog)
	}
}

// RegisterResumenRoutes registra ruta de resumen
func RegisterResumenRoutes(rg *gin.RouterGroup) {
	r := rg.Group("/resumen")
	{
		r.GET("", GetResumenFinanciero)
	}
}

// RegisterNotifyRoutes registra rutas de notificaciones
func RegisterNotifyRoutes(rg *gin.RouterGroup) {
	r := rg.Group("/notify")
	{
		r.GET("/test", NotifyTest)
	}
}

// RegisterOdooRoutes registra rutas relacionadas con Odoo
func RegisterOdooRoutes(rg *gin.RouterGroup) {
	r := rg.Group("/odoo")
	{
		r.POST("/cashout", OdooCashOut)
		r.GET("/pos", OdooListPOS)
		r.GET("/billing", OdooGetBilling)
		r.GET("/orders", OdooListInvoicedOrders)
		r.GET("/orders/overview", OdooOrdersOverviewByPOS)
		r.POST("/orders/:id/refund", OdooRefundOrderFull)
	}
}

// RegisterBillingRoutes registra rutas del módulo de informes de facturación
func RegisterBillingRoutes(rg *gin.RouterGroup) {
	r := rg.Group("/billing")
	{
		r.GET("/monthly", GetBillingMonthly)
		r.POST("/monthly", SaveBillingMonthly)
		r.POST("/confirm", ConfirmBillingMonthly)
		r.DELETE("/report", DeleteBillingReport)
		r.GET("/commission", GetEmployeeCommission)
		r.GET("/gastos", GetBillingGastos)
		r.GET("/gastos-batch", GetBillingGastosBatch)
		r.POST("/gastos", CreateBillingGasto)
		r.POST("/gastos/exclude", ExcludeBillingGasto)
		r.GET("/configs", GetBillingConfigs)
		r.POST("/configs", SaveBillingConfigs)
		r.GET("/fixed-costs", GetFixedCosts)
		r.POST("/fixed-costs", CreateFixedCost)
		r.PUT("/fixed-costs/:id", UpdateFixedCost)
		r.DELETE("/fixed-costs/:id", DeleteFixedCost)
		r.GET("/status", GetBillingStatus)
		r.GET("/nomina-by-pos", GetNominaByPOS)
		r.GET("/nomina-available", GetAvailableNominaPayments)
		r.GET("/nomina-summary", GetNominaSummary)
		r.POST("/nomina-assign", AssignNominaToPOS)
		r.POST("/nomina-unassign", RemoveNominaFromPOS)
		r.POST("/nomina-reset", ResetNominaAssignmentsForMonth)
	}
}

// RegisterCacheRoutes registra rutas para gestionar el cache de Odoo
func RegisterCacheRoutes(rg *gin.RouterGroup) {
	r := rg.Group("/cache")
	{
		r.POST("/clear", ClearOdooCache)
	}
}

// RegisterCuentaRoutes registra rutas de cuenta bancaria
func RegisterCuentaRoutes(rg *gin.RouterGroup) {
	r := rg.Group("/cuenta")
	{
		r.POST("/retiro", RetiroCuenta)
	}
}

// RegisterLimpiarRoutes registra la ruta para limpiar la base de datos
func RegisterLimpiarRoutes(rg *gin.RouterGroup) {
	r := rg.Group("/limpiar")
	{
		r.POST("", DeleteAllData)
	}
}

// RegisterGastosRoutes registra rutas de gastos operativos
func RegisterGastosRoutes(rg *gin.RouterGroup) {
	r := rg.Group("/gastos")
	{
		r.POST("", CreateGasto)
		r.GET("", GetGastos)
		r.DELETE(":id", DeleteGasto)
	}
}

// RegisterNominaRoutes registra rutas de nómina
func RegisterNominaRoutes(rg *gin.RouterGroup) {
	r := rg.Group("/nomina")
	{
		r.GET("/config", GetNominaConfig)
		r.POST("/config", UpdateNominaConfig)
		r.GET("/employees", GetNominaEmployees)
		r.POST("/employees/:id/salary", UpdateEmployeeDetails)
		r.POST("/pay", GeneratePayment)
		r.POST("/payments/:id/sign", UploadSignedContract)
		r.POST("/payments/:id/sign-link", CreatePaymentSignLink)
		r.POST("/sign/:token/access", AccessPaymentSigningLink)
		r.POST("/sign/:token/complete", CompletePaymentSignature)
		r.DELETE("/payments/:id", DeleteNominaPayment)
		r.PATCH("/payments/:id/commission", UpdatePaymentCommission)
		r.GET("/history", GetNominaHistory)
		r.POST("/period-inclusion", SetNominaPeriodInclusion)

		// POS Assignments
		r.GET("/employees/:id/pos-assignments", GetEmployeePOSAssignments)
		r.POST("/employees/:id/pos-assignments", SaveEmployeePOSAssignments)
		r.GET("/pos-assignments", GetAllPOSAssignments)

		// Odoo Helpers for Wizard
		r.GET("/odoo/pos", GetOdooPOSConfigs)
		r.GET("/odoo/sessions", GetOdooSessions)

		// Matrix Report
		r.GET("/matrix", GetNominaMatrix)
	}
}
