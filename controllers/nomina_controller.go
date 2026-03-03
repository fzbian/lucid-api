package controllers

import (
	"atm/models"
	"atm/notify"
	atmOdoo "atm/odoo"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	signatureTokenTTL   = 7 * 24 * time.Hour
	maxSignedPDFSize    = 5 << 20 // 5MB
	signatureAuditLabel = "token+cedula+drawn_signature"
)

const paymentSigningPageHTML = `<!doctype html>
<html lang="es">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>Firma de Comprobante</title>
  <style>
    :root { color-scheme: light; }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      background: #f6f7fb;
      color: #111827;
    }
    .wrap {
      max-width: 640px;
      margin: 32px auto;
      background: #fff;
      border-radius: 12px;
      padding: 20px;
      box-shadow: 0 8px 24px rgba(17, 24, 39, 0.08);
    }
    h1 { margin: 0 0 16px; font-size: 1.4rem; }
    label { display: block; margin: 12px 0 6px; font-weight: 600; }
    input, button {
      width: 100%;
      padding: 10px 12px;
      border-radius: 8px;
      border: 1px solid #d1d5db;
      font-size: 0.95rem;
    }
    button {
      border: none;
      background: #111827;
      color: #fff;
      font-weight: 600;
      cursor: pointer;
      margin-top: 12px;
    }
    button:disabled { opacity: .55; cursor: not-allowed; }
    .card {
      margin-top: 14px;
      background: #f9fafb;
      border: 1px solid #e5e7eb;
      border-radius: 10px;
      padding: 12px;
    }
    .msg {
      margin-top: 12px;
      border-radius: 8px;
      padding: 10px 12px;
      font-size: 0.92rem;
    }
    .ok { background: #ecfdf5; color: #065f46; border: 1px solid #a7f3d0; }
    .err { background: #fef2f2; color: #991b1b; border: 1px solid #fecaca; }
    .muted { color: #6b7280; font-size: 0.88rem; }
  </style>
</head>
<body>
  <main class="wrap">
    <h1>Firma de comprobante de nomina</h1>
    <p class="muted">Ingresa tu cedula para validar el enlace y sube el PDF firmado.</p>

    <label for="cedula">Cedula</label>
    <input id="cedula" name="cedula" type="text" autocomplete="off" placeholder="Ej: 1234567890" />

    <button id="validateBtn" type="button">Validar enlace</button>

    <div id="paymentCard" class="card" style="display:none;"></div>

    <form id="signForm" style="display:none;">
      <label for="signedPdf">PDF firmado</label>
      <input id="signedPdf" name="signed_pdf" type="file" accept="application/pdf,.pdf" />
      <button id="submitBtn" type="submit">Confirmar firma</button>
    </form>

    <div id="message"></div>
  </main>

  <script>
    (function () {
      const token = decodeURIComponent((window.location.pathname.split("/").pop() || "").trim());
      const cedulaInput = document.getElementById("cedula");
      const validateBtn = document.getElementById("validateBtn");
      const signForm = document.getElementById("signForm");
      const signedPdfInput = document.getElementById("signedPdf");
      const submitBtn = document.getElementById("submitBtn");
      const paymentCard = document.getElementById("paymentCard");
      const message = document.getElementById("message");

      let isValidated = false;

      function setMessage(text, isError) {
        message.innerHTML = "";
        if (!text) return;
        const box = document.createElement("div");
        box.className = "msg " + (isError ? "err" : "ok");
        box.textContent = text;
        message.appendChild(box);
      }

      function parseError(resp, fallback) {
        return resp.json()
          .then(data => (data && data.error) ? data.error : fallback)
          .catch(() => fallback);
      }

      if (!token) {
        setMessage("Token invalido.", true);
        validateBtn.disabled = true;
        return;
      }

      validateBtn.addEventListener("click", async function () {
        const cedula = (cedulaInput.value || "").trim();
        if (!cedula) {
          setMessage("Debes ingresar la cedula.", true);
          return;
        }

        validateBtn.disabled = true;
        setMessage("Validando enlace...", false);
        try {
          const resp = await fetch("/api/nomina/sign/" + encodeURIComponent(token) + "/access", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ cedula: cedula })
          });

          if (!resp.ok) {
            const errText = await parseError(resp, "No se pudo validar el enlace.");
            throw new Error(errText);
          }

          const data = await resp.json();
          isValidated = true;
          paymentCard.style.display = "block";
          signForm.style.display = "block";

          const p = data.payment || {};
          const u = data.user || {};
          paymentCard.innerHTML =
            "<strong>Empleado:</strong> " + (u.full_name || u.name || "N/A") + "<br>" +
            "<strong>Periodo:</strong> " + (p.period_start || "") + " - " + (p.period_end || "") + "<br>" +
            "<strong>Total pagado:</strong> " + (p.total_paid || 0) + "<br>" +
            "<strong>Vence:</strong> " + (data.expires_at || "");

          setMessage("Enlace valido. Ahora sube el PDF firmado.", false);
        } catch (err) {
          isValidated = false;
          signForm.style.display = "none";
          paymentCard.style.display = "none";
          setMessage(err.message || "No se pudo validar el enlace.", true);
        } finally {
          validateBtn.disabled = false;
        }
      });

      signForm.addEventListener("submit", async function (ev) {
        ev.preventDefault();
        if (!isValidated) {
          setMessage("Primero valida el enlace con tu cedula.", true);
          return;
        }

        const cedula = (cedulaInput.value || "").trim();
        const file = signedPdfInput.files && signedPdfInput.files[0];
        if (!cedula) {
          setMessage("Debes ingresar la cedula.", true);
          return;
        }
        if (!file) {
          setMessage("Debes seleccionar el PDF firmado.", true);
          return;
        }

        submitBtn.disabled = true;
        setMessage("Enviando firma...", false);

        try {
          const form = new FormData();
          form.append("cedula", cedula);
          form.append("signed_pdf", file);

          const resp = await fetch("/api/nomina/sign/" + encodeURIComponent(token) + "/complete", {
            method: "POST",
            body: form
          });

          if (!resp.ok) {
            const errText = await parseError(resp, "No se pudo confirmar la firma.");
            throw new Error(errText);
          }

          setMessage("Firma confirmada correctamente.", false);
          signForm.reset();
          submitBtn.disabled = true;
        } catch (err) {
          setMessage(err.message || "No se pudo confirmar la firma.", true);
          submitBtn.disabled = false;
        }
      });
    })();
  </script>
</body>
</html>
`

