package database

import (
	"strings"
)

// IsUniqueViolation detects unique-constraint violations across the supported drivers
// (Postgres code "23505" / message "unique constraint"; SQLite "UNIQUE constraint failed").
func IsUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "23505") ||
		strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(strings.ToLower(msg), "unique constraint")
}
