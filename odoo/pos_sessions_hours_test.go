package odoo

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func hoursDate(value string) time.Time { result, _ := time.Parse(time.RFC3339, value); return result }
func hoursFixture(id int, start, stop, state string) map[string]any {
	var end any = stop
	if stop == "" {
		end = false
	}
	return map[string]any{"id": id, "name": "POS/test", "config_id": []any{1, "Centro"}, "user_id": []any{5, "Ana"}, "start_at": start, "stop_at": end, "state": state}
}
func hoursConfigs() []map[string]any {
	return []map[string]any{{"id": 1, "name": "Centro", "active": true}, {"id": 2, "name": "Sin actividad", "active": true}, {"id": 3, "name": "Archivo sin sesiones", "active": false}}
}

func TestHoursUnionsOverlapsAndExcludesBreaksAndPending(t *testing.T) {
	q := POSSessionHoursQuery{From: hoursDate("2026-10-19T05:00:00Z"), To: hoursDate("2026-10-20T05:00:00Z")}
	rows := []map[string]any{
		hoursFixture(1, "2026-10-19 13:00:00", "2026-10-19 17:00:00", "closed"), // 08-12
		hoursFixture(2, "2026-10-19 16:00:00", "2026-10-19 18:00:00", "closed"), // 11-13: overlap 1h
		hoursFixture(3, "2026-10-19 19:00:00", "2026-10-19 21:00:00", "closed"), // 14-16: 1h break
		hoursFixture(4, "2026-10-19 22:00:00", "", "opened"),
	}
	result := buildPOSHours(q, hoursConfigs(), rows, hoursDate("2026-10-20T10:00:00Z"))
	if len(result.Data) != 2 {
		t.Fatalf("expected active stores including empty store; got %+v", result.Data)
	}
	card := result.Data[0]
	if card.WorkedMinutes != 420 || card.PendingDays != 1 || card.ConfirmedDays != 0 || card.AverageMinutes != 0 {
		t.Fatalf("wrong union/average %+v", card)
	}
	day := card.Days[0]
	if !day.HasOverlap || !day.HasBreaks || day.PendingSessions != 1 || len(day.Sessions) != 4 {
		t.Fatalf("wrong daily report %+v", day)
	}
	if result.Data[1].DaysWithSession != 0 || len(result.Data[1].Days) != 1 || len(result.Data[1].Days[0].Sessions) != 0 {
		t.Fatal("empty store disappeared or got invented activity")
	}
}

func TestHoursSplitMidnightAndIncludeSessionsFromBeforePeriod(t *testing.T) {
	q := POSSessionHoursQuery{From: hoursDate("2026-10-19T05:00:00Z"), To: hoursDate("2026-10-21T05:00:00Z")}
	rows := []map[string]any{hoursFixture(1, "2026-10-19 03:00:00", "2026-10-19 07:00:00", "closed"), hoursFixture(2, "2026-10-20 03:00:00", "2026-10-20 07:00:00", "closed")}
	card := buildPOSHours(q, hoursConfigs(), rows, hoursDate("2026-10-22T10:00:00Z")).Data[0]
	if card.WorkedMinutes != 360 || card.ConfirmedDays != 2 || card.AverageMinutes != 180 {
		t.Fatalf("wrong period totals %+v", card)
	}
	if card.Days[0].WorkedMinutes != 240 || card.Days[1].WorkedMinutes != 120 {
		t.Fatalf("hours were assigned to wrong calendar days: %+v", card.Days)
	}
	if !card.Days[0].Sessions[0].ContinuesFromPrevious || !card.Days[0].Sessions[1].ContinuesToNext || !card.Days[1].Sessions[0].ContinuesFromPrevious {
		t.Fatal("missing overnight labels")
	}
	if card.Days[0].LastStopISO != "2026-10-20T05:00:00Z" {
		t.Fatalf("wrong calendar-day closing boundary %s", card.Days[0].LastStopISO)
	}
}

func TestHoursPendingInvalidZeroAndFutureDays(t *testing.T) {
	q := POSSessionHoursQuery{From: hoursDate("2026-10-18T05:00:00Z"), To: hoursDate("2026-10-23T05:00:00Z")}
	rows := []map[string]any{
		hoursFixture(1, "2026-10-18 13:00:00", "2026-10-18 12:00:00", "closed"),
		hoursFixture(2, "2026-10-19 13:00:00", "2026-10-19 13:00:00", "closed"),
	}
	card := buildPOSHours(q, hoursConfigs(), rows, hoursDate("2026-10-20T10:00:00Z")).Data[0]
	if len(card.Days) != 3 {
		t.Fatalf("future dates should not appear: %d", len(card.Days))
	}
	if card.Days[0].PendingSessions != 1 || card.Days[0].Sessions[0].Issue != "invalid_dates" {
		t.Fatal("invalid dates not flagged")
	}
	if len(card.Days[1].Sessions) != 1 || !card.Days[1].Sessions[0].Counted || card.Days[1].WorkedMinutes != 0 {
		t.Fatal("zero-duration closing was treated as unknown")
	}
	if len(card.Days[2].Sessions) != 0 {
		t.Fatal("zero-duration session leaked into later days")
	}
}

