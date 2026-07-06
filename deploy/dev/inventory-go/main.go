// inventory — der BEYLA-GO-BEWEIS: ein stdlib-Go-Service OHNE eine Zeile
// Tracing-Code. Kein traceparent-Lesen, kein Weiterreichen, keine Header —
// Beyla instrumentiert net/http per uprobes und injiziert den W3C-Kontext
// selbst in den ausgehenden catalog-Call. Hängt dieser Trace zusammen
// (checkout → inventory → catalog), ist Zero-Instrumentation für Go bewiesen.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"time"
)

var redisAddr = envOr("REDIS_HOST", "redis") + ":6379"

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// redisCmd: minimaler RESP-Client (stdlib) — Beyla parst das Protokoll und
// erzeugt den DB-Client-Span von allein.
func redisCmd(args ...string) error {
	conn, err := net.DialTimeout("tcp", redisAddr, 1500*time.Millisecond)
	if err != nil {
		return err
	}
	defer conn.Close()
	payload := fmt.Sprintf("*%d\r\n", len(args))
	for _, a := range args {
		payload += fmt.Sprintf("$%d\r\n%s\r\n", len(a), a)
	}
	if _, err := conn.Write([]byte(payload)); err != nil {
		return err
	}
	buf := make([]byte, 4096)
	_, err = conn.Read(buf)
	return err
}

func main() {
	client := &http.Client{Timeout: 4 * time.Second}

	http.HandleFunc("/stock", func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		status := http.StatusOK

		// DB-Kante: Bestand "lesen" (RESP → Beyla-Client-Span)
		_ = redisCmd("GET", fmt.Sprintf("stock:%d", rand.Intn(50)))

		// Ausgehender HTTP-Call — OHNE traceparent-Code. Die Propagation
		// macht Beyla (uprobe auf net/http).
		if resp, err := client.Get("http://catalog/products"); err == nil {
			resp.Body.Close()
		}

		// Etwas Realismus
		time.Sleep(time.Duration(5+rand.Intn(25)) * time.Millisecond)
		if rand.Float64() < 0.03 {
			status = http.StatusServiceUnavailable
			log.Printf("ERROR inventory warehouse sync unavailable")
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sku":   fmt.Sprintf("sku-%d", rand.Intn(1000)),
			"stock": rand.Intn(500),
		})
		log.Printf("INFO inventory \"GET /stock\" %d %dms", status, time.Since(start).Milliseconds())
	})

	log.Printf("INFO inventory starting on :8080 (go, zero-instrumentation)")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
