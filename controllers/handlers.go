package controllers

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"atm/models"
	"atm/notify"
	"atm/odoo"
)

// -------------------- CATEGORIAS --------------------
// GetCategorias godoc
// @Summary Listar categorias
// @Produce json
// @Success 200 {array} models.Categoria
// @Failure 500 {object} map[string]interface{}
// @Router /api/categorias [get]
func GetCategorias(c *gin.Context) {
	var categorias []models.Categoria
	if err := DB.Find(&categorias).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, categorias)
}

// CreateCategoria godoc
// @Summary Crear categoria
// @Accept json
// @Produce json
// @Param categoria body models.CategoriaCreateInput true "Categoria"
// @Success 201 {object} models.Categoria
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/categorias [post]
func CreateCategoria(c *gin.Context) {
	var input models.CategoriaCreateInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// validar tipo
	tipo := strings.ToUpper(strings.TrimSpace(input.Tipo))
	if tipo != "INGRESO" && tipo != "EGRESO" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tipo inválido: debe ser 'INGRESO' o 'EGRESO'"})
		return
	}
	categoria := models.Categoria{
		Nombre: input.Nombre,
		Tipo:   tipo,
	}
	if err := DB.Create(&categoria).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, categoria)
}

// GetCategoria godoc
// @Summary Obtener categoria por id
// @Produce json
// @Param id path int true "ID"
// @Success 200 {object} models.Categoria
// @Failure 404 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/categorias/{id} [get]
func GetCategoria(c *gin.Context) {
	id := c.Param("id")
	var categoria models.Categoria
	if err := DB.First(&categoria, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "categoria no encontrada"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, categoria)
}

// UpdateCategoria godoc
// @Summary Actualizar categoria (parcial)
// @Accept json
// @Produce json
// @Param id path int true "ID"
// @Param categoria body models.CategoriaUpdateInput false "Campos a actualizar (parcial)"
// @Success 200 {object} models.Categoria
// @Failure 400 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/categorias/{id} [put]
func UpdateCategoria(c *gin.Context) {
	id := c.Param("id")
	var categoria models.Categoria
	if err := DB.First(&categoria, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "categoria no encontrada"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	var input models.CategoriaUpdateInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if input.Nombre == nil && input.Tipo == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no hay campos para actualizar"})
		return
	}
	if input.Nombre != nil {
		categoria.Nombre = *input.Nombre
	}
	if input.Tipo != nil {
		tipo := strings.ToUpper(strings.TrimSpace(*input.Tipo))
		if tipo != "INGRESO" && tipo != "EGRESO" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "tipo inválido: debe ser 'INGRESO' o 'EGRESO'"})
			return
		}
		categoria.Tipo = tipo
	}
	if err := DB.Save(&categoria).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, categoria)
}