func TestHoursAverageUsesOnlyDaysWithCompleteClosures(t *testing.T) {
	q := POSSessionHoursQuery{From: hoursDate("2026-10-18T05:00:00Z"), To: hoursDate("2026-10-21T05:00:00Z")}
	rows := []map[string]any{hoursFixture(1, "2026-10-18 13:00:00", "2026-10-18 21:00:00", "closed"), hoursFixture(2, "2026-10-19 13:00:00", "2026-10-19 17:00:00", "closed"), hoursFixture(3, "2026-10-19 18:00:00", "", "closing_control")}
	card := buildPOSHours(q, hoursConfigs(), rows, hoursDate("2026-10-22T10:00:00Z")).Data[0]
	if card.WorkedMinutes != 720 || card.ConfirmedDays != 1 || card.AverageMinutes != 480 || card.PendingDays != 2 {
		t.Fatalf("partial or missing days diluted average: %+v", card)
	}
}

func TestHoursCatalogPaginationAndOverlapQuery(t *testing.T) {
	calls := map[string]int{}
	client := closingTestClient(t, func(model string, kwargs map[string]any) (any, bool) {
		calls[model]++
		if kwargs["context"].(map[string]any)["active_test"] != false {
			t.Error("archived configurations/sessions hidden")
		}
		if model == "pos.config" {
			if kwargs["offset"].(float64) == 500 {
				return []map[string]any{{"id": 501, "name": "Nuevo local", "active": true}}, false
			}
			rows := make([]map[string]any, 500)
			for i := range rows {
				rows[i] = map[string]any{"id": i + 1, "name": "Local", "active": true}
			}
			return rows, false
		}
		domain, _ := json.Marshal(kwargs["domain"])
		domainText := strings.ReplaceAll(string(domain), `\u003e`, ">")
		if !strings.Contains(domainText, `["stop_at",">","2026-10-19 05:00:00"]`) || !strings.Contains(domainText, `["stop_at","=",false]`) {
			t.Errorf("query omits overnight/open sessions: %s", domain)
		}
		fixture := hoursFixture(1, "2026-10-19 03:00:00", "2026-10-19 10:00:00", "closed")
		fixture["config_id"] = []any{900, "Local archivado con historial"}
		return []map[string]any{fixture}, false
	})
	result, err := client.GetPOSSessionsHoursOverview(POSSessionHoursQuery{From: hoursDate("2026-10-19T05:00:00Z"), To: hoursDate("2026-10-20T05:00:00Z"), Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Data) != 502 || calls["pos.config"] != 2 {
		t.Fatalf("incomplete catalog: %d stores, %v", len(result.Data), calls)
	}
	found := false
	for _, c := range result.Data {
		if c.LocalID == 900 {
			found = true
			if c.WorkedMinutes != 300 {
				t.Fatalf("wrong history %+v", c)
			}
		}
	}
	if !found {
		t.Fatal("session missing from catalog was dropped")
	}
}

func TestHoursFailsWithoutPartialResults(t *testing.T) {
	client := closingTestClient(t, func(model string, kwargs map[string]any) (any, bool) { return []any{}, model == "pos.session" })
	result, err := client.GetPOSSessionsHoursOverview(POSSessionHoursQuery{From: hoursDate("2026-10-19T05:00:00Z"), To: hoursDate("2026-10-20T05:00:00Z")})
	if err == nil || len(result.Data) != 0 {
		t.Fatal("failed request was presented as complete")
	}
}

func TestHoursPaginatesSessionsWithoutDuplicatingWorkTime(t *testing.T) {
	calls := 0
	client := closingTestClient(t, func(model string, kwargs map[string]any) (any, bool) {
		if model == "pos.config" {
			return hoursConfigs(), false
		}
		calls++
		offset := int(kwargs["offset"].(float64))
		count := 500
		if offset == 500 {
			count = 1
		}
		rows := make([]map[string]any, count)
		for i := range rows {
			rows[i] = hoursFixture(offset+i+1, "2025-10-19 13:00:00", "2025-10-19 21:00:00", "closed")
		}
		return rows, false
	})
	result, err := client.GetPOSSessionsHoursOverview(POSSessionHoursQuery{From: hoursDate("2025-10-19T05:00:00Z"), To: hoursDate("2025-10-20T05:00:00Z"), Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || result.Data[0].SessionsCount != 501 || result.Data[0].WorkedMinutes != 480 {
		t.Fatalf("wrong pagination/union: calls=%d sessions=%d minutes=%d", calls, result.Data[0].SessionsCount, result.Data[0].WorkedMinutes)
	}
}
