// Package repository tiene el acceso a datos (PostgreSQL) de admin.
package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/discordia-grupo01/admin/internal/config"
	_ "github.com/lib/pq"
)

const (
	connectAttempts = 5
	connectWait     = 2 * time.Second
)

// Open abre el pool de conexiones y espera a que Postgres responda (en
// docker-compose la base puede tardar unos segundos más que el servicio).
func Open(ctx context.Context, cfg config.Database) (*sql.DB, error) {
	db, err := sql.Open("postgres", cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("abrir el driver de Postgres: %w", err)
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	var pingErr error
	for attempt := 1; attempt <= connectAttempts; attempt++ {
		if pingErr = db.PingContext(ctx); pingErr == nil {
			return db, nil
		}
		select {
		case <-ctx.Done():
			_ = db.Close()
			return nil, ctx.Err()
		case <-time.After(connectWait):
		}
	}
	_ = db.Close()
	return nil, fmt.Errorf("Postgres no respondió tras %d intentos: %w", connectAttempts, pingErr)
}
