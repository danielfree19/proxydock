// Package db owns the Postgres connection pool and the embedded SQL
// migration runner.
//
// Migrations live in db/migrations/NNN_*.sql. They are applied in
// lexicographic order inside a single transaction per file and recorded
// in schema_migrations.
package db

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Open returns a pgx connection pool, blocking briefly until Postgres is
// reachable so the manager can race Postgres on startup without crashing.
func Open(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse DSN: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	return pool, nil
}

// Migration is a single embedded SQL file.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

func loadMigrations() ([]Migration, error) {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return nil, err
	}
	out := make([]Migration, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		v, err := versionFromFilename(e.Name())
		if err != nil {
			return nil, fmt.Errorf("migration %s: %w", e.Name(), err)
		}
		body, err := fs.ReadFile(migrationsFS, "migrations/"+e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, Migration{
			Version: v,
			Name:    e.Name(),
			SQL:     string(body),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })

	// Reject duplicate versions early so a typo doesn't silently skip work.
	for i := 1; i < len(out); i++ {
		if out[i].Version == out[i-1].Version {
			return nil, fmt.Errorf("duplicate migration version %d (%s and %s)",
				out[i].Version, out[i-1].Name, out[i].Name)
		}
	}
	return out, nil
}

func versionFromFilename(name string) (int, error) {
	parts := strings.SplitN(name, "_", 2)
	if len(parts) < 2 {
		return 0, errors.New("expected NNN_name.sql")
	}
	return strconv.Atoi(parts[0])
}

// Migrate applies every pending migration in order.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	migs, err := loadMigrations()
	if err != nil {
		return err
	}

	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
		    version    INT         PRIMARY KEY,
		    name       TEXT        NOT NULL,
		    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	rows, err := pool.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("select schema_migrations: %w", err)
	}
	applied := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		applied[v] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, m := range migs {
		if applied[m.Version] {
			continue
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, m.SQL); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply %s: %w", m.Name, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`,
			m.Version, m.Name); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record %s: %w", m.Name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit %s: %w", m.Name, err)
		}
	}
	return nil
}
