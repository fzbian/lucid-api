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
	To    time.Time // Exclusive end of the requested period.
	Local string
	Limit int // Kept for older callers; results are always fully paginated.
}

type POSSessionHoursItem struct {
	SessionID             int64  `json:"session_id"`
	SessionName           string `json:"session_name"`
	SessionState          string `json:"session_state"`
	User                  string `json:"user"`
	StartAtISO            string `json:"start_at_iso"`
	StopAtISO             string `json:"stop_at_iso"`
	DurationMinutes       int64  `json:"duration_minutes"`
	Counted               bool   `json:"counted"`
	Issue                 string `json:"issue,omitempty"`
	DayMinutes            int64  `json:"day_minutes"`
	ContinuesFromPrevious bool   `json:"continues_from_previous"`
	ContinuesToNext       bool   `json:"continues_to_next"`
}

type POSHoursDay struct {
	Date            string                `json:"date"`
	WorkedMinutes   int64                 `json:"worked_minutes"`
	FirstStartISO   string                `json:"first_start_iso"`
	LastStopISO     string                `json:"last_stop_iso"`
	PendingSessions int                   `json:"pending_sessions"`
	HasOverlap      bool                  `json:"has_overlap"`
	HasBreaks       bool                  `json:"has_breaks"`
	Sessions        []POSSessionHoursItem `json:"sessions"`
}

type POSSessionHoursCard struct {
	LocalID         int64                 `json:"local_id"`
	LocalName       string                `json:"local_name"`
	Archived        bool                  `json:"archived"`
	SessionsCount   int                   `json:"sessions_count"`
	OpenSessions    int                   `json:"open_sessions"`
	DaysWithSession int                   `json:"days_with_session"`
	ConfirmedDays   int                   `json:"confirmed_days"`
	PendingDays     int                   `json:"pending_days"`
	WorkedMinutes   int64                 `json:"worked_minutes"`
	AverageMinutes  float64               `json:"average_minutes"`
	FirstStartISO   string                `json:"first_start_iso"`
	LastStartISO    string                `json:"last_start_iso"`
	LastStopISO     string                `json:"last_stop_iso"`
	Sessions        []POSSessionHoursItem `json:"sessions"`
	Days            []POSHoursDay         `json:"days"`
}

type POSSessionHoursTotals struct {
	TotalPOS         int   `json:"total_pos"`
	TotalSessions    int   `json:"total_sessions"`
	OpenSessions     int   `json:"open_sessions"`
	DaysWithSessions int   `json:"days_with_sessions"`
	WorkedMinutes    int64 `json:"worked_minutes"`
	PendingDays      int   `json:"pending_days"`
}

type POSSessionHoursResult struct {
	Totals      POSSessionHoursTotals `json:"totals"`
	Data        []POSSessionHoursCard `json:"data"`
	GeneratedAt string                `json:"generated_at"`
}

type hoursInterval struct{ start, end time.Time }

// Read the current catalog directly, including archived configurations that may
// still have historical sessions. Do not inherit billing visibility or caches.
func (c *Client) hoursRows(model string, domain []any, fields []string) ([]map[string]any, error) {
	rows := make([]map[string]any, 0)
	for offset := 0; ; offset += 500 {
		raw, err := c.callOdoo(model, "search_read", []any{}, map[string]any{
			"domain": domain, "fields": fields, "context": map[string]any{"active_test": false},
			"order": "id asc", "offset": offset, "limit": 500,
		})
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

func (c *Client) GetPOSSessionsHoursOverview(q POSSessionHoursQuery) (POSSessionHoursResult, error) {
	now := time.Now()
	if q.From.IsZero() || q.To.IsZero() || !q.To.After(q.From) || q.To.Sub(q.From) > 366*24*time.Hour {
		return POSSessionHoursResult{}, fmt.Errorf("seleccione un período de hasta 366 días")
	}
	configs, err := c.hoursRows("pos.config", []any{}, []string{"name", "active"})
	if err != nil {
		return POSSessionHoursResult{}, err
	}
	layout := "2006-01-02 15:04:05"
	// Include sessions crossing into the period and missing/invalid closures.
	domain := []any{
		[]any{"start_at", "!=", false}, []any{"start_at", "<", q.To.UTC().Format(layout)},
		"|", "|", []any{"stop_at", "=", false}, []any{"stop_at", ">", q.From.UTC().Format(layout)},
		[]any{"start_at", ">=", q.From.UTC().Format(layout)},
	}
	rows, err := c.hoursRows("pos.session", domain, []string{"name", "state", "start_at", "stop_at", "config_id", "user_id"})
	if err != nil {
		return POSSessionHoursResult{}, err
	}
	return buildPOSHours(q, configs, rows, now), nil
}

func unionHours(intervals []hoursInterval) (time.Duration, bool) {
	if len(intervals) == 0 {
		return 0, false
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].start.Before(intervals[j].start) })
	start, end := intervals[0].start, intervals[0].end
	var total time.Duration
	overlap := false
	for _, item := range intervals[1:] {
		if !item.start.After(end) {
			if item.start.Before(end) {
				overlap = true
			}
			if item.end.After(end) {
				end = item.end
			}
		} else {
			total += end.Sub(start)
			start, end = item.start, item.end
		}
	}
	return total + end.Sub(start), overlap
}

