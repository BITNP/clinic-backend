package model_test

import (
	"testing"

	roommodel "clinic-backend/internal/room/model"
)

func TestRoomType_Valid(t *testing.T) {
	tests := []struct {
		name string
		typ  roommodel.RoomType
		want bool
	}{
		{name: "normal", typ: roommodel.RoomTypeNormal, want: true},
		{name: "dynamic", typ: roommodel.RoomTypeDynamic, want: true},
		{name: "auto select", typ: roommodel.RoomTypeAutoSelect, want: true},
		{name: "empty", typ: "", want: false},
		{name: "unknown", typ: "unknown", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.typ.Valid(); got != tc.want {
				t.Errorf("Valid() = %v, want %v", got, tc.want)
			}
		})
	}
}
