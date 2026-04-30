package odoo

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

type POSSessionHoursQuery struct {
	From  time.Time
	To    time.Time
	Local string
	Limit int
}

type POSSessionHoursItem struct {
	SessionID       int64  `json:"session_id"`
	SessionName     string `json:"session_name"`
	SessionState    string `json:"session_state"`
	StartAtISO      string `json:"start_at_iso"`
	StopAtISO       string `json:"stop_at_iso"`
	DurationMinutes int64  `json:"duration_minutes"`
}

type POSSessionHoursCard struct {
	LocalID         int64                 `json:"local_id"`
	LocalName       string                `json:"local_name"`
	SessionsCount   int                   `json:"sessions_count"`
	OpenSessions    int                   `json:"open_sessions"`
	DaysWithSession int                   `json:"days_with_session"`
	FirstStartISO   string                `json:"first_start_iso"`
	LastStartISO    string                `json:"last_start_iso"`
	LastStopISO     string                `json:"last_stop_iso"`
	Sessions        []POSSessionHoursItem `json:"sessions"`
}

type POSSessionHoursTotals struct {
	TotalPOS         int `json:"total_pos"`
	TotalSessions    int `json:"total_sessions"`
	OpenSessions     int `json:"open_sessions"`
	DaysWithSessions int `json:"days_with_sessions"`
}

type POSSessionHoursResult struct {
	Totals POSSessionHoursTotals `json:"totals"`
	Data   []POSSessionHoursCard `json:"data"`
}

type posSessionHoursAgg struct {
	LocalID   int64
	LocalName string
	StartAt   time.Time
	LastStart time.Time
	StopAt    time.Time
	Sessions  []POSSessionHoursItem
	OpenDays  map[string]struct{}
}

