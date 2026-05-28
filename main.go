package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

type Response struct {
	OK        bool      `json:"ok"`
	Service   string    `json:"service"`
	Version   string    `json:"version,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

func getenv(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func main() {
	port := getenv("PORT", "8080")
	serviceName := getenv("SERVICE_NAME", "scaleway-http-template")
	version := getenv("APP_VERSION", "dev")

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, Response{OK: true, Service: serviceName, Version: version, Timestamp: time.Now().UTC()})
	})

	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	http.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})

	addr := "0.0.0.0:" + port
	log.Printf("%s listening on %s", serviceName, addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal(err)
	}
}
