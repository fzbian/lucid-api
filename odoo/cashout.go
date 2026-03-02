package odoo

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

type rpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
	ID      string      `json:"id"`
}

type rpcResponse[T any] struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id"`
	Result  T               `json:"result"`
	Error   *rpcErrorObject `json:"error"`
}

type rpcErrorObject struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data"`
}

// CashOutResult refleja el retorno del Python
type CashOutResult struct {
	OK         bool    `json:"ok"`
	Message    string  `json:"message"`
	SessionID  *int    `json:"session_id"`
	MySQL      *bool   `json:"mysql,omitempty"`
	MySQLError *string `json:"mysql_error,omitempty"`
}

// CashOutPOS realiza un cash out en Odoo POS y opcionalmente registra el gasto en MySQL
func CashOutPOS(ctx context.Context, odooURL, db, user, password, posName string, amount float64, reason string, categoryName string, mysqlURI string) (CashOutResult, error) {
	res := CashOutResult{OK: false, Message: "", SessionID: nil}
	if amount <= 0 {
		res.Message = "Monto debe ser > 0"
		return res, nil
	}

	// login
	uid, err := odooLogin(ctx, odooURL, db, user, password)
	if err != nil || uid == 0 {
		if err != nil {
			return res, err
		}
		res.Message = "Login fallido"
		return res, nil
	}

	// pos y sesion
	pos, err := odooGetPOSByName(ctx, odooURL, db, uid, password, posName)
	if err != nil {
		return res, err
	}
	if pos == nil {
		res.Message = fmt.Sprintf("POS \"%s\" no encontrado", posName)
		return res, nil
	}
	var sessionID int
	if cur, ok := pos["current_session_id"].([]any); ok && len(cur) >= 1 {
		if v, ok := cur[0].(float64); ok {
			sessionID = int(v)
		}
	}
	if sessionID == 0 {
		res.Message = fmt.Sprintf("POS \"%s\" sin sesión abierta", posName)
		return res, nil
	}

	session, err := odooGetSession(ctx, odooURL, db, uid, password, sessionID)
	if err != nil {
		return res, err
	}
	if session == nil {
		res.SessionID = &sessionID
		res.Message = "No se pudo leer la sesión"
		return res, nil
	}
	if st, _ := session["state"].(string); st != "opened" {
		res.SessionID = &sessionID
		res.Message = fmt.Sprintf("Sesión %d no está abierta", sessionID)
		return res, nil
	}
	saldoVal := 0.0
	switch v := session["cash_register_balance_end"].(type) {
	case float64:
		saldoVal = v
	case string:
		// intentar parsear
		var f float64
		if _, err := fmt.Sscanf(strings.ReplaceAll(v, ",", ""), "%f", &f); err == nil {
			saldoVal = f
		} else {
			res.SessionID = &sessionID
			res.Message = fmt.Sprintf("Saldo no numérico: %v", v)
			return res, nil
		}
	default:
		res.SessionID = &sessionID
		res.Message = fmt.Sprintf("Saldo no numérico: %v", v)
		return res, nil
	}
	if amount > saldoVal {
		res.SessionID = &sessionID
		res.Message = fmt.Sprintf("Monto %v excede saldo %v", amount, saldoVal)
		return res, nil
	}

	// auth web y try_cash_in_out
	if err := odooWebTryCashInOut(ctx, odooURL, db, user, password, sessionID, "out", amount, reason); err != nil {
		return res, err
	}
	res.OK = true
	res.SessionID = &sessionID
	res.Message = fmt.Sprintf("Cash out OK sesión %d monto %v", sessionID, amount)

	// MySQL opcional
	if mysqlURI != "" && categoryName != "" {
		ok, msg := mysqlRegisterGasto(mysqlURI, posName, categoryName, amount, reason)
		res.MySQL = &ok
		if !ok {
			res.MySQLError = &msg
		}
	}
	return res, nil
}

// ======== Odoo helpers ========

func odooLogin(ctx context.Context, baseURL, db, user, password string) (int, error) {
	var result int
	err := jsonRPC(ctx, baseURL, "common", "login", []any{db, user, password}, &result)
	return result, err
}

func odooExecuteKW[T any](ctx context.Context, baseURL, db string, uid int, password, model, method string, args []any, kwargs map[string]any, dest *T) error {
	if args == nil {
		args = []any{}
	}
	if kwargs == nil {
		kwargs = map[string]any{}
	}
	return jsonRPC(ctx, baseURL, "object", "execute_kw", []any{db, uid, password, model, method, args, kwargs}, dest)
}

