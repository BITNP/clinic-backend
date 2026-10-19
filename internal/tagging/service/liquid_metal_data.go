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

// liquidMetalCSVColumns is the number of columns in the liquid-metal laptop
// table: 所属列表, 品牌, 年份, 型号或情形, 型号代码或CPU限定, GPU配置, 配置限制与提醒, 参考链接.
const liquidMetalCSVColumns = 8

// LiquidMetalRow is one row of the liquid-metal laptop table as loaded from
// the embedded CSV.
type LiquidMetalRow struct {
	List      string // 所属列表: 液金正表, 待核实附表 or 易混淆列表
	Brand     string // 品牌
	Year      string // 年份
	Model     string // 型号或情形
	Code      string // 型号代码或CPU限定
	GPU       string // GPU配置
	Notes     string // 配置限制与提醒
	Reference string // 参考链接
}

var (
	liquidMetalRowsOnce sync.Once
	liquidMetalRows     []LiquidMetalRow
	liquidMetalRowsErr  error
)

// LiquidMetalRows returns the embedded liquid-metal laptop table parsed into
// memory. The CSV is parsed once and the result is reused on later calls.
func LiquidMetalRows() ([]LiquidMetalRow, error) {
	liquidMetalRowsOnce.Do(func() {
		liquidMetalRows, liquidMetalRowsErr = parseLiquidMetalCSV(liquidMetalCSV)
	})
	return liquidMetalRows, liquidMetalRowsErr
}

// parseLiquidMetalCSV decodes the table and trims whitespace from every field.
// The header row is validated but not returned.
func parseLiquidMetalCSV(data []byte) ([]LiquidMetalRow, error) {
	// The file starts with a UTF-8 BOM, which would otherwise end up in the
	// first header field.
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))

	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = liquidMetalCSVColumns

	if _, err := r.Read(); err != nil {
		return nil, fmt.Errorf("parse liquid metal csv header: %w", err)
	}

	rows := make([]LiquidMetalRow, 0, 200)
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse liquid metal csv row %d: %w", len(rows)+2, err)
		}
		rows = append(rows, LiquidMetalRow{
			List:      strings.TrimSpace(rec[0]),
			Brand:     strings.TrimSpace(rec[1]),
			Year:      strings.TrimSpace(rec[2]),
			Model:     strings.TrimSpace(rec[3]),
			Code:      strings.TrimSpace(rec[4]),
			GPU:       strings.TrimSpace(rec[5]),
			Notes:     strings.TrimSpace(rec[6]),
			Reference: strings.TrimSpace(rec[7]),
		})
	}
	if len(rows) == 0 {
		return nil, errors.New("parse liquid metal csv: no rows")
	}
	return rows, nil
}
