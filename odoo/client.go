package odoo

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"time"

	"atm/models"
)

type Client struct {
	baseURL  string
	db       string
	user     string
	password string
	hc       *http.Client
}

type authRequest struct {
	JSONRPC string     `json:"jsonrpc"`
	Method  string     `json:"method"`
	Params  authParams `json:"params"`
	ID      int        `json:"id"`
}

type authParams struct {
	DB       string `json:"db"`
	Login    string `json:"login"`
	Password string `json:"password"`
}

type jsonRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
	ID      int         `json:"id"`
}

type callKwParams struct {
	Model  string                 `json:"model"`
	Method string                 `json:"method"`
	Args   []interface{}          `json:"args"`
	Kwargs map[string]interface{} `json:"kwargs"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data"`
}

type posSession struct {
	BalanceEndReal float64       `json:"cash_register_balance_end_real"`
	State          string        `json:"state"`
	ConfigID       []interface{} `json:"config_id"`
}

func NewFromEnv() (*Client, error) {
	base := os.Getenv("ODOO_URL")
	db := os.Getenv("ODOO_DB")
	user := os.Getenv("ODOO_USER")
	pass := os.Getenv("ODOO_PASSWORD")
	if base == "" || db == "" || user == "" || pass == "" {
		return nil, errors.New("variables ODOO_URL, ODOO_DB, ODOO_USER, ODOO_PASSWORD requeridas")
	}
	base = strings.TrimRight(base, "/")
	jar, _ := cookiejar.New(nil)
	return &Client{baseURL: base, db: db, user: user, password: pass, hc: &http.Client{Timeout: 20 * time.Second, Jar: jar}}, nil
}

func (c *Client) Authenticate() error {
	payload := authRequest{
		JSONRPC: "2.0",
		Method:  "call",
		Params: authParams{
			DB:       c.db,
			Login:    c.user,
			Password: c.password,
		},
		ID: 1,
	}
	b, _ := json.Marshal(payload)
	resp, err := c.hc.Post(c.baseURL+"/web/session/authenticate", "application/json", strings.NewReader(string(b)))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("auth fallo status %d", resp.StatusCode)
	}
	var r jsonRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return err
	}
	if r.Error != nil {
		return fmt.Errorf("auth error: %s", r.Error.Message)
	}
	return nil
}

// Helper genérico para llamar a Odoo (call_kw)
func (c *Client) callOdoo(model, method string, args []interface{}, kwargs map[string]interface{}) (json.RawMessage, error) {
	req := jsonRPCRequest{JSONRPC: "2.0", Method: "call", Params: callKwParams{Model: model, Method: method, Args: args, Kwargs: kwargs}, ID: 99}
	b, _ := json.Marshal(req)
	resp, err := c.hc.Post(c.baseURL+"/web/dataset/call_kw", "application/json", strings.NewReader(string(b)))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	var r jsonRPCResponse
	if err = json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if r.Error != nil {
		dataJSON, _ := json.Marshal(r.Error.Data)
		return nil, fmt.Errorf("rpc code=%d msg=%s data=%s", r.Error.Code, r.Error.Message, string(dataJSON))
	}
	return r.Result, nil
}

// FetchPOSBalances optimizado con cache TTL de 45 segundos:
// 1. Busca todos los pos.config disponibles en Odoo.
// 2. Obtiene sesiones abiertas/abriendo por esos config_id (orden desc) y toma la más reciente por local.
// 3. Para locales sin sesión abierta, obtiene la sesión cerrada más reciente.
// 4. Calcula balance según estado: opened/opening_control -> cash_register_balance_end; closed -> cash_register_balance_end_real.
func (c *Client) FetchPOSBalances() (map[string]models.POSLocalDetail, float64, error) {
	// Check cache first (TTL: 45s)
	const cacheKey = "pos_balances"
	if cached, ok := getCached(cacheKey); ok {
		if result, ok := cached.(posBalancesCache); ok {
			return result.locales, result.total, nil
		}
	}

	locales, total, err := c.fetchPOSBalancesUncached()
	if err != nil {
		return nil, 0, err
	}

	// Store in cache
	setCache(cacheKey, posBalancesCache{locales: locales, total: total}, 45*time.Second)
	return locales, total, nil
}