func normalizeCedula(v string) string {
	replacer := strings.NewReplacer(".", "", "-", "", " ", "")
	return replacer.Replace(strings.TrimSpace(strings.ToLower(v)))
}

type nominaAdjustment struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
}

func sumNominaAdjustments(raw string) (int64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, nil
	}

	var items []nominaAdjustment
	if err := json.Unmarshal([]byte(trimmed), &items); err != nil {
		return 0, err
	}

	var total int64
	for _, item := range items {
		total += int64(item.Value)
	}
	return total, nil
}

func resolveSundayValueByPeriod(global models.NominaConfig, periodStart time.Time) int64 {
	month := int(periodStart.Month()) // 1..12
	if month >= 1 && month <= 6 {
		if global.ValorDominicalS1 > 0 {
			return global.ValorDominicalS1
		}
		if global.ValorDominical > 0 {
			return global.ValorDominical
		}
		return global.ValorDominicalS2
	}

	if global.ValorDominicalS2 > 0 {
		return global.ValorDominicalS2
	}
	if global.ValorDominical > 0 {
		return global.ValorDominical
	}
	return global.ValorDominicalS1
}

func hashSignatureToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func generateSignatureToken() (plain string, hash string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	plain = hex.EncodeToString(raw)
	hash = hashSignatureToken(plain)
	return plain, hash, nil
}

func normalizePublicBaseURL(raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return ""
	}

	u, err := url.Parse(v)
	if err != nil || strings.TrimSpace(u.Scheme) == "" || strings.TrimSpace(u.Host) == "" {
		return ""
	}

	path := strings.TrimRight(strings.TrimSpace(u.Path), "/")
	if path == "/" {
		path = ""
	}

	return fmt.Sprintf("%s://%s%s", u.Scheme, u.Host, path)
}

func resolveConfiguredSigningBaseURL() string {
	keys := []string{
		"SIGNING_BASE_URL",
		"PAYROLL_SIGN_BASE_URL",
		"FRONTEND_BASE_URL",
		"CLIENT_BASE_URL",
		"CLIENT_APP_BASE_URL",
	}

	for _, key := range keys {
		if base := normalizePublicBaseURL(os.Getenv(key)); base != "" {
			return base
		}
	}

	return ""
}

func resolveRequestBaseURL(c *gin.Context) string {
	if configured := resolveConfiguredSigningBaseURL(); configured != "" {
		return configured
	}

	if origin := normalizePublicBaseURL(c.GetHeader("Origin")); origin != "" {
		return origin
	}

	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	if xfProto := strings.TrimSpace(c.GetHeader("X-Forwarded-Proto")); xfProto != "" {
		scheme = strings.TrimSpace(strings.Split(xfProto, ",")[0])
	}

	host := strings.TrimSpace(c.GetHeader("X-Forwarded-Host"))
	if host == "" {
		host = c.Request.Host
	}

	return strings.TrimRight(fmt.Sprintf("%s://%s", scheme, host), "/")
}

// ServePaymentSigningPage renderiza una vista básica para firmar comprobantes por token.
func ServePaymentSigningPage(c *gin.Context) {
	if strings.TrimSpace(c.Param("token")) == "" {
		c.String(http.StatusBadRequest, "Token requerido")
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(paymentSigningPageHTML))
}

func getSignaturePaymentByToken(token string) (*models.NominaPayment, error) {
	hash := hashSignatureToken(token)

	var payment models.NominaPayment
	err := DB.Where("signature_token_hash = ?", hash).First(&payment).Error
	if err != nil {
		return nil, err
	}
	return &payment, nil
}

func ensureSignablePayment(payment *models.NominaPayment) error {
	if payment.IsSigned {
		return errors.New("El pago ya fue firmado")
	}
	if payment.IsPartial {
		return errors.New("Debes completar la comisión antes de enviar el comprobante")
	}
	if payment.SignatureTokenExpiresAt == nil {
		return errors.New("Link inválido o expirado")
	}
	if time.Now().After(*payment.SignatureTokenExpiresAt) {
		return errors.New("Link inválido o expirado")
	}
	return nil
}

