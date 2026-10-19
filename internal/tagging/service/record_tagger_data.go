package service

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

// recordTagCSVColumns is the number of columns in the record tag catalog:
// title, description, apply_rule.
const recordTagCSVColumns = 3

// RecordTagRow is one row of the record tag catalog as loaded from the
// embedded CSV.
type RecordTagRow struct {
	Title       string // title
	Description string // description
	ApplyRule   string // apply_rule
}

var (
	recordTagRowsOnce sync.Once
	recordTagRows     []RecordTagRow
	recordTagRowsErr  error
)

// RecordTagRows returns the embedded record tag catalog parsed into memory.
// The CSV is parsed once and the result is reused on later calls.
func RecordTagRows() ([]RecordTagRow, error) {
	recordTagRowsOnce.Do(func() {
		recordTagRows, recordTagRowsErr = parseRecordTagCSV(recordTagCSV)
	})
	return recordTagRows, recordTagRowsErr
}

// RecordTaggerPrompt returns the embedded prompt registered for the record tag
// set.
func RecordTaggerPrompt() string {
	return recordTaggerPrompt
}

// parseRecordTagCSV decodes the catalog and trims whitespace from every field.
// The header row is validated but not returned.
func parseRecordTagCSV(data []byte) ([]RecordTagRow, error) {
	// The file may start with a UTF-8 BOM, which would otherwise end up in the
	// first header field.
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))

	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = recordTagCSVColumns

	if _, err := r.Read(); err != nil {
		return nil, fmt.Errorf("parse record tag csv header: %w", err)
	}

	rows := make([]RecordTagRow, 0, 32)
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse record tag csv row %d: %w", len(rows)+2, err)
		}
		row := RecordTagRow{
			Title:       strings.TrimSpace(rec[0]),
			Description: strings.TrimSpace(rec[1]),
			ApplyRule:   strings.TrimSpace(rec[2]),
		}
		if row.Title == "" {
			return nil, fmt.Errorf("parse record tag csv row %d: empty title", len(rows)+2)
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, errors.New("parse record tag csv: no rows")
	}
	return rows, nil
}
