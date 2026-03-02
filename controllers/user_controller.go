package controllers

import (
	"atm/models"
	"atm/odoo"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// RegisterUserRoutes registra las rutas de usuarios y auth
func RegisterUserRoutes(r *gin.RouterGroup) {
	users := r.Group("/users")
	{
		users.GET("", GetUsers)
		users.POST("/sync", SyncUsers)
		users.POST("", CreateUser)
		users.PUT("/:username", UpdateUser)
		users.DELETE("/:username", DeleteUser)
	}

	r.POST("/login", Login)
}

// GetUsers lista los usuarios
func GetUsers(c *gin.Context) {
	var users []models.User
	if err := DB.Find(&users).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error cargando usuarios"})
		return
	}
	// Ocultar PIN
	for i := range users {
		users[i].PIN = ""
	}
	c.JSON(http.StatusOK, users)
}

// SyncUsers sincroniza empleados de Odoo como usuarios del sistema
func SyncUsers(c *gin.Context) {
	client, err := odoo.NewFromEnv()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error de configuración Odoo: " + err.Error()})
		return
	}

	if err := client.Authenticate(); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Error autenticando con Odoo: " + err.Error()})
		return
	}

	employees, err := client.FetchEmployees()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error obteniendo empleados: " + err.Error()})
		return
	}

	count := 0
	updated := 0

	for _, emp := range employees {
		// PIN es la contraseña. Si no tiene PIN, no puede loguear, pero lo importamos igual si se desea
		// Odoo pin puede venir como string o boolean false
		var pinStr string
		if p, ok := emp.Pin.(string); ok {
			pinStr = p
		}

		// Determinar rol basado en JobID o nombre (lógica simple por ahora, todos 'user', 'dev' se mantiene manual)
		// Si job_id array [id, "Nombre Puesto"]
		role := "user"
		// Ejemplo para asignar admin si puesto contiene "Gerente" o similar
		// Por ahora default "user"

		// Intentar buscar por OdooID
		var existing models.User
		res := DB.Where("odoo_id = ?", emp.ID).First(&existing)

		if res.RowsAffected > 0 {
			// Update
			existing.Name = emp.Name
			if pinStr != "" {
				existing.PIN = pinStr
			}
			// Username se mantiene el de Odoo (NombreNormalizado) o se actualiza?
			// Mejor normalizar nombre a username si no existe conflicto
			DB.Save(&existing)
			updated++
		} else {
			// Create
			// Generar username unico
			username := normalizeUsername(emp.Name)
			// Check si username ya existe (por colision o usuario manual)
			var check models.User
			if DB.Where("username = ?", username).First(&check).RowsAffected > 0 {
				username = fmt.Sprintf("%s_%d", username, emp.ID)
			}

			user := models.User{
				Username: username,
				Name:     emp.Name,
				PIN:      pinStr,
				Role:     role,
				OdooID:   emp.ID,
			}
			DB.Create(&user)
			count++
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message": fmt.Sprintf("Sincronización completa. Nuevos: %d, Actualizados: %d", count, updated),
		"total":   len(employees),
	})
}

// Login maneja autenticación simple contra MySQL
func Login(c *gin.Context) {
	var creds struct {
		Username string `json:"username"`
		PIN      string `json:"pin"`
	}
	if err := c.ShouldBindJSON(&creds); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Datos inválidos"})
		return
	}

	var user models.User
	if err := DB.Where("username = ?", creds.Username).First(&user).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Usuario no encontrado"})
		return
	}

	// Comparación directa de PIN (Odoo guarda PIN en texto plano usualmente en hr.employee)
	// Si quisiéramos usar hash, deberíamos hashear al sincronizar o comparar hash aquí.
	// Asumimos texto plano por simplicidad y compatibilidad con lo que viene de Odoo (que viene plano o tal vez hash si odoo config).
	// El requerimiento dice: "la contraseña sera el campo pin de ese modelo"
	if user.PIN != creds.PIN {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "PIN incorrecto"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"username":    user.Username,
		"displayName": user.Name,
		"role":        user.Role,
	})
}

// CRUD básicos

func CreateUser(c *gin.Context) {
	var body struct {
		Username   string `json:"username"`
		Name       string `json:"name"`
		FullName   string `json:"full_name"`
		Cedula     string `json:"cedula"`
		Celular    string `json:"celular"`
		PIN        string `json:"pin"`
		Role       string `json:"role"`
		PayType    string `json:"pay_type"`    // "fixed" or "daily" (optional)
		BaseSalary int64  `json:"base_salary"` // optional
		DailyRate  int64  `json:"daily_rate"`  // optional
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if body.Username == "" || body.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username y name son requeridos"})
		return
	}

	u := models.User{
		Username: body.Username,
		Name:     body.Name,
		FullName: body.FullName,
		Cedula:   body.Cedula,
		Celular:  body.Celular,
		PIN:      body.PIN,
		Role:     body.Role,
	}
	if u.Role == "" {
		u.Role = "user"
	}

	if err := DB.Create(&u).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error creando usuario: " + err.Error()})
		return
	}

	// Crear UserPayroll si se especificó pay_type
	if body.PayType == "daily" || body.PayType == "fixed" {
		payroll := models.UserPayroll{
			UserID:     u.ID,
			PayType:    body.PayType,
			BaseSalary: body.BaseSalary,
			DailyRate:  body.DailyRate,
		}
		DB.Create(&payroll)
	}

	c.JSON(http.StatusOK, u)
}

func UpdateUser(c *gin.Context) {
	username := c.Param("username")
	var u models.User
	if err := DB.Where("username = ?", username).First(&u).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "No existe"})
		return
	}
	var patch map[string]interface{}
	if err := c.BindJSON(&patch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Bad JSON"})
		return
	}
	DB.Model(&u).Updates(patch)
	c.JSON(http.StatusOK, u)
}

func DeleteUser(c *gin.Context) {
	username := c.Param("username")

	// Buscar usuario primero para obtener ID
	var user models.User
	if err := DB.Where("username = ?", username).First(&user).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Usuario no encontrado"})
		return
	}

	// Eliminar registros relacionados (cascade manual, solo BD local)
	DB.Where("user_id = ?", user.ID).Delete(&models.UserPayroll{})
	DB.Where("user_id = ?", user.ID).Delete(&models.EmployeePOSAssignment{})
	DB.Where("user_id = ?", user.ID).Delete(&models.BillingNominaAssignment{})
	DB.Where("user_id = ?", user.ID).Delete(&models.NominaPayment{})

	// Eliminar usuario
	if err := DB.Delete(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error eliminando usuario"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted", "username": username})
}

func normalizeUsername(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "")
	// remover tildes simple
	tr := strings.NewReplacer("á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ñ", "n")
	return tr.Replace(s)
}