func normalizeWhatsAppNumber(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	var digits strings.Builder
	digits.Grow(len(trimmed))
	for _, r := range trimmed {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	normalized := digits.String()
	if strings.HasPrefix(normalized, "57") && len(normalized) == 12 {
		normalized = normalized[2:]
	}
	if len(normalized) != 10 {
		return ""
	}
	return normalized
}

func resolvePayrollFortnight(periodStart, periodEnd time.Time) int {
	startUTC := periodStart.UTC()
	endUTC := periodEnd.UTC()

	if startUTC.Day() > 15 {
		return 2
	}
	// Si cruza de mes o termina después del 15, se considera 2da quincena.
	if startUTC.Month() != endUTC.Month() || endUTC.Day() > 15 {
		return 2
	}
	return 1
}

func isUserExcludedFromNominaPeriod(year, month, period int, userID uint) (bool, error) {
	var count int64
	if err := DB.Model(&models.NominaPeriodExclusion{}).
		Where("year = ? AND month = ? AND period = ? AND user_id = ?", year, month, period, userID).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func buildSignatureWhatsAppMessage(employeeName, signingURL string, expiresAt *time.Time) string {
	name := strings.TrimSpace(employeeName)
	if name == "" {
		name = "equipo"
	}
	if expiresAt != nil {
		// Presentamos hora local de Colombia para que sea clara al empleado.
		bogota, err := time.LoadLocation("America/Bogota")
		if err == nil {
			return fmt.Sprintf(
				"Hola %s. Tu recibo de pago está listo. Por favor revísalo y fírmalo en este enlace: %s. Debes ingresar tu cédula para validar la firma. Este enlace vence el %s.",
				name,
				signingURL,
				expiresAt.In(bogota).Format("02/01/2006 03:04 PM"),
			)
		}
	}
	return fmt.Sprintf(
		"Hola %s. Tu recibo de pago está listo. Por favor revísalo y fírmalo en este enlace: %s. Debes ingresar tu cédula para validar la firma.",
		name,
		signingURL,
	)
}

func dispatchSignatureLinkPreview(payment *models.NominaPayment, user *models.User, signingURL string) string {
	if payment == nil || user == nil || strings.TrimSpace(signingURL) == "" {
		return "preview_only"
	}

	// El número principal viene de "celular"; fallback a username para compatibilidad.
	number := normalizeWhatsAppNumber(user.Celular)
	if number == "" {
		number = normalizeWhatsAppNumber(user.Username)
	}
	if number == "" {
		return "missing_phone"
	}

	displayName := strings.TrimSpace(user.FullName)
	if displayName == "" {
		displayName = strings.TrimSpace(user.Name)
	}
	if displayName == "" {
		displayName = strings.TrimSpace(user.Username)
	}

	message := buildSignatureWhatsAppMessage(displayName, signingURL, payment.SignatureTokenExpiresAt)
	if err := notify.SendToNumber(number, message); err != nil {
		fmt.Printf("[NOMINA] no se pudo enviar link de firma pago=%d user=%d numero=%s: %v\n", payment.ID, user.ID, number, err)
		return "dispatch_error"
	}

	return "whatsapp_sent"
}

func buildSignedPDFDeliveryMessage(employeeName string) (caption string, message string) {
	name := strings.TrimSpace(employeeName)
	if name == "" {
		name = "equipo"
	}
	caption = "Comprobante de nomina firmado"
	message = fmt.Sprintf("Hola %s. Aqui tienes tu comprobante de nomina firmado.", name)
	return caption, message
}

func dispatchSignedPDFToEmployee(payment *models.NominaPayment, user *models.User, signedPDFPath string) string {
	if payment == nil || user == nil || strings.TrimSpace(signedPDFPath) == "" {
		return "preview_only"
	}

	number := normalizeWhatsAppNumber(user.Celular)
	if number == "" {
		number = normalizeWhatsAppNumber(user.Username)
	}
	if number == "" {
		return "missing_phone"
	}

	pdfBytes, err := os.ReadFile(signedPDFPath)
	if err != nil {
		fmt.Printf("[NOMINA] no se pudo leer PDF firmado pago=%d path=%s: %v\n", payment.ID, signedPDFPath, err)
		return "pdf_read_error"
	}

	displayName := strings.TrimSpace(user.FullName)
	if displayName == "" {
		displayName = strings.TrimSpace(user.Name)
	}
	if displayName == "" {
		displayName = strings.TrimSpace(user.Username)
	}

	caption, message := buildSignedPDFDeliveryMessage(displayName)
	pdfName := fmt.Sprintf("Comprobante_Firmado_%d.pdf", payment.ID)
	if err := notify.SendPDFToNumber(number, pdfBytes, pdfName, caption, message); err != nil {
		fmt.Printf("[NOMINA] no se pudo enviar PDF firmado pago=%d user=%d numero=%s: %v\n", payment.ID, user.ID, number, err)
		return "dispatch_error"
	}

	return "pdf_sent"
}

// --- CONFIGURATION ---

// GetNominaConfig obtiene la configuración global
func GetNominaConfig(c *gin.Context) {
	var config []models.NominaConfig
	// Asumimos ID=1 para la configuración global
	DB.Limit(1).Find(&config, 1)

	if len(config) == 0 {
		// Retornar defaults si no existe
		c.JSON(http.StatusOK, models.NominaConfig{
			AuxilioTransporte: 162000,
			ValorDominical:    100000,
			ValorDominicalS1:  100000,
			ValorDominicalS2:  100000,
			PorcentajeSalud:   4.0,
			PorcentajePension: 4.0,
		})
		return
	}
	c.JSON(http.StatusOK, config[0])
}

// UpdateNominaConfig actualiza valores globales
func UpdateNominaConfig(c *gin.Context) {
	var input models.NominaConfig
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var config models.NominaConfig
	if err := DB.First(&config, 1).Error; err != nil {
		config.ID = 1
	}

	config.AuxilioTransporte = input.AuxilioTransporte
	config.ValorDominical = input.ValorDominical
	config.ValorDominicalS1 = input.ValorDominicalS1
	config.ValorDominicalS2 = input.ValorDominicalS2
	config.ValorMadrugon = input.ValorMadrugon
	config.PorcentajeSalud = input.PorcentajeSalud
	config.PorcentajePension = input.PorcentajePension
	config.SalarioMinimo = input.SalarioMinimo
	config.CompanyName = input.CompanyName
	config.NIT = input.NIT
	config.UpdatedAt = time.Now()

	DB.Save(&config)
	c.JSON(http.StatusOK, config)
}

// --- EMPLOYEES ---

// GetNominaEmployees lista usuarios con su info de nómina optimizado
func GetNominaEmployees(c *gin.Context) {
	var users []models.User
	if err := DB.Order("name asc").Find(&users).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Batch fetch all payroll records to avoid N+1 problem
	var payrolls []models.UserPayroll
	DB.Find(&payrolls)
	payrollMap := make(map[uint]*models.UserPayroll)
	for i := range payrolls {
		payrollMap[payrolls[i].UserID] = &payrolls[i]
	}

	type EmployeePayrollDTO struct {
		models.User
		Payroll *models.UserPayroll `json:"payroll"`
	}

	var result []EmployeePayrollDTO
	for _, u := range users {
		result = append(result, EmployeePayrollDTO{
			User:    u,
			Payroll: payrollMap[u.ID],
		})
	}

	c.JSON(http.StatusOK, result)
}

// UpdateEmployeeDetails actualiza detalles del empleado (Salario, Info Legal)
func UpdateEmployeeDetails(c *gin.Context) {
	id := c.Param("id")
	userID, _ := strconv.Atoi(id)

	var input struct {
		BaseSalary  *int64  `json:"base_salary"` // Pointer to check nil if not updating
		DailyRate   *int64  `json:"daily_rate"`  // Valor por día (solo para pay_type=daily)
		HourlyRate  *int64  `json:"hourly_rate"` // Valor por hora (solo para pay_type=madrugones)
		HasSecurity *bool   `json:"has_security"`
		PayType     *string `json:"pay_type"` // "fixed", "daily" o "madrugones"
		FullName    *string `json:"full_name"`
		Cedula      *string `json:"cedula"`
		Celular     *string `json:"celular"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 1. Update Payroll Info (Base Salary, Daily/Hourly Rate, Security & Pay Type)
	if input.BaseSalary != nil || input.DailyRate != nil || input.HourlyRate != nil || input.HasSecurity != nil || input.PayType != nil {
		var p models.UserPayroll
		if err := DB.First(&p, userID).Error; err != nil {
			p.UserID = uint(userID)
		}
		if input.BaseSalary != nil {
			p.BaseSalary = *input.BaseSalary
		}
		if input.DailyRate != nil {
			p.DailyRate = *input.DailyRate
		}
		if input.HourlyRate != nil {
			p.HourlyRate = *input.HourlyRate
		}
		if input.HasSecurity != nil {
			p.HasSecurity = input.HasSecurity
		}
		if input.PayType != nil {
			p.PayType = *input.PayType
		}
		p.UpdatedAt = time.Now()
		DB.Save(&p)
	}

	// 2. Update Personal Info (User)
	if input.FullName != nil || input.Cedula != nil || input.Celular != nil {
		var u models.User
		if err := DB.First(&u, userID).Error; err == nil {
			if input.FullName != nil {
				u.FullName = *input.FullName
			}
			if input.Cedula != nil {
				u.Cedula = *input.Cedula
			}
			if input.Celular != nil {
				u.Celular = strings.TrimSpace(*input.Celular)
			}
			DB.Save(&u)
		}
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// --- PAYMENTS ---

// GeneratePayment calcula y guarda un pago de nómina
func GeneratePayment(c *gin.Context) {
	var input struct {
		UserID               uint      `json:"user_id" binding:"required"`
		PeriodStart          time.Time `json:"period_start"`
		PeriodEnd            time.Time `json:"period_end"`
		DaysWorked           int       `json:"days_worked"`  // Días trabajados (solo para pay_type=daily)
		HoursWorked          float64   `json:"hours_worked"` // Horas trabajadas (solo para pay_type=madrugones)
		SundaysQty           int       `json:"sundays_qty"`
		MadrugonesQty        float64   `json:"madrugones_qty"`
		Advance              int64     `json:"advance"`
		Commission           int64     `json:"commission"` // Comisión por administración POS (solo 2da quincena)
		IsPartial            bool      `json:"is_partial"` // Si es pago parcial (sin comisión aún)
		IncludesSecurity     bool      `json:"includes_security"`
		IncludesTransportAid *bool     `json:"includes_transport_aid"`
		Aditions             string    `json:"aditions"`   // JSON string
		Deductions           string    `json:"deductions"` // JSON string
		Notes                string    `json:"notes"`
		CreatedBy            string    `json:"created_by"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if input.PeriodStart.IsZero() || input.PeriodEnd.IsZero() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Periodo inválido"})
		return
	}
	if input.PeriodStart.After(input.PeriodEnd) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Periodo inválido: fecha inicial mayor a fecha final"})
		return
	}

	periodStartUTC := input.PeriodStart.UTC()
	periodEndUTC := input.PeriodEnd.UTC()
	periodNum := resolvePayrollFortnight(periodStartUTC, periodEndUTC)
	periodYear := periodStartUTC.Year()
	periodMonth := int(periodStartUTC.Month())

	excluded, err := isUserExcludedFromNominaPeriod(periodYear, periodMonth, periodNum, input.UserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo validar la inclusión del empleado"})
		return
	}
	if excluded {
		c.JSON(http.StatusConflict, gin.H{"error": "El empleado está excluido de esta quincena"})
		return
	}

	// 1. Obtener Config Global
	var global models.NominaConfig
	if err := DB.First(&global, 1).Error; err != nil {
		// Defaults si falla config
		global.AuxilioTransporte = 0
		global.ValorDominical = 0
		global.ValorMadrugon = 10000
	}
	if global.ValorMadrugon == 0 {
		global.ValorMadrugon = 10000
	}

	// 2. Obtener Info Empleado
	var payroll models.UserPayroll
	if err := DB.First(&payroll, input.UserID).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "El empleado no tiene salario base configurado"})
		return
	}

	// 3. Cálculos
	payType := payroll.PayType
	if payType == "" {
		payType = "fixed"
	}

	daysWorkedSnapshot := input.DaysWorked
	if daysWorkedSnapshot < 0 {
		daysWorkedSnapshot = 0
	}
	hoursWorkedSnapshot := input.HoursWorked
	if hoursWorkedSnapshot < 0 {
		hoursWorkedSnapshot = 0
	}

	var paidBase int64
	switch payType {
	case "daily":
		// Pago por días: Valor por día * Días trabajados
		paidBase = payroll.DailyRate * int64(daysWorkedSnapshot)
	case "madrugones":
		// Pago por horas: Valor por hora * Horas trabajadas
		paidBase = int64(hoursWorkedSnapshot * float64(payroll.HourlyRate))
	default:
		// Salario Fijo Quincenal = Base / 2
		paidBase = payroll.BaseSalary / 2
	}

	// Auxilio Transporte Quincenal = Auxilio / 2 (aplica para fijo y madrugones; no aplica para daily)
	var transport int64
	includesTransportAid := payType != "daily"
	if input.IncludesTransportAid != nil {
		includesTransportAid = payType != "daily" && *input.IncludesTransportAid
	}
	if includesTransportAid {
		transport = global.AuxilioTransporte / 2
	}

	// Defaults if missing
	if global.PorcentajeSalud == 0 {
		global.PorcentajeSalud = 4.0
	}
	if global.PorcentajePension == 0 {
		global.PorcentajePension = 4.0
	}

	// Salud and Pension (Dynamic %)
	var health, pension int64
	if input.IncludesSecurity {
		health = int64(float64(paidBase) * (global.PorcentajeSalud / 100))
		pension = int64(float64(paidBase) * (global.PorcentajePension / 100))
	} else {
		health = 0
		pension = 0
	}

	sundaysQtySnapshot := 0
	madrugonesQtySnapshot := float64(0)
	var sundaysTotal int64
	var madrugonesTotal int64
	if payType == "fixed" {
		sundaysQtySnapshot = input.SundaysQty
		if sundaysQtySnapshot < 0 {
			sundaysQtySnapshot = 0
		}
		madrugonesQtySnapshot = input.MadrugonesQty
		if madrugonesQtySnapshot < 0 {
			madrugonesQtySnapshot = 0
		}

		// Dominicales (S1/S2 según mes, con fallback al valor legacy)
		sundayValue := resolveSundayValueByPeriod(global, input.PeriodStart)
		sundaysTotal = int64(sundaysQtySnapshot) * sundayValue

		// Madrugones (recargos sobre salario fijo)
		madrugonesTotal = int64(madrugonesQtySnapshot * float64(global.ValorMadrugon))
	}

	// Ajustes manuales
	aditionsTotal, err := sumNominaAdjustments(input.Aditions)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Formato inválido en aditions"})
		return
	}
	deductionsTotal, err := sumNominaAdjustments(input.Deductions)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Formato inválido en deductions"})
		return
	}

	// Total
	// Base + Transporte + Dominicales + Madrugones + Comisión + Adiciones - Salud - Pensión - Adelantos - Deducciones
	totalPaid := paidBase + transport + sundaysTotal + madrugonesTotal + input.Commission + aditionsTotal - health - pension - input.Advance - deductionsTotal

	// 4. Determinar si el pago debe ser parcial:
	//    - Solo 2da quincena (día > 15) puede ser parcial
	//    - Solo si el empleado tiene comisión asignada (EmployeePOSAssignment con CommissionPercentage > 0)
	//    - Primera quincena SIEMPRE se completa de inmediato
	//    - Sin comisión asignada SIEMPRE se completa de inmediato
	isPartial := false
	is2ndFortnight := periodNum == 2
	if is2ndFortnight {
		// Verificar si el empleado tiene comisión asignada en algún POS
		var hasCommission int64
		DB.Model(&models.EmployeePOSAssignment{}).
			Where("user_id = ? AND commission_percentage > 0", input.UserID).
			Count(&hasCommission)
		if hasCommission > 0 {
			isPartial = true
		}
	}

	// 5. Guardar Pago
	payment := models.NominaPayment{
		UserID:               input.UserID,
		PeriodStart:          input.PeriodStart,
		PeriodEnd:            input.PeriodEnd,
		BaseSalary:           payroll.BaseSalary,
		DailyRate:            payroll.DailyRate,
		HourlyRate:           payroll.HourlyRate,
		PayType:              payType,
		DaysWorked:           daysWorkedSnapshot,
		HoursWorked:          hoursWorkedSnapshot,
		PaidBase:             paidBase,
		TransportAid:         transport,
		SundaysQty:           sundaysQtySnapshot,
		SundaysTotal:         sundaysTotal,
		MadrugonesQty:        madrugonesQtySnapshot,
		MadrugonesTotal:      madrugonesTotal,
		IncludesTransportAid: includesTransportAid,
		IncludesSecurity:     input.IncludesSecurity,
		Health:               health,
		Pension:              pension,
		Advance:              input.Advance,
		Commission:           input.Commission,
		IsPartial:            isPartial,
		Aditions:             input.Aditions,
		Deductions:           input.Deductions,
		TotalPaid:            totalPaid,
		Notes:                input.Notes,
		CreatedBy:            input.CreatedBy,
		CreatedAt:            time.Now(),
	}

	if err := DB.Create(&payment).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error guardando pago"})
		return
	}

	c.JSON(http.StatusOK, payment)
}