// GetPOSSessionsHoursOverview devuelve las sesiones POS agrupadas por punto de venta,
// usando directamente pos.session para mostrar horarios reales de apertura y cierre.
func (c *Client) GetPOSSessionsHoursOverview(q POSSessionHoursQuery) (POSSessionHoursResult, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 5000
	}
	if limit > 20000 {
		limit = 20000
	}

	configs, err := c.ListPOSConfigs()
	if err != nil {
		return POSSessionHoursResult{}, fmt.Errorf("listar POS configs: %w", err)
	}

	localFilter := strings.ToLower(strings.TrimSpace(q.Local))
	cardsByID := make(map[int64]*posSessionHoursAgg)
	configIDs := make([]any, 0, len(configs))

	for _, cfg := range configs {
		localID := asInt64(cfg["id"])
		localName := strings.TrimSpace(asString(cfg["name"]))
		if localID <= 0 || localName == "" {
			continue
		}
		if localFilter != "" && !strings.Contains(strings.ToLower(localName), localFilter) {
			continue
		}

		cardsByID[localID] = &posSessionHoursAgg{
			LocalID:   localID,
			LocalName: localName,
			OpenDays:  make(map[string]struct{}),
		}
		configIDs = append(configIDs, localID)
	}

	if len(configIDs) == 0 {
		return POSSessionHoursResult{
			Totals: POSSessionHoursTotals{},
			Data:   []POSSessionHoursCard{},
		}, nil
	}

	layout := "2006-01-02 15:04:05"
	domain := make([]any, 0, 4)
	domain = append(domain, []any{"config_id", "in", configIDs})
	if !q.From.IsZero() {
		domain = append(domain, []any{"start_at", ">=", q.From.UTC().Format(layout)})
	}
	if !q.To.IsZero() {
		domain = append(domain, []any{"start_at", "<=", q.To.UTC().Format(layout)})
	}

	rawRows, err := c.callOdoo("pos.session", "search_read", []any{}, map[string]any{
		"domain": domain,
		"fields": []string{"id", "name", "state", "start_at", "stop_at", "config_id"},
		"order":  "start_at desc, id desc",
		"limit":  limit,
	})
	if err != nil {
		return POSSessionHoursResult{}, fmt.Errorf("consultar sesiones POS: %w", err)
	}

	var rows []map[string]any
	if err := json.Unmarshal(rawRows, &rows); err != nil {
		return POSSessionHoursResult{}, fmt.Errorf("decode pos.session search_read: %w", err)
	}

	totals := POSSessionHoursTotals{}

	for _, row := range rows {
		localID, localName := extractMany2One(row["config_id"])
		card := cardsByID[localID]
		if card == nil {
			trimmedName := strings.TrimSpace(localName)
			if trimmedName == "" {
				trimmedName = "Sin local"
			}
			card = &posSessionHoursAgg{
				LocalID:   localID,
				LocalName: trimmedName,
				OpenDays:  make(map[string]struct{}),
			}
			cardsByID[localID] = card
		}

		startAt := parseOdooDate(asString(row["start_at"]))
		stopAt := parseOdooDate(asString(row["stop_at"]))
		sessionState := normalizeSessionState(asString(row["state"]))

		if !startAt.IsZero() && (card.StartAt.IsZero() || startAt.Before(card.StartAt)) {
			card.StartAt = startAt
		}
		if !startAt.IsZero() && (card.LastStart.IsZero() || startAt.After(card.LastStart)) {
			card.LastStart = startAt
		}
		if !stopAt.IsZero() && (card.StopAt.IsZero() || stopAt.After(card.StopAt)) {
			card.StopAt = stopAt
		}

		dayKey := ""
		if !startAt.IsZero() {
			dayKey = startAt.Format("2006-01-02")
			card.OpenDays[dayKey] = struct{}{}
		}

		durationMinutes := int64(0)
		if !startAt.IsZero() && !stopAt.IsZero() && !stopAt.Before(startAt) {
			durationMinutes = int64(stopAt.Sub(startAt).Minutes())
		}

		card.Sessions = append(card.Sessions, POSSessionHoursItem{
			SessionID:       asInt64(row["id"]),
			SessionName:     strings.TrimSpace(asString(row["name"])),
			SessionState:    sessionState,
			StartAtISO:      timeToISO(startAt),
			StopAtISO:       timeToISO(stopAt),
			DurationMinutes: durationMinutes,
		})

		totals.TotalSessions++
		if sessionState == "abierta" || sessionState == "abriendo" {
			totals.OpenSessions++
		}
	}

	cards := make([]POSSessionHoursCard, 0, len(cardsByID))
	for _, agg := range cardsByID {
		sort.Slice(agg.Sessions, func(i, j int) bool {
			ti := parseISO(agg.Sessions[i].StartAtISO)
			tj := parseISO(agg.Sessions[j].StartAtISO)
			if !ti.Equal(tj) {
				return tj.Before(ti)
			}
			return agg.Sessions[i].SessionID > agg.Sessions[j].SessionID
		})

		openSessions := 0
		for _, session := range agg.Sessions {
			if session.SessionState == "abierta" || session.SessionState == "abriendo" {
				openSessions++
			}
		}

		cards = append(cards, POSSessionHoursCard{
			LocalID:         agg.LocalID,
			LocalName:       agg.LocalName,
			SessionsCount:   len(agg.Sessions),
			OpenSessions:    openSessions,
			DaysWithSession: len(agg.OpenDays),
			FirstStartISO:   timeToISO(agg.StartAt),
			LastStartISO:    timeToISO(agg.LastStart),
			LastStopISO:     timeToISO(agg.StopAt),
			Sessions:        agg.Sessions,
		})
		totals.DaysWithSessions += len(agg.OpenDays)
	}

	sort.Slice(cards, func(i, j int) bool {
		return strings.ToLower(cards[i].LocalName) < strings.ToLower(cards[j].LocalName)
	})
	totals.TotalPOS = len(cards)

	return POSSessionHoursResult{
		Totals: totals,
		Data:   cards,
	}, nil
}
