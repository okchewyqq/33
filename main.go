package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ProcState struct {
	Status    string `json:"status"`
	LastError string `json:"last_error,omitempty"`
	Restarts  int    `json:"restarts"`
	StartedAt string `json:"started_at,omitempty"`
	UpdatedAt string `json:"updated_at"`
}

type Response struct {
	OK        bool                 `json:"ok"`
	Service   string               `json:"service"`
	Version   string               `json:"version,omitempty"`
	Timestamp time.Time            `json:"timestamp"`
	Processes map[string]ProcState `json:"processes"`
}

var (
	procMu sync.RWMutex
	procs  = map[string]ProcState{}
)

func getenv(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func splitArgs(s string) []string {
	return strings.Fields(strings.TrimSpace(s))
}

func setProc(name, status, lastErr string, incRestart bool) {
	procMu.Lock()
	defer procMu.Unlock()
	st := procs[name]
	if incRestart {
		st.Restarts++
	}
	st.Status = status
	st.LastError = lastErr
	st.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if status == "running" {
		st.StartedAt = time.Now().UTC().Format(time.RFC3339)
	}
	procs[name] = st
}

func getProcs() map[string]ProcState {
	procMu.RLock()
	defer procMu.RUnlock()
	out := make(map[string]ProcState, len(procs))
	for k, v := range procs {
		out[k] = v
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func findExecutable(paths []string, names []string) string {
	for _, p := range paths {
		if p == "" {
			continue
		}
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	for _, name := range names {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}

func supervise(name, bin string, args []string, maskedArgs string) {
	if bin == "" {
		setProc(name, "error", "binary not found", false)
		log.Printf("%s binary not found", name)
		return
	}

	go func() {
		for {
			shown := maskedArgs
			if shown == "" {
				shown = strings.Join(args, " ")
			}
			log.Printf("starting %s: %s %s", name, bin, shown)
			cmd := exec.Command(bin, args...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Start(); err != nil {
				setProc(name, "error", err.Error(), true)
				log.Printf("failed to start %s: %v", name, err)
				time.Sleep(10 * time.Second)
				continue
			}
			setProc(name, "running", "", true)
			err := cmd.Wait()
			if err != nil {
				setProc(name, "exited", err.Error(), false)
				log.Printf("%s exited: %v", name, err)
			} else {
				setProc(name, "exited", "", false)
				log.Printf("%s exited", name)
			}
			time.Sleep(10 * time.Second)
		}
	}()
}

func startTraffmonetizer() {
	token := strings.TrimSpace(os.Getenv("TM_TOKEN"))
	if token == "" {
		setProc("traffmonetizer", "not_configured", "missing TM_TOKEN", false)
		log.Println("TM_TOKEN is empty; Traffmonetizer will not start")
		return
	}
	cli := findExecutable([]string{
		"/Cli", "/cli", "/tm", "/traffmonetizer", "/app/Cli", "/app/cli",
		"/usr/local/bin/Cli", "/usr/local/bin/cli", "/usr/local/bin/traffmonetizer",
		"/usr/bin/Cli", "/usr/bin/cli", "/usr/bin/traffmonetizer",
		"/tmroot/Cli", "/tmroot/cli", "/tmroot/tm", "/tmroot/traffmonetizer",
		"/tmroot/app/Cli", "/tmroot/app/cli", "/tmroot/usr/local/bin/Cli",
		"/tmroot/usr/local/bin/cli", "/tmroot/usr/local/bin/traffmonetizer",
		"/tmroot/usr/bin/Cli", "/tmroot/usr/bin/cli", "/tmroot/usr/bin/traffmonetizer",
	}, []string{"Cli", "cli", "tm", "traffmonetizer"})
	argsText := getenv("TM_ARGS", "start accept")
	args := append(splitArgs(argsText), "--token", token)
	supervise("traffmonetizer", cli, args, argsText+" --token ****")
}

func writeXrayConfig() (string, error) {
	port, err := strconv.Atoi(getenv("XRAY_PORT", "10000"))
	if err != nil || port < 1 || port > 65535 {
		return "", fmt.Errorf("invalid XRAY_PORT")
	}

	listen := getenv("XRAY_LISTEN", "127.0.0.1")
	wsPath := getenv("VLESS_WS_PATH", "/ws")
	if !strings.HasPrefix(wsPath, "/") {
		wsPath = "/" + wsPath
	}
	uuid := getenv("VLESS_UUID", "10974d1a-cbd6-4b6f-db1d-38d78b3fb109")

	cfg := map[string]any{
		"log": map[string]any{"loglevel": getenv("XRAY_LOG_LEVEL", "warning")},
		"inbounds": []map[string]any{{
			"tag":      "vless-ws-in",
			"listen":   listen,
			"port":     port,
			"protocol": "vless",
			"settings": map[string]any{
				"clients":    []map[string]any{{"id": uuid, "email": "default"}},
				"decryption": "none",
			},
			"streamSettings": map[string]any{
				"network": "ws",
				"wsSettings": map[string]any{
					"path": wsPath,
				},
			},
		}},
		"outbounds": []map[string]any{
			{"tag": "direct", "protocol": "freedom"},
			{"tag": "blocked", "protocol": "blackhole"},
		},
	}

	if err := os.MkdirAll("/tmp/xray", 0755); err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	path := "/tmp/xray/config.json"
	if err := os.WriteFile(path, b, 0644); err != nil {
		return "", err
	}
	return path, nil
}

func startXray() {
	if strings.EqualFold(getenv("XRAY_ENABLED", "true"), "false") {
		setProc("xray", "disabled", "", false)
		return
	}
	bin := findExecutable([]string{"/usr/local/bin/xray", "/usr/bin/xray", "/bin/xray"}, []string{"xray"})
	configPath, err := writeXrayConfig()
	if err != nil {
		setProc("xray", "error", err.Error(), false)
		return
	}
	supervise("xray", bin, []string{"run", "-config", configPath}, "run -config /tmp/xray/config.json")
}

func easyTierArgs() []string {
	if text := strings.TrimSpace(os.Getenv("EASYTIER_WEB_ARGS")); text != "" {
		return splitArgs(text)
	}
	args := []string{
		"--api-server-port", getenv("EASYTIER_API_PORT", "11211"),
		"--config-server-port", getenv("EASYTIER_CONFIG_PORT", "22020"),
		"--config-server-protocol", getenv("EASYTIER_CONFIG_PROTOCOL", "udp"),
	}
	if apiHost := strings.TrimSpace(os.Getenv("EASYTIER_API_HOST")); apiHost != "" {
		args = append(args, "--api-host", apiHost)
	}
	return args
}

func startEasyTierWeb() {
	if strings.EqualFold(getenv("EASYTIER_WEB_ENABLED", "true"), "false") {
		setProc("easytier-web", "disabled", "", false)
		return
	}
	bin := findExecutable([]string{
		"/usr/local/bin/easytier-web-embed", "/usr/bin/easytier-web-embed", "/app/easytier-web-embed", "/easytier/easytier-web-embed",
	}, []string{"easytier-web-embed"})
	supervise("easytier-web", bin, easyTierArgs(), "")
}

func response(serviceName, version string) Response {
	return Response{
		OK:        true,
		Service:   serviceName,
		Version:   version,
		Timestamp: time.Now().UTC(),
		Processes: getProcs(),
	}
}

func proxyTo(target string) *httputil.ReverseProxy {
	u, err := url.Parse(target)
	if err != nil {
		log.Fatalf("invalid proxy target %s: %v", target, err)
	}
	p := httputil.NewSingleHostReverseProxy(u)
	p.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"ok":    false,
			"error": err.Error(),
			"path":  r.URL.Path,
		})
	}
	return p
}

func main() {
	port := getenv("PORT", "8080")
	serviceName := getenv("SERVICE_NAME", "easytier-xray-tm")
	version := getenv("APP_VERSION", "dev")

	startEasyTierWeb()
	startXray()
	startTraffmonetizer()

	xrayTarget := "http://127.0.0.1:" + getenv("XRAY_PORT", "10000")
	easyTierTarget := "http://127.0.0.1:" + getenv("EASYTIER_API_PORT", "11211")
	xrayProxy := proxyTo(xrayTarget)
	easyTierProxy := proxyTo(easyTierTarget)
	wsPath := getenv("VLESS_WS_PATH", "/ws")
	if !strings.HasPrefix(wsPath, "/") {
		wsPath = "/" + wsPath
	}

	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	http.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, response(serviceName, version))
	})
	http.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, response(serviceName, version))
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == wsPath || strings.HasPrefix(r.URL.Path, wsPath+"/") {
			xrayProxy.ServeHTTP(w, r)
			return
		}
		easyTierProxy.ServeHTTP(w, r)
	})

	addr := "0.0.0.0:" + port
	log.Printf("%s listening on %s, / -> %s, %s -> %s", serviceName, addr, easyTierTarget, wsPath, xrayTarget)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal(err)
	}
}
