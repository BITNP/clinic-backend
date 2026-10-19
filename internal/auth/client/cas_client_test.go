package client_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	authclient "clinic-backend/internal/auth/client"
)

func TestCASClient_LoginURL(t *testing.T) {
	client := authclient.NewCASClient("https://cas.example.edu", "url", 5*time.Second)
	loc := client.LoginURL("https://app.example.edu/login?next=%2Fmanage%2F")
	wantPrefix := "https://cas.example.edu/login?service="
	if !strings.HasPrefix(loc, wantPrefix) {
		t.Fatalf("unexpected login url: %s", loc)
	}
	encodedService := strings.TrimPrefix(loc, wantPrefix)
	service, err := url.QueryUnescape(encodedService)
	if err != nil {
		t.Fatalf("decode service: %v", err)
	}
	u, err := url.Parse(service)
	if err != nil {
		t.Fatalf("parse service: %v", err)
	}
	if u.Path != "/login" {
		t.Errorf("path: got %q", u.Path)
	}
	if u.Query().Get("next") != "/manage/" {
		t.Errorf("next: got %q", u.Query().Get("next"))
	}
}

func TestCASClient_LogoutURL(t *testing.T) {
	client := authclient.NewCASClient("https://cas.example.edu", "url", 5*time.Second)
	loc := client.LogoutURL("https://app.example.edu/manage/")
	if !strings.HasPrefix(loc, "https://cas.example.edu/logout?url=") {
		t.Errorf("unexpected logout url: %s", loc)
	}

	clientService := authclient.NewCASClient("https://cas.example.edu", "service", 5*time.Second)
	loc2 := clientService.LogoutURL("https://app.example.edu/manage/")
	if !strings.HasPrefix(loc2, "https://cas.example.edu/logout?service=") {
		t.Errorf("unexpected logout url with service param: %s", loc2)
	}
}

func TestCASClient_ValidateTicket_Success(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/serviceValidate" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		ticket := r.URL.Query().Get("ticket")
		service := r.URL.Query().Get("service")
		if ticket != "ST-123" {
			t.Errorf("ticket: got %q", ticket)
		}
		if service != "https://app.example.edu/login?next=/manage/" {
			t.Errorf("service: got %q", service)
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = fmt.Fprint(w, `
<cas:serviceResponse xmlns:cas='http://www.yale.edu/tp/cas'>
  <cas:authenticationSuccess>
    <cas:user>student42</cas:user>
    <cas:attributes>
      <cas:preferred_username>student42</cas:preferred_username>
      <cas:name>Alice Smith</cas:name>
      <cas:groups>/management</cas:groups>
      <cas:groups>/clinic</cas:groups>
    </cas:attributes>
  </cas:authenticationSuccess>
</cas:serviceResponse>`)
	}))
	defer fake.Close()

	client := authclient.NewCASClient(fake.URL, "url", 5*time.Second)
	attrs, err := client.ValidateTicket("ST-123", "https://app.example.edu/login?next=/manage/")
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if attrs.User != "student42" {
		t.Errorf("user: got %q", attrs.User)
	}
	if attrs.Realname != "Alice Smith" {
		t.Errorf("realname: got %q", attrs.Realname)
	}
	if len(attrs.Groups) != 2 {
		t.Errorf("groups: got %+v", attrs.Groups)
	}
}

func TestCASClient_ValidateTicket_CommaGroups(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = fmt.Fprint(w, `
<cas:serviceResponse xmlns:cas='http://www.yale.edu/tp/cas'>
  <cas:authenticationSuccess>
    <cas:user>student42</cas:user>
    <cas:attributes>
      <cas:name>Alice Smith</cas:name>
      <cas:groups>/clinic, /management</cas:groups>
    </cas:attributes>
  </cas:authenticationSuccess>
</cas:serviceResponse>`)
	}))
	defer fake.Close()

	client := authclient.NewCASClient(fake.URL, "url", 5*time.Second)
	attrs, err := client.ValidateTicket("ST-123", "https://app.example.edu/login")
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(attrs.Groups) != 2 {
		t.Errorf("groups: got %+v", attrs.Groups)
	}
}

func TestCASClient_ValidateTicket_Failure(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `
<cas:serviceResponse xmlns:cas='http://www.yale.edu/tp/cas'>
  <cas:authenticationFailure code="INVALID_TICKET">Ticket not recognized</cas:authenticationFailure>
</cas:serviceResponse>`)
	}))
	defer fake.Close()

	client := authclient.NewCASClient(fake.URL, "url", 5*time.Second)
	_, err := client.ValidateTicket("ST-bad", "https://app.example.edu/login")
	if err == nil {
		t.Fatal("expected error for invalid ticket")
	}
}

func TestCASClient_ValidateTicket_Non200(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer fake.Close()

	client := authclient.NewCASClient(fake.URL, "url", 5*time.Second)
	_, err := client.ValidateTicket("ST-123", "https://app.example.edu/login")
	if err == nil {
		t.Fatal("expected error for non-200 cas response")
	}
}
