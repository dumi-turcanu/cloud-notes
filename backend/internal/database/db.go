// Package database owns the PostgreSQL connection pool, the embedded schema
// migrations and the repositories.
package database

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // registers the pgx5:// driver
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dumi-turcanu/cloud-notes/backend/internal/config"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// NewPool creates the connection pool (honouring DB_MAX_CONNS) and verifies
// connectivity.
func NewPool(ctx context.Context, cfg config.Config) (*pgxpool.Pool, error) {
	pcfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	pcfg.MaxConns = int32(cfg.DBMaxConns)

	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// Migrate applies all pending migrations. golang-migrate holds a PostgreSQL
// advisory lock, so concurrent replicas are safe.
func Migrate(ctx context.Context, cfg config.Config) error {
	return migrateUp(ctx, cfg.DatabaseURL)
}

func migrateUp(ctx context.Context, dsn string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	src, err := iofs.New(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("load embedded migrations: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, migrateURL(dsn))
	if err != nil {
		return fmt.Errorf("init migrate: %w", err)
	}
	defer func() { _, _ = m.Close() }()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

// migrateURL rewrites postgres:// to the pgx5:// scheme of the pgx/v5 driver.
func migrateURL(dsn string) string {
	if u, err := url.Parse(dsn); err == nil && (u.Scheme == "postgres" || u.Scheme == "postgresql") {
		return "pgx5" + strings.TrimPrefix(dsn, u.Scheme)
	}
	return dsn
}
