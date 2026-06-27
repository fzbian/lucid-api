package odoo

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

type POSRefundPaymentMethod struct {
	PaymentMethodID   int64   `json:"payment_method_id"`
	PaymentMethodName string  `json:"payment_method_name"`
	Amount            float64 `json:"amount"`
}

type POSRefundStockLine struct {
	ProductID             int64   `json:"product_id"`
	ProductName           string  `json:"product_name"`
	QtyReturned           float64 `json:"qty_returned"`
	StockBefore           float64 `json:"stock_before"`
	StockAfterOdoo        float64 `json:"stock_after_odoo"`
	StockExpected         float64 `json:"stock_expected"`
	ManualAdjustmentDelta float64 `json:"manual_adjustment_delta"`
	StockFinal            float64 `json:"stock_final"`
	AdjustedManually      bool    `json:"adjusted_manually"`
}

type POSFullRefundResult struct {
	OriginalOrderID        int64                    `json:"original_order_id"`
	OriginalOrderName      string                   `json:"original_order_name"`
	OriginalInvoiceID      int64                    `json:"original_invoice_id"`
	OriginalInvoiceName    string                   `json:"original_invoice_name"`
	RefundOrderID          int64                    `json:"refund_order_id"`
	RefundOrderName        string                   `json:"refund_order_name"`
	RefundOrderState       string                   `json:"refund_order_state"`
	RefundInvoiceID        int64                    `json:"refund_invoice_id"`
	RefundInvoiceName      string                   `json:"refund_invoice_name"`
	RefundSessionID        int64                    `json:"refund_session_id"`
	RefundSessionName      string                   `json:"refund_session_name"`
	RefundConfigID         int64                    `json:"refund_config_id"`
	RefundConfigName       string                   `json:"refund_config_name"`
	AmountTotal            float64                  `json:"amount_total"`
	AmountTax              float64                  `json:"amount_tax"`
	AmountPaid             float64                  `json:"amount_paid"`
	PaymentsByMethod       []POSRefundPaymentMethod `json:"payments_by_method"`
	RealtimeStockProcessed bool                     `json:"realtime_stock_processed"`
	StockLocationID        int64                    `json:"stock_location_id"`
	StockLocationName      string                   `json:"stock_location_name"`
	StockAudit             []POSRefundStockLine     `json:"stock_audit"`
	ManualStockAdjusted    bool                     `json:"manual_stock_adjusted"`
	CreatedAtISO           string                   `json:"created_at_iso"`
}

type POSRefundValidationError struct {
	Message string
}

func (e *POSRefundValidationError) Error() string {
	return e.Message
}

func IsPOSRefundValidationError(err error) bool {
	var vErr *POSRefundValidationError
	return errors.As(err, &vErr)
}

type posOrderRefundSnapshot struct {
	ID                 int64
	Name               string
	State              string
	AmountTotal        float64
	AmountTax          float64
	AmountPaid         float64
	SessionID          int64
	SessionName        string
	ConfigID           int64
	ConfigName         string
	InvoiceID          int64
	InvoiceName        string
	PaymentIDs         []int64
	HasRefundableLines bool
	IsRefunded         bool
}

type posConfigRefundSnapshot struct {
	ID                   int64
	Name                 string
	CurrentSessionID     int64
	CurrentSessionName   string
	StockLocationID      int64
	StockLocationName    string
	PickingTypeID        int64
	PickingTypeName      string
	UpdateStockAtClosing bool
	HasUpdateStockField  bool
}

type posSessionRefundSnapshot struct {
	ID                   int64
	Name                 string
	State                string
	ConfigID             int64
	UpdateStockAtClosing bool
	HasUpdateStockField  bool
}

type posRefundStockTarget struct {
	ProductID   int64
	ProductName string
	QtyReturned float64
}