func odooGetPOSByName(ctx context.Context, baseURL, db string, uid int, password, name string) (map[string]any, error) {
	var ids []int
	if err := odooExecuteKW(ctx, baseURL, db, uid, password, "pos.config", "search", []any{[][]any{{"name", "=", name}}}, map[string]any{"limit": 1}, &ids); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	var recs []map[string]any
	if err := odooExecuteKW(ctx, baseURL, db, uid, password, "pos.config", "read", []any{ids, []string{"name", "current_session_id"}}, nil, &recs); err != nil {
		return nil, err
	}
	if len(recs) == 0 {
		return nil, nil
	}
	return recs[0], nil
}

// ListPOSNames devuelve la lista de nombres de POS configurados en Odoo (cache TTL: 5 min)
func ListPOSNames(ctx context.Context, baseURL, db, user, password string) ([]string, error) {
	// Check cache first (TTL: 5 min)
	const cacheKey = "pos_names"
	if cached, ok := getCached(cacheKey); ok {
		if names, ok := cached.([]string); ok {
			return names, nil
		}
	}

	uid, err := odooLogin(ctx, baseURL, db, user, password)
	if err != nil || uid == 0 {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("login fallido")
	}
	var ids []int
	if err := odooExecuteKW(ctx, baseURL, db, uid, password, "pos.config", "search", []any{[][]any{}}, map[string]any{}, &ids); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []string{}, nil
	}
	var recs []map[string]any
	if err := odooExecuteKW(ctx, baseURL, db, uid, password, "pos.config", "read", []any{ids, []string{"name"}}, nil, &recs); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(recs))
	for _, r := range recs {
		if n, ok := r["name"].(string); ok && n != "" {
			names = append(names, n)
		}
	}

	// Store in cache
	setCache(cacheKey, names, 5*time.Minute)
	return names, nil
}

func odooGetSession(ctx context.Context, baseURL, db string, uid int, password string, sessionID int) (map[string]any, error) {
	var recs []map[string]any
	if err := odooExecuteKW(ctx, baseURL, db, uid, password, "pos.session", "read", []any{[]int{sessionID}, []string{"state", "cash_register_balance_end", "name"}}, nil, &recs); err != nil {
		return nil, err
	}
	if len(recs) == 0 {
		return nil, nil
	}
	return recs[0], nil
}

func jsonRPC[T any](ctx context.Context, baseURL, service, method string, args []any, dest *T) error {
	endpoint := strings.TrimRight(baseURL, "/") + "/jsonrpc"
	payload := rpcRequest{
		JSONRPC: "2.0",
		Method:  "call",
		Params: map[string]any{
			"service": service,
			"method":  method,
			"args":    args,
		},
		ID: fmt.Sprintf("%d", time.Now().UnixNano()),
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	httpClient := &http.Client{Timeout: 15 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var rr rpcResponse[T]
	if err := json.NewDecoder(resp.Body).Decode(&rr); err != nil {
		return err
	}
	if rr.Error != nil {
		dataMsg := ""
		if rr.Error.Data != nil {
			dataMsg = fmt.Sprintf(" | Data: %+v", rr.Error.Data)
		}
		return fmt.Errorf("%s%s", rr.Error.Message, dataMsg)
	}
	*dest = rr.Result
	return nil
}

func odooWebTryCashInOut(ctx context.Context, baseURL, db, user, password string, sessionID int, typ string, amount float64, reason string) error {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Timeout: 15 * time.Second, Jar: jar}

	// authenticate
	authURL := strings.TrimRight(baseURL, "/") + "/web/session/authenticate"
	authPayload := map[string]any{
		"jsonrpc": "2.0",
		"method":  "call",
		"params": map[string]any{
			"db":       db,
			"login":    user,
			"password": password,
		},
		"id": fmt.Sprintf("%d", time.Now().UnixNano()),
	}
	var authRes map[string]any
	if err := doJSON(ctx, client, authURL, authPayload, &authRes); err != nil {
		return fmt.Errorf("web authenticate error: %w", err)
	}

	// construir contexto desde la respuesta
	userCtx := map[string]any{"lang": "es_ES", "tz": "UTC", "allowed_company_ids": []int{1}}
	if uc, ok := authRes["user_context"].(map[string]any); ok {
		if v, ok := uc["lang"].(string); ok && v != "" {
			userCtx["lang"] = v
		}
		if v, ok := uc["tz"].(string); ok && v != "" {
			userCtx["tz"] = v
		}
		if v, ok := uc["allowed_company_ids"].([]any); ok {
			ids := make([]int, 0, len(v))
			for _, it := range v {
				if f, ok := it.(float64); ok {
					ids = append(ids, int(f))
				}
			}
			if len(ids) > 0 {
				userCtx["allowed_company_ids"] = ids
			}
		}
	}
	if uidv, ok := authRes["uid"].(float64); ok {
		userCtx["uid"] = int(uidv)
	}
	urlKW := strings.TrimRight(baseURL, "/") + "/web/dataset/call_kw/pos.session/try_cash_in_out"
	payload := map[string]any{
		"jsonrpc": "2.0",
		"method":  "call",
		"params": map[string]any{
			"model":  "pos.session",
			"method": "try_cash_in_out",
			"args":   []any{[]int{sessionID}, typ, amount, reason, map[string]any{"formattedAmount": fmt.Sprintf("$ %.2f", amount), "translatedType": typ}},
			"kwargs": map[string]any{"context": userCtx},
		},
		"id": 1,
	}
	return doJSON(ctx, client, urlKW, payload, nil)
}

func doJSON(ctx context.Context, client *http.Client, endpoint string, payload any, dest any) error {
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var rr struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      any             `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   *rpcErrorObject `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rr); err != nil {
		return err
	}
	if rr.Error != nil {
		return fmt.Errorf("rpc error: %s", rr.Error.Message)
	}
	if dest != nil && rr.Result != nil {
		return json.Unmarshal(rr.Result, dest)
	}
	return nil
}

