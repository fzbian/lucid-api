package notify

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Payload representa el cuerpo JSON que exige el endpoint externo
type Payload struct {
	Chat    string `json:"chat"`
	Message string `json:"message"`
}

// NumberPayload representa el cuerpo JSON del endpoint por número
type NumberPayload struct {
	Numero  string `json:"numero"`
	Mensaje string `json:"mensaje"`
}

// PDFNumberPayload representa el cuerpo JSON del endpoint de envío de PDF por número
type PDFNumberPayload struct {
	Numero    string `json:"numero"`
	PDFBase64 string `json:"pdf_base64"`
	PDFNombre string `json:"pdf_nombre"`
	Caption   string `json:"caption"`
	Mensaje   string `json:"mensaje"`
}

// getEnvAny devuelve el primer valor no vacío para una lista de claves
func getEnvAny(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

// SendMovement envía un mensaje de confirmación de movimiento (create/update/delete)
// action: CREATE | UPDATE | DELETE
// entity: nombre de la entidad (ej: TRANSACCION, CATEGORIA)
// detail: texto adicional (ej: id, montos)
func SendMovement(action, entity, detail string) {
	base := strings.TrimRight(getEnvAny("NOTIFY_URL"), "/")
	if base == "" {
		fmt.Printf("[NOTIFY] configuración incompleta (url=%t)\n", base != "")
		return
	}
	endpoint := buildSendURL(base)
	msg := fmt.Sprintf("%s %s: %s", action, entity, detail)
	sendTo(endpoint, "atm", msg)
}

// SendText envía un mensaje de texto arbitrario usando las credenciales del .env
func SendText(text string) {
	base := strings.TrimRight(getEnvAny("NOTIFY_URL"), "/")
	if base == "" {
		fmt.Printf("[NOTIFY] configuración incompleta (url=%t)\n", base != "")
		return
	}
	endpoint := buildSendURL(base)
	sendTo(endpoint, "atm", text)
}

// SendTo envía un mensaje de texto al chat indicado
func SendTo(chat, text string) {
	base := strings.TrimRight(getEnvAny("NOTIFY_URL"), "/")
	if base == "" {
		fmt.Printf("[NOTIFY] configuración incompleta (url=%t)\n", base != "")
		return
	}
	endpoint := buildSendURL(base)
	if strings.TrimSpace(chat) == "" {
		chat = "retiradas"
	}
	sendTo(endpoint, chat, text)
}

// SendToNumber envía un mensaje de texto a un número de WhatsApp.
// Usa NOTIFY_NUMBER_URL (o NOTIFY_URL) y como fallback la URL de noti.chinatownlogistic.com.
func SendToNumber(number, text string) error {
	base := strings.TrimRight(getEnvAny("NOTIFY_NUMBER_URL", "NOTIFY_URL"), "/")
	if base == "" {
		base = "https://noti.chinatownlogistic.com"
	}
	endpoint := buildSendNumberURL(base)
	return sendToNumber(endpoint, number, text)
}

// SendPDFToNumber envía un PDF a un número de WhatsApp.
func SendPDFToNumber(number string, pdfBytes []byte, pdfName, caption, message string) error {
	base := strings.TrimRight(getEnvAny("NOTIFY_NUMBER_URL", "NOTIFY_URL"), "/")
	if base == "" {
		base = "https://noti.chinatownlogistic.com"
	}
	endpoint := buildSendPDFNumberURL(base)
	return sendPDFToNumber(endpoint, number, pdfBytes, pdfName, caption, message)
}

// buildSendURL arma la URL final para enviar texto
func buildSendURL(base string) string {
	// base ya viene sin slash final; agregamos uno fijo + path
	return base + "/whatsapp/send-text"
}

// buildSendNumberURL arma la URL final para enviar texto por número
func buildSendNumberURL(base string) string {
	return base + "/whatsapp/send-text-number"
}

// buildSendPDFNumberURL arma la URL final para enviar PDF por número
func buildSendPDFNumberURL(base string) string {
	return base + "/whatsapp/send-pdf-number"
}

// send ejecuta el POST con el nuevo formato {chat:"atm", message:"..."}
func sendTo(endpoint, chat, message string) {
	payload := Payload{Chat: chat, Message: message}
	b, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		fmt.Printf("[NOTIFY] error creando request: %v\n", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "atm-notify/2.0")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("[NOTIFY] error enviando request: %v\n", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		fmt.Printf("[NOTIFY] respuesta no exitosa: status=%d body=%s\n", resp.StatusCode, string(body))
	}
}

// sendToNumber ejecuta el POST con formato {numero:"...", mensaje:"..."}
func sendToNumber(endpoint, number, message string) error {
	payload := NumberPayload{
		Numero:  strings.TrimSpace(number),
		Mensaje: strings.TrimSpace(message),
	}
	if payload.Numero == "" || payload.Mensaje == "" {
		return fmt.Errorf("numero y mensaje son requeridos")
	}

	b, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("crear request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "atm-notify/2.0")
	if apiKey := strings.TrimSpace(getEnvAny("NOTIFY_APIKEY")); apiKey != "" {
		req.Header.Set("apikey", apiKey)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("enviar request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("respuesta no exitosa status=%d body=%s", resp.StatusCode, string(body))
	}
	return nil
}

// sendPDFToNumber ejecuta el POST con formato:
// {numero, pdf_base64, pdf_nombre, caption, mensaje}
func sendPDFToNumber(endpoint, number string, pdfBytes []byte, pdfName, caption, message string) error {
	payload := PDFNumberPayload{
		Numero:    strings.TrimSpace(number),
		PDFBase64: base64.StdEncoding.EncodeToString(pdfBytes),
		PDFNombre: strings.TrimSpace(pdfName),
		Caption:   strings.TrimSpace(caption),
		Mensaje:   strings.TrimSpace(message),
	}
	if payload.Numero == "" || payload.PDFBase64 == "" || payload.PDFNombre == "" {
		return fmt.Errorf("numero, pdf_base64 y pdf_nombre son requeridos")
	}

	b, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("crear request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "atm-notify/2.0")
	if apiKey := strings.TrimSpace(getEnvAny("NOTIFY_APIKEY")); apiKey != "" {
		req.Header.Set("apikey", apiKey)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("enviar request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("respuesta no exitosa status=%d body=%s", resp.StatusCode, string(body))
	}
	return nil
}
