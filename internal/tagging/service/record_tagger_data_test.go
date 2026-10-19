package service_test

import (
	"strings"
	"testing"

	taggingservice "clinic-backend/internal/tagging/service"
)

func TestRecordTagRows_LoadsEmbeddedCSV(t *testing.T) {
	rows, err := taggingservice.RecordTagRows()
	if err != nil {
		t.Fatalf("load record tag rows: %v", err)
	}
	if len(rows) != 16 {
		t.Fatalf("loaded %d rows, want 16", len(rows))
	}

	var found bool
	for _, row := range rows {
		if row.Title == "" {
			t.Errorf("row has an empty title: %+v", row)
		}
		if row.Description == "" {
			t.Errorf("row %q has an empty description", row.Title)
		}
		if row.ApplyRule == "" {
			t.Errorf("row %q has an empty apply rule", row.Title)
		}
		if row.Title == "风扇故障" {
			found = true
			if !strings.Contains(row.Description, "风扇") {
				t.Errorf("风扇故障 description = %q", row.Description)
			}
			if !strings.Contains(row.ApplyRule, "风扇") {
				t.Errorf("风扇故障 apply rule = %q", row.ApplyRule)
			}
		}
	}
	if !found {
		t.Error("expected to find the 风扇故障 row")
	}
}

func TestRecordTagRows_CachesResult(t *testing.T) {
	first, err := taggingservice.RecordTagRows()
	if err != nil {
		t.Fatalf("load record tag rows: %v", err)
	}
	second, err := taggingservice.RecordTagRows()
	if err != nil {
		t.Fatalf("load record tag rows again: %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("cached rows length = %d, want %d", len(second), len(first))
	}
}

func TestRecordTaggerPrompt_LoadsEmbeddedText(t *testing.T) {
	prompt := taggingservice.RecordTaggerPrompt()
	if prompt == "" {
		t.Fatal("expected a non-empty prompt")
	}
	for _, want := range []string{"电脑维修报修分类助手", "【分类目标】", "【输出要求】"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}