func (c *Client) CreateFullOrderRefund(orderID int64) (POSFullRefundResult, error) {
	if orderID <= 0 {
		return POSFullRefundResult{}, &POSRefundValidationError{Message: "order_id inválido"}
	}

	order, err := c.fetchOrderRefundSnapshot(orderID)
	if err != nil {
		return POSFullRefundResult{}, err
	}

	if order.ID <= 0 {
		return POSFullRefundResult{}, &POSRefundValidationError{Message: "pedido no encontrado"}
	}

	state := strings.ToLower(strings.TrimSpace(order.State))
	if state != "paid" && state != "done" && state != "invoiced" {
		return POSFullRefundResult{}, &POSRefundValidationError{Message: "el pedido debe estar pagado para reembolsar"}
	}
	if order.AmountTotal <= 0 {
		return POSFullRefundResult{}, &POSRefundValidationError{Message: "solo se permiten reembolsos de pedidos con total positivo"}
	}
	if order.IsRefunded || !order.HasRefundableLines {
		return POSFullRefundResult{}, &POSRefundValidationError{Message: "el pedido ya fue reembolsado completamente"}
	}
	if order.ConfigID <= 0 {
		return POSFullRefundResult{}, &POSRefundValidationError{Message: "el pedido no tiene POS configurado para reembolso"}
	}
	if len(order.PaymentIDs) == 0 {
		return POSFullRefundResult{}, &POSRefundValidationError{Message: "el pedido no tiene pagos asociados para devolver"}
	}

	stockTargets, err := c.fetchRefundStockTargets(order.ID)
	if err != nil {
		return POSFullRefundResult{}, fmt.Errorf("cargar productos para auditoría de stock: %w", err)
	}

	config, err := c.fetchPOSConfigRefundSnapshot(order.ConfigID)
	if err != nil {
		return POSFullRefundResult{}, err
	}
	if config.ID <= 0 {
		return POSFullRefundResult{}, &POSRefundValidationError{Message: "no se pudo cargar la configuración POS del pedido"}
	}
	if config.CurrentSessionID <= 0 {
		return POSFullRefundResult{}, &POSRefundValidationError{Message: "para reembolsar debes tener una sesión POS abierta en ese local"}
	}

	session, err := c.fetchPOSSessionRefundSnapshot(config.CurrentSessionID)
	if err != nil {
		return POSFullRefundResult{}, err
	}
	if session.ID <= 0 {
		return POSFullRefundResult{}, &POSRefundValidationError{Message: "no se encontró la sesión POS activa del local"}
	}
	if session.ConfigID > 0 && session.ConfigID != order.ConfigID {
		return POSFullRefundResult{}, &POSRefundValidationError{Message: "la sesión abierta no corresponde al mismo local del pedido"}
	}
	sessionState := strings.ToLower(strings.TrimSpace(session.State))
	if sessionState != "opened" && sessionState != "opening_control" {
		return POSFullRefundResult{}, &POSRefundValidationError{Message: "la sesión del local debe estar abierta para reembolsar"}
	}

	stockLocationID, stockLocationName, err := c.resolvePOSStockLocation(config)
	if err != nil {
		return POSFullRefundResult{}, err
	}
	if len(stockTargets) > 0 && stockLocationID <= 0 {
		return POSFullRefundResult{}, fmt.Errorf("no se pudo determinar la ubicación de stock del local para validar el reembolso")
	}
	stockBeforeByProduct := make(map[int64]float64)
	if stockLocationID > 0 && len(stockTargets) > 0 {
		stockBeforeByProduct, err = c.fetchStockByProductAtLocation(stockTargets, stockLocationID)
		if err != nil {
			return POSFullRefundResult{}, fmt.Errorf("leer stock antes del reembolso: %w", err)
		}
	}

	canCallPrivate := false
	needsRealtimePicking := false
	if rawRealtime, rawErr := c.callOdoo("pos.order", "_should_create_picking_real_time", []interface{}{[]interface{}{order.ID}}, map[string]interface{}{}); rawErr == nil {
		var realtime bool
		if err := json.Unmarshal(rawRealtime, &realtime); err == nil {
			canCallPrivate = true
			needsRealtimePicking = realtime
		}
	}

	requiresRealtimePicking := false
	if canCallPrivate {
		requiresRealtimePicking = needsRealtimePicking
	} else if config.HasUpdateStockField {
		// Fallback para versiones de Odoo que no exponen _should_create_picking_real_time.
		requiresRealtimePicking = !config.UpdateStockAtClosing
	}

	paymentDetailsByID, err := c.fetchPOSPaymentsByIDs(order.PaymentIDs)
	if err != nil {
		return POSFullRefundResult{}, fmt.Errorf("cargar pagos del pedido: %w", err)
	}

	methodTotals := make(map[int64]*POSRefundPaymentMethod)
	methodOrder := make([]int64, 0)
	for _, paymentID := range order.PaymentIDs {
		p, ok := paymentDetailsByID[paymentID]
		if !ok {
			continue
		}
		if p.PaymentMethodID <= 0 {
			continue
		}
		if p.IsChange {
			continue
		}
		entry := methodTotals[p.PaymentMethodID]
		if entry == nil {
			entry = &POSRefundPaymentMethod{
				PaymentMethodID:   p.PaymentMethodID,
				PaymentMethodName: p.PaymentMethod,
				Amount:            0,
			}
			methodTotals[p.PaymentMethodID] = entry
			methodOrder = append(methodOrder, p.PaymentMethodID)
		}
		entry.Amount += p.Amount
		if strings.TrimSpace(entry.PaymentMethodName) == "" && strings.TrimSpace(p.PaymentMethod) != "" {
			entry.PaymentMethodName = p.PaymentMethod
		}
	}

	paymentsByMethod := make([]POSRefundPaymentMethod, 0, len(methodOrder))
	totalMethods := 0.0
	for _, methodID := range methodOrder {
		entry := methodTotals[methodID]
		if entry == nil {
			continue
		}
		if entry.Amount <= 0 {
			continue
		}
		paymentsByMethod = append(paymentsByMethod, *entry)
		totalMethods += entry.Amount
	}
	sort.Slice(paymentsByMethod, func(i, j int) bool {
		return paymentsByMethod[i].PaymentMethodID < paymentsByMethod[j].PaymentMethodID
	})

	if len(paymentsByMethod) == 0 {
		return POSFullRefundResult{}, &POSRefundValidationError{Message: "no se pudo determinar el método de pago original para el reembolso"}
	}

	amountTolerance := math.Max(0.01, math.Abs(order.AmountPaid)*0.001)
	if math.Abs(totalMethods-order.AmountPaid) > amountTolerance {
		return POSFullRefundResult{}, &POSRefundValidationError{
			Message: "los pagos del pedido no cuadran con el total pagado; no se genera reembolso automático",
		}
	}

	rawRefundAction, err := c.callOdoo("pos.order", "refund", []interface{}{[]interface{}{order.ID}}, map[string]interface{}{})
	if err != nil {
		return POSFullRefundResult{}, fmt.Errorf("crear borrador de reembolso: %w", err)
	}
	var refundAction map[string]any
	if err := json.Unmarshal(rawRefundAction, &refundAction); err != nil {
		return POSFullRefundResult{}, fmt.Errorf("interpretar respuesta de reembolso: %w", err)
	}
	refundOrderID := asInt64(refundAction["res_id"])
	if refundOrderID <= 0 {
		return POSFullRefundResult{}, fmt.Errorf("odoo no devolvió id del pedido de reembolso")
	}
	if err := c.moveRefundOrderToOpenSession(refundOrderID, session); err != nil {
		return POSFullRefundResult{}, err
	}

	nowRaw := time.Now().UTC().Format("2006-01-02 15:04:05")
	for _, payment := range paymentsByMethod {
		paymentData := map[string]interface{}{
			"pos_order_id":      refundOrderID,
			"amount":            -payment.Amount,
			"payment_date":      nowRaw,
			"payment_method_id": payment.PaymentMethodID,
		}
		if _, err := c.callOdoo("pos.order", "add_payment", []interface{}{[]interface{}{refundOrderID}, paymentData}, map[string]interface{}{}); err != nil {
			return POSFullRefundResult{}, fmt.Errorf("registrar pago de reembolso (%s): %w", payment.PaymentMethodName, err)
		}
	}

	if _, err := c.callOdoo("pos.order", "action_pos_order_paid", []interface{}{[]interface{}{refundOrderID}}, map[string]interface{}{}); err != nil {
		return POSFullRefundResult{}, fmt.Errorf("confirmar pedido de reembolso: %w", err)
	}

	realtimeStockProcessed := false
	if processed, err := c.processRefundStock(refundOrderID, session, requiresRealtimePicking, canCallPrivate); err != nil {
		return POSFullRefundResult{}, err
	} else {
		realtimeStockProcessed = processed
	}

	stockAudit, manualStockAdjusted, err := c.auditAndRepairRefundStock(stockTargets, stockLocationID, stockBeforeByProduct)
	if err != nil {
		return POSFullRefundResult{}, err
	}

	refundOrder, err := c.fetchOrderRefundSnapshot(refundOrderID)
	if err != nil {
		return POSFullRefundResult{}, err
	}

	return POSFullRefundResult{
		OriginalOrderID:        order.ID,
		OriginalOrderName:      order.Name,
		OriginalInvoiceID:      order.InvoiceID,
		OriginalInvoiceName:    order.InvoiceName,
		RefundOrderID:          refundOrder.ID,
		RefundOrderName:        refundOrder.Name,
		RefundOrderState:       refundOrder.State,
		RefundInvoiceID:        refundOrder.InvoiceID,
		RefundInvoiceName:      refundOrder.InvoiceName,
		RefundSessionID:        refundOrder.SessionID,
		RefundSessionName:      refundOrder.SessionName,
		RefundConfigID:         refundOrder.ConfigID,
		RefundConfigName:       refundOrder.ConfigName,
		AmountTotal:            refundOrder.AmountTotal,
		AmountTax:              refundOrder.AmountTax,
		AmountPaid:             refundOrder.AmountPaid,
		PaymentsByMethod:       paymentsByMethod,
		RealtimeStockProcessed: realtimeStockProcessed,
		StockLocationID:        stockLocationID,
		StockLocationName:      stockLocationName,
		StockAudit:             stockAudit,
		ManualStockAdjusted:    manualStockAdjusted,
		CreatedAtISO:           time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func (c *Client) moveRefundOrderToOpenSession(refundOrderID int64, session posSessionRefundSnapshot) error {
	refundDraft, err := c.fetchOrderRefundSnapshot(refundOrderID)
	if err != nil {
		return fmt.Errorf("validar sesión del reembolso: %w", err)
	}
	if refundDraft.ID <= 0 {
		return fmt.Errorf("validar sesión del reembolso: pedido de reembolso no encontrado")
	}
	if refundDraft.SessionID == session.ID {
		return nil
	}

	if _, err := c.callOdoo("pos.order", "write", []interface{}{[]interface{}{refundOrderID}, map[string]interface{}{"session_id": session.ID}}, map[string]interface{}{}); err != nil {
		return fmt.Errorf("asignar reembolso a la sesión abierta del local: %w", err)
	}

	updated, err := c.fetchOrderRefundSnapshot(refundOrderID)
	if err != nil {
		return fmt.Errorf("verificar sesión del reembolso: %w", err)
	}
	if updated.SessionID != session.ID {
		return fmt.Errorf("asignar reembolso a la sesión abierta del local: Odoo no aceptó la sesión activa")
	}
	return nil
}

func (c *Client) fetchOrderRefundSnapshot(orderID int64) (posOrderRefundSnapshot, error) {
	fieldsMeta, _ := c.fetchModelFieldsMeta("pos.order")
	fields := selectExistingFields(fieldsMeta, []string{
		"id",
		"name",
		"state",
		"amount_total",
		"amount_tax",
		"amount_paid",
		"session_id",
		"config_id",
		"account_move",
		"account_move_id",
		"invoice_id",
		"payment_ids",
		"has_refundable_lines",
		"is_refunded",
	})
	if len(fields) == 0 {
		fields = []string{"id", "name", "state", "amount_total", "amount_tax", "amount_paid", "session_id", "config_id", "payment_ids"}
	}

	rawRows, err := c.callOdoo("pos.order", "search_read", []interface{}{}, map[string]interface{}{
		"domain": []any{[]any{"id", "=", orderID}},
		"fields": fields,
		"limit":  1,
	})
	if err != nil {
		return posOrderRefundSnapshot{}, fmt.Errorf("consultar pedido %d: %w", orderID, err)
	}

	var rows []map[string]any
	if err := json.Unmarshal(rawRows, &rows); err != nil {
		return posOrderRefundSnapshot{}, fmt.Errorf("decodificar pedido %d: %w", orderID, err)
	}
	if len(rows) == 0 {
		return posOrderRefundSnapshot{}, nil
	}
	row := rows[0]

	sessionID, sessionName := extractMany2One(row["session_id"])
	configID, configName := extractMany2One(row["config_id"])
	invoiceID, invoiceName := extractInvoiceReference(row)
	hasRefundable := true
	if _, ok := row["has_refundable_lines"]; ok {
		hasRefundable = asBool(row["has_refundable_lines"])
	}

	return posOrderRefundSnapshot{
		ID:                 asInt64(row["id"]),
		Name:               asString(row["name"]),
		State:              asString(row["state"]),
		AmountTotal:        asFloat(row["amount_total"]),
		AmountTax:          asFloat(row["amount_tax"]),
		AmountPaid:         asFloat(row["amount_paid"]),
		SessionID:          sessionID,
		SessionName:        sessionName,
		ConfigID:           configID,
		ConfigName:         configName,
		InvoiceID:          invoiceID,
		InvoiceName:        invoiceName,
		PaymentIDs:         asInt64Slice(row["payment_ids"]),
		HasRefundableLines: hasRefundable,
		IsRefunded:         asBool(row["is_refunded"]),
	}, nil
}

func (c *Client) fetchPOSConfigRefundSnapshot(configID int64) (posConfigRefundSnapshot, error) {
	fieldsMeta, _ := c.fetchModelFieldsMeta("pos.config")
	fields := selectExistingFields(fieldsMeta, []string{"id", "name", "current_session_id", "stock_location_id", "picking_type_id", "update_stock_at_closing"})
	if len(fields) == 0 {
		fields = []string{"id", "name", "current_session_id"}
	}

	hasUpdateStock := false
	if _, ok := fieldsMeta["update_stock_at_closing"]; ok {
		hasUpdateStock = true
	}

	rawRows, err := c.callOdoo("pos.config", "search_read", []interface{}{}, map[string]interface{}{
		"domain": []any{[]any{"id", "=", configID}},
		"fields": fields,
		"limit":  1,
	})
	if err != nil {
		return posConfigRefundSnapshot{}, fmt.Errorf("consultar configuración POS %d: %w", configID, err)
	}

	var rows []map[string]any
	if err := json.Unmarshal(rawRows, &rows); err != nil {
		return posConfigRefundSnapshot{}, fmt.Errorf("decodificar configuración POS %d: %w", configID, err)
	}
	if len(rows) == 0 {
		return posConfigRefundSnapshot{}, nil
	}
	row := rows[0]
	sessionID, sessionName := extractMany2One(row["current_session_id"])
	stockLocationID, stockLocationName := extractMany2One(row["stock_location_id"])
	pickingTypeID, pickingTypeName := extractMany2One(row["picking_type_id"])

	return posConfigRefundSnapshot{
		ID:                   asInt64(row["id"]),
		Name:                 asString(row["name"]),
		CurrentSessionID:     sessionID,
		CurrentSessionName:   sessionName,
		StockLocationID:      stockLocationID,
		StockLocationName:    stockLocationName,
		PickingTypeID:        pickingTypeID,
		PickingTypeName:      pickingTypeName,
		UpdateStockAtClosing: asBool(row["update_stock_at_closing"]),
		HasUpdateStockField:  hasUpdateStock,
	}, nil
}

func (c *Client) fetchPOSSessionRefundSnapshot(sessionID int64) (posSessionRefundSnapshot, error) {
	fieldsMeta, _ := c.fetchModelFieldsMeta("pos.session")
	fields := selectExistingFields(fieldsMeta, []string{"id", "name", "state", "config_id", "update_stock_at_closing"})
	if len(fields) == 0 {
		fields = []string{"id", "name", "state", "config_id"}
	}
	_, hasUpdateStock := fieldsMeta["update_stock_at_closing"]

	rawRows, err := c.callOdoo("pos.session", "search_read", []interface{}{}, map[string]interface{}{
		"domain": []any{[]any{"id", "=", sessionID}},
		"fields": fields,
		"limit":  1,
	})
	if err != nil {
		return posSessionRefundSnapshot{}, fmt.Errorf("consultar sesión POS %d: %w", sessionID, err)
	}

	var rows []map[string]any
	if err := json.Unmarshal(rawRows, &rows); err != nil {
		return posSessionRefundSnapshot{}, fmt.Errorf("decodificar sesión POS %d: %w", sessionID, err)
	}
	if len(rows) == 0 {
		return posSessionRefundSnapshot{}, nil
	}
	row := rows[0]
	configID, _ := extractMany2One(row["config_id"])

	return posSessionRefundSnapshot{
		ID:                   asInt64(row["id"]),
		Name:                 asString(row["name"]),
		State:                asString(row["state"]),
		ConfigID:             configID,
		UpdateStockAtClosing: asBool(row["update_stock_at_closing"]),
		HasUpdateStockField:  hasUpdateStock,
	}, nil
}

func (c *Client) processRefundStock(refundOrderID int64, session posSessionRefundSnapshot, requiresRealtimePicking bool, canCallPrivate bool) (bool, error) {
	hasStockableLines, err := c.posOrderHasStockableLines(refundOrderID)
	if err != nil {
		return false, fmt.Errorf("validar productos de stock del reembolso: %w", err)
	}
	if !hasStockableLines {
		return false, nil
	}

	if session.HasUpdateStockField && session.UpdateStockAtClosing {
		before, _ := c.countPickings([]any{[]any{"pos_session_id", "=", session.ID}, []any{"state", "=", "done"}})
		if _, err := c.callOdoo("pos.session", "_create_picking_at_end_of_session", []interface{}{[]interface{}{session.ID}}, map[string]interface{}{}); err != nil {
			return false, nil
		}
		after, err := c.countPickings([]any{[]any{"pos_session_id", "=", session.ID}, []any{"state", "=", "done"}})
		if err != nil {
			return false, nil
		}
		if after <= before {
			return false, nil
		}
		if _, err := c.callOdoo("pos.session", "write", []interface{}{[]interface{}{session.ID}, map[string]interface{}{"update_stock_at_closing": false}}, map[string]interface{}{}); err != nil {
			return false, nil
		}
		_, _ = c.callOdoo("pos.order", "_compute_total_cost_in_real_time", []interface{}{[]interface{}{refundOrderID}}, map[string]interface{}{})
		return true, nil
	}

	if done, _ := c.hasDonePickingForOrder(refundOrderID); done {
		return true, nil
	}

	stockUpdated := false
	if _, err := c.callOdoo("pos.order", "_create_order_picking", []interface{}{[]interface{}{refundOrderID}}, map[string]interface{}{}); err == nil {
		stockUpdated = true
	} else if _, err2 := c.callOdoo("pos.order", "create_picking", []interface{}{[]interface{}{refundOrderID}}, map[string]interface{}{}); err2 == nil {
		stockUpdated = true
	}

	if stockUpdated {
		done, err := c.hasDonePickingForOrder(refundOrderID)
		if err != nil {
			return false, fmt.Errorf("verificar inventario del reembolso: %w", err)
		}
		if done {
			_, _ = c.callOdoo("pos.order", "_compute_total_cost_in_real_time", []interface{}{[]interface{}{refundOrderID}}, map[string]interface{}{})
			return true, nil
		}
	}

	return false, nil
}

func (c *Client) resolvePOSStockLocation(config posConfigRefundSnapshot) (int64, string, error) {
	if config.StockLocationID > 0 {
		return config.StockLocationID, config.StockLocationName, nil
	}
	if config.PickingTypeID <= 0 {
		return 0, "", nil
	}

	fieldsMeta, _ := c.fetchModelFieldsMeta("stock.picking.type")
	fields := selectExistingFields(fieldsMeta, []string{"id", "default_location_src_id", "default_location_dest_id"})
	if len(fields) == 0 {
		fields = []string{"id", "default_location_src_id", "default_location_dest_id"}
	}

	rawRows, err := c.callOdoo("stock.picking.type", "search_read", []interface{}{}, map[string]interface{}{
		"domain": []any{[]any{"id", "=", config.PickingTypeID}},
		"fields": fields,
		"limit":  1,
	})
	if err != nil {
		return 0, "", fmt.Errorf("consultar ubicación de stock del POS: %w", err)
	}

	var rows []map[string]any
	if err := json.Unmarshal(rawRows, &rows); err != nil {
		return 0, "", fmt.Errorf("decodificar ubicación de stock del POS: %w", err)
	}
	if len(rows) == 0 {
		return 0, "", nil
	}

	locationID, locationName := extractMany2One(rows[0]["default_location_src_id"])
	if locationID <= 0 {
		locationID, locationName = extractMany2One(rows[0]["default_location_dest_id"])
	}
	return locationID, locationName, nil
}

func (c *Client) fetchRefundStockTargets(orderID int64) ([]posRefundStockTarget, error) {
	rawRows, err := c.callOdoo("pos.order.line", "search_read", []interface{}{}, map[string]interface{}{
		"domain": []any{[]any{"order_id", "=", orderID}},
		"fields": []string{"id", "product_id", "qty"},
		"limit":  1000,
	})
	if err != nil {
		return nil, err
	}

	var rows []map[string]any
	if err := json.Unmarshal(rawRows, &rows); err != nil {
		return nil, err
	}

	qtyByProduct := make(map[int64]float64)
	nameByProduct := make(map[int64]string)
	for _, row := range rows {
		qty := asFloat(row["qty"])
		if qty <= 0 {
			continue
		}
		productID, productName := extractMany2One(row["product_id"])
		if productID <= 0 {
			continue
		}
		qtyByProduct[productID] += qty
		if strings.TrimSpace(nameByProduct[productID]) == "" {
			nameByProduct[productID] = productName
		}
	}
	if len(qtyByProduct) == 0 {
		return []posRefundStockTarget{}, nil
	}

	productIDs := make([]int64, 0, len(qtyByProduct))
	for productID := range qtyByProduct {
		productIDs = append(productIDs, productID)
	}
	sort.Slice(productIDs, func(i, j int) bool { return productIDs[i] < productIDs[j] })

	fieldsMeta, _ := c.fetchModelFieldsMeta("product.product")
	fields := selectExistingFields(fieldsMeta, []string{"id", "display_name", "name", "type", "detailed_type"})
	if len(fields) == 0 {
		fields = []string{"id", "name", "type"}
	}
	rawProducts, err := c.callOdoo("product.product", "search_read", []interface{}{}, map[string]interface{}{
		"domain": []any{[]any{"id", "in", toAnyInt64Slice(productIDs)}},
		"fields": fields,
		"limit":  len(productIDs) + 20,
	})
	if err != nil {
		return nil, err
	}

	var products []map[string]any
	if err := json.Unmarshal(rawProducts, &products); err != nil {
		return nil, err
	}

	stockable := make(map[int64]bool)
	for _, product := range products {
		productID := asInt64(product["id"])
		productType := strings.ToLower(strings.TrimSpace(asString(product["type"])))
		if productType == "" {
			productType = strings.ToLower(strings.TrimSpace(asString(product["detailed_type"])))
		}
		if productType == "product" || productType == "consu" {
			stockable[productID] = true
			if name := strings.TrimSpace(asString(product["display_name"])); name != "" {
				nameByProduct[productID] = name
			} else if name := strings.TrimSpace(asString(product["name"])); name != "" {
				nameByProduct[productID] = name
			}
		}
	}

	targets := make([]posRefundStockTarget, 0, len(productIDs))
	for _, productID := range productIDs {
		if !stockable[productID] {
			continue
		}
		targets = append(targets, posRefundStockTarget{
			ProductID:   productID,
			ProductName: nameByProduct[productID],
			QtyReturned: qtyByProduct[productID],
		})
	}
	return targets, nil
}

func (c *Client) fetchStockByProductAtLocation(targets []posRefundStockTarget, locationID int64) (map[int64]float64, error) {
	result := make(map[int64]float64)
	if locationID <= 0 || len(targets) == 0 {
		return result, nil
	}

	productIDs := make([]int64, 0, len(targets))
	for _, target := range targets {
		if target.ProductID > 0 {
			productIDs = append(productIDs, target.ProductID)
			result[target.ProductID] = 0
		}
	}

	fieldsMeta, _ := c.fetchModelFieldsMeta("stock.quant")
	fields := selectExistingFields(fieldsMeta, []string{"id", "product_id", "location_id", "quantity"})
	if len(fields) == 0 {
		fields = []string{"id", "product_id", "location_id", "quantity"}
	}

	rawRows, err := c.callOdoo("stock.quant", "search_read", []interface{}{}, map[string]interface{}{
		"domain": []any{
			[]any{"product_id", "in", toAnyInt64Slice(productIDs)},
			[]any{"location_id", "=", locationID},
		},
		"fields": fields,
		"limit":  len(productIDs) + 100,
	})
	if err != nil {
		return result, err
	}

	var rows []map[string]any
	if err := json.Unmarshal(rawRows, &rows); err != nil {
		return result, err
	}
	for _, row := range rows {
		productID, _ := extractMany2One(row["product_id"])
		if productID > 0 {
			result[productID] += asFloat(row["quantity"])
		}
	}
	return result, nil
}

func (c *Client) auditAndRepairRefundStock(targets []posRefundStockTarget, locationID int64, beforeByProduct map[int64]float64) ([]POSRefundStockLine, bool, error) {
	if locationID <= 0 || len(targets) == 0 {
		return []POSRefundStockLine{}, false, nil
	}

	afterOdooByProduct, err := c.fetchStockByProductAtLocation(targets, locationID)
	if err != nil {
		return nil, false, fmt.Errorf("leer stock después del reembolso: %w", err)
	}

	const stockTolerance = 0.000001
	audit := make([]POSRefundStockLine, 0, len(targets))
	manualAdjusted := false
	for _, target := range targets {
		before := beforeByProduct[target.ProductID]
		afterOdoo := afterOdooByProduct[target.ProductID]
		expected := before + target.QtyReturned
		delta := 0.0
		adjusted := false

		if afterOdoo+stockTolerance < expected {
			delta = expected - afterOdoo
			if err := c.applyManualStockAdjustment(target.ProductID, locationID, expected, delta); err != nil {
				return nil, false, fmt.Errorf("ajustar stock manual de %s: %w", target.ProductName, err)
			}
			adjusted = true
			manualAdjusted = true
		}

		finalByProduct, err := c.fetchStockByProductAtLocation([]posRefundStockTarget{target}, locationID)
		if err != nil {
			return nil, false, fmt.Errorf("verificar stock final de %s: %w", target.ProductName, err)
		}

		audit = append(audit, POSRefundStockLine{
			ProductID:             target.ProductID,
			ProductName:           target.ProductName,
			QtyReturned:           target.QtyReturned,
			StockBefore:           before,
			StockAfterOdoo:        afterOdoo,
			StockExpected:         expected,
			ManualAdjustmentDelta: delta,
			StockFinal:            finalByProduct[target.ProductID],
			AdjustedManually:      adjusted,
		})
	}

	return audit, manualAdjusted, nil
}

func (c *Client) applyManualStockAdjustment(productID int64, locationID int64, expectedQty float64, delta float64) error {
	if productID <= 0 || locationID <= 0 {
		return fmt.Errorf("producto o ubicación inválidos")
	}

	if _, err := c.callOdoo("stock.quant", "_update_available_quantity", []interface{}{productID, locationID, delta}, map[string]interface{}{}); err == nil {
		return nil
	}

	quantID, err := c.findOrCreateStockQuant(productID, locationID)
	if err != nil {
		return err
	}
	if quantID <= 0 {
		return fmt.Errorf("no se pudo preparar stock.quant")
	}

	if _, err := c.callOdoo("stock.quant", "write", []interface{}{[]interface{}{quantID}, map[string]interface{}{"inventory_quantity": expectedQty}}, map[string]interface{}{}); err != nil {
		return fmt.Errorf("escribir inventario físico: %w", err)
	}
	if _, err := c.callOdoo("stock.quant", "action_apply_inventory", []interface{}{[]interface{}{quantID}}, map[string]interface{}{}); err != nil {
		return fmt.Errorf("aplicar inventario físico: %w", err)
	}
	return nil
}

func (c *Client) findOrCreateStockQuant(productID int64, locationID int64) (int64, error) {
	rawRows, err := c.callOdoo("stock.quant", "search_read", []interface{}{}, map[string]interface{}{
		"domain": []any{
			[]any{"product_id", "=", productID},
			[]any{"location_id", "=", locationID},
		},
		"fields": []string{"id"},
		"limit":  1,
	})
	if err != nil {
		return 0, fmt.Errorf("buscar stock.quant: %w", err)
	}

	var rows []map[string]any
	if err := json.Unmarshal(rawRows, &rows); err != nil {
		return 0, fmt.Errorf("decodificar stock.quant: %w", err)
	}
	if len(rows) > 0 {
		return asInt64(rows[0]["id"]), nil
	}

	rawID, err := c.callOdoo("stock.quant", "create", []interface{}{map[string]interface{}{
		"product_id":  productID,
		"location_id": locationID,
	}}, map[string]interface{}{})
	if err != nil {
		return 0, fmt.Errorf("crear stock.quant: %w", err)
	}
	return asInt64FromRaw(rawID), nil
}

func (c *Client) posOrderHasStockableLines(orderID int64) (bool, error) {
	rawRows, err := c.callOdoo("pos.order.line", "search_read", []interface{}{}, map[string]interface{}{
		"domain": []any{[]any{"order_id", "=", orderID}},
		"fields": []string{"id", "product_id", "qty"},
		"limit":  1000,
	})
	if err != nil {
		return false, err
	}

	var rows []map[string]any
	if err := json.Unmarshal(rawRows, &rows); err != nil {
		return false, err
	}

	productIDsSet := make(map[int64]struct{})
	for _, row := range rows {
		if math.Abs(asFloat(row["qty"])) <= 0.000001 {
			continue
		}
		productID, _ := extractMany2One(row["product_id"])
		if productID > 0 {
			productIDsSet[productID] = struct{}{}
		}
	}
	if len(productIDsSet) == 0 {
		return false, nil
	}

	productIDs := make([]int64, 0, len(productIDsSet))
	for id := range productIDsSet {
		productIDs = append(productIDs, id)
	}

	rawProducts, err := c.callOdoo("product.product", "search_read", []interface{}{}, map[string]interface{}{
		"domain": []any{[]any{"id", "in", toAnyInt64Slice(productIDs)}},
		"fields": []string{"id", "type"},
		"limit":  len(productIDs) + 20,
	})
	if err != nil {
		return false, err
	}

	var products []map[string]any
	if err := json.Unmarshal(rawProducts, &products); err != nil {
		return false, err
	}
	for _, product := range products {
		productType := strings.ToLower(strings.TrimSpace(asString(product["type"])))
		if productType == "product" || productType == "consu" {
			return true, nil
		}
	}
	return false, nil
}

func (c *Client) hasDonePickingForOrder(orderID int64) (bool, error) {
	count, err := c.countPickings([]any{[]any{"pos_order_id", "=", orderID}, []any{"state", "=", "done"}})
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (c *Client) countPickings(domain []any) (int, error) {
	rawCount, err := c.callOdoo("stock.picking", "search_count", []interface{}{domain}, map[string]interface{}{})
	if err != nil {
		return 0, err
	}

	var countInt int
	if err := json.Unmarshal(rawCount, &countInt); err == nil {
		return countInt, nil
	}

	var countFloat float64
	if err := json.Unmarshal(rawCount, &countFloat); err == nil {
		return int(countFloat), nil
	}

	return 0, fmt.Errorf("no se pudo interpretar search_count de stock.picking")
}

func asInt64FromRaw(raw json.RawMessage) int64 {
	var idInt int64
	if err := json.Unmarshal(raw, &idInt); err == nil {
		return idInt
	}
	var idFloat float64
	if err := json.Unmarshal(raw, &idFloat); err == nil {
		return int64(idFloat)
	}
	var idAny any
	if err := json.Unmarshal(raw, &idAny); err == nil {
		return asInt64(idAny)
	}
	return 0
}
