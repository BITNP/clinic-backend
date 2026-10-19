package httpx

import (
	"net/http"
)

// IsMutatingMethod reports whether a method can change application data.
func IsMutatingMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}
