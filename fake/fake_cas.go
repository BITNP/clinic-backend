//go:build ignore

package main

import (
	"fmt"
	"net/http"
)

func main() {
	http.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		service := r.URL.Query().Get("service")
		// Simulate the user already authenticated at CAS.
		http.Redirect(w, r, service+"&ticket=ST-MANUAL-123", http.StatusFound)
	})
	http.HandleFunc("/serviceValidate", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, `<?xml version="1.0"?>
<cas:serviceResponse xmlns:cas="http://www.yale.edu/tp/cas">
  <cas:authenticationSuccess>
    <cas:user>manualuser</cas:user>
    <cas:attributes>
      <cas:name>Manual Tester</cas:name>
      <cas:groups>/clinic</cas:groups>
      <cas:groups>/management</cas:groups>
    </cas:attributes>
  </cas:authenticationSuccess>
</cas:serviceResponse>`)
	})
	http.HandleFunc("/logout", func(w http.ResponseWriter, r *http.Request) {
		returnURL := r.URL.Query().Get("url")
		http.Redirect(w, r, returnURL, http.StatusFound)
	})
	fmt.Println("Fake CAS listening on http://127.0.0.1:9999")
	http.ListenAndServe("127.0.0.1:9999", nil)
}
