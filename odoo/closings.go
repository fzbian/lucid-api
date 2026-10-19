package odoo

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Closing reports deliberately use only search_read; opening/closing POS sessions
// and sending notifications remain outside this read-only module.
var ClosingLocation = time.FixedZone("America/Bogota", -5*60*60)

type ClosingPayment struct {
	Method string  `json:"method"`
	Amount float64 `json:"amount"`
	Paid   float64 `json:"paid"`
	Change float64 `json:"change"`
}
type ClosingLine struct {
	ProductID int64   `json:"product_id"`
	Product   string  `json:"product"`
	Quantity  float64 `json:"quantity"`
	Price     float64 `json:"price"`
	Subtotal  float64 `json:"subtotal"`
}
type ClosingOrder struct {
	ID       int64            `json:"id"`
	Name     string           `json:"name"`
	Date     string           `json:"date"`
	Amount   float64          `json:"amount"`
	Refund   bool             `json:"refund"`
	Lines    []ClosingLine    `json:"lines"`
	Payments []ClosingPayment `json:"payments"`
}
type ClosingMovement struct {
	Description string  `json:"description"`
	Amount      float64 `json:"amount"`
}
type Closing struct {
	ID                int64             `json:"id"`
	Name              string            `json:"name"`
	LocalID           int64             `json:"local_id"`
	Local             string            `json:"local"`
	User              string            `json:"user"`
	Start             string            `json:"start"`
	Stop              string            `json:"stop"`
	Opening           float64           `json:"opening"`
	OpeningDifference float64           `json:"opening_difference"`
	Expected          float64           `json:"expected"`
	Counted           float64           `json:"counted"`
	Difference        float64           `json:"difference"`
	Total             float64           `json:"total"`
	Orders            []ClosingOrder    `json:"orders"`
	Movements         []ClosingMovement `json:"movements"`
}

// Read every page so large days never silently display partial financial totals.
func (c *Client) closingRows(model string, domain []any, fields []string) ([]map[string]any, error) {
	rows := make([]map[string]any, 0)
	for offset := 0; ; offset += 500 {
		kwargs := map[string]any{"domain": domain, "order": "id asc", "offset": offset, "limit": 500}
		if len(fields) > 0 {
			kwargs["fields"] = fields
		}
		raw, err := c.callOdoo(model, "search_read", []any{}, kwargs)
		if err != nil {
			return nil, fmt.Errorf("consultar %s: %w", model, err)
		}
		var page []map[string]any
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, fmt.Errorf("decodificar %s: %w", model, err)
		}
		rows = append(rows, page...)
		if len(page) < 500 {
			return rows, nil
		}
	}
}

