// Command tcomtester serves TCOM conformance challenges to models under
// test, and a separate password-protected dashboard for the operator.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"log"
	"net/http"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address for the challenge API")
	admin := flag.String("admin", "127.0.0.1:8081", "listen address for the dashboard")
	adminKey := flag.String("admin-key", "", "dashboard password (random if empty)")
	logPath := flag.String("log", "tester.jsonl", "submission log, replayed at startup")
	flag.Parse()

	if *adminKey == "" {
		b := make([]byte, 9)
		rand.Read(b)
		*adminKey = hex.EncodeToString(b)
	}

	s, err := OpenStore(*logPath)
	if err != nil {
		log.Fatal(err)
	}

	go func() {
		log.Printf("dashboard on http://%s/ (any user name, password %s)", *admin, *adminKey)
		d := &Dashboard{s: s, password: *adminKey}
		log.Fatal(http.ListenAndServe(*admin, d.routes()))
	}()
	log.Printf("challenge API on %s with %d challenges", *addr, len(catalog))
	api := &API{s: s}
	log.Fatal(http.ListenAndServe(*addr, api.routes()))
}
