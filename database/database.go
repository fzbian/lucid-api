package database

import (
	"atm/models"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlogger "gorm.io/gorm/logger"
)

// Convierte un URI tipo mysql://user:pass@host:port/db a DSN para GORM
func uriToDSN(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", err
	}
	if u.Scheme != "mysql" {
		return "", fmt.Errorf("solo se soporta mysql://")
	}
	user := u.User.Username()
	pass, _ := u.User.Password()
	host := u.Hostname()
	if host == "" {
		return "", fmt.Errorf("host vacío en DB_URI")
	}
	port := u.Port()
	if port == "" {
		port = "3306"
	}
	hostPort := net.JoinHostPort(host, port)
	db := u.Path
	if len(db) > 0 && db[0] == '/' {
		db = db[1:]
	}
	if db == "" {
		return "", fmt.Errorf("database vacío en DB_URI")
	}

	// charset, parseTime y loc son recomendados para GORM.
	params := u.Query()
	if params.Get("charset") == "" {
		params.Set("charset", "utf8mb4")
	}
	if params.Get("parseTime") == "" {
		params.Set("parseTime", "True")
	}
	if params.Get("loc") == "" {
		params.Set("loc", "Local")
	}

	return fmt.Sprintf("%s:%s@tcp(%s)/%s?%s", user, pass, hostPort, db, params.Encode()), nil
}

// Connect abre la conexión a la base de datos MySQL usando solo DB_URI
func Connect() (*gorm.DB, error) {
	_ = godotenv.Load()

	dburi := strings.TrimSpace(os.Getenv("DB_URI"))
	if dburi == "" {
		dburi = strings.TrimSpace(os.Getenv("MYSQL_URI"))
		if dburi != "" {
			log.Printf("[DB] DB_URI vacío, usando MYSQL_URI")
		}
	}
	if dburi == "" {
		return nil, fmt.Errorf("DB_URI/MYSQL_URI no está definido en el entorno")
	}
	dsn, err := uriToDSN(dburi)
	if err != nil {
		return nil, fmt.Errorf("DB_URI inválido: %w", err)
	}
	log.Printf("[DB] Conectando a %s", maskDBURI(dburi))
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
		// Evita migraciones implícitas por relaciones (ej: NominaPayment.User -> users),
		// que en MySQL pueden disparar DROP FOREIGN KEY erróneo sobre uni_users_username.
		IgnoreRelationshipsWhenMigrating: true,
		Logger:                           buildGormLogger(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to db: %w", err)
	}

	// Code-First Migration (skip if DB_SKIP_MIGRATIONS=1 for faster dev cycles)
	if os.Getenv("DB_SKIP_MIGRATIONS") == "1" {
		log.Println("[DB] DB_SKIP_MIGRATIONS=1 -> saltando migraciones y seeding")
	} else {
		log.Println("[DB] Ejecutando migraciones automáticas...")
		if err = db.AutoMigrate(
			&models.Caja{},
			&models.Categoria{},
			&models.Transaccion{},
			&models.TransaccionLog{},
			&models.GastoLocal{},
			&models.RoleConfig{},
			&models.NominaConfig{},
			&models.UserPayroll{},
			&models.NominaPayment{},
			&models.NominaPeriodExclusion{},
			&models.BillingMonthly{},
			&models.BillingConfig{},
			&models.BillingFixedCost{},
			&models.BillingGastoExclusion{},
			&models.EmployeePOSAssignment{},
			&models.BillingNominaAssignment{},
		); err != nil {
			if isKnownUsersMigrationBug(err) {
				log.Printf("[DB] Warning: se omite reconciliación de índice users.username por bug de AutoMigrate en MySQL (%v)", err)
			} else {
				return nil, fmt.Errorf("fallo migracion automatica: %w", err)
			}
		}

		if err := safeAutoMigrateUsers(db); err != nil {
			return nil, fmt.Errorf("fallo migracion automatica (users): %w", err)
		}

		// Seeding (bloqueante para garantizar datos base al arrancar)
		if err := Seed(db); err != nil {
			return nil, fmt.Errorf("fallo seeding: %w", err)
		}
	}

	if err := BackfillGastoImageURLs(db); err != nil {
		return nil, fmt.Errorf("fallo backfill imagenes de gastos: %w", err)
	}

	return db, nil
}

