package database

import (
	"database/sql"
	"embed"
	"fmt"
	"log"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/lib/pq"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func Connect() (*sql.DB, error) {
	host := getEnv("DB_HOST", "localhost")
	port := getEnv("DB_PORT", "5432")
	user := getEnv("DB_USER", "lsat_user")
	password := getEnv("DB_PASSWORD", "lsat_password")
	dbname := getEnv("DB_NAME", "lsat_prep")
	sslmode := getEnv("DB_SSLMODE", "disable")

	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		host, port, user, password, dbname, sslmode,
	)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)

	return db, nil
}

func RunMigrations(db *sql.DB) error {
	sourceDriver, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("failed to create migration source: %w", err)
	}

	dbDriver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		return fmt.Errorf("failed to create migration db driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", sourceDriver, "postgres", dbDriver)
	if err != nil {
		return fmt.Errorf("failed to create migrate instance: %w", err)
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("migration failed: %w", err)
	}

	version, dirty, _ := m.Version()
	log.Printf("Database migration version: %d (dirty: %v)", version, dirty)

	return nil
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

// --- Username generation (used by auth/handler.go) ---

var rng = rand.New(rand.NewSource(time.Now().UnixNano()))

func generateUsernameBase(name string) string {
	var result []byte
	for _, c := range strings.ToLower(name) {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			result = append(result, byte(c))
		}
	}
	if len(result) == 0 {
		return "user"
	}
	if len(result) > 12 {
		result = result[:12]
	}
	return string(result)
}

func randomInt(max int) int {
	return rng.Intn(max)
}

// GenerateUsername creates a unique username from a name by appending random digits.
// Caller should handle the unique constraint.
func GenerateUsername(name string) string {
	base := generateUsernameBase(name)
	return fmt.Sprintf("%s%04d", base, randomInt(10000))
}
