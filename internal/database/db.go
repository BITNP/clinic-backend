package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Driver identifies the SQL dialect used by the configured database.
type Driver string

const (
	DriverSQLite   Driver = "sqlite"
	DriverPostgres Driver = "postgres"
)

// DB carries the standard SQL handle together with the active dialect for the
// few queries that require driver-specific SQL, such as row locking.
type DB struct {
	*sql.DB
	Driver Driver
}

// Wrap attaches a dialect to an existing SQL handle.
func Wrap(db *sql.DB, driver Driver) *DB {
	return &DB{DB: db, Driver: driver}
}

// WithTx runs fn in a transaction and commits only when fn succeeds.
func WithTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	if err := fn(tx); err != nil {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			return fmt.Errorf("%w (rollback: %v)", err, rollbackErr)
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// Placeholders builds a comma-separated PostgreSQL-style placeholder list
// starting at start, for example Placeholders(2, 3) returns "$2, $3, $4".
func Placeholders(start, count int) string {
	if count <= 0 {
		return ""
	}
	parts := make([]string, count)
	for i := range parts {
		parts[i] = fmt.Sprintf("$%d", start+i)
	}
	return strings.Join(parts, ", ")
}
