package main

import (
	"log"
	"net/http"
	"os"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	"atm/controllers"
	"atm/database"
	"atm/docs"
)

// @title ATM API
// @version 1.0
// @description API para gestionar categorias, transacciones, caja y logs. Documentación generada con swaggo.
// @contact.name Soporte
// @contact.email soporte@example.com
// @host localhost:8080
// @BasePath /
func main() {
	// Leer puerto de variable de entorno (Coolify suele usar PORT)
	port := os.Getenv("API_PORT")
	if port == "" {
		port = os.Getenv("PORT")
	}
	if port == "" {
		port = "8080"
	}

	// Inicializar conexión a la base de datos
	db, err := database.Connect()
	if err != nil {
		log.Fatalf("failed to connect database: %v", err)
	}
	// asignar DB a controladores
	controllers.SetDB(db)

	// documentación (swag) - valores por defecto, sobreescribibles por swag init
	docs.SwaggerInfo.Title = "ATM API"
	docs.SwaggerInfo.Description = "API para gestionar el sistema ATM"
	docs.SwaggerInfo.Version = "1.0"
	// usar puerto efectivo
	docs.SwaggerInfo.Host = "localhost:" + port
	docs.SwaggerInfo.BasePath = "/"

	r := gin.Default()

	// Habilitar CORS
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "apikey"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
	}))

	// Rutas de la API
	api := r.Group("/api")
	{
		controllers.RegisterCategoriaRoutes(api)
		controllers.RegisterTransaccionRoutes(api)
		controllers.RegisterCajaRoutes(api)
		controllers.RegisterLogsRoutes(api)
		controllers.RegisterResumenRoutes(api)
		controllers.RegisterNotifyRoutes(api)
		controllers.RegisterOdooRoutes(api)
		controllers.RegisterBillingRoutes(api)
		controllers.RegisterCuentaRoutes(api)
		controllers.RegisterLimpiarRoutes(api)
		controllers.RegisterGastosRoutes(api)
		controllers.RegisterUserRoutes(api)
		controllers.RegisterConfigRoutes(api)
		controllers.RegisterNominaRoutes(api)
		controllers.RegisterCacheRoutes(api)
	}

	// Swagger UI en /swagger/index.html
	r.Static("/uploads", "./uploads")
	r.GET("/firma/:token", controllers.ServePaymentSigningPage)
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	// endpoint health
	r.GET("/", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	if err := r.Run(":" + port); err != nil {
		log.Fatalf("failed to run server: %v", err)
	}
}