// ======== MySQL helpers ========

func mysqlRegisterGasto(mysqlURI, localNombre, categoriaNombre string, monto float64, descripcion string) (bool, string) {
	dsn, err := mysqlURIToDSN(mysqlURI)
	if err != nil {
		return false, err.Error()
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return false, err.Error()
	}
	defer db.Close()
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)

	tx, err := db.Begin()
	if err != nil {
		return false, err.Error()
	}
	defer func() {
		_ = tx.Rollback()
	}()

	var localID int64
	if err := tx.QueryRow("SELECT id FROM locales WHERE nombre=?", localNombre).Scan(&localID); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return false, err.Error()
		}
		res, err := tx.Exec("INSERT INTO locales (nombre) VALUES (?)", localNombre)
		if err != nil {
			return false, err.Error()
		}
		localID, _ = res.LastInsertId()
	}

	var categoriaID int64
	if err := tx.QueryRow("SELECT id FROM categorias WHERE nombre=?", categoriaNombre).Scan(&categoriaID); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return false, err.Error()
		}
		res, err := tx.Exec("INSERT INTO categorias (nombre) VALUES (?)", categoriaNombre)
		if err != nil {
			return false, err.Error()
		}
		categoriaID, _ = res.LastInsertId()
	}

	_, err = tx.Exec("INSERT INTO gastos (local_id, categoria_id, monto, descripcion, fecha) VALUES (?, ?, ?, ?, CURRENT_DATE)", localID, categoriaID, monto, descripcion)
	if err != nil {
		return false, err.Error()
	}
	if err := tx.Commit(); err != nil {
		return false, err.Error()
	}
	return true, ""
}

func mysqlURIToDSN(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", err
	}
	if u.Scheme != "mysql" && u.Scheme != "mysql+pymysql" {
		return "", fmt.Errorf("esquema de URI MySQL inválido")
	}
	user := ""
	if u.User != nil {
		uname := u.User.Username()
		if dec, err := url.QueryUnescape(uname); err == nil {
			uname = dec
		}
		if pass, ok := u.User.Password(); ok {
			if dec, err := url.QueryUnescape(pass); err == nil {
				pass = dec
			}
			user = fmt.Sprintf("%s:%s", uname, pass)
		} else {
			user = uname
		}
	}
	host := u.Host
	if !strings.Contains(host, ":") {
		host += ":3306"
	}
	db := strings.TrimPrefix(u.Path, "/")
	if db == "" {
		return "", fmt.Errorf("base de datos no especificada en URI")
	}
	// charset y parseTime
	q := u.Query()
	if q.Get("charset") == "" {
		q.Set("charset", "utf8mb4")
	}
	if q.Get("parseTime") == "" {
		q.Set("parseTime", "true")
	}
	return fmt.Sprintf("%s@tcp(%s)/%s?%s", user, host, db, q.Encode()), nil
}
