package odoo

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

type POSOrdersQuery struct {
	From      time.Time
	To        time.Time
	Local     string
	SessionID int64
	Limit     int
	Offset    int
}

type OdooFieldMeta struct {
	Label string `json:"label"`
	Type  string `json:"type"`
}

type POSOrdersSummary struct {
	OrdersCount  int     `json:"orders_count"`
	AmountTotal  float64 `json:"amount_total"`
	AmountTax    float64 `json:"amount_tax"`
	AmountPaid   float64 `json:"amount_paid"`
	AmountReturn float64 `json:"amount_return"`
}

type POSOrderLine struct {
	ID           int64   `json:"id"`
	OrderID      int64   `json:"order_id"`
	OrderName    string  `json:"order_name"`
	ProductID    int64   `json:"product_id"`
	ProductName  string  `json:"product_name"`
	Qty          float64 `json:"qty"`
	PriceUnit    float64 `json:"price_unit"`
	Discount     float64 `json:"discount"`
	Subtotal     float64 `json:"subtotal"`
	SubtotalIncl float64 `json:"subtotal_incl"`
	TaxAmount    float64 `json:"tax_amount"`
}

type POSOrderPayment struct {
	ID              int64   `json:"id"`
	OrderID         int64   `json:"order_id"`
	OrderName       string  `json:"order_name"`
	PaymentMethodID int64   `json:"payment_method_id"`
	PaymentMethod   string  `json:"payment_method"`
	Amount          float64 `json:"amount"`
	IsChange        bool    `json:"is_change"`
	PaymentDateRaw  string  `json:"payment_date_raw"`
	PaymentDateISO  string  `json:"payment_date_iso"`
}

type POSInvoicedOrder struct {
	ID                  int64             `json:"id"`
	Name                string            `json:"name"`
	POSReference        string            `json:"pos_reference"`
	State               string            `json:"state"`
	DateOrderRaw        string            `json:"date_order_raw"`
	DateOrderISO        string            `json:"date_order_iso"`
	LocalID             int64             `json:"local_id"`
	LocalName           string            `json:"local_name"`
	SessionID           int64             `json:"session_id"`
	SessionName         string            `json:"session_name"`
	InvoiceID           int64             `json:"invoice_id"`
	InvoiceName         string            `json:"invoice_name"`
	CustomerID          int64             `json:"customer_id"`
	CustomerName        string            `json:"customer_name"`
	CashierID           int64             `json:"cashier_id"`
	CashierName         string            `json:"cashier_name"`
	AmountTotal         float64           `json:"amount_total"`
	AmountTax           float64           `json:"amount_tax"`
	AmountPaid          float64           `json:"amount_paid"`
	AmountReturn        float64           `json:"amount_return"`
	AmountSubtotal      float64           `json:"amount_subtotal"`
	PaymentsTotal       float64           `json:"payments_total"`
	TotalQty            float64           `json:"total_qty"`
	ItemsCount          int               `json:"items_count"`
	Margin              float64           `json:"margin"`
	Note                string            `json:"note"`
	CurrencyID          int64             `json:"currency_id"`
	CurrencyName        string            `json:"currency_name"`
	IsRefunded          bool              `json:"is_refunded"`
	HasRefundableLines  bool              `json:"has_refundable_lines"`
	RefundedOrdersCount int               `json:"refunded_orders_count"`
	LineIDs             []int64           `json:"-"`
	PaymentIDs          []int64           `json:"-"`
	LinesDetail         []POSOrderLine    `json:"lines_detail"`
	PaymentsDetail      []POSOrderPayment `json:"payments_detail"`
	Raw                 map[string]any    `json:"raw"`
}

type POSInvoicedOrdersResult struct {
	Total             int                      `json:"total"`
	Limit             int                      `json:"limit"`
	Offset            int                      `json:"offset"`
	Summary           POSOrdersSummary         `json:"summary"`
	AvailableLocales  []string                 `json:"available_locales"`
	AvailableSessions []int64                  `json:"available_sessions"`
	ReturnedFields    []string                 `json:"returned_fields"`
	FieldCatalog      map[string]OdooFieldMeta `json:"field_catalog"`
	Data              []POSInvoicedOrder       `json:"data"`
}