// UploadSignedContract sube un contrato firmado
func UploadSignedContract(c *gin.Context) {
	id := c.Param("id")

	// Validar que existe el pago
	var payment models.NominaPayment
	if err := DB.First(&payment, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Pago no encontrado"})
		return
	}

	// Recibir archivo
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Archivo no enviado"})
		return
	}

	// Guardar en disco
	// Asegurar que existe uploads/
	// (Debe crearse al inicio o aqui mismo)

	// Nombre único: payment_ID_timestamp.pdf
	filename := fmt.Sprintf("payment_%s_%d.pdf", id, time.Now().Unix())
	dst := filepath.Join("uploads", filename)

	if err := c.SaveUploadedFile(file, dst); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error guardando archivo"})
		return
	}

	// Actualizar BD
	payment.IsSigned = true
	payment.SignedFile = "/uploads/" + filename
	DB.Save(&payment)

	c.JSON(http.StatusOK, payment)
}

// CreatePaymentSignLink genera un link temporal de firma para un pago
func CreatePaymentSignLink(c *gin.Context) {
	id := c.Param("id")

	var payment models.NominaPayment
	if err := DB.First(&payment, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Pago no encontrado"})
		return
	}
	if payment.IsSigned {
		c.JSON(http.StatusConflict, gin.H{"error": "El pago ya está firmado"})
		return
	}
	if payment.IsPartial {
		c.JSON(http.StatusConflict, gin.H{"error": "Debes completar la comisión antes de enviar el comprobante"})
		return
	}

	var user models.User
	if err := DB.First(&user, payment.UserID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Empleado no encontrado"})
		return
	}
	if normalizeCedula(user.Cedula) == "" {
		c.JSON(http.StatusConflict, gin.H{"error": "El empleado no tiene cédula registrada"})
		return
	}

	token, tokenHash, err := generateSignatureToken()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo generar el link de firma"})
		return
	}

	now := time.Now()
	expiresAt := now.Add(signatureTokenTTL)
	requestedBy := strings.TrimSpace(c.GetHeader("X-Actor-Username"))
	if requestedBy == "" {
		requestedBy = "Sistema"
	}

	payment.SignatureTokenHash = tokenHash
	payment.SignatureTokenExpiresAt = &expiresAt
	payment.SignatureLinkSentAt = &now
	payment.SignatureRequestedBy = requestedBy

	if err := DB.Save(&payment).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo registrar el link de firma"})
		return
	}

	signingURL := fmt.Sprintf("%s/firma/%s", strings.TrimRight(resolveRequestBaseURL(c), "/"), token)
	dispatchMode := dispatchSignatureLinkPreview(&payment, &user, signingURL)

	c.JSON(http.StatusOK, gin.H{
		"payment_id":    payment.ID,
		"signing_url":   signingURL,
		"expires_at":    expiresAt,
		"dispatch_mode": dispatchMode,
	})
}

