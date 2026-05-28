package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type Response struct {
	OK                  bool      `json:"ok"`
	Service             string    `json:"service"`
	Version             string    `json:"version,omitempty"`
	Timestamp           time.Time `json:"timestamp"`
	Traffmonetizer      string    `json:"traffmonetizer"`
	TraffmonetizerError string    `json:"traffmonetizer_error,omitempty"`
}

var (
	tmMu      sync.RWMutex
	tmStatus  = "not_configured"
	tmLastErr string
)

func getenv(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func setTMStatus(status, lastErr string) {
	tmMu.Lock()
	defer tmMu.Unlock()
	tmStatus = status
	tmLastErr = lastErr
}

func getTMStatus() (string, string) {
	tmMu.RLock()
	defer tmMu.RUnlock()
	return tmStatus, tmLastErr
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func findTraffmonetizerCLI() string {
	candidates := []string{
		"/Cli", "/cli", "/tm", "/traffmonetizer",
		"/app/Cli", "/app/cli",
		"/usr/local/bin/Cli", "/usr/local/bin/cli", "/usr/local/bin/traffmonetizer",
		"/usr/bin/Cli", "/usr/bin/cli", "/usr/bin/traffmonetizer",
		"/tmroot/Cli", "/tmroot/cli", "/tmroot/tm", "/tmroot/traffmonetizer",
		"/tmroot/app/Cli", "/tmroot/app/cli",
		"/tmroot/usr/local/bin/Cli", "/tmroot/usr/local/bin/cli", "/tmroot/usr/local/bin/traffmonetizer",
		"/tmroot/usr/bin/Cli", "/tmroot/usr/bin/cli", "/tmroot/usr/bin/traffmonetizer",
	}
	for _, p := range candidates {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	for _, name := range []string{"Cli", "cli", "tm", "traffmonetizer"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}

func startTraffmonetizerSupervisor() {
	token := strings.TrimSpace(os.Getenv("TM_TOKEN"))
	if token == "" {
		log.Println("TM_TOKEN is empty; Traffmonetizer will not start")
		setTMStatus("not_configured", "missing TM_TOKEN")
		return
	}

	cli := findTraffmonetizerCLI()
	if cli == "" {
		log.Println("Traffmonetizer CLI binary not found")
		setTMStatus("error", "Traffmonetizer CLI binary not found")
		return
	}

	argsText := getenv("TM_ARGS", "start accept")
	args := append(strings.Fields(argsText), "--token", token)

	go func() {
		for {
			log.Printf("starting Traffmonetizer: %s %s --token ****", cli, argsText)
			cmd := exec.Command(cli, args...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr

			if err := cmd.Start(); err != nil {
				log.Printf("failed to start Traffmonetizer: %v", err)
				setTMStatus("error", err.Error())
				time.Sleep(10 * time.Second)
				continue
			}

			setTMStatus("running", "")
			err := cmd.Wait()
			if err != nil {
				log.Printf("Traffmonetizer exited: %v", err)
				setTMStatus("exited", err.Error())
			} else {
				log.Printf("Traffmonetizer exited")
				setTMStatus("exited", "")
			}
			time.Sleep(10 * time.Second)
		}
	}()
}

func response(serviceName, version string) Response {
	status, errText := getTMStatus()
	return Response{
		OK:                  true,
		Service:             serviceName,
		Version:             version,
		Timestamp:           time.Now().UTC(),
		Traffmonetizer:      status,
		TraffmonetizerError: errText,
	}
}

func main() {
	port := getenv("PORT", "8080")
	serviceName := getenv("SERVICE_NAME", "scaleway-http-template")
	version := getenv("APP_VERSION", "dev")

	startTraffmonetizerSupervisor()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, response(serviceName, version))
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