func buildPOSHours(q POSSessionHoursQuery, configs, rows []map[string]any, now time.Time) POSSessionHoursResult {
	end := q.To
	if end.After(now) {
		end = now
	}
	cards := make(map[int64]*POSSessionHoursCard)
	for _, cfg := range configs {
		id := asInt64(cfg["id"])
		if id <= 0 {
			continue
		}
		active, ok := cfg["active"].(bool)
		cards[id] = &POSSessionHoursCard{LocalID: id, LocalName: asString(cfg["name"]), Archived: ok && !active, Sessions: []POSSessionHoursItem{}, Days: []POSHoursDay{}}
	}
	for _, row := range rows {
		start := parseOdooDate(asString(row["start_at"]))
		stop := parseOdooDate(asString(row["stop_at"]))
		if start.IsZero() || !start.Before(end) {
			continue
		}
		state := asString(row["state"])
		counted := state == "closed" && !stop.IsZero() && !stop.Before(start) && !stop.After(now)
		issue := ""
		if !counted {
			issue = "missing_close"
			if !stop.IsZero() {
				issue = "invalid_dates"
			}
		}
		// Do not include a completed session ending exactly at the range boundary.
		if counted && !stop.After(q.From) && start.Before(q.From) {
			continue
		}
		if !counted && !stop.IsZero() && stop.Before(q.From) && start.Before(q.From) {
			continue
		}
		id, name := extractMany2One(row["config_id"])
		if id <= 0 {
			continue
		}
		card := cards[id]
		if card == nil {
			card = &POSSessionHoursCard{LocalID: id, LocalName: name, Sessions: []POSSessionHoursItem{}, Days: []POSHoursDay{}}
			cards[id] = card
		}
		_, user := extractMany2One(row["user_id"])
		duration := int64(0)
		if counted {
			duration = int64(stop.Sub(start) / time.Minute)
		}
		item := POSSessionHoursItem{SessionID: asInt64(row["id"]), SessionName: asString(row["name"]),
			SessionState: normalizeSessionState(state), User: user, StartAtISO: timeToISO(start), StopAtISO: timeToISO(stop),
			DurationMinutes: duration, Counted: counted, Issue: issue}
		card.Sessions = append(card.Sessions, item)
	}
	result := POSSessionHoursResult{Data: []POSSessionHoursCard{}, GeneratedAt: timeToISO(now)}
	for _, card := range cards {
		if card.Archived && len(card.Sessions) == 0 {
			continue
		}
		if q.Local != "" && !strings.Contains(strings.ToLower(card.LocalName), strings.ToLower(q.Local)) {
			continue
		}
		sort.Slice(card.Sessions, func(i, j int) bool { return card.Sessions[i].StartAtISO < card.Sessions[j].StartAtISO })
		card.SessionsCount = len(card.Sessions)
		for _, item := range card.Sessions {
			if !item.Counted {
				card.OpenSessions++
			}
			if card.FirstStartISO == "" || item.StartAtISO < card.FirstStartISO {
				card.FirstStartISO = item.StartAtISO
			}
			if item.StartAtISO > card.LastStartISO {
				card.LastStartISO = item.StartAtISO
			}
			if item.Counted && item.StopAtISO > card.LastStopISO {
				card.LastStopISO = item.StopAtISO
			}
		}
		startDay := q.From.In(ClosingLocation)
		startDay = time.Date(startDay.Year(), startDay.Month(), startDay.Day(), 0, 0, 0, 0, ClosingLocation)
		for day := startDay; day.Before(end); day = day.AddDate(0, 0, 1) {
			next := day.AddDate(0, 0, 1)
			left, right := day, next
			if left.Before(q.From) {
				left = q.From
			}
			if right.After(end) {
				right = end
			}
			record := POSHoursDay{Date: day.Format("2006-01-02"), Sessions: []POSSessionHoursItem{}}
			intervals := []hoursInterval{}
			for _, item := range card.Sessions {
				start, stop := parseISO(item.StartAtISO), parseISO(item.StopAtISO)
				effectiveStop := stop
				if !item.Counted {
					effectiveStop = end
				}
				intersects := start.Before(right) && effectiveStop.After(left)
				zeroDuration := item.Counted && start.Equal(stop) && !start.Before(left) && start.Before(right)
				if !intersects && !zeroDuration {
					continue
				}
				// Bad dates only affect the day the session opened; their duration is unknown.
				if item.Issue == "invalid_dates" && start.In(ClosingLocation).Format("2006-01-02") != record.Date {
					continue
				}
				a, b := start, effectiveStop
				if a.Before(left) {
					a = left
				}
				if b.After(right) {
					b = right
				}
				if b.Before(a) {
					b = a
				}
				item.ContinuesFromPrevious = start.Before(day)
				item.ContinuesToNext = effectiveStop.After(next)
				item.DayMinutes = 0
				if item.Counted {
					item.DayMinutes = int64(b.Sub(a) / time.Minute)
					intervals = append(intervals, hoursInterval{a, b})
					if record.LastStopISO == "" || timeToISO(b) > record.LastStopISO {
						record.LastStopISO = timeToISO(b)
					}
				} else {
					record.PendingSessions++
				}
				if record.FirstStartISO == "" || timeToISO(a) < record.FirstStartISO {
					record.FirstStartISO = timeToISO(a)
				}
				record.Sessions = append(record.Sessions, item)
			}
			duration, overlap := unionHours(intervals)
			record.WorkedMinutes = int64(duration / time.Minute)
			record.HasOverlap = overlap
			if len(intervals) > 1 {
				last := intervals[0].end
				for _, interval := range intervals[1:] {
					if interval.end.After(last) {
						last = interval.end
					}
				}
				record.HasBreaks = last.Sub(intervals[0].start) > duration
			}
			if len(record.Sessions) > 0 {
				card.DaysWithSession++
			}
			if len(intervals) > 0 && record.PendingSessions == 0 {
				card.ConfirmedDays++
			}
			if record.PendingSessions > 0 {
				card.PendingDays++
			}
			card.WorkedMinutes += record.WorkedMinutes
			card.Days = append(card.Days, record)
		}
		// Only complete days form the daily average. Pending days can have partial
		// confirmed hours, which are included in totals but not in this denominator.
		var completeMinutes int64
		for _, day := range card.Days {
			if day.PendingSessions == 0 && len(day.Sessions) > 0 {
				completeMinutes += day.WorkedMinutes
			}
		}
		if card.ConfirmedDays > 0 {
			card.AverageMinutes = float64(completeMinutes) / float64(card.ConfirmedDays)
		}
		result.Data = append(result.Data, *card)
		result.Totals.TotalSessions += card.SessionsCount
		result.Totals.OpenSessions += card.OpenSessions
		result.Totals.DaysWithSessions += card.DaysWithSession
		result.Totals.WorkedMinutes += card.WorkedMinutes
		result.Totals.PendingDays += card.PendingDays
	}
	sort.Slice(result.Data, func(i, j int) bool {
		return strings.ToLower(result.Data[i].LocalName) < strings.ToLower(result.Data[j].LocalName)
	})
	result.Totals.TotalPOS = len(result.Data)
	return result
}
