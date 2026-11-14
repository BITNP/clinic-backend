package model

// RoomType classifies how a clinic room is used.
type RoomType string

const (
	RoomTypeNormal     RoomType = "normal"
	RoomTypeDynamic    RoomType = "dynamic"
	RoomTypeAutoSelect RoomType = "auto_select"
)

// Valid reports whether t is a supported room type.
func (t RoomType) Valid() bool {
	switch t {
	case RoomTypeNormal, RoomTypeDynamic, RoomTypeAutoSelect:
		return true
	}
	return false
}