func (c *Client) GetClosings(day time.Time) ([]Closing, error) {
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, ClosingLocation)
	end := start.AddDate(0, 0, 1)
	// Read the same session fields as noti-api (including optional opening difference).
	sessions, err := c.closingRows("pos.session", []any{
		[]any{"state", "=", "closed"}, []any{"stop_at", ">=", start.UTC().Format("2006-01-02 15:04:05")},
		[]any{"stop_at", "<", end.UTC().Format("2006-01-02 15:04:05")},
	}, nil)
	if err != nil {
		return nil, err
	}
	result := make([]Closing, 0, len(sessions))
	if len(sessions) == 0 {
		return result, nil
	}
	ids := make([]int64, 0, len(sessions))
	sessionIndex := make(map[int64]int)
	for _, row := range sessions {
		id := asInt64(row["id"])
		localID, local := extractMany2One(row["config_id"])
		_, user := extractMany2One(row["user_id"])
		sessionIndex[id] = len(result)
		ids = append(ids, id)
		result = append(result, Closing{ID: id, Name: asString(row["name"]), LocalID: localID, Local: local, User: user,
			Start: timeToISO(parseOdooDate(asString(row["start_at"]))), Stop: timeToISO(parseOdooDate(asString(row["stop_at"]))),
			Opening: asFloat(row["cash_register_balance_start"]), OpeningDifference: asFloat(row["cash_register_balance_start_difference"]),
			Expected: asFloat(row["cash_register_balance_end"]), Counted: asFloat(row["cash_register_balance_end_real"]),
			Difference: asFloat(row["cash_register_difference"]), Total: asFloat(row["total_payments_amount"]),
			Orders: []ClosingOrder{}, Movements: []ClosingMovement{},
		})
	}
	orders, err := c.closingRows("pos.order", []any{[]any{"session_id", "in", ids}, []any{"state", "in", []string{"paid", "done", "invoiced"}}}, []string{"name", "session_id", "date_order", "amount_total"})
	if err != nil {
		return nil, err
	}
	type orderPosition struct{ session, order int }
	positions := make(map[int64]orderPosition)
	orderIDs := make([]int64, 0, len(orders))
	for _, row := range orders {
		sessionID, _ := extractMany2One(row["session_id"])
		si, ok := sessionIndex[sessionID]
		if !ok {
			continue
		}
		id := asInt64(row["id"])
		amount := asFloat(row["amount_total"])
		positions[id] = orderPosition{si, len(result[si].Orders)}
		orderIDs = append(orderIDs, id)
		result[si].Orders = append(result[si].Orders, ClosingOrder{ID: id, Name: asString(row["name"]), Date: timeToISO(parseOdooDate(asString(row["date_order"]))), Amount: amount, Refund: amount < 0, Lines: []ClosingLine{}, Payments: []ClosingPayment{}})
	}
	if len(orderIDs) > 0 {
		lines, err := c.closingRows("pos.order.line", []any{[]any{"order_id", "in", orderIDs}}, []string{"order_id", "product_id", "qty", "price_unit", "price_subtotal"})
		if err != nil {
			return nil, err
		}
		for _, row := range lines {
			orderID, _ := extractMany2One(row["order_id"])
			p, ok := positions[orderID]
			if !ok {
				continue
			}
			productID, product := extractMany2One(row["product_id"])
			o := &result[p.session].Orders[p.order]
			qty := asFloat(row["qty"])
			o.Lines = append(o.Lines, ClosingLine{ProductID: productID, Product: product, Quantity: qty, Price: asFloat(row["price_unit"]), Subtotal: asFloat(row["price_subtotal"])})
		}
		payments, err := c.closingRows("pos.payment", []any{[]any{"pos_order_id", "in", orderIDs}}, []string{"pos_order_id", "payment_method_id", "amount"})
		if err != nil {
			return nil, err
		}
		for _, row := range payments {
			orderID, _ := extractMany2One(row["pos_order_id"])
			p, ok := positions[orderID]
			if !ok {
				continue
			}
			_, method := extractMany2One(row["payment_method_id"])
			amount := asFloat(row["amount"])
			o := &result[p.session].Orders[p.order]
			pi := -1
			for i := range o.Payments {
				if o.Payments[i].Method == method {
					pi = i
					break
				}
			}
			if pi < 0 {
				o.Payments = append(o.Payments, ClosingPayment{Method: method})
				pi = len(o.Payments) - 1
			}
			o.Payments[pi].Amount += amount
			if amount > 0 {
				o.Payments[pi].Paid += amount
			} else {
				o.Payments[pi].Change -= amount
			}
		}
	}
	movements, err := c.closingRows("account.bank.statement.line", []any{[]any{"pos_session_id", "in", ids}}, []string{"pos_session_id", "amount", "payment_ref"})
	if err != nil {
		return nil, err
	}
	for _, row := range movements {
		sessionID, _ := extractMany2One(row["pos_session_id"])
		si, ok := sessionIndex[sessionID]
		if !ok {
			continue
		}
		ref := asString(row["payment_ref"])
		// Same manual cash-movement selection as the original report; automatic
		// sales statement lines must not be counted again as cash income.
		if !strings.Contains(ref, "POS/") || !strings.Contains(ref, "-") {
			continue
		}
		parts := strings.Split(ref, "-")
		description := strings.TrimSpace(parts[len(parts)-1])
		if description == "" {
			description = "Sin descripción"
		}
		result[si].Movements = append(result[si].Movements, ClosingMovement{Description: description, Amount: asFloat(row["amount"])})
	}
	return result, nil
}