var preferredPOSOrderFields = []string{
	"id",
	"name",
	"pos_reference",
	"state",
	"date_order",
	"create_date",
	"write_date",
	"config_id",
	"session_id",
	"partner_id",
	"user_id",
	"employee_id",
	"company_id",
	"currency_id",
	"pricelist_id",
	"fiscal_position_id",
	"account_move",
	"account_move_id",
	"invoice_id",
	"to_invoice",
	"amount_total",
	"amount_tax",
	"amount_paid",
	"amount_return",
	"margin",
	"nb_print",
	"is_tipped",
	"tip_amount",
	"uid",
	"uuid",
	"note",
	"is_refunded",
	"has_refundable_lines",
	"refunded_orders_count",
	"payment_ids",
	"statement_ids",
	"lines",
}

// GetPOSInvoicedOrders lista pedidos facturados de Odoo con filtros por local, fecha y sesión.
func (c *Client) GetPOSInvoicedOrders(q POSOrdersQuery) (POSInvoicedOrdersResult, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}

	offset := q.Offset
	if offset < 0 {
		offset = 0
	}

	layout := "2006-01-02 15:04:05"
	domain := make([]any, 0, 6)
	// En Odoo POS, pedidos ya facturados/cerrados suelen quedar en paid/done/invoiced
	// dependiendo de la versión y configuración.
	domain = append(domain, []any{"state", "in", []string{"paid", "done", "invoiced"}})

	if !q.From.IsZero() {
		domain = append(domain, []any{"date_order", ">=", q.From.UTC().Format(layout)})
	}
	if !q.To.IsZero() {
		domain = append(domain, []any{"date_order", "<=", q.To.UTC().Format(layout)})
	}
	if strings.TrimSpace(q.Local) != "" {
		domain = append(domain, []any{"config_id", "ilike", strings.TrimSpace(q.Local)})
	}
	if q.SessionID > 0 {
		domain = append(domain, []any{"session_id", "=", q.SessionID})
	}

	fieldsMeta, _ := c.fetchPOSOrderFieldsMeta()
	selectedFields := selectPOSOrderFields(fieldsMeta)
	if len(selectedFields) == 0 {
		selectedFields = append([]string{}, preferredPOSOrderFields...)
	}

	total, err := c.fetchPOSOrderCount(domain)
	if err != nil {
		return POSInvoicedOrdersResult{}, err
	}

	kwargs := map[string]any{
		"domain": domain,
		"fields": selectedFields,
		"order":  "date_order desc, id desc",
		"limit":  limit,
		"offset": offset,
	}

	rawRows, err := c.callOdoo("pos.order", "search_read", []any{}, kwargs)
	if err != nil {
		return POSInvoicedOrdersResult{}, err
	}

	var rows []map[string]any
	if err := json.Unmarshal(rawRows, &rows); err != nil {
		return POSInvoicedOrdersResult{}, fmt.Errorf("decode pos.order search_read: %w", err)
	}

	orders := make([]POSInvoicedOrder, 0, len(rows))
	localSet := make(map[string]struct{})
	sessionSet := make(map[int64]struct{})
	lineIDsSet := make(map[int64]struct{})
	paymentIDsSet := make(map[int64]struct{})
	summary := POSOrdersSummary{}

	for _, row := range rows {
		order := mapPOSInvoicedOrder(row)
		orders = append(orders, order)

		if strings.TrimSpace(order.LocalName) != "" {
			localSet[order.LocalName] = struct{}{}
		}
		if order.SessionID > 0 {
			sessionSet[order.SessionID] = struct{}{}
		}
		for _, id := range order.LineIDs {
			if id > 0 {
				lineIDsSet[id] = struct{}{}
			}
		}
		for _, id := range order.PaymentIDs {
			if id > 0 {
				paymentIDsSet[id] = struct{}{}
			}
		}

		summary.OrdersCount++
		summary.AmountTotal += order.AmountTotal
		summary.AmountTax += order.AmountTax
		summary.AmountPaid += order.AmountPaid
		summary.AmountReturn += order.AmountReturn
	}

	lineDetailsByID := make(map[int64]POSOrderLine)
	lineIDs := sortedIDsFromSet(lineIDsSet)
	if len(lineIDs) > 0 {
		if fetchedLines, err := c.fetchPOSOrderLinesByIDs(lineIDs); err == nil {
			lineDetailsByID = fetchedLines
		}
	}

	paymentDetailsByID := make(map[int64]POSOrderPayment)
	paymentIDs := sortedIDsFromSet(paymentIDsSet)
	if len(paymentIDs) > 0 {
		if fetchedPayments, err := c.fetchPOSPaymentsByIDs(paymentIDs); err == nil {
			paymentDetailsByID = fetchedPayments
		}
	}

	for i := range orders {
		subtotal := 0.0
		totalQty := 0.0
		paymentsTotal := 0.0
		linesDetail := make([]POSOrderLine, 0, len(orders[i].LineIDs))
		paymentsDetail := make([]POSOrderPayment, 0, len(orders[i].PaymentIDs))

		for _, lineID := range orders[i].LineIDs {
			line, ok := lineDetailsByID[lineID]
			if !ok {
				continue
			}
			linesDetail = append(linesDetail, line)
			subtotal += line.Subtotal
			totalQty += line.Qty
		}

		for _, paymentID := range orders[i].PaymentIDs {
			payment, ok := paymentDetailsByID[paymentID]
			if !ok {
				continue
			}
			paymentsDetail = append(paymentsDetail, payment)
			paymentsTotal += payment.Amount
		}

		if len(linesDetail) == 0 {
			subtotal = orders[i].AmountTotal - orders[i].AmountTax
		}
		if subtotal < 0 {
			subtotal = 0
		}

		orders[i].LinesDetail = linesDetail
		orders[i].PaymentsDetail = paymentsDetail
		orders[i].AmountSubtotal = subtotal
		orders[i].TotalQty = totalQty
		orders[i].ItemsCount = len(linesDetail)
		orders[i].PaymentsTotal = paymentsTotal
	}

	availableLocales := make([]string, 0, len(localSet))
	for name := range localSet {
		availableLocales = append(availableLocales, name)
	}
	sort.Strings(availableLocales)

	availableSessions := make([]int64, 0, len(sessionSet))
	for id := range sessionSet {
		availableSessions = append(availableSessions, id)
	}
	sort.Slice(availableSessions, func(i, j int) bool { return availableSessions[i] < availableSessions[j] })

	return POSInvoicedOrdersResult{
		Total:             total,
		Limit:             limit,
		Offset:            offset,
		Summary:           summary,
		AvailableLocales:  availableLocales,
		AvailableSessions: availableSessions,
		ReturnedFields:    selectedFields,
		FieldCatalog:      buildPOSFieldCatalog(fieldsMeta, selectedFields),
		Data:              orders,
	}, nil
}