// DeleteCategoria godoc
// @Summary Eliminar categoria
// @Produce json
// @Param id path int true "ID"
// @Success 204
// @Failure 404 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/categorias/{id} [delete]
func DeleteCategoria(c *gin.Context) {
	id := c.Param("id")
	var categoria models.Categoria
	if err := DB.First(&categoria, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "categoria no encontrada"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := DB.Delete(&categoria).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Notificación removida para categorías
	c.Status(http.StatusNoContent)
}

// SetGastoOperativo marca una categoría como la de gastos operativos.
// Solo una categoría puede tener is_gasto_operativo=true a la vez.
func SetGastoOperativo(c *gin.Context) {
	id := c.Param("id")
	var categoria models.Categoria
	if err := DB.First(&categoria, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "categoria no encontrada"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var body struct {
		IsGastoOperativo bool `json:"is_gasto_operativo"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	err := DB.Transaction(func(tx *gorm.DB) error {
		// Quitar is_gasto_operativo de todas las categorías
		if err := tx.Model(&models.Categoria{}).Where("is_gasto_operativo = ?", true).Update("is_gasto_operativo", false).Error; err != nil {
			return err
		}
		// Si se activa, marcar solo esta categoría
		if body.IsGastoOperativo {
			if err := tx.Model(&categoria).Update("is_gasto_operativo", true).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	DB.First(&categoria, id) // reload
	c.JSON(http.StatusOK, categoria)
}

// -------------------- TRANSACCIONES --------------------
// GetTransacciones godoc
// @Summary Listar transacciones
// @Produce json
// @Param limit query int false "Número máximo de movimientos a devolver"
// @Param from query string false "Fecha inicio (RFC3339 o YYYY-MM-DD)"
// @Param to query string false "Fecha fin (RFC3339 o YYYY-MM-DD)"
// @Param tipo query string false "Tipo de movimiento: INGRESO|EGRESO"
// @Param caja_id query int false "Filtrar por ID de caja"
// @Param descripcion query string false "Buscar por descripcion (texto parcial)"
// @Success 200 {array} models.Transaccion
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/transacciones [get]
func GetTransacciones(c *gin.Context) {
	// Parámetros opcionales
	limitStr := c.Query("limit")
	fromStr := c.Query("from")            // fecha inicio (inclusive) RFC3339 o YYYY-MM-DD
	toStr := c.Query("to")                // fecha fin (inclusive) RFC3339 o YYYY-MM-DD
	tipo := c.Query("tipo")               // INGRESO o EGRESO (filtrado por categoria)
	cajaIDStr := c.Query("caja_id")       // Filtro por caja
	descripcion := c.Query("descripcion") // Filtro por descripción
	usuario := c.Query("usuario")         // Nuevo filtro por usuario

	var transacciones []models.Transaccion
	query := DB.Model(&models.Transaccion{})

	// Si se filtra por tipo, hacemos JOIN con categorias
	if tipo != "" {
		query = query.Joins("JOIN categorias c ON c.id = transacciones.categoria_id").Where("c.tipo = ?", tipo)
	}

	// Filtros de descripcion
	if descripcion != "" {
		query = query.Where("transacciones.descripcion LIKE ?", "%"+descripcion+"%")
	}

	// Filtro por usuario
	if usuario != "" {
		query = query.Where("transacciones.usuario = ?", usuario)
	}
	// Filtro por caja
	if cajaIDStr != "" {
		if cajaID, err := strconv.Atoi(cajaIDStr); err == nil && cajaID > 0 {
			query = query.Where("transacciones.caja_id = ?", cajaID)
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": "parametro 'caja_id' inválido"})
			return
		}
	}

	// Parsear y aplicar from/to
	timeLayouts := []string{time.RFC3339, "2006-01-02"}
	if fromStr != "" {
		var t time.Time
		var err error
		for _, l := range timeLayouts {
			t, err = time.Parse(l, fromStr)
			if err == nil {
				break
			}
		}
		if t.IsZero() {
			c.JSON(http.StatusBadRequest, gin.H{"error": "formato de 'from' inválido. Use RFC3339 o YYYY-MM-DD"})
			return
		}
		query = query.Where("transacciones.fecha >= ?", t)
	}
	if toStr != "" {
		var t time.Time
		var err error
		for _, l := range timeLayouts {
			t, err = time.Parse(l, toStr)
			if err == nil {
				break
			}
		}
		if t.IsZero() {
			c.JSON(http.StatusBadRequest, gin.H{"error": "formato de 'to' inválido. Use RFC3339 o YYYY-MM-DD"})
			return
		}
		// hacer la fecha inclusiva hasta el fin del día si se usó YYYY-MM-DD
		if len(toStr) == len("2006-01-02") {
			t = t.Add(24*time.Hour - time.Nanosecond)
		}
		query = query.Where("transacciones.fecha <= ?", t)
	}

	// Orden por fecha desc
	query = query.Order("transacciones.fecha desc")

	// Aplicar limit si viene
	if limitStr != "" {
		l, err := strconv.Atoi(limitStr)
		if err != nil || l <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "parametro 'limit' inválido"})
			return
		}
		query = query.Limit(l)
	}

	if err := query.Find(&transacciones).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, transacciones)
}

// CreateTransaccion godoc
// @Summary Crear transaccion
// @Accept json
// @Produce json
// @Param transaccion body models.TransaccionCreateInput true "Transaccion (requiere caja_id)"
// @Success 201 {object} models.Transaccion
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/transacciones [post]
func CreateTransaccion(c *gin.Context) {
	var input models.TransaccionCreateInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var transaccion models.Transaccion
	var categoria models.Categoria

	err := DB.Transaction(func(tx *gorm.DB) error {
		// 1. Obtener Categoría
		if err := tx.First(&categoria, input.CategoriaID).Error; err != nil {
			return fmt.Errorf("categoria no encontrada") // Simplificado para devolver error genérico si falla
		}

		// 2. Obtener y Bloquear Caja (para evitar condiciones de carrera)
		var caja models.Caja
		// Clauses(clause.Locking{Strength: "UPDATE"}) sería ideal, pero GORM básico:
		if err := tx.First(&caja, input.CajaID).Error; err != nil {
			return fmt.Errorf("caja no encontrada")
		}

		// 3. Validar saldo si es EGRESO
		isEgreso := strings.ToUpper(categoria.Tipo) == "EGRESO"
		if isEgreso {
			if caja.Saldo < input.Monto {
				// Retornamos un error especial para manejar el 409 fuera
				return fmt.Errorf("saldo_insuficiente|%f|%f", caja.Saldo, input.Monto)
			}
		}

		// 4. Crear Transacción
		transaccion = models.Transaccion{
			CategoriaID: input.CategoriaID,
			CajaID:      input.CajaID,
			Monto:       input.Monto,
			Descripcion: input.Descripcion,
			Usuario:     input.Usuario,
		}
		if err := tx.Create(&transaccion).Error; err != nil {
			return err
		}

		// 5. Actualizar Saldo Caja
		saldoAntes := caja.Saldo
		var newSaldo float64
		if isEgreso {
			newSaldo = caja.Saldo - input.Monto
		} else {
			// INGRESO
			newSaldo = caja.Saldo + input.Monto
		}

		if err := tx.Model(&caja).Update("saldo", newSaldo).Error; err != nil {
			return err
		}

		// 6. Generar Log (Reemplazo de Trigger)
		logEntry := models.TransaccionLog{
			TransaccionID: transaccion.ID,
			Accion:        "INSERT",
			Usuario:       input.Usuario,
			Detalle:       fmt.Sprintf("Creación de %s %s por %s", categoria.Tipo, categoria.Nombre, formatMonto(input.Monto)),
			SaldoAntes:    saldoAntes,
			SaldoDespues:  newSaldo,
		}
		if err := tx.Create(&logEntry).Error; err != nil {
			return err // Fallar transacción si no se puede loguear
		}

		return nil
	})

	if err != nil {
		// Manejo de errores
		errMsg := err.Error()
		if strings.HasPrefix(errMsg, "saldo_insuficiente") {
			parts := strings.Split(errMsg, "|")
			saldo, _ := strconv.ParseFloat(parts[1], 64)
			monto, _ := strconv.ParseFloat(parts[2], 64)
			c.JSON(http.StatusConflict, gin.H{
				"error":            "Saldo insuficiente en la caja para realizar el egreso",
				"saldo_actual":     saldo,
				"monto_solicitado": monto,
				"caja_id":          input.CajaID,
			})
			return
		}
		if errMsg == "categoria no encontrada" || errMsg == "caja no encontrada" {
			c.JSON(http.StatusBadRequest, gin.H{"error": errMsg})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Si la categoría es de gastos operativos y se indicó un local, crear GastoLocal automáticamente
	if categoria.IsGastoOperativo && input.Local != "" {
		gasto := models.GastoLocal{
			Local:   input.Local,
			Fecha:   transaccion.Fecha,
			Tipo:    "GASTO_OPERATIVO",
			Motivo:  transaccion.Descripcion,
			Monto:   transaccion.Monto,
			Usuario: transaccion.Usuario,
		}
		if err := DB.Create(&gasto).Error; err != nil {
			// Log but don't fail the transaction
			fmt.Printf("[WARN] No se pudo crear GastoLocal automático: %v\n", err)
		}
	}

	emoji := tipoEmoji(categoria.Tipo)
	msg := fmt.Sprintf("*TRANSACCION CREADA*\n📪 *ID:* %d\n📄 *Descripcion:* %s\n📚 *Categoria:* %s\n🏷️ *Tipo de movimiento:* %s %s\n💲*Monto:* %s\n🧾 *Caja:* %s\n👤 *Usuario:* %s", transaccion.ID, transaccion.Descripcion, categoria.Nombre, categoria.Tipo, emoji, formatMonto(transaccion.Monto), labelCaja(transaccion.CajaID), transaccion.Usuario)
	notify.SendText(msg)
	c.JSON(http.StatusCreated, transaccion)
}

// GetTransaccion godoc
// @Summary Obtener transaccion por id
// @Produce json
// @Param id path int true "ID"
// @Success 200 {object} models.Transaccion
// @Failure 404 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/transacciones/{id} [get]
func GetTransaccion(c *gin.Context) {
	id := c.Param("id")
	var t models.Transaccion
	if err := DB.First(&t, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "transaccion no encontrada"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, t)
}

// UpdateTransaccion godoc
// @Summary Actualizar transaccion parcialmente
// @Accept json
// @Produce json
// @Param id path int true "ID"
// @Param transaccion body models.TransaccionUpdateInput false "Campos a actualizar (parcial). Debe incluir caja_id en body o query."
// @Param usuario query string true "Usuario que actualiza la transacción"
// @Param caja_id query int false "ID de caja (obligatorio si no se envía en el body)"
// @Success 200 {object} models.Transaccion
// @Failure 400 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/transacciones/{id} [put]
// UpdateTransaccion godoc
// @Summary Actualizar transaccion
// @Produce json
// @Param id path int true "ID"
// @Param usuario query string true "Usuario que actualiza"
// @Success 200 {object} models.Transaccion
// @Failure 404 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/transacciones/{id} [put]
func UpdateTransaccion(c *gin.Context) {
	id := c.Param("id")
	usuario := c.Query("usuario")
	if usuario == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "parametro 'usuario' es requerido"})
		return
	}

	var input models.TransaccionUpdateInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var updatedT models.Transaccion

	err := DB.Transaction(func(tx *gorm.DB) error {
		var t models.Transaccion
		if err := tx.First(&t, id).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return fmt.Errorf("transaccion no encontrada")
			}
			return err
		}

		// 1. Capturar Estado Anterior
		oldCatID := t.CategoriaID
		oldCajaID := t.CajaID
		oldMonto := t.Monto
		oldDesc := t.Descripcion

		var oldCat models.Categoria
		if err := tx.First(&oldCat, oldCatID).Error; err != nil {
			return fmt.Errorf("categoria anterior no encontrada")
		}
		var oldCaja models.Caja
		if err := tx.First(&oldCaja, oldCajaID).Error; err != nil {
			return fmt.Errorf("caja anterior no encontrada")
		}

		// 2. Determinar Nuevos Valores
		newCatID := oldCatID
		if input.CategoriaID != nil {
			newCatID = *input.CategoriaID
		}
		newCajaID := oldCajaID
		if input.CajaID != nil {
			newCajaID = *input.CajaID
		} else if qCaja := c.Query("caja_id"); qCaja != "" {
			if v, err := strconv.Atoi(qCaja); err == nil && v > 0 {
				newCajaID = int32(v)
			}
		}

		newMonto := oldMonto
		if input.Monto != nil {
			newMonto = *input.Monto
		}
		newDesc := oldDesc
		if input.Descripcion != nil {
			newDesc = *input.Descripcion
		}

		var newCat models.Categoria
		if newCatID == oldCatID {
			newCat = oldCat
		} else {
			if err := tx.First(&newCat, newCatID).Error; err != nil {
				return fmt.Errorf("nueva categoria no encontrada")
			}
		}

		var newCaja models.Caja
		if newCajaID == oldCajaID {
			newCaja = oldCaja
			if err := tx.First(&newCaja, newCajaID).Error; err != nil {
				return fmt.Errorf("nueva caja no encontrada")
			}
		} else {
			if err := tx.First(&newCaja, newCajaID).Error; err != nil {
				return fmt.Errorf("nueva caja no encontrada")
			}
		}

		// 3. Lógica de Reversión y Aplicación
		// A. Revertir impacto anterior en oldCaja
		saldoTrasRevertir := oldCaja.Saldo
		if strings.ToUpper(oldCat.Tipo) == "EGRESO" {
			saldoTrasRevertir += oldMonto
		} else {
			saldoTrasRevertir -= oldMonto
		}

		// B. Aplicar impacto nuevo
		var saldoFinal float64
		if oldCajaID == newCajaID {
			saldoFinal = saldoTrasRevertir
			if strings.ToUpper(newCat.Tipo) == "EGRESO" {
				if saldoFinal < newMonto {
					return fmt.Errorf("saldo_insuficiente|%f|%f", saldoFinal, newMonto)
				}
				saldoFinal -= newMonto
			} else {
				saldoFinal += newMonto
			}
			if err := tx.Model(&models.Caja{}).Where("id = ?", oldCajaID).Update("saldo", saldoFinal).Error; err != nil {
				return err
			}
		} else {
			// Cajas Distintas
			if err := tx.Model(&models.Caja{}).Where("id = ?", oldCajaID).Update("saldo", saldoTrasRevertir).Error; err != nil {
				return err
			}
			saldoFinal = newCaja.Saldo
			if strings.ToUpper(newCat.Tipo) == "EGRESO" {
				if saldoFinal < newMonto {
					return fmt.Errorf("saldo_insuficiente|%f|%f|%d", saldoFinal, newMonto, newCajaID)
				}
				saldoFinal -= newMonto
			} else {
				saldoFinal += newMonto
			}
			if err := tx.Model(&models.Caja{}).Where("id = ?", newCajaID).Update("saldo", saldoFinal).Error; err != nil {
				return err
			}
		}

		// 4. Actualizar Transacción
		updates := map[string]interface{}{
			"categoria_id": newCatID,
			"caja_id":      newCajaID,
			"monto":        newMonto,
			"descripcion":  newDesc,
		}
		if err := tx.Model(&t).Updates(updates).Error; err != nil {
			return err
		}

		// 5. Generar Log
		saldoAntesLog := newCaja.Saldo
		if oldCajaID == newCajaID {
			saldoAntesLog = oldCaja.Saldo
		}

		logDetails := []string{}
		if oldMonto != newMonto {
			logDetails = append(logDetails, fmt.Sprintf("Monto: %s -> %s", formatMonto(oldMonto), formatMonto(newMonto)))
		}
		if oldDesc != newDesc {
			logDetails = append(logDetails, fmt.Sprintf("Desc: %s -> %s", oldDesc, newDesc))
		}
		if oldCatID != newCatID {
			logDetails = append(logDetails, fmt.Sprintf("Cat: %s -> %s", oldCat.Nombre, newCat.Nombre))
		}
		if oldCajaID != newCajaID {
			logDetails = append(logDetails, fmt.Sprintf("Caja: %s -> %s", labelCaja(oldCajaID), labelCaja(newCajaID)))
		}

		detailStr := "Actualización: " + strings.Join(logDetails, ", ")
		if len(logDetails) == 0 {
			detailStr = "Actualización sin cambios sustanciales"
		}

		logEntry := models.TransaccionLog{
			TransaccionID: t.ID,
			Accion:        "UPDATE",
			Usuario:       usuario,
			Detalle:       detailStr,
			SaldoAntes:    saldoAntesLog,
			SaldoDespues:  saldoFinal,
		}
		if err := tx.Create(&logEntry).Error; err != nil {
			return err
		}

		updatedT = t
		updatedT.CategoriaID = newCatID
		updatedT.CajaID = newCajaID
		updatedT.Monto = newMonto
		updatedT.Descripcion = newDesc
		return nil
	})

	if err != nil {
		errMsg := err.Error()
		if strings.HasPrefix(errMsg, "saldo_insuficiente") {
			parts := strings.Split(errMsg, "|")
			saldo, _ := strconv.ParseFloat(parts[1], 64)
			monto, _ := strconv.ParseFloat(parts[2], 64)
			cID := input.CajaID
			if len(parts) > 3 {
				v, _ := strconv.Atoi(parts[3])
				v32 := int32(v)
				cID = &v32
			}
			c.JSON(http.StatusConflict, gin.H{
				"error":           "Saldo insuficiente en la caja",
				"saldo_actual":    saldo,
				"monto_requerido": monto,
				"caja_id":         cID,
			})
			return
		}
		if errMsg == "transaccion no encontrada" {
			c.JSON(http.StatusNotFound, gin.H{"error": errMsg})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Notificación
	msg := fmt.Sprintf("*TRANSACCION ACTUALIZADA*\n📪 *ID:* %d\n📄 *Desc:* %s\n💰 *Monto:* %s\n👤 *Por:* %s", updatedT.ID, updatedT.Descripcion, formatMonto(updatedT.Monto), usuario)
	notify.SendText(msg)

	c.JSON(http.StatusOK, updatedT)
}

// DeleteTransaccion godoc
// @Summary Eliminar transaccion
// @Produce json
// @Param id path int true "ID"
// @Param usuario query string true "Usuario que elimina la transacción"
// @Success 204
// @Failure 404 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/transacciones/{id} [delete]
func DeleteTransaccion(c *gin.Context) {
	id := c.Param("id")
	usuario := c.Query("usuario")
	if usuario == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "parametro 'usuario' es requerido"})
		return
	}

	var t models.Transaccion
	var cajaID int32
	var monto float64
	var desc string

	err := DB.Transaction(func(tx *gorm.DB) error {
		// 1. Obtener Transacción
		if err := tx.First(&t, id).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return fmt.Errorf("transaccion no encontrada")
			}
			return err
		}
		cajaID = t.CajaID
		monto = t.Monto
		desc = t.Descripcion

		// 2. Obtener Categoría para saber tipo
		var cat models.Categoria
		if err := tx.First(&cat, t.CategoriaID).Error; err != nil {
			// Si no existe categoria, asumimos neutro? Mejor fallar
			return fmt.Errorf("categoria de la transaccion no encontrada")
		}

		// 3. Obtener Caja
		var caja models.Caja
		if err := tx.First(&caja, cajaID).Error; err != nil {
			return fmt.Errorf("caja no encontrada")
		}

		// 4. Revertir Saldo
		// Si era INGRESO -> Restamos
		// Si era EGRESO -> Sumamos
		saldoAntes := caja.Saldo
		var newSaldo float64
		if strings.ToUpper(cat.Tipo) == "EGRESO" {
			newSaldo = caja.Saldo + monto
		} else {
			// INGRESO
			newSaldo = caja.Saldo - monto
		}

		if err := tx.Model(&caja).Update("saldo", newSaldo).Error; err != nil {
			return err
		}

		// 5. Generar Log antes de borrar (Audit trail)
		logEntry := models.TransaccionLog{
			TransaccionID: t.ID,
			Accion:        "DELETE",
			Usuario:       usuario,
			Detalle:       fmt.Sprintf("Borrado de %s por %s", t.Descripcion, formatMonto(t.Monto)),
			SaldoAntes:    saldoAntes,
			SaldoDespues:  newSaldo,
		}
		if err := tx.Create(&logEntry).Error; err != nil {
			return err
		}

		// 6. Borrar Transacción
		if err := tx.Delete(&t).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		if err.Error() == "transaccion no encontrada" {
			c.JSON(http.StatusNotFound, gin.H{"error": "transaccion no encontrada"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Logs generados explícitamente, no necesitamos updates manuales.
	msg := fmt.Sprintf("*TRANSACCION ELIMINADA*\n📪 *ID:* %s\n📄 *Descripcion:* %s\n💲*Monto:* %s\n🧾 *Caja:* %s\n👤 *Usuario:* %s", id, desc, formatMonto(monto), labelCaja(cajaID), usuario)
	notify.SendText(msg)
	c.Status(http.StatusNoContent)
}

// -------------------- CAJA --------------------
// GetCaja godoc
// @Summary Obtener saldo en caja
// @Description Si solo_caja=true, devuelve solo los saldos locales (caja 1 y caja 2). Si no, devuelve saldos locales y saldos de Odoo (POS) combinados.
// @Produce json
// @Param solo_caja query bool false "Solo saldo de caja local (sin Odoo)"
// @Success 200 {object} models.CajaOdoo
// @Success 200 {object} models.Caja
// @Failure 500 {object} map[string]interface{}
// @Router /api/caja [get]
func GetCaja(c *gin.Context) {
	soloCaja := c.Query("solo_caja")
	var cajaLocal models.Caja
	if err := DB.First(&cajaLocal, 1).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "caja local no inicializada"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "error obteniendo caja local", "detalle": err.Error()})
		return
	}
	// Obtener también caja id 2 (cuenta bancaria) si existe
	var caja2 models.Caja
	if err := DB.First(&caja2, 2).Error; err != nil {
		// Si no existe, asumimos saldo 0 sin fallar toda la solicitud
		caja2 = models.Caja{ID: 2, Saldo: 0}
	}
	if soloCaja == "true" || soloCaja == "1" {
		c.JSON(http.StatusOK, gin.H{
			"id":                   cajaLocal.ID,
			"saldo_caja":           cajaLocal.Saldo,
			"saldo_caja2":          caja2.Saldo,
			"ultima_actualizacion": cajaLocal.UltimaActualizacion,
		})
		return
	}
	client, err := odoo.NewFromEnv()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "configuración Odoo incompleta", "detalle": err.Error()})
		return
	}
	if err = client.Authenticate(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudo autenticar en Odoo", "detalle": err.Error()})
		return
	}
	locales, _, err := client.FetchPOSBalances()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudo obtener saldos filtrados de Odoo", "detalle": err.Error()})
		return
	}
	truncateToPeso := func(v float64) float64 {
		if v >= 0 {
			return math.Floor(v)
		}
		return math.Ceil(v)
	}
	localesEnteros := make(map[string]models.POSLocalDetail, len(locales))
	var totalLocalesEnteros float64
	for k, v := range locales {
		v.SaldoEnCaja = truncateToPeso(v.SaldoEnCaja)
		if v.Vendido != nil {
			val := truncateToPeso(*v.Vendido)
			v.Vendido = &val
		}
		localesEnteros[k] = v
		totalLocalesEnteros += v.SaldoEnCaja
	}
	resp := models.CajaOdoo{
		ID:                  1,
		SaldoCaja:           cajaLocal.Saldo,
		SaldoCaja2:          caja2.Saldo,
		Locales:             localesEnteros,
		TotalLocales:        totalLocalesEnteros,
		SaldoTotal:          cajaLocal.Saldo + caja2.Saldo + totalLocalesEnteros,
		UltimaActualizacion: time.Now(),
	}
	c.JSON(http.StatusOK, resp)
}

// -------------------- LOGS --------------------
// GetTransaccionesLog godoc
// @Summary Listar logs de transacciones
// @Produce json
// @Success 200 {array} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/logs [get]
func GetTransaccionesLog(c *gin.Context) {
	var logs []map[string]interface{}
	if err := DB.Table("transacciones_log").Order("fecha desc").Find(&logs).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, logs)
}

// -------------------- RESUMEN --------------------
// GetResumenFinanciero godoc
// @Summary Obtener resumen financiero (vista resumen_financiero)
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/resumen [get]
func GetResumenFinanciero(c *gin.Context) {
	// Cálculo en tiempo real (Code-First)
	var totalIngresos float64
	var totalEgresos float64
	var count int64

	// 1. Calcular Ingresos/Egresos desde Transacciones
	// Join con Categorias para filtrar por Tipo
	DB.Model(&models.Transaccion{}).
		Joins("JOIN categorias ON categorias.id = transacciones.categoria_id").
		Where("categorias.tipo = ?", "INGRESO").
		Select("COALESCE(SUM(monto), 0)").Scan(&totalIngresos)

	DB.Model(&models.Transaccion{}).
		Joins("JOIN categorias ON categorias.id = transacciones.categoria_id").
		Where("categorias.tipo = ?", "EGRESO").
		Select("COALESCE(SUM(monto), 0)").Scan(&totalEgresos)

	DB.Model(&models.Transaccion{}).Count(&count)

	// 2. Obtener Saldo Actual de Cajas
	var caja1, caja2 models.Caja
	var saldoCaja1, saldoCaja2 float64
	if err := DB.First(&caja1, 1).Error; err == nil {
		saldoCaja1 = caja1.Saldo
	}
	if err := DB.First(&caja2, 2).Error; err == nil {
		saldoCaja2 = caja2.Saldo
	}

	resumen := gin.H{
		"total_ingresos":      totalIngresos,
		"total_egresos":       totalEgresos,
		"saldo_actual":        saldoCaja1 + saldoCaja2, // Total global
		"saldo_efectivo":      saldoCaja1,
		"saldo_banco":         saldoCaja2,
		"total_transacciones": count,
		"neto_periodo":        totalIngresos - totalEgresos, // Diferencia simple
	}

	c.JSON(http.StatusOK, resumen)
}

// DeleteAllData godoc
// @Summary Eliminar todos los datos de la base de datos
// @Description Elimina todas las transacciones, logs, caja, y categorías (deja la estructura vacía)
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/limpiar [post]
func DeleteAllData(c *gin.Context) {
	// GORM requiere una condición para deletes masivos por seguridad (AllowGlobalUpdate)
	// O simplemente usamos Where("1 = 1")

	// Borrar hijos primero (Logs)
	if err := DB.Where("1 = 1").Delete(&models.TransaccionLog{}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error borrando logs", "detalle": err.Error()})
		return
	}

	// Borrar padre (Transacciones)
	if err := DB.Where("1 = 1").Delete(&models.Transaccion{}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error borrando transacciones", "detalle": err.Error()})
		return
	}

	// Resetear Cajas
	if err := DB.Model(&models.Caja{}).Where("1 = 1").Update("saldo", 0).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error reseteando cajas", "detalle": err.Error()})
		return
	}

	// Borrar Categorias
	if err := DB.Where("1 = 1").Delete(&models.Categoria{}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error borrando categorias", "detalle": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "Base de datos limpiada y reseteada (Code-First)"})
}

// Helpers de formato
func formatMonto(val float64) string {
	// Convertimos a enteros y decimales (2) para presentación estilo es_CO: miles con '.' y decimales con ','
	enteros := int64(val)
	dec := int64((val-float64(enteros))*100 + 0.0000001)
	intStr := strconv.FormatInt(enteros, 10)
	var grupos []string
	for len(intStr) > 3 {
		grupos = append([]string{intStr[len(intStr)-3:]}, grupos...)
		intStr = intStr[:len(intStr)-3]
	}
	if intStr != "" {
		grupos = append([]string{intStr}, grupos...)
	}
	res := strings.Join(grupos, ".")
	if dec > 0 {
		res = fmt.Sprintf("%s,%02d", res, dec)
	}
	return res
}

func tipoEmoji(tipo string) string {
	switch strings.ToUpper(tipo) {
	case "INGRESO":
		return "🟢"
	case "EGRESO":
		return "🔴"
	default:
		return "🏷️"
	}
}

// labelCaja devuelve una etiqueta amigable para la caja según su ID.
// 1 -> Efectivo, 2 -> Cuenta bancaria, cualquier otro -> "Caja <id>"
func labelCaja(id int32) string {
	switch id {
	case 1:
		return "Efectivo"
	case 2:
		return "Cuenta bancaria"
	default:
		return fmt.Sprintf("Caja %d", id)
	}
}

// -------------------- NOTIFY --------------------
// NotifyTest godoc
// @Summary Enviar mensaje de prueba de notificación
// @Description Envía un mensaje de prueba al chat fijo 'atm' usando NOTIFY_URL (nuevo formato /whatsapp/send-text). Por defecto envía "ping".
// @Produce json
// @Param text query string false "Texto a enviar"
// @Success 200 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/notify/test [get]
func NotifyTest(c *gin.Context) {
	text := c.Query("text")
	if strings.TrimSpace(text) == "" {
		text = "ping"
	}
	// Enviar y confirmar con 200 (la función interna ya hace logging de errores)
	notify.SendText(text)
	c.JSON(http.StatusOK, gin.H{"status": "sent", "text": text})
}

// -------------------- ODOO --------------------
type cashOutRequest struct {
	POSName      string  `json:"pos_name" binding:"required"`
	Amount       float64 `json:"amount" binding:"required"`
	CategoryName string  `json:"category_name" binding:"required"`
	Reason       string  `json:"reason" binding:"required"`
	Usuario      string  `json:"usuario"`
	CategoriaID  int32   `json:"categoria_id,omitempty"`
}

// OdooCashOut godoc
// @Summary Cash out en Odoo POS
// @Description Realiza una retirada de caja (cash out) en una sesión de POS abierta en Odoo. Usa credenciales de entorno ODOO_*.
// @Accept json
// @Produce json
// @Param payload body cashOutRequest true "Cash out request (usuario puede ir en body o como query param). Si categoria_id == 16 (Efectivos Puntos de Venta), crea ingreso interno en caja_id=1."
// @Param usuario query string false "Usuario que realiza el cashout (alternativo a body.usuario)"
// @Success 200 {object} odoo.CashOutResult
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/odoo/cashout [post]
func OdooCashOut(c *gin.Context) {
	var req cashOutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Amount <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "amount debe ser > 0"})
		return
	}
	// Validar categoría
	cat := strings.ToUpper(strings.TrimSpace(req.CategoryName))
	if cat != "GASTO" && cat != "RETIRADA" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "category_name debe ser 'GASTO' o 'RETIRADA'"})
		return
	}
	// Validar razón no vacía
	if strings.TrimSpace(req.Reason) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "reason no puede estar en blanco"})
		return
	}
	// Determinar usuario (body o query)
	usuario := strings.TrimSpace(req.Usuario)
	if usuario == "" {
		usuario = strings.TrimSpace(c.Query("usuario"))
	}
	if usuario == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "usuario es requerido (en body.usuario o query ?usuario=)"})
		return
	}
	// Cargar env Odoo
	odooURL := strings.TrimSpace(os.Getenv("ODOO_URL"))
	db := strings.TrimSpace(os.Getenv("ODOO_DB"))
	user := strings.TrimSpace(os.Getenv("ODOO_USER"))
	pass := strings.TrimSpace(os.Getenv("ODOO_PASSWORD"))
	mysqlURI := strings.TrimSpace(os.Getenv("MYSQL_URI"))
	if odooURL == "" || db == "" || user == "" || pass == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "variables ODOO_* faltantes"})
		return
	}
	// Timeout de operación
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	// Validar existencia de POS; si no existe, devolver lista
	names, err := odoo.ListPOSNames(ctx, odooURL, db, user, pass)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	found := false
	for _, n := range names {
		if n == req.POSName {
			found = true
			break
		}
	}
	if !found {
		c.JSON(http.StatusBadRequest, gin.H{"error": "pos_name no encontrado", "disponibles": names})
		return
	}
	res, err := odoo.CashOutPOS(ctx, odooURL, db, user, pass, req.POSName, req.Amount, req.Reason, cat, mysqlURI)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Notificaciones y transacción interna según reglas
	if res.OK {
		// Siempre enviar mensaje al chat "gastos" indicando la retirada en el POS
		gastoMsg := fmt.Sprintf("*RETIRADA EN CAJA*\n🏬 *PUNTO DE VENTA:* %s\n💲 *Monto:* %s\n📝 *Razón:* %s", req.POSName, formatMonto(req.Amount), req.Reason)
		notify.SendTo("retiradas", gastoMsg)

		// Si categoria_id == 16 (Efectivo Puntos de Venta), crear ingreso interno en caja_id=1
		if req.CategoriaID == 16 {
			const transferCatID int32 = 16
			const cajaID int32 = 1

			err := DB.Transaction(func(tx *gorm.DB) error {
				var catRec models.Categoria
				// Ensure category 16 exists and has the expected values
				if err := tx.Where(models.Categoria{ID: transferCatID}).
					Assign(models.Categoria{Nombre: "Efectivo Puntos de Venta", Tipo: "INGRESO"}).
					FirstOrCreate(&catRec).Error; err != nil {
					return fmt.Errorf("error asegurando categoria 16: %w", err)
				}

				// Bloquear y obtener caja
				var caja models.Caja
				if err := tx.First(&caja, cajaID).Error; err != nil {
					return fmt.Errorf("caja 1 no encontrada: %w", err)
				}

				desc := fmt.Sprintf("%s: %s", strings.ReplaceAll(req.POSName, "_", " "), req.Reason)
				t := models.Transaccion{
					CategoriaID: transferCatID,
					CajaID:      cajaID,
					Monto:       req.Amount,
					Descripcion: desc,
					Usuario:     usuario,
				}
				if err := tx.Create(&t).Error; err != nil {
					return err
				}

				// Actualizar saldo
				saldoAntes := caja.Saldo
				newSaldo := caja.Saldo + req.Amount
				if err := tx.Model(&caja).Update("saldo", newSaldo).Error; err != nil {
					return err
				}

				// Log
				logEntry := models.TransaccionLog{
					TransaccionID: t.ID,
					Accion:        "INSERT",
					Usuario:       usuario,
					Detalle:       fmt.Sprintf("Creación autom. por POS %s: %s", req.POSName, formatMonto(req.Amount)),
					SaldoAntes:    saldoAntes,
					SaldoDespues:  newSaldo,
				}
				if err := tx.Create(&logEntry).Error; err != nil {
					return err
				}

				// Notificar variable para uso fuera del tx si se requiere, pero aquí enviamos msg dentro
				emoji := tipoEmoji(catRec.Tipo)
				globalMsg := fmt.Sprintf("*TRANSACCION CREADA*\n📪 *ID:* %d\n📄 *Descripcion:* %s\n📚 *Categoria:* %s\n🏷️ *Tipo de movimiento:* %s %s\n💲*Monto:* %s\n🧾 *Caja:* %s\n👤 *Usuario:* %s", t.ID, t.Descripcion, catRec.Nombre, catRec.Tipo, emoji, formatMonto(t.Monto), labelCaja(t.CajaID), t.Usuario)

				// Hack: enviamos notificación aquí o retornamos para enviarla fuera.
				// Como es asíncrono el send, está bien lanzarlo.
				notify.SendText(globalMsg)

				return nil
			})

			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "fallo transacción interna POS", "detalle": err.Error()})
				return
			}
		}
	}
	c.JSON(http.StatusOK, res)
}

// OdooListPOS godoc
// @Summary Listar nombres de POS en Odoo
// @Description Devuelve los nombres de los puntos de venta disponibles en Odoo usando las credenciales ODOO_* del entorno.
// @Produce json
// @Success 200 {array} string
// @Failure 500 {object} map[string]interface{}
// @Router /api/odoo/pos [get]
func OdooListPOS(c *gin.Context) {
	odooURL := strings.TrimSpace(os.Getenv("ODOO_URL"))
	db := strings.TrimSpace(os.Getenv("ODOO_DB"))
	user := strings.TrimSpace(os.Getenv("ODOO_USER"))
	pass := strings.TrimSpace(os.Getenv("ODOO_PASSWORD"))
	if odooURL == "" || db == "" || user == "" || pass == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "variables ODOO_* faltantes"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	names, err := odoo.ListPOSNames(ctx, odooURL, db, user, pass)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, names)
}

// -------------------- CUENTA BANCARIA --------------------
type RetiroCuentaRequest struct {
	Monto       float64 `json:"monto" binding:"required"`
	Descripcion string  `json:"descripcion" binding:"required"`
	Usuario     string  `json:"usuario" binding:"required"`
}

// RetiroCuenta godoc
// @Summary Retiro en cuenta bancaria (caja_2) con ingreso automático en efectivo (caja_1)
// @Description Crea un EGRESO en caja_id=2 por el monto indicado y, en la misma operación, crea un INGRESO en caja_id=1 por el mismo monto. La operación es atómica.
// @Accept json
// @Produce json
// @Param payload body RetiroCuentaRequest true "Datos del retiro desde la cuenta bancaria (usa categorias fijas: egreso=30, ingreso=20)"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 409 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/cuenta/retiro [post]
func RetiroCuenta(c *gin.Context) {
	var req RetiroCuentaRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Monto <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "monto debe ser > 0"})
		return
	}
	// Categorías fijas: egreso=30, ingreso=20
	const egresoCatID int32 = 30
	const ingresoCatID int32 = 20
	var catEg models.Categoria
	if err := DB.First(&catEg, egresoCatID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "categoria de egreso (ID 30) no existe", "detalle": err.Error()})
		return
	}
	if strings.ToUpper(catEg.Tipo) != "EGRESO" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "categoria de egreso (ID 30) no es de tipo EGRESO"})
		return
	}
	var catIn models.Categoria
	if err := DB.First(&catIn, ingresoCatID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "categoria de ingreso (ID 20) no existe", "detalle": err.Error()})
		return
	}
	if strings.ToUpper(catIn.Tipo) != "INGRESO" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "categoria de ingreso (ID 20) no es de tipo INGRESO"})
		return
	}
	// Validar caja 2 y saldo suficiente
	var caja2 models.Caja
	if err := DB.First(&caja2, 2).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusBadRequest, gin.H{"error": "caja_2 no existe"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if caja2.Saldo < req.Monto {
		c.JSON(http.StatusConflict, gin.H{"error": "Saldo insuficiente en la cuenta bancaria (caja_2)", "saldo_actual": caja2.Saldo, "monto_solicitado": req.Monto})
		return
	}
	// Transacción atómica
	var tEgreso, tIngreso models.Transaccion
	err := DB.Transaction(func(tx *gorm.DB) error {
		// 1. Refetch Caja 2 (Locking for update ideally)
		var c2 models.Caja
		if err := tx.First(&c2, 2).Error; err != nil {
			return err
		}
		if c2.Saldo < req.Monto {
			return fmt.Errorf("saldo_insuficiente")
		}

		// 2. Transacción de Egreso (Caja 2)
		tEgreso = models.Transaccion{
			CategoriaID: egresoCatID,
			CajaID:      2,
			Monto:       req.Monto,
			Descripcion: req.Descripcion,
			Usuario:     req.Usuario,
		}
		if err := tx.Create(&tEgreso).Error; err != nil {
			return err
		}

		// 2.1 Actualizar Saldo Caja 2
		newSaldo2 := c2.Saldo - req.Monto
		if err := tx.Model(&models.Caja{}).Where("id = ?", 2).Update("saldo", newSaldo2).Error; err != nil {
			return err
		}

		// 2.2 Log Egreso
		logEgreso := models.TransaccionLog{
			TransaccionID: tEgreso.ID,
			Accion:        "INSERT",
			Usuario:       req.Usuario,
			Detalle:       fmt.Sprintf("Egreso automático por retiro de banco: %s", formatMonto(req.Monto)),
			SaldoAntes:    c2.Saldo,
			SaldoDespues:  newSaldo2,
		}
		if err := tx.Create(&logEgreso).Error; err != nil {
			return err
		}

		// 3. Transacción de Ingreso (Caja 1)
		tIngreso = models.Transaccion{
			CategoriaID: ingresoCatID,
			CajaID:      1,
			Monto:       req.Monto,
			Descripcion: "Ingreso por retiro desde Cuenta bancaria",
			Usuario:     req.Usuario,
		}
		if err := tx.Create(&tIngreso).Error; err != nil {
			return err
		}

		// 3.1 Obtener Caja 1
		var c1 models.Caja
		if err := tx.First(&c1, 1).Error; err != nil {
			return err
		}

		// 3.2 Actualizar Saldo Caja 1
		newSaldo1 := c1.Saldo + req.Monto
		if err := tx.Model(&models.Caja{}).Where("id = ?", 1).Update("saldo", newSaldo1).Error; err != nil {
			return err
		}

		// 3.3 Log Ingreso
		logIngreso := models.TransaccionLog{
			TransaccionID: tIngreso.ID,
			Accion:        "INSERT",
			Usuario:       req.Usuario,
			Detalle:       fmt.Sprintf("Ingreso automático por retiro de banco: %s", formatMonto(req.Monto)),
			SaldoAntes:    c1.Saldo,
			SaldoDespues:  newSaldo1,
		}
		if err := tx.Create(&logIngreso).Error; err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudo completar el retiro/ingreso", "detalle": err.Error()})
		return
	}
	// Notificaciones
	emojiEg := tipoEmoji(catEg.Tipo)
	msgEg := fmt.Sprintf("*TRANSACCION CREADA*\n📪 *ID:* %d\n📄 *Descripcion:* %s\n📚 *Categoria:* %s\n🏷️ *Tipo de movimiento:* %s %s\n💲*Monto:* %s\n🧾 *Caja:* %s\n👤 *Usuario:* %s", tEgreso.ID, tEgreso.Descripcion, catEg.Nombre, catEg.Tipo, emojiEg, formatMonto(tEgreso.Monto), labelCaja(tEgreso.CajaID), tEgreso.Usuario)
	notify.SendText(msgEg)
	emojiIn := tipoEmoji(catIn.Tipo)
	msgIn := fmt.Sprintf("*TRANSACCION CREADA*\n📪 *ID:* %d\n📄 *Descripcion:* %s\n📚 *Categoria:* %s\n🏷️ *Tipo de movimiento:* %s %s\n💲*Monto:* %s\n🧾 *Caja:* %s\n👤 *Usuario:* %s", tIngreso.ID, tIngreso.Descripcion, catIn.Nombre, catIn.Tipo, emojiIn, formatMonto(tIngreso.Monto), labelCaja(tIngreso.CajaID), tIngreso.Usuario)
	notify.SendText(msgIn)

	c.JSON(http.StatusOK, gin.H{
		"egreso":  tEgreso,
		"ingreso": tIngreso,
	})
}

// OdooGetBilling obtiene la facturación mensual por punto de venta
func OdooGetBilling(c *gin.Context) {
	yearStr := c.DefaultQuery("year", strconv.Itoa(time.Now().Year()))
	year, err := strconv.Atoi(yearStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Año inválido"})
		return
	}

	res, err := odoo.GetMonthlyBilling(context.Background(), os.Getenv("ODOO_URL"), os.Getenv("ODOO_DB"), os.Getenv("ODOO_USER"), os.Getenv("ODOO_PASSWORD"), year)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, res)
}
