// Command mockprovider runs the local Brevo/Mailjet imitation used by docker
// compose. See internal/mockprovider for the failure modes.
package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/cadentic/portfolio-cms/backend/internal/mockprovider"
)

func main() {
	addr := os.Getenv("MOCK_ADDR")
	if addr == "" {
		addr = ":8025"
	}
	m := mockprovider.New(os.Getenv("MOCK_WEBHOOK_BREVO_URL"), os.Getenv("MOCK_WEBHOOK_MAILJET_URL"))
	log.Printf("mock email providers on %s (UI at http://localhost%s/)", addr, addr)
	log.Fatal((&http.Server{Addr: addr, Handler: m.Handler(), ReadHeaderTimeout: 10 * time.Second}).ListenAndServe())
}