func (c *Client) fetchPOSOrderLinesByIDs(ids []int64) (map[int64]POSOrderLine, error) {
	result := make(map[int64]POSOrderLine)
	if len(ids) == 0 {
		return result, nil
	}

	fieldsMeta, _ := c.fetchModelFieldsMeta("pos.order.line")
	fields := selectExistingFields(fieldsMeta, []string{
		"id",
		"order_id",
		"product_id",
		"full_product_name",
		"display_name",
		"name",
		"qty",
		"price_unit",
		"discount",
		"price_subtotal",
		"price_subtotal_incl",
	})
	if len(fields) == 0 {
		fields = []string{
			"id",
			"order_id",
			"product_id",
			"qty",
			"price_unit",
			"discount",
			"price_subtotal",
			"price_subtotal_incl",
		}
	}

	rawRows, err := c.callOdoo("pos.order.line", "search_read", []any{}, map[string]any{
		"domain": []any{[]any{"id", "in", toAnyInt64Slice(ids)}},
		"fields": fields,
		"limit":  len(ids) + 20,
	})
	if err != nil {
		return result, err
	}

	var rows []map[string]any
	if err := json.Unmarshal(rawRows, &rows); err != nil {
		return result, fmt.Errorf("decode pos.order.line search_read: %w", err)
	}

	for _, row := range rows {
		line := mapPOSOrderLine(row)
		if line.ID > 0 {
			result[line.ID] = line
		}
	}
	return result, nil
}