// AccessPaymentSigningLink valida token + cédula y retorna el contexto para firmar
func AccessPaymentSigningLink(c *gin.Context) {
	token := strings.TrimSpace(c.Param("token"))
	if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Token requerido"})
		return
	}

	var input struct {
		Cedula string `json:"cedula" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Cédula requerida"})
		return
	}

	payment, err := getSignaturePaymentByToken(token)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Link inválido o expirado"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo validar el link"})
		return
	}

	if err := ensureSignablePayment(payment); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	var user models.User
	if err := DB.First(&user, payment.UserID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Empleado no encontrado"})
		return
	}

	if normalizeCedula(user.Cedula) == "" {
		c.JSON(http.StatusConflict, gin.H{"error": "La cédula del empleado no está configurada"})
		return
	}
	if normalizeCedula(input.Cedula) != normalizeCedula(user.Cedula) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Cédula incorrecta"})
		return
	}

	var cfg models.NominaConfig
	DB.First(&cfg, 1)

	c.JSON(http.StatusOK, gin.H{
		"payment": payment,
		"user": gin.H{
			"id":        user.ID,
			"name":      user.Name,
			"full_name": user.FullName,
			"cedula":    user.Cedula,
		},
		"config": gin.H{
			"company_name":       cfg.CompanyName,
			"nit":                cfg.NIT,
			"porcentaje_salud":   cfg.PorcentajeSalud,
			"porcentaje_pension": cfg.PorcentajePension,
		},
		"expires_at": payment.SignatureTokenExpiresAt,
	})
}

// CompletePaymentSignature valida token + cédula y confirma la firma cargando PDF firmado
func CompletePaymentSignature(c *gin.Context) {
	token := strings.TrimSpace(c.Param("token"))
	if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Token requerido"})
		return
	}

	cedula := strings.TrimSpace(c.PostForm("cedula"))
	if cedula == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Cédula requerida"})
		return
	}

	payment, err := getSignaturePaymentByToken(token)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Link inválido o expirado"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo validar el link"})
		return
	}
	if err := ensureSignablePayment(payment); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	var user models.User
	if err := DB.First(&user, payment.UserID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Empleado no encontrado"})
		return
	}
	if normalizeCedula(cedula) != normalizeCedula(user.Cedula) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Cédula incorrecta"})
		return
	}

	file, err := c.FormFile("signed_pdf")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Archivo signed_pdf requerido"})
		return
	}
	if file.Size <= 0 || file.Size > maxSignedPDFSize {
		c.JSON(http.StatusBadRequest, gin.H{"error": "El PDF firmado debe ser menor o igual a 5MB"})
		return
	}
	if !strings.HasSuffix(strings.ToLower(file.Filename), ".pdf") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "El archivo debe ser PDF"})
		return
	}

	reader, err := file.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No se pudo leer el archivo"})
		return
	}
	defer reader.Close()

	header := make([]byte, 5)
	n, readErr := io.ReadFull(reader, header)
	if readErr != nil || n < 5 || string(header) != "%PDF-" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Archivo PDF inválido"})
		return
	}

	if err := os.MkdirAll("uploads", 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo preparar el almacenamiento"})
		return
	}

	filename := fmt.Sprintf("payment_signed_%d_%d.pdf", payment.ID, time.Now().Unix())
	dst := filepath.Join("uploads", filename)
	if err := c.SaveUploadedFile(file, dst); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo guardar el PDF firmado"})
		return
	}

	now := time.Now()
	payment.IsSigned = true
	payment.SignedFile = "/uploads/" + filename
	payment.SignedAt = &now
	payment.SignedIP = c.ClientIP()
	payment.SignedUserAgent = strings.TrimSpace(c.Request.UserAgent())
	payment.SignatureMethod = signatureAuditLabel
	payment.SignatureTokenHash = ""
	payment.SignatureTokenExpiresAt = nil

	if err := DB.Save(payment).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo confirmar la firma"})
		return
	}
	pdfDispatch := dispatchSignedPDFToEmployee(payment, &user, dst)

	c.JSON(http.StatusOK, gin.H{
		"status":       "signed",
		"signed_file":  payment.SignedFile,
		"pdf_dispatch": pdfDispatch,
	})
}

// DeleteNominaPayment elimina un pago de nómina
func DeleteNominaPayment(c *gin.Context) {
	id := c.Param("id")
	if err := DB.Delete(&models.NominaPayment{}, id).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error eliminando pago"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Pago eliminado"})
}

// --- ODOO INTEGRATION ---

// GetOdooPOSConfigs lista los POS disponibles
func GetOdooPOSConfigs(c *gin.Context) {
	client, err := atmOdoo.NewFromEnv()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := client.Authenticate(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Odoo Auth: " + err.Error()})
		return
	}
	configs, err := client.ListPOSConfigs()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, configs)
}

// GetOdooSessions obtiene sesiones filtradas
func GetOdooSessions(c *gin.Context) {
	posIDStr := c.Query("pos_id")
	startStr := c.Query("start")
	endStr := c.Query("end")

	posID, _ := strconv.Atoi(posIDStr)
	start, err1 := time.Parse(time.RFC3339, startStr)
	end, err2 := time.Parse(time.RFC3339, endStr)

	if posID == 0 || err1 != nil || err2 != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid params"})
		return
	}

	client, err := atmOdoo.NewFromEnv()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := client.Authenticate(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Odoo Auth: " + err.Error()})
		return
	}

	sessions, err := client.GetPOSSessions(posID, start, end)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, sessions)
}

// GetNominaHistory obtiene historial de pagos
func GetNominaHistory(c *gin.Context) {
	var history []models.NominaPayment
	DB.Preload("User").Order("created_at desc").Limit(50).Find(&history)
	c.JSON(http.StatusOK, history)
}

// SetNominaPeriodInclusion incluye/excluye un empleado de una quincena específica.
func SetNominaPeriodInclusion(c *gin.Context) {
	var input struct {
		Year     int   `json:"year" binding:"required"`
		Month    int   `json:"month" binding:"required,min=1,max=12"`
		Period   int   `json:"period" binding:"required,oneof=1 2"`
		UserID   uint  `json:"user_id" binding:"required"`
		Included *bool `json:"included"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if input.Included == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "included es requerido"})
		return
	}

	if *input.Included {
		// Incluir: eliminar exclusión explícita si existe.
		if err := DB.Where("year = ? AND month = ? AND period = ? AND user_id = ?", input.Year, input.Month, input.Period, input.UserID).
			Delete(&models.NominaPeriodExclusion{}).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo actualizar la inclusión"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok", "included": true})
		return
	}

	// Excluir: crear exclusión si no existe.
	exclusion := models.NominaPeriodExclusion{
		Year:   input.Year,
		Month:  input.Month,
		Period: input.Period,
		UserID: input.UserID,
	}

	var existing models.NominaPeriodExclusion
	if err := DB.Where("year = ? AND month = ? AND period = ? AND user_id = ?", input.Year, input.Month, input.Period, input.UserID).
		First(&existing).Error; err == nil {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "included": false})
		return
	}

	if err := DB.Create(&exclusion).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo actualizar la inclusión"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok", "included": false})
}

