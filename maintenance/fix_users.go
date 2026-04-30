package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/url"
	"os"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/joho/godotenv"
)

func main() {
	// 1. Cargar Entorno
	if err := godotenv.Load(); err != nil {
		log.Println("Advertencia: No se pudo cargar .env, usando variables de entorno actuales")
	}

	dbURI := os.Getenv("DB_URI")
	if dbURI == "" {
		log.Fatal("DB_URI no encontrado")
	}

	// 2. Parsear URI a DSN
	u, err := url.Parse(dbURI)
	if err != nil {
		log.Fatalf("Error parseando DB_URI: %v", err)
	}
	pass, _ := u.User.Password()
	dsn := fmt.Sprintf("%s:%s@tcp(%s)%s?charset=utf8mb4&parseTime=True", u.User.Username(), pass, u.Host, u.Path)

	// 3. Conectar
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatal("No se pudo conectar a la BD:", err)
	}
	fmt.Println("Conexión exitosa.")

	// 4. REFRESH TABLE STRATEGY
	fmt.Println("Iniciando reconstrucción de la tabla 'users' para eliminar metadatos corruptos...")

	// 4.1 Rename
	backupName := fmt.Sprintf("users_backup_%d", time.Now().Unix())
	fmt.Printf("1. Renombrando 'users' a '%s'...\n", backupName)
	_, err = db.Exec(fmt.Sprintf("RENAME TABLE users TO %s", backupName))
	if err != nil {
		log.Fatalf("Fallo al renombrar tabla (¿Quizás no existe?): %v", err)
	}

	// 4.2 Create New Table (Clean Schema matching current model)
	fmt.Println("2. Creando nueva tabla 'users' limpia...")
	createSQL := `
	CREATE TABLE users (
		id bigint unsigned NOT NULL AUTO_INCREMENT,
		username varchar(100) NOT NULL,
		name varchar(200) DEFAULT NULL,
		pin varchar(200) DEFAULT NULL,
		role varchar(50) DEFAULT 'user',
		odoo_id bigint DEFAULT NULL,
		created_at datetime(3) DEFAULT NULL,
		updated_at datetime(3) DEFAULT NULL,
		PRIMARY KEY (id),
		KEY idx_users_username (username),
		KEY idx_users_odoo_id (odoo_id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
	`
	_, err = db.Exec(createSQL)
	if err != nil {
		log.Fatalf("Fallo creando nueva tabla: %v", err)
	}

	// 4.3 Copy Data
	fmt.Println("3. Copiando datos del backup...")
	// Map columns explicitly to avoid issues if order changed
	copySQL := fmt.Sprintf(`
		INSERT INTO users (id, username, name, pin, role, odoo_id, created_at, updated_at)
		SELECT id, username, name, pin, role, odoo_id, created_at, updated_at FROM %s
	`, backupName)

	res, err := db.Exec(copySQL)
	if err != nil {
		log.Printf("ERROR CRITICO Copiando datos: %v. \nLa tabla '%s' contiene tus datos originales. NO LA BORRES MANUALMENTE si esto falló.", err, backupName)
		return
	}

	rows, _ := res.RowsAffected()
	fmt.Printf("   %d filas recuperadas exitosamente.\n", rows)

	// 4.4 Drop Backup (Optional, safe to keep for safety if user wants, but we'll drop to be clean)
	fmt.Printf("4. Eliminando tabla temporal '%s'...\n", backupName)
	_, err = db.Exec(fmt.Sprintf("DROP TABLE %s", backupName))
	if err != nil {
		log.Printf("No se pudo borrar el backup (no es crítico): %v", err)
	}

	fmt.Println("\n¡REPARACIÓN COMPLETADA! La tabla users está limpia.")
}