func (c *Client) fetchPOSPaymentsByIDs(ids []int64) (map[int64]POSOrderPayment, error) {
	result := make(map[int64]POSOrderPayment)
	if len(ids) == 0 {
		return result, nil
	}

	fieldsMeta, _ := c.fetchModelFieldsMeta("pos.payment")
	fields := selectExistingFields(fieldsMeta, []string{
		"id",
		"pos_order_id",
		"order_id",
		"payment_method_id",
		"amount",
		"is_change",
		"payment_date",
		"create_date",
	})
	if len(fields) == 0 {
		fields = []string{
			"id",
			"payment_method_id",
			"amount",
			"is_change",
			"payment_date",
			"create_date",
		}
	}

	rawRows, err := c.callOdoo("pos.payment", "search_read", []any{}, map[string]any{
		"domain": []any{[]any{"id", "in", toAnyInt64Slice(ids)}},
		"fields": fields,
		"limit":  len(ids) + 20,
	})
	if err != nil {
		return result, err
	}

	var rows []map[string]any
	if err := json.Unmarshal(rawRows, &rows); err != nil {
		return result, fmt.Errorf("decode pos.payment search_read: %w", err)
	}

	for _, row := range rows {
		payment := mapPOSOrderPayment(row)
		if payment.ID > 0 {
			result[payment.ID] = payment
		}
	}
	return result, nil
}

func (c *Client) fetchPOSOrderCount(domain []any) (int, error) {
	rawCount, err := c.callOdoo("pos.order", "search_count", []any{domain}, map[string]any{})
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

	return 0, fmt.Errorf("no se pudo interpretar search_count")
}

func (c *Client) fetchModelFieldsMeta(model string) (map[string]map[string]any, error) {
	raw, err := c.callOdoo(model, "fields_get", []any{}, map[string]any{
		"attributes": []string{"string", "type"},
	})
	if err != nil {
		return nil, err
	}

	meta := make(map[string]map[string]any)
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, err
	}
	return meta, nil
}

func (c *Client) fetchPOSOrderFieldsMeta() (map[string]map[string]any, error) {
	return c.fetchModelFieldsMeta("pos.order")
}

func selectExistingFields(meta map[string]map[string]any, preferred []string) []string {
	if len(meta) == 0 {
		return []string{}
	}
	fields := make([]string, 0, len(preferred))
	for _, field := range preferred {
		if _, ok := meta[field]; ok {
			fields = append(fields, field)
		}
	}
	return fields
}

func selectPOSOrderFields(meta map[string]map[string]any) []string {
	if len(meta) == 0 {
		return append([]string{}, preferredPOSOrderFields...)
	}

	selected := make([]string, 0, len(preferredPOSOrderFields))
	seen := make(map[string]struct{})
	for _, field := range preferredPOSOrderFields {
		if _, ok := meta[field]; ok {
			selected = append(selected, field)
			seen[field] = struct{}{}
		}
	}

	customFields := make([]string, 0)
	for field := range meta {
		if strings.HasPrefix(field, "x_") {
			customFields = append(customFields, field)
		}
	}
	sort.Strings(customFields)
	for _, field := range customFields {
		if _, exists := seen[field]; exists {
			continue
		}
		selected = append(selected, field)
	}
	return selected
}

func buildPOSFieldCatalog(meta map[string]map[string]any, selected []string) map[string]OdooFieldMeta {
	catalog := make(map[string]OdooFieldMeta, len(selected))
	for _, field := range selected {
		label := field
		typ := ""
		if m, ok := meta[field]; ok {
			if s, ok := m["string"].(string); ok && strings.TrimSpace(s) != "" {
				label = s
			}
			if t, ok := m["type"].(string); ok {
				typ = t
			}
		}
		catalog[field] = OdooFieldMeta{
			Label: label,
			Type:  typ,
		}
	}
	return catalog
}

