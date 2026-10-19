package odoo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func closingTestClient(t *testing.T, reply func(string, map[string]any) (any, bool)) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Params struct {
				Model  string         `json:"model"`
				Method string         `json:"method"`
				Kwargs map[string]any `json:"kwargs"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if request.Params.Method != "search_read" {
			t.Errorf("report attempted mutation: %s", request.Params.Method)
		}
		result, fail := reply(request.Params.Model, request.Params.Kwargs)
		w.Header().Set("Content-Type", "application/json")
		if fail {
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": 1, "message": "read failed"}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": result})
	}))
	t.Cleanup(server.Close)
	return &Client{baseURL: server.URL, hc: server.Client()}
}

func TestClosingsReadCompleteSessionByColombianClosingDay(t *testing.T) {
	calls := map[string]int{}
	client := closingTestClient(t, func(model string, kwargs map[string]any) (any, bool) {
		calls[model]++
		switch model {
		case "pos.session":
			expected := []any{[]any{"state", "=", "closed"}, []any{"stop_at", ">=", "2026-10-19 05:00:00"}, []any{"stop_at", "<", "2026-10-20 05:00:00"}}
			if !reflect.DeepEqual(kwargs["domain"], expected) {
				t.Errorf("wrong day boundaries: %v", kwargs["domain"])
			}
			return []map[string]any{{"id": 1, "name": "POS/0001", "config_id": []any{10, "Centro"}, "user_id": []any{7, "Ana"}, "start_at": "2026-10-18 14:00:00", "stop_at": "2026-10-20 04:59:59", "total_payments_amount": 80, "cash_register_balance_start": 50, "cash_register_balance_end": 130, "cash_register_balance_end_real": 125, "cash_register_difference": -5}}, false
		case "pos.order":
			return []map[string]any{{"id": 20, "session_id": []any{1, "POS/0001"}, "name": "Venta 1", "amount_total": 100, "date_order": "2026-10-19 12:00:00"}, {"id": 21, "session_id": []any{1, "POS/0001"}, "name": "Devolución", "amount_total": -20, "date_order": "2026-10-19 15:00:00"}}, false
		case "pos.order.line":
			return []map[string]any{{"id": 30, "order_id": []any{20, "Venta 1"}, "product_id": []any{40, "Café"}, "qty": 5, "price_unit": 20, "price_subtotal": 100}, {"id": 31, "order_id": []any{21, "Devolución"}, "product_id": []any{40, "Café"}, "qty": -1, "price_unit": 20, "price_subtotal": -20}}, false
		case "pos.payment":
			return []map[string]any{{"id": 50, "pos_order_id": []any{20, "Venta 1"}, "payment_method_id": []any{5, "Efectivo"}, "amount": 150}, {"id": 51, "pos_order_id": []any{20, "Venta 1"}, "payment_method_id": []any{5, "Efectivo"}, "amount": -50}, {"id": 52, "pos_order_id": []any{21, "Devolución"}, "payment_method_id": []any{5, "Efectivo"}, "amount": -20}}, false
		case "account.bank.statement.line":
			return []map[string]any{{"id": 60, "pos_session_id": []any{1, "POS/0001"}, "payment_ref": "POS/0001 - Retiro", "amount": -10}, {"id": 61, "pos_session_id": []any{1, "POS/0001"}, "payment_ref": "Pago automático", "amount": 100}}, false
		default:
			t.Errorf("unexpected model %s", model)
			return []any{}, false
		}
	})
	day, _ := time.Parse("2006-01-02", "2026-10-19")
	rows, err := client.GetClosings(day)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d closings", len(rows))
	}
	c := rows[0]
	if c.Total != 80 || c.Difference != -5 || c.OpeningDifference != 0 || len(c.Orders) != 2 {
		t.Fatalf("incorrect summary: %+v", c)
	}
	if c.Orders[0].Payments[0].Amount != 100 || c.Orders[0].Payments[0].Paid != 150 || c.Orders[0].Payments[0].Change != 50 {
		t.Errorf("cash change counted as sales: %+v", c.Orders[0].Payments)
	}
	if !c.Orders[1].Refund || c.Orders[1].Lines[0].Quantity != -1 {
		t.Error("refund lost")
	}
	if len(c.Movements) != 1 || c.Movements[0].Description != "Retiro" {
		t.Errorf("manual movements: %+v", c.Movements)
	}
	if c.Stop != "2026-10-20T04:59:59Z" {
		t.Errorf("wrong stop date %s", c.Stop)
	}
	for model, n := range calls {
		if n != 1 {
			t.Errorf("N+1 request to %s: %d", model, n)
		}
	}
}

func TestClosingRowsReadAllPagesAndRejectPartialData(t *testing.T) {
	for _, failSecond := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "failed page"}[failSecond], func(t *testing.T) {
			client := closingTestClient(t, func(model string, kwargs map[string]any) (any, bool) {
				if kwargs["offset"].(float64) == 500 {
					return []map[string]any{{"id": 501}}, failSecond
				}
				rows := make([]map[string]any, 500)
				for i := range rows {
					rows[i] = map[string]any{"id": i + 1}
				}
				return rows, false
			})
			rows, err := client.closingRows("pos.order", []any{}, []string{"id"})
			if failSecond {
				if err == nil || rows != nil {
					t.Fatal("partial report returned as complete")
				}
			} else if err != nil || len(rows) != 501 {
				t.Fatalf("truncated result: %d, %v", len(rows), err)
			}
		})
	}
}

func TestClosingsEmptyDayDoesNotReadOrders(t *testing.T) {
	client := closingTestClient(t, func(model string, kwargs map[string]any) (any, bool) {
		if model != "pos.session" {
			t.Errorf("unnecessary query %s", model)
		}
		return []any{}, false
	})
	rows, err := client.GetClosings(time.Now())
	if err != nil || rows == nil || len(rows) != 0 {
		t.Fatalf("empty day: %v %v", rows, err)
	}
}
