package testutil

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"time"
)

const TicketAPIKey = "ticket-fake-secret"

func SignedTicketReq(method, path string, body any, user string) *http.Request {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	if user == "" {
		user = "1120221234"
	}
	date := time.Now().UTC().Format(time.RFC1123)
	sig := SignKey(TicketAPIKey, user, date)
	// Append the trusted username query param the middleware reads.
	sep := "?"
	if has := len(path) > 0 && path[len(path)-1] == '?'; has {
		sep = ""
	}
	if uq := path + sep + "username=" + user; true {
		path = uq
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Date", date)
	req.Header.Set("X-API-KEY", sig)
	return req
}