func mapPOSInvoicedOrder(row map[string]any) POSInvoicedOrder {
	localID, localName := extractMany2One(row["config_id"])
	sessionID, sessionName := extractMany2One(row["session_id"])
	customerID, customerName := extractMany2One(row["partner_id"])
	cashierID, cashierName := extractMany2One(row["user_id"])
	if cashierID == 0 && strings.TrimSpace(cashierName) == "" {
		cashierID, cashierName = extractMany2One(row["employee_id"])
	}

	invoiceID, invoiceName := extractInvoiceReference(row)
	currencyID, currencyName := extractMany2One(row["currency_id"])

	dateOrderRaw := asString(row["date_order"])
	dateOrderISO := toRFC3339(dateOrderRaw)

	return POSInvoicedOrder{
		ID:                  asInt64(row["id"]),
		Name:                asString(row["name"]),
		POSReference:        asString(row["pos_reference"]),
		State:               asString(row["state"]),
		DateOrderRaw:        dateOrderRaw,
		DateOrderISO:        dateOrderISO,
		LocalID:             localID,
		LocalName:           localName,
		SessionID:           sessionID,
		SessionName:         sessionName,
		InvoiceID:           invoiceID,
		InvoiceName:         invoiceName,
		CustomerID:          customerID,
		CustomerName:        customerName,
		CashierID:           cashierID,
		CashierName:         cashierName,
		AmountTotal:         asFloat(row["amount_total"]),
		AmountTax:           asFloat(row["amount_tax"]),
		AmountPaid:          asFloat(row["amount_paid"]),
		AmountReturn:        asFloat(row["amount_return"]),
		Margin:              asFloat(row["margin"]),
		Note:                asString(row["note"]),
		CurrencyID:          currencyID,
		CurrencyName:        currencyName,
		IsRefunded:          asBool(row["is_refunded"]),
		HasRefundableLines:  asBool(row["has_refundable_lines"]),
		RefundedOrdersCount: int(asInt64(row["refunded_orders_count"])),
		LineIDs:             asInt64Slice(row["lines"]),
		PaymentIDs:          asInt64Slice(row["payment_ids"]),
		LinesDetail:         []POSOrderLine{},
		PaymentsDetail:      []POSOrderPayment{},
		Raw:                 row,
	}
}

func mapPOSOrderLine(row map[string]any) POSOrderLine {
	orderID, orderName := extractMany2One(row["order_id"])
	productID, productName := extractMany2One(row["product_id"])

	name := strings.TrimSpace(asString(row["full_product_name"]))
	if name == "" {
		name = strings.TrimSpace(asString(row["display_name"]))
	}
	if name == "" {
		name = strings.TrimSpace(asString(row["name"]))
	}
	if name == "" {
		name = strings.TrimSpace(productName)
	}

	subtotal := asFloat(row["price_subtotal"])
	subtotalIncl := asFloat(row["price_subtotal_incl"])
	if subtotalIncl == 0 && subtotal > 0 {
		subtotalIncl = subtotal
	}
	taxAmount := subtotalIncl - subtotal
	if taxAmount < 0 && taxAmount > -0.000001 {
		taxAmount = 0
	}

	return POSOrderLine{
		ID:           asInt64(row["id"]),
		OrderID:      orderID,
		OrderName:    orderName,
		ProductID:    productID,
		ProductName:  name,
		Qty:          asFloat(row["qty"]),
		PriceUnit:    asFloat(row["price_unit"]),
		Discount:     asFloat(row["discount"]),
		Subtotal:     subtotal,
		SubtotalIncl: subtotalIncl,
		TaxAmount:    taxAmount,
	}
}

