//go:build ignore

package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
)

type fakeIdentity struct {
	User     string
	Realname string
	QQNumber string
	Realms   []string
	Groups   []string
}

func main() {
	role := flag.String("role", "admin", "role to serve: staff, admin, or superadmin")
	addr := flag.String("addr", "127.0.0.1:9999", "listen address")
	flag.Parse()

	identity, ok := identityForRole(strings.ToLower(strings.TrimSpace(*role)))
	if !ok {
		log.Fatalf("invalid role %q: choose staff, admin, or superadmin", *role)
	}

	http.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		service := r.URL.Query().Get("service")
		// Simulate the user already authenticated at CAS.
		http.Redirect(w, r, service+"&ticket=ST-MANUAL-123", http.StatusFound)
	})
	http.HandleFunc("/serviceValidate", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprintf(w, `<?xml version="1.0"?>
<cas:serviceResponse xmlns:cas="http://www.yale.edu/tp/cas">
  <cas:authenticationSuccess>
    <cas:user>%s</cas:user>
    <cas:attributes>
      <cas:name>%s</cas:name>
      <cas:qq-number>%s</cas:qq-number>
%s
    </cas:attributes>
  </cas:authenticationSuccess>
</cas:serviceResponse>`, identity.User, identity.Realname, identity.QQNumber, identity.attributesXML())
	})
	http.HandleFunc("/logout", func(w http.ResponseWriter, r *http.Request) {
		returnURL := r.URL.Query().Get("url")
		http.Redirect(w, r, returnURL, http.StatusFound)
	})
	fmt.Printf("Fake CAS listening on http://%s (role: %s, account: %s)\n", *addr, *role, identity.User)
	log.Fatal(http.ListenAndServe(*addr, nil))
}

func identityForRole(role string) (fakeIdentity, bool) {
	switch role {
	case "staff":
		return fakeIdentity{
			User:     "manual-staff",
			Realname: "Manual Staff",
			QQNumber: "123456781",
			Groups:   []string{"/clinic"},
		}, true
	case "admin":
		return fakeIdentity{
			User:     "manual-admin",
			Realname: "Manual Admin",
			QQNumber: "123456782",
			Groups:   []string{"/clinic", "/management"},
		}, true
	case "superadmin":
		return fakeIdentity{
			User:     "manual-superadmin",
			Realname: "Manual Superadmin",
			QQNumber: "123456783",
			Realms:   []string{"clinic-maintainer"},
			Groups:   []string{"/clinic"},
		}, true
	default:
		return fakeIdentity{}, false
	}
}

func (identity fakeIdentity) attributesXML() string {
	attributes := make([]string, 0, len(identity.Realms)+len(identity.Groups))
	for _, realm := range identity.Realms {
		attributes = append(attributes, "      <cas:realm>"+realm+"</cas:realm>")
	}
	for _, group := range identity.Groups {
		attributes = append(attributes, "      <cas:groups>"+group+"</cas:groups>")
	}
	return strings.Join(attributes, "\n")
}
