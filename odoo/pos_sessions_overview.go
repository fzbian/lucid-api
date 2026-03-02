package odoo

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

type POSSessionOverviewQuery struct {
	From  time.Time
	To    time.Time
	Local string
	Limit int
}

type POSSessionOverviewItem struct {
	SessionID     int64   `json:"session_id"`
	SessionName   string  `json:"session_name"`
	SessionState  string  `json:"session_state"`
	StartAtISO    string  `json:"start_at_iso"`
	StopAtISO     string  `json:"stop_at_iso"`
	FirstOrderISO string  `json:"first_order_iso"`
	LastOrderISO  string  `json:"last_order_iso"`
	OrdersCount   int     `json:"orders_count"`
	AmountTotal   float64 `json:"amount_total"`
	AmountTax     float64 `json:"amount_tax"`
	TicketProm    float64 `json:"ticket_promedio"`
}

type POSCardOverview struct {
	LocalID       int64                    `json:"local_id"`
	LocalName     string                   `json:"local_name"`
	SessionsCount int                      `json:"sessions_count"`
	OpenSessions  int                      `json:"open_sessions"`
	OrdersCount   int                      `json:"orders_count"`
	AmountTotal   float64                  `json:"amount_total"`
	AmountTax     float64                  `json:"amount_tax"`
	LastOrderISO  string                   `json:"last_order_iso"`
	Sessions      []POSSessionOverviewItem `json:"sessions"`
}

type POSOverviewTotals struct {
	TotalPOS      int     `json:"total_pos"`
	TotalSessions int     `json:"total_sessions"`
	TotalOrders   int     `json:"total_orders"`
	AmountTotal   float64 `json:"amount_total"`
	AmountTax     float64 `json:"amount_tax"`
}

type POSSessionsOverviewResult struct {
	Totals POSOverviewTotals `json:"totals"`
	Data   []POSCardOverview `json:"data"`
}

type posSessionAgg struct {
	LocalID     int64
	LocalName   string
	SessionID   int64
	SessionName string
	OrdersCount int
	AmountTotal float64
	AmountTax   float64
	FirstOrder  time.Time
	LastOrder   time.Time
}

type posCardAgg struct {
	LocalID     int64
	LocalName   string
	OrdersCount int
	AmountTotal float64
	AmountTax   float64
	LastOrder   time.Time
	Sessions    map[int64]*posSessionAgg
}

