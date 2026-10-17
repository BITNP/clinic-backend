package tests

import (
	"testing"

	"clinic-backend/services"
)

func TestLiquidMetalRows_LoadsEmbeddedCSV(t *testing.T) {
	rows, err := services.LiquidMetalRows()
	if err != nil {
		t.Fatalf("load liquid metal rows: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected at least one row")
	}

	var found bool
	for _, row := range rows {
		if row.List == "" {
			t.Errorf("row has empty list: %+v", row)
		}
		if row.Code == "GX701L" {
			found = true
			if row.List != "液金正表" {
				t.Errorf("GX701L list = %q, want 液金正表", row.List)
			}
			if row.GPU != "RTX 2080 SUPER" {
				t.Errorf("GX701L GPU = %q, want RTX 2080 SUPER", row.GPU)
			}
			if row.Brand != "华硕 ROG" {
				t.Errorf("GX701L brand = %q, want 华硕 ROG", row.Brand)
			}
		}
	}
	if !found {
		t.Error("expected to find the GX701L row")
	}
}

func TestLiquidMetalRows_CachesResult(t *testing.T) {
	first, err := services.LiquidMetalRows()
	if err != nil {
		t.Fatalf("load liquid metal rows: %v", err)
	}
	second, err := services.LiquidMetalRows()
	if err != nil {
		t.Fatalf("load liquid metal rows again: %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("cached rows length = %d, want %d", len(second), len(first))
	}
}