// --- PARTIAL PAYMENT COMMISSION ---

// UpdatePaymentCommission agrega comisión a un pago parcial y recalcula el total
func UpdatePaymentCommission(c *gin.Context) {
	id := c.Param("id")

	var input struct {
		Commission *int64 `json:"commission"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if input.Commission == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "commission es requerido"})
		return
	}
	if *input.Commission < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "commission no puede ser negativo"})
		return
	}

	var payment models.NominaPayment
	if err := DB.First(&payment, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Pago no encontrado"})
		return
	}

	if !payment.IsPartial {
		c.JSON(http.StatusConflict, gin.H{"error": "Este pago no es parcial"})
		return
	}

	// Agregar comisión al total existente
	payment.Commission = *input.Commission
	payment.TotalPaid = payment.TotalPaid + *input.Commission
	payment.IsPartial = false

	if err := DB.Save(&payment).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error actualizando pago"})
		return
	}

	c.JSON(http.StatusOK, payment)
}

// --- MATRIX REPORT ---

// NominaMatrixDTO estructura de respuesta para la matriz
type NominaMatrixDTO struct {
	Users            interface{}                    `json:"users"` // []EmployeePayrollDTO
	Payments         []models.NominaPayment         `json:"payments"`
	Stats            map[string]map[int]int64       `json:"stats"` // [month][period] -> total_paid
	PeriodExclusions []models.NominaPeriodExclusion `json:"period_exclusions"`
}

// GetNominaMatrix retorna datos para el grid anual de pagos
func GetNominaMatrix(c *gin.Context) {
	yearStr := c.Query("year")
	year, _ := strconv.Atoi(yearStr)
	if year == 0 {
		year = time.Now().Year()
	}

	// 1. Obtener empleados activos
	// 1. Obtener empleados activos con su info de nómina
	type EmployeePayrollDTO struct {
		models.User
		Payroll *models.UserPayroll `json:"payroll"`
	}
	var users []models.User
	if err := DB.Order("name asc").Find(&users).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error cargando empleados"})
		return
	}

	// Batch fetch payrolls
	var payrolls []models.UserPayroll
	DB.Find(&payrolls)
	payrollMap := make(map[uint]*models.UserPayroll)
	for i := range payrolls {
		payrollMap[payrolls[i].UserID] = &payrolls[i]
	}

	var userDTOs []EmployeePayrollDTO
	for _, u := range users {
		userDTOs = append(userDTOs, EmployeePayrollDTO{
			User:    u,
			Payroll: payrollMap[u.ID], // Will be nil if not found, which is fine
		})
	}

	// 2. Obtener pagos del año
	startYear := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	endYear := time.Date(year+1, 1, 1, 0, 0, 0, 0, time.UTC)

	var payments []models.NominaPayment
	if err := DB.Preload("User").
		Where("period_start >= ? AND period_start < ?", startYear, endYear).
		Order("period_start asc").
		Find(&payments).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error cargando pagos"})
		return
	}

	var periodExclusions []models.NominaPeriodExclusion
	if err := DB.Where("year = ?", year).Find(&periodExclusions).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error cargando exclusiones de quincena"})
		return
	}

	// 3. Generar estadísticas simples para el grid (Frontend puede calcular detalle)
	// Pero ayudamos con totales por quincena
	stats := make(map[string]map[int]int64)

	for _, p := range payments {
		month := p.PeriodStart.Month().String() // "January", etc
		period := resolvePayrollFortnight(p.PeriodStart, p.PeriodEnd)

		if stats[month] == nil {
			stats[month] = make(map[int]int64)
		}
		stats[month][period] += p.TotalPaid
	}

	c.JSON(http.StatusOK, NominaMatrixDTO{
		Users:            userDTOs,
		Payments:         payments,
		Stats:            stats,
		PeriodExclusions: periodExclusions,
	})
}
