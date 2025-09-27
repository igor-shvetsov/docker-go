package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
	"context"
	"os/signal"
	"syscall"

	_ "github.com/lib/pq"
)

// Config структура для конфигурации
type Config struct {
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string
	ServerPort string
}

func loadConfig() *Config {
	return &Config{
		DBHost:     getEnv("DB_HOST", "db"),
		DBPort:     getEnv("DB_PORT", "5432"),
		DBUser:     getEnv("DB_USER", "golang"),  // Исправлено на golang
		DBPassword: getEnv("DB_PASSWORD", "goland"),
		DBName:     getEnv("DB_NAME", "goland"),
		ServerPort: getEnv("SERVER_PORT", "8080"),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func connectDB(cfg *Config) (*sql.DB, error) {
	connStr := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, err
	}

	// Увеличиваем таймаут для Docker Compose (БД может запускаться медленнее)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Пытаемся подключиться с повторными попытками
	for i := 0; i < 5; i++ {
		if err := db.PingContext(ctx); err == nil {
			break
		}
		log.Printf("⚠️  Attempt %d: Waiting for database connection...", i+1)
		time.Sleep(2 * time.Second)
	}

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to connect to database after retries: %v", err)
	}

	log.Println("✅ Successfully connected to PostgreSQL")
	return db, nil
}

func createTable(db *sql.DB) error {
	query := `
	CREATE TABLE IF NOT EXISTS users (
		id SERIAL PRIMARY KEY,
		name VARCHAR(100) NOT NULL,
		email VARCHAR(100) UNIQUE NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	`
	_, err := db.Exec(query)
	return err
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	response := fmt.Sprintf(
		`{"message": "Hello from Go API!", "timestamp": "%s", "status": "running"}`,
		time.Now().Format(time.RFC3339),
	)
	w.Write([]byte(response))
}

func healthHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if err := db.Ping(); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			response := fmt.Sprintf(
				`{"status": "error", "message": "Database connection failed: %s"}`,
				err.Error(),
			)
			w.Write([]byte(response))
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "healthy", "database": "connected", "service": "go-api"}`))
	}
}

func main() {
	log.Println("🚀 Starting Go application...")

	cfg := loadConfig()
	log.Printf("📋 Configuration: DB=%s@%s:%s/%s", 
		cfg.DBUser, cfg.DBHost, cfg.DBPort, cfg.DBName)

	// Подключаемся к БД с повторными попытками
	var db *sql.DB
	var err error

	for i := 0; i < 10; i++ {
		db, err = connectDB(cfg)
		if err == nil {
			break
		}
		log.Printf("⏳ Database not ready, retrying... (%d/10)", i+1)
		time.Sleep(2 * time.Second)
	}

	if err != nil {
		log.Fatalf("❌ Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Устанавливаем ограничения на соединения
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(25)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := createTable(db); err != nil {
		log.Fatalf("❌ Failed to create table: %v", err)
	}
	log.Println("✅ Database table created/verified")

	// Настраиваем маршруты
	http.HandleFunc("/", homeHandler)
	http.HandleFunc("/health", healthHandler(db))

	// Сервер
	server := &http.Server{
		Addr:         ":" + cfg.ServerPort,
		Handler:      nil,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Запуск в горутине
	go func() {
		log.Printf("🌐 Server starting on http://0.0.0.0:%s", cfg.ServerPort)
		log.Printf("📊 Health check: http://0.0.0.0:%s/health", cfg.ServerPort)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("❌ Server error: %v", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("🛑 Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("❌ Server shutdown error: %v", err)
	}

	log.Println("✅ Server stopped gracefully")
}