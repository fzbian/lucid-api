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

type POSFullRefundResult struct {
	OriginalOrderID        int64                    `json:"original_order_id"`
	OriginalOrderName      string                   `json:"original_order_name"`
	RefundOrderID          int64                    `json:"refund_order_id"`
	RefundOrderName        string                   `json:"refund_order_name"`
	RefundOrderState       string                   `json:"refund_order_state"`
	RefundSessionID        int64                    `json:"refund_session_id"`
	RefundSessionName      string                   `json:"refund_session_name"`
	RefundConfigID         int64                    `json:"refund_config_id"`
	RefundConfigName       string                   `json:"refund_config_name"`
	AmountTotal            float64                  `json:"amount_total"`
	AmountTax              float64                  `json:"amount_tax"`
	AmountPaid             float64                  `json:"amount_paid"`
	PaymentsByMethod       []POSRefundPaymentMethod `json:"payments_by_method"`
	RealtimeStockProcessed bool                     `json:"realtime_stock_processed"`
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
	PaymentIDs         []int64
	HasRefundableLines bool
	IsRefunded         bool
}

type posConfigRefundSnapshot struct {
	ID                   int64
	Name                 string
	CurrentSessionID     int64
	CurrentSessionName   string
	UpdateStockAtClosing bool
	HasUpdateStockField  bool
}

type posSessionRefundSnapshot struct {
	ID       int64
	Name     string
	State    string
	ConfigID int64
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
	if requiresRealtimePicking {
		stockUpdated := false
		if _, err := c.callOdoo("pos.order", "_create_order_picking", []interface{}{[]interface{}{refundOrderID}}, map[string]interface{}{}); err == nil {
			stockUpdated = true
		} else if _, err2 := c.callOdoo("pos.order", "create_picking", []interface{}{[]interface{}{refundOrderID}}, map[string]interface{}{}); err2 == nil {
			stockUpdated = true
		}

		if stockUpdated {
			_, _ = c.callOdoo("pos.order", "_compute_total_cost_in_real_time", []interface{}{[]interface{}{refundOrderID}}, map[string]interface{}{})
			realtimeStockProcessed = true
		} else if canCallPrivate {
			// Si Odoo confirmó que debía crear picking en tiempo real, mantener fallo duro.
			return POSFullRefundResult{}, fmt.Errorf("actualizar inventario del reembolso: no se pudo crear picking")
		}
	}

	refundOrder, err := c.fetchOrderRefundSnapshot(refundOrderID)
	if err != nil {
		return POSFullRefundResult{}, err
	}

	return POSFullRefundResult{
		OriginalOrderID:        order.ID,
		OriginalOrderName:      order.Name,
		RefundOrderID:          refundOrder.ID,
		RefundOrderName:        refundOrder.Name,
		RefundOrderState:       refundOrder.State,
		RefundSessionID:        refundOrder.SessionID,
		RefundSessionName:      refundOrder.SessionName,
		RefundConfigID:         refundOrder.ConfigID,
		RefundConfigName:       refundOrder.ConfigName,
		AmountTotal:            refundOrder.AmountTotal,
		AmountTax:              refundOrder.AmountTax,
		AmountPaid:             refundOrder.AmountPaid,
		PaymentsByMethod:       paymentsByMethod,
		RealtimeStockProcessed: realtimeStockProcessed,
		CreatedAtISO:           time.Now().UTC().Format(time.RFC3339),
	}, nil
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
		PaymentIDs:         asInt64Slice(row["payment_ids"]),
		HasRefundableLines: hasRefundable,
		IsRefunded:         asBool(row["is_refunded"]),
	}, nil
}

func (c *Client) fetchPOSConfigRefundSnapshot(configID int64) (posConfigRefundSnapshot, error) {
	fieldsMeta, _ := c.fetchModelFieldsMeta("pos.config")
	fields := selectExistingFields(fieldsMeta, []string{"id", "name", "current_session_id", "update_stock_at_closing"})
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

	return posConfigRefundSnapshot{
		ID:                   asInt64(row["id"]),
		Name:                 asString(row["name"]),
		CurrentSessionID:     sessionID,
		CurrentSessionName:   sessionName,
		UpdateStockAtClosing: asBool(row["update_stock_at_closing"]),
		HasUpdateStockField:  hasUpdateStock,
	}, nil
}

func (c *Client) fetchPOSSessionRefundSnapshot(sessionID int64) (posSessionRefundSnapshot, error) {
	rawRows, err := c.callOdoo("pos.session", "search_read", []interface{}{}, map[string]interface{}{
		"domain": []any{[]any{"id", "=", sessionID}},
		"fields": []string{"id", "name", "state", "config_id"},
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
		ID:       asInt64(row["id"]),
		Name:     asString(row["name"]),
		State:    asString(row["state"]),
		ConfigID: configID,
	}, nil
}