type posBalancesCache struct {
	locales map[string]models.POSLocalDetail
	total   float64
}

func (c *Client) fetchPOSBalancesUncached() (map[string]models.POSLocalDetail, float64, error) {
	cfgFields := []string{"name"}
	cfgKw := map[string]interface{}{"domain": []interface{}{}, "fields": cfgFields, "limit": 1000}
	rawCfg, err := c.callOdoo("pos.config", "search_read", []interface{}{}, cfgKw)
	if err != nil {
		return nil, 0, fmt.Errorf("config search: %w", err)
	}
	var cfgs []map[string]interface{}
	if err = json.Unmarshal(rawCfg, &cfgs); err != nil {
		return nil, 0, fmt.Errorf("config decode: %w", err)
	}
	if len(cfgs) == 0 {
		return map[string]models.POSLocalDetail{}, 0, nil
	}

	configIDs := make([]int64, 0, len(cfgs))
	nameByID := make(map[int64]string)
	for _, cobj := range cfgs {
		if idf, ok := cobj["id"].(float64); ok {
			id := int64(idf)
			configIDs = append(configIDs, id)
			if n, okn := cobj["name"].(string); okn {
				nameByID[id] = n
			}
		}
	}
	if len(configIDs) == 0 {
		return map[string]models.POSLocalDetail{}, 0, nil
	}

	// Helper dominio config_ids IN
	makeInDomain := func(ids []int64) []interface{} {
		val := make([]interface{}, 0, len(ids))
		for _, id := range ids {
			val = append(val, id)
		}
		return []interface{}{[]interface{}{"config_id", "in", val}}
	}

	// Paso 2: sesiones abiertas / opening_control
	openDomain := makeInDomain(configIDs)
	openDomain = append(openDomain, []interface{}{"state", "in", []interface{}{"opened", "opening_control"}})
	sessFields := []string{"id", "config_id", "state", "cash_register_balance_end_real", "cash_register_balance_end"}
	openKw := map[string]interface{}{"domain": openDomain, "fields": sessFields, "limit": len(configIDs) * 3, "order": "id desc"}
	rawOpen, err := c.callOdoo("pos.session", "search_read", []interface{}{}, openKw)
	if err != nil {
		return nil, 0, fmt.Errorf("open sessions: %w", err)
	}
	var openSessions []map[string]interface{}
	if err = json.Unmarshal(rawOpen, &openSessions); err != nil {
		return nil, 0, fmt.Errorf("open decode: %w", err)
	}

	chosen := make(map[int64]map[string]interface{})
	openSessionIDs := make([]int64, 0)
	for _, s := range openSessions {
		cfgID := extractConfigID(s)
		if cfgID == 0 {
			continue
		}
		if _, exists := chosen[cfgID]; !exists { // primera (orden desc => más reciente)
			chosen[cfgID] = s
			if st := fmt.Sprintf("%v", s["state"]); st == "opened" || st == "opening_control" {
				if sid := extractSessionID(s); sid != 0 {
					openSessionIDs = append(openSessionIDs, sid)
				}
			}
		}
	}

	// Paso 3: sesiones cerradas para los locales sin abierta
	missing := make([]int64, 0)
	for _, id := range configIDs {
		if _, ok := chosen[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		closedDomain := makeInDomain(missing) // quitar filtro de estado para capturar cualquier última sesión
		closedKw := map[string]interface{}{"domain": closedDomain, "fields": sessFields, "limit": len(missing) * 5, "order": "id desc"}
		rawClosed, err2 := c.callOdoo("pos.session", "search_read", []interface{}{}, closedKw)
		if err2 == nil {
			var closedSessions []map[string]interface{}
			if err3 := json.Unmarshal(rawClosed, &closedSessions); err3 == nil {
				for _, s := range closedSessions {
					cfgID := extractConfigID(s)
					if cfgID == 0 {
						continue
					}
					if _, exists := chosen[cfgID]; !exists {
						chosen[cfgID] = s
						if st := fmt.Sprintf("%v", s["state"]); st == "opened" || st == "opening_control" {
							if sid := extractSessionID(s); sid != 0 {
								openSessionIDs = append(openSessionIDs, sid)
							}
						}
					}
				}
			}
		}
	}

	// Paso 4: obtener ventas por sesión abierta (read_group en pos.order)
	salesBySession := make(map[int64]float64)
	if len(openSessionIDs) > 0 {
		domain := []interface{}{[]interface{}{"session_id", "in", toInterfaceSlice(openSessionIDs)}}
		fields := []interface{}{"amount_total"}
		groupBy := []interface{}{"session_id"}
		args := []interface{}{domain, fields, groupBy}
		rawSales, errSales := c.callOdoo("pos.order", "read_group", args, map[string]interface{}{"lazy": false})
		if errSales == nil {
			var salesRows []map[string]interface{}
			if errDecode := json.Unmarshal(rawSales, &salesRows); errDecode == nil {
				for _, row := range salesRows {
					sid := extractIDFromGroup(row["session_id"])
					if sid == 0 {
						continue
					}
					salesBySession[sid] = numberAsFloat(row["amount_total"])
				}
			}
		}
	}

	// Paso 5: calcular balances y ventas por local
	locales := make(map[string]models.POSLocalDetail)
	var total float64
	for cfgID, sess := range chosen {
		name := nameByID[cfgID]
		key := normalizeLocalKey(name)
		state := fmt.Sprintf("%v", sess["state"])
		estado := "cerrada"
		if state == "opened" {
			estado = "abierta"
		} else if state == "opening_control" {
			estado = "abriendo"
		}
		var balance float64
		if state == "opened" || state == "opening_control" {
			balance = numberAsFloat(sess["cash_register_balance_end"])
		} else if state == "closed" {
			balance = numberAsFloat(sess["cash_register_balance_end_real"])
			if balance == 0 {
				balance = numberAsFloat(sess["cash_register_balance_end"])
			}
		} else { // cualquier otro estado
			balance = numberAsFloat(sess["cash_register_balance_end_real"])
			if balance == 0 {
				balance = numberAsFloat(sess["cash_register_balance_end"])
			}
		}

		info := locales[key]
		info.SaldoEnCaja += balance
		// Priorizar estados abiertos sobre cerrados cuando se combinan múltiples configs
		if info.EstadoSesion == "" || (info.EstadoSesion == "cerrada" && (estado == "abierta" || estado == "abriendo")) {
			info.EstadoSesion = estado
		}
		if state == "opened" || state == "opening_control" {
			sid := extractSessionID(sess)
			if sid != 0 {
				v := salesBySession[sid]
				if info.Vendido == nil {
					info.Vendido = new(float64)
				}
				*info.Vendido += v
			}
		}
		locales[key] = info
		total += balance
	}
	// Agregar explícitamente configs sin sesión (balance 0) para que aparezcan en respuesta
	for _, cfgID := range configIDs {
		if _, ok := chosen[cfgID]; !ok {
			key := normalizeLocalKey(nameByID[cfgID])
			if _, exists := locales[key]; !exists {
				locales[key] = models.POSLocalDetail{SaldoEnCaja: 0, EstadoSesion: "sin_sesion"}
			}
		}
	}
	return locales, total, nil
}

func extractConfigID(sess map[string]interface{}) int64 {
	if cfg, ok := sess["config_id"].([]interface{}); ok && len(cfg) >= 1 {
		if idf, ok2 := cfg[0].(float64); ok2 {
			return int64(idf)
		}
	}
	return 0
}

func extractSessionID(sess map[string]interface{}) int64 {
	if idf, ok := sess["id"].(float64); ok {
		return int64(idf)
	}
	return 0
}

func extractIDFromGroup(v interface{}) int64 {
	if arr, ok := v.([]interface{}); ok && len(arr) > 0 {
		if idf, ok2 := arr[0].(float64); ok2 {
			return int64(idf)
		}
	}
	return 0
}

func toInterfaceSlice(ids []int64) []interface{} {
	out := make([]interface{}, 0, len(ids))
	for _, id := range ids {
		out = append(out, id)
	}
	return out
}

func normalizeLocalKey(name string) string {
	return strings.TrimSpace(name)
}

// Utilidades
func numberAsFloat(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	default:
		return 0
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max < 10 {
		return s[:max]
	}
	return s[:max-7] + "[...]"
}

// EmployeeStruct para mapear respuesta de hr.employee
type OdooEmployee struct {
	ID    int         `json:"id"`
	Name  string      `json:"name"`
	Pin   interface{} `json:"pin"`    // Puede venir string o false/null
	JobID interface{} `json:"job_id"` // [id, name] o false
}

// SyncEmployees obtiene empleados de Odoo para sincronizar usuarios
func (c *Client) FetchEmployees() ([]OdooEmployee, error) {
	// Buscar empleados activos
	domain := []interface{}{}
	fields := []string{"id", "name", "pin", "job_id"} // pin es usado como contraseña
	kwargs := map[string]interface{}{"domain": domain, "fields": fields}

	// Usamos hr.employee
	raw, err := c.callOdoo("hr.employee", "search_read", []interface{}{}, kwargs)
	if err != nil {
		return nil, fmt.Errorf("employee search: %w", err)
	}

	var employees []OdooEmployee
	// Odoo devuelve un array de objetos
	if err := json.Unmarshal(raw, &employees); err != nil {
		return nil, fmt.Errorf("employee decode: %w", err)
	}

	return employees, nil
}

// POSSessionShort estructura para listado de sesiones en wizard
type POSSessionShort struct {
	ID       int       `json:"id"`
	Name     string    `json:"name"`
	StartAt  time.Time `json:"start_at"`
	StopAt   time.Time `json:"stop_at"`
	State    string    `json:"state"`
	ConfigID []any     `json:"config_id"`
}

// GetPOSSessions busca sesiones de un POS específico en un rango de fechas
func (c *Client) GetPOSSessions(posID int, start, end time.Time) ([]POSSessionShort, error) {
	// domain: [['config_id', '=', posID], ['start_at', '>=', start], ['start_at', '<=', end]]
	// Odoo dates are UTC string. We'll pass string to keep it simple or let Odoo handle it?
	// Odoo expects "%Y-%m-%d %H:%M:%S" usually.
	layout := "2006-01-02 15:04:05"
	sStr := start.UTC().Format(layout)
	eStr := end.UTC().Format(layout)

	domain := []any{
		[]any{"config_id", "=", posID},
		[]any{"start_at", ">=", sStr},
		[]any{"start_at", "<=", eStr},
	}

	fields := []string{"id", "name", "start_at", "stop_at", "state", "config_id"}
	kwargs := map[string]any{"domain": domain, "fields": fields, "order": "start_at asc"}

	raw, err := c.callOdoo("pos.session", "search_read", []any{}, kwargs)
	if err != nil {
		return nil, fmt.Errorf("search sessions: %w", err)
	}

	var results []map[string]any
	if err := json.Unmarshal(raw, &results); err != nil {
		return nil, fmt.Errorf("decode sessions: %w", err)
	}

	var sessions []POSSessionShort
	for _, r := range results {
		var s POSSessionShort
		if id, ok := r["id"].(float64); ok {
			s.ID = int(id)
		}
		if name, ok := r["name"].(string); ok {
			s.Name = name
		}
		if cfg, ok := r["config_id"].([]any); ok {
			s.ConfigID = cfg
		}
		if st, ok := r["state"].(string); ok {
			s.State = st
		}

		// Parse dates (Odoo sends string UTC)
		if startStr, ok := r["start_at"].(string); ok && startStr != "" {
			if t, err := time.Parse(layout, startStr); err == nil {
				s.StartAt = t
			}
		}
		if stopStr, ok := r["stop_at"].(string); ok && stopStr != "" {
			if t, err := time.Parse(layout, stopStr); err == nil {
				s.StopAt = t
			}
		}
		sessions = append(sessions, s)
	}
	return sessions, nil

}

func (c *Client) ListPOSConfigs() ([]map[string]any, error) {
	// Simple list of ID, Name for selection
	domain := []any{}
	fields := []string{"id", "name"}
	kwargs := map[string]any{"domain": domain, "fields": fields}

	raw, err := c.callOdoo("pos.config", "search_read", []any{}, kwargs)
	if err != nil {
		return nil, err
	}

	var res []map[string]any
	json.Unmarshal(raw, &res)
	return res, nil
}
