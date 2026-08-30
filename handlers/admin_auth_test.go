package handlers

import (
	"reflect"
	"testing"
)

func TestExtractWorkYears(t *testing.T) {
	tests := []struct {
		name   string
		groups []string
		want   []int
	}{
		{name: "single year group", groups: []string{"2025-clinic"}, want: []int{2025}},
		{name: "slash prefixed year group", groups: []string{"/2024-management"}, want: []int{2024}},
		{name: "multiple year groups", groups: []string{"2025-clinic", "2024-management"}, want: []int{2025, 2024}},
		{name: "dedupe duplicate years", groups: []string{"2025-clinic", "2025-management"}, want: []int{2025}},
		{name: "no year format", groups: []string{"/clinic", "/management"}, want: nil},
		{name: "no dash", groups: []string{"2025"}, want: nil},
		{name: "non-numeric year", groups: []string{"year-clinic"}, want: nil},
		{name: "short year", groups: []string{"25-clinic"}, want: nil},
		{name: "out of range year", groups: []string{"123-clinic", "10000-clinic"}, want: nil},
		{name: "empty department", groups: []string{"2025-"}, want: nil},
		{name: "empty groups", groups: nil, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractWorkYears(tt.groups)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("extractWorkYears(%v) = %v, want %v", tt.groups, got, tt.want)
			}
		})
	}
}