// Seed puebla la base de datos con datos iniciales si está vacía
func Seed(db *gorm.DB) error {
	// 1. Cajas
	cajas := []models.Caja{
		{ID: 1, Saldo: 0},
		{ID: 2, Saldo: 0},
	}
	for _, c := range cajas {
		if err := db.FirstOrCreate(&c, models.Caja{ID: c.ID}).Error; err != nil {
			return err
		}
	}

	// 2. Categorias obligatorias (siempre presentes con IDs fijos)
	requiredCategorias := []models.Categoria{
		{ID: 15, Nombre: "Cartera de Clientes", Tipo: "INGRESO"},
		{ID: 16, Nombre: "Efectivo Puntos de Venta", Tipo: "INGRESO"},
		{ID: 17, Nombre: "Logstica y Transporte", Tipo: "INGRESO"},
		{ID: 18, Nombre: "Trading", Tipo: "INGRESO"},
		{ID: 19, Nombre: "Operacin Monetaria", Tipo: "INGRESO"},
		{ID: 20, Nombre: "Retiros Bancarios", Tipo: "INGRESO"},
		{ID: 21, Nombre: "Otros Ingresos", Tipo: "INGRESO"},
		{ID: 22, Nombre: "Pago a Proveedores", Tipo: "EGRESO"},
		{ID: 23, Nombre: "Gastos Operativos", Tipo: "EGRESO"},
		{ID: 24, Nombre: "Logstica y Transporte", Tipo: "EGRESO"},
		{ID: 25, Nombre: "Nomina y Beneficios", Tipo: "EGRESO"},
		{ID: 26, Nombre: "Obligaciones Bancarias", Tipo: "EGRESO"},
		{ID: 27, Nombre: "Operacin Monetaria", Tipo: "EGRESO"},
		{ID: 28, Nombre: "Otros Egresos", Tipo: "EGRESO"},
		{ID: 29, Nombre: "Ingreso dinero a banco", Tipo: "INGRESO"},
		{ID: 30, Nombre: "Retiros Bancarios", Tipo: "EGRESO"},
	}
	if err := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"nombre", "tipo"}),
	}).Create(&requiredCategorias).Error; err != nil {
		return fmt.Errorf("error asegurando categorias requeridas: %w", err)
	}

	// 3. Role Config (Admin)
	var adminRole models.RoleConfig
	if err := db.First(&adminRole, "role = ?", "admin").Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Create default admin
			encoded, _ := json.Marshal(models.DefaultAdminRoleViews())
			views := string(encoded)
			adminRole = models.RoleConfig{Role: "admin", Views: views}
			if err := db.Create(&adminRole).Error; err != nil {
				log.Printf("[DB] Error seeding admin role: %v", err)
			}
		}
	} else {
		current, err := parseRoleViews(adminRole.Views)
		if err != nil {
			current = []string{}
		}

		required := models.DefaultAdminRoleViews()
		next := append(current, required...)
		next = models.EnsureMandatoryRoleViews("admin", next)
		if !sameStringSet(current, next) {
			encoded, _ := json.Marshal(next)
			adminRole.Views = string(encoded)
			if err := db.Save(&adminRole).Error; err != nil {
				log.Printf("[DB] Error updating admin role views: %v", err)
			} else {
				log.Println("[DB] Admin role updated with required views")
			}
		}
	}

	return nil
}

func parseRoleViews(raw string) ([]string, error) {
	safe := strings.TrimSpace(raw)
	if safe == "" {
		return []string{}, nil
	}

	var direct []string
	if err := json.Unmarshal([]byte(safe), &direct); err == nil {
		return models.CanonicalizeRoleViews(direct), nil
	}

	var nested string
	if err := json.Unmarshal([]byte(safe), &nested); err != nil {
		return nil, err
	}

	var parsed []string
	if err := json.Unmarshal([]byte(nested), &parsed); err != nil {
		return nil, err
	}

	return models.CanonicalizeRoleViews(parsed), nil
}

func sameStringSet(a []string, b []string) bool {
	left := models.CanonicalizeRoleViews(a)
	right := models.CanonicalizeRoleViews(b)

	if len(left) != len(right) {
		return false
	}

	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}

	return true
}

func BackfillGastoImageURLs(db *gorm.DB) error {
	result := db.Model(&models.GastoLocal{}).
		Where("imagen_url IS NULL OR TRIM(imagen_url) = ''").
		Update("imagen_url", models.DefaultGastoImageURL)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		log.Printf("[DB] Backfill de imagen por defecto en gastos: %d registro(s)", result.RowsAffected)
	}
	return nil
}

func safeAutoMigrateUsers(db *gorm.DB) error {
	hasUsersTable := db.Migrator().HasTable(&models.User{})
	err := db.AutoMigrate(&models.User{})
	if err == nil {
		return nil
	}

	if hasUsersTable && isKnownUsersMigrationBug(err) {
		log.Printf("[DB] Warning: se omite reconciliación de índice users.username por bug de AutoMigrate en MySQL (%v)", err)
		return nil
	}

	return err
}

func isKnownUsersMigrationBug(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "uni_users_username") ||
		strings.Contains(msg, "DROP FOREIGN KEY `uni_users_username`") ||
		strings.Contains(msg, "Can't DROP 'uni_users_username'")
}

func buildGormLogger() gormlogger.Interface {
	level := strings.ToLower(strings.TrimSpace(os.Getenv("DB_LOG_LEVEL")))
	logLevel := gormlogger.Warn
	switch level {
	case "silent":
		logLevel = gormlogger.Silent
	case "error":
		logLevel = gormlogger.Error
	case "warn", "":
		logLevel = gormlogger.Warn
	case "info":
		logLevel = gormlogger.Info
	default:
		log.Printf("[DB] DB_LOG_LEVEL desconocido (%s), usando warn", level)
	}

	slowMs := 2000
	if raw := strings.TrimSpace(os.Getenv("DB_SLOW_SQL_MS")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			log.Printf("[DB] DB_SLOW_SQL_MS inválido (%s), usando %d", raw, slowMs)
		} else {
			slowMs = parsed
		}
	}

	return gormlogger.New(
		log.New(os.Stdout, "\r\n", log.LstdFlags),
		gormlogger.Config{
			SlowThreshold:             time.Duration(slowMs) * time.Millisecond,
			LogLevel:                  logLevel,
			IgnoreRecordNotFoundError: true,
			Colorful:                  false,
		},
	)
}

func maskDBURI(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<db_uri_invalida>"
	}
	if u.User != nil {
		user := u.User.Username()
		if _, hasPassword := u.User.Password(); hasPassword {
			u.User = url.UserPassword(user, "***")
		}
	}
	return u.String()
}