func mapPOSOrderPayment(row map[string]any) POSOrderPayment {
	orderID, orderName := extractMany2One(row["pos_order_id"])
	if orderID == 0 && strings.TrimSpace(orderName) == "" {
		orderID, orderName = extractMany2One(row["order_id"])
	}
	methodID, methodName := extractMany2One(row["payment_method_id"])

	paymentDateRaw := asString(row["payment_date"])
	if strings.TrimSpace(paymentDateRaw) == "" {
		paymentDateRaw = asString(row["create_date"])
	}

	return POSOrderPayment{
		ID:              asInt64(row["id"]),
		OrderID:         orderID,
		OrderName:       orderName,
		PaymentMethodID: methodID,
		PaymentMethod:   methodName,
		Amount:          asFloat(row["amount"]),
		IsChange:        asBool(row["is_change"]),
		PaymentDateRaw:  paymentDateRaw,
		PaymentDateISO:  toRFC3339(paymentDateRaw),
	}
}

func extractInvoiceReference(row map[string]any) (int64, string) {
	candidates := []string{"account_move", "account_move_id", "invoice_id"}
	for _, field := range candidates {
		id, name := extractMany2One(row[field])
		if id > 0 || strings.TrimSpace(name) != "" {
			return id, name
		}
	}
	return 0, ""
}

func extractMany2One(v any) (int64, string) {
	arr, ok := v.([]any)
	if !ok || len(arr) == 0 {
		return 0, ""
	}

	var id int64
	var name string
	if len(arr) > 0 {
		id = asInt64(arr[0])
	}
	if len(arr) > 1 {
		name = asString(arr[1])
	}
	return id, name
}

func asString(v any) string {
	switch n := v.(type) {
	case string:
		return n
	case []byte:
		return string(n)
	default:
		if v == nil {
			return ""
		}
		return fmt.Sprintf("%v", v)
	}
}

func asInt64(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case float32:
		return int64(n)
	case int:
		return int64(n)
	case int32:
		return int64(n)
	case int64:
		return n
	case json.Number:
		i, err := n.Int64()
		if err == nil {
			return i
		}
		f, ferr := n.Float64()
		if ferr == nil {
			return int64(f)
		}
		return 0
	case string:
		safe := strings.TrimSpace(n)
		if safe == "" {
			return 0
		}
		if i, err := strconv.ParseInt(safe, 10, 64); err == nil {
			return i
		}
		if f, err := strconv.ParseFloat(safe, 64); err == nil {
			return int64(f)
		}
		return 0
	default:
		return 0
	}
}

func asFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int32:
		return float64(n)
	case int64:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	case string:
		safe := strings.TrimSpace(n)
		if safe == "" {
			return 0
		}
		f, err := strconv.ParseFloat(safe, 64)
		if err != nil {
			return 0
		}
		return f
	default:
		return 0
	}
}

func asInt64Slice(v any) []int64 {
	switch arr := v.(type) {
	case []any:
		out := make([]int64, 0, len(arr))
		for _, item := range arr {
			id := asInt64(item)
			if id > 0 {
				out = append(out, id)
			}
		}
		return out
	case []int64:
		out := make([]int64, 0, len(arr))
		for _, id := range arr {
			if id > 0 {
				out = append(out, id)
			}
		}
		return out
	case []int:
		out := make([]int64, 0, len(arr))
		for _, id := range arr {
			if id > 0 {
				out = append(out, int64(id))
			}
		}
		return out
	case []float64:
		out := make([]int64, 0, len(arr))
		for _, id := range arr {
			if id > 0 {
				out = append(out, int64(id))
			}
		}
		return out
	default:
		return []int64{}
	}
}

func asBool(v any) bool {
	switch b := v.(type) {
	case bool:
		return b
	case string:
		safe := strings.TrimSpace(strings.ToLower(b))
		return safe == "true" || safe == "1" || safe == "yes"
	case float64:
		return b != 0
	case float32:
		return b != 0
	case int:
		return b != 0
	case int64:
		return b != 0
	default:
		return false
	}
}

func toAnyInt64Slice(ids []int64) []any {
	out := make([]any, 0, len(ids))
	for _, id := range ids {
		if id > 0 {
			out = append(out, id)
		}
	}
	return out
}

func sortedIDsFromSet(set map[int64]struct{}) []int64 {
	ids := make([]int64, 0, len(set))
	for id := range set {
		if id > 0 {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func toRFC3339(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	layouts := []string{
		"2006-01-02 15:04:05",
		time.RFC3339,
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC().Format(time.RFC3339)
		}
	}
	return ""
}