// GetPOSSessionsOverview devuelve resumen por POS y sus sesiones para minimizar payload y peticiones frontend.
func (c *Client) GetPOSSessionsOverview(q POSSessionOverviewQuery) (POSSessionsOverviewResult, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 12000
	}
	if limit > 30000 {
		limit = 30000
	}

	layout := "2006-01-02 15:04:05"
	domain := make([]any, 0, 6)
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

	orderFields := []string{"id", "config_id", "session_id", "amount_total", "amount_tax", "date_order"}
	rawRows, err := c.callOdoo("pos.order", "search_read", []any{}, map[string]any{
		"domain": domain,
		"fields": orderFields,
		"order":  "date_order desc, id desc",
		"limit":  limit,
	})
	if err != nil {
		return POSSessionsOverviewResult{}, err
	}

	var rows []map[string]any
	if err := json.Unmarshal(rawRows, &rows); err != nil {
		return POSSessionsOverviewResult{}, fmt.Errorf("decode pos.order search_read: %w", err)
	}

	posMap := make(map[string]*posCardAgg)
	sessionIDsSet := make(map[int64]struct{})
	totals := POSOverviewTotals{}

	for _, row := range rows {
		localID, localName := extractMany2One(row["config_id"])
		if strings.TrimSpace(localName) == "" {
			localName = "Sin local"
		}
		sessionID, sessionName := extractMany2One(row["session_id"])

		localKey := fmt.Sprintf("%d|%s", localID, localName)
		card := posMap[localKey]
		if card == nil {
			card = &posCardAgg{
				LocalID:   localID,
				LocalName: localName,
				Sessions:  make(map[int64]*posSessionAgg),
			}
			posMap[localKey] = card
		}

		amountTotal := asFloat(row["amount_total"])
		amountTax := asFloat(row["amount_tax"])
		orderAt := parseOdooDate(asString(row["date_order"]))

		card.OrdersCount++
		card.AmountTotal += amountTotal
		card.AmountTax += amountTax
		if !orderAt.IsZero() && (card.LastOrder.IsZero() || orderAt.After(card.LastOrder)) {
			card.LastOrder = orderAt
		}

		sAgg := card.Sessions[sessionID]
		if sAgg == nil {
			sAgg = &posSessionAgg{
				LocalID:     localID,
				LocalName:   localName,
				SessionID:   sessionID,
				SessionName: sessionName,
			}
			card.Sessions[sessionID] = sAgg
		}

		sAgg.OrdersCount++
		sAgg.AmountTotal += amountTotal
		sAgg.AmountTax += amountTax
		if !orderAt.IsZero() {
			if sAgg.FirstOrder.IsZero() || orderAt.Before(sAgg.FirstOrder) {
				sAgg.FirstOrder = orderAt
			}
			if sAgg.LastOrder.IsZero() || orderAt.After(sAgg.LastOrder) {
				sAgg.LastOrder = orderAt
			}
		}

		if sessionID > 0 {
			sessionIDsSet[sessionID] = struct{}{}
		}

		totals.TotalOrders++
		totals.AmountTotal += amountTotal
		totals.AmountTax += amountTax
	}

	sessionMeta := map[int64]map[string]any{}
	if len(sessionIDsSet) > 0 {
		sessionIDs := make([]any, 0, len(sessionIDsSet))
		for id := range sessionIDsSet {
			sessionIDs = append(sessionIDs, id)
		}

		rawSessions, err := c.callOdoo("pos.session", "search_read", []any{}, map[string]any{
			"domain": []any{[]any{"id", "in", sessionIDs}},
			"fields": []string{"id", "name", "state", "start_at", "stop_at"},
			"limit":  len(sessionIDsSet) + 20,
		})
		if err == nil {
			var sessions []map[string]any
			if decErr := json.Unmarshal(rawSessions, &sessions); decErr == nil {
				for _, s := range sessions {
					id := asInt64(s["id"])
					if id > 0 {
						sessionMeta[id] = s
					}
				}
			}
		}
	}

	cards := make([]POSCardOverview, 0, len(posMap))
	for _, cardAgg := range posMap {
		sessions := make([]POSSessionOverviewItem, 0, len(cardAgg.Sessions))
		openCount := 0

		for _, sAgg := range cardAgg.Sessions {
			meta := sessionMeta[sAgg.SessionID]
			state := normalizeSessionState(asString(meta["state"]))
			if state == "abierta" || state == "abriendo" {
				openCount++
			}

			sessionName := strings.TrimSpace(sAgg.SessionName)
			if sessionName == "" {
				sessionName = strings.TrimSpace(asString(meta["name"]))
			}

			startAt := parseOdooDate(asString(meta["start_at"]))
			stopAt := parseOdooDate(asString(meta["stop_at"]))
			ticketProm := 0.0
			if sAgg.OrdersCount > 0 {
				ticketProm = sAgg.AmountTotal / float64(sAgg.OrdersCount)
			}

			sessions = append(sessions, POSSessionOverviewItem{
				SessionID:     sAgg.SessionID,
				SessionName:   sessionName,
				SessionState:  state,
				StartAtISO:    timeToISO(startAt),
				StopAtISO:     timeToISO(stopAt),
				FirstOrderISO: timeToISO(sAgg.FirstOrder),
				LastOrderISO:  timeToISO(sAgg.LastOrder),
				OrdersCount:   sAgg.OrdersCount,
				AmountTotal:   sAgg.AmountTotal,
				AmountTax:     sAgg.AmountTax,
				TicketProm:    ticketProm,
			})
		}

		sort.Slice(sessions, func(i, j int) bool {
			ti := parseISO(sessions[i].LastOrderISO)
			tj := parseISO(sessions[j].LastOrderISO)
			if !ti.Equal(tj) {
				return tj.Before(ti)
			}
			return sessions[i].SessionID > sessions[j].SessionID
		})

		cards = append(cards, POSCardOverview{
			LocalID:       cardAgg.LocalID,
			LocalName:     cardAgg.LocalName,
			SessionsCount: len(sessions),
			OpenSessions:  openCount,
			OrdersCount:   cardAgg.OrdersCount,
			AmountTotal:   cardAgg.AmountTotal,
			AmountTax:     cardAgg.AmountTax,
			LastOrderISO:  timeToISO(cardAgg.LastOrder),
			Sessions:      sessions,
		})
		totals.TotalSessions += len(sessions)
	}

	sort.Slice(cards, func(i, j int) bool {
		return cards[i].LocalName < cards[j].LocalName
	})
	totals.TotalPOS = len(cards)

	return POSSessionsOverviewResult{
		Totals: totals,
		Data:   cards,
	}, nil
}

func parseOdooDate(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	layouts := []string{
		"2006-01-02 15:04:05",
		time.RFC3339,
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func timeToISO(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func parseISO(raw string) time.Time {
	if strings.TrimSpace(raw) == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}
	}
	return t
}

func normalizeSessionState(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "opened":
		return "abierta"
	case "opening_control":
		return "abriendo"
	case "closed":
		return "cerrada"
	default:
		if strings.TrimSpace(raw) == "" {
			return "sin_estado"
		}
		return strings.ToLower(strings.TrimSpace(raw))
	}
}
