// Package sql holds standalone SQL scripts that are embedded into the binary.
package sql

import _ "embed"

// RecordTagCatalog seeds the built-in record tags with their descriptions and
// natural-language apply rules. It is idempotent across SQLite and Postgres
// via ON CONFLICT.
//
//go:embed record_tag_catalog.sql
var RecordTagCatalog string
