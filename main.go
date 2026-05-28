package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
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
	Routes    map[string]string    `json:"routes"`
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
	wsPath := normalizePath(getenv("VLESS_WS_PATH", "/ws"))
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

func response(serviceName, version, wsPath, dockerRegistry string) Response {
	return Response{
		OK:        true,
		Service:   serviceName,
		Version:   version,
		Timestamp: time.Now().UTC(),
		Routes: map[string]string{
			"/":        "proxy usage page",
			"/proxy":   "stream URL relay: /proxy?url=https://example.com/file",
			"/v2/":     "stream Docker registry relay for " + dockerRegistry,
			wsPath:     "xray websocket endpoint",
			"/healthz": "local health check",
			"/readyz":  "local status",
			"/status":  "local status",
		},
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

var hopByHopHeaders = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailer":             true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
}

func copyRequestHeaders(dst, src http.Header) {
	for key, values := range src {
		canonical := http.CanonicalHeaderKey(key)
		if hopByHopHeaders[canonical] {
			continue
		}
		for _, value := range values {
			dst.Add(canonical, value)
		}
	}
}

func copyResponseHeaders(dst, src http.Header) {
	for key, values := range src {
		canonical := http.CanonicalHeaderKey(key)
		if hopByHopHeaders[canonical] {
			continue
		}
		dst.Del(canonical)
		for _, value := range values {
			dst.Add(canonical, value)
		}
	}
}

func streamHTTP(w http.ResponseWriter, r *http.Request, target string) {
	if r.Body != nil {
		defer r.Body.Close()
	}
	upReq, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	copyRequestHeaders(upReq.Header, r.Header)
	upReq.Host = upReq.URL.Host

	client := &http.Client{Timeout: 0}
	res, err := client.Do(upReq)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	defer res.Body.Close()
	copyResponseHeaders(w.Header(), res.Header)
	w.WriteHeader(res.StatusCode)
	_, _ = io.Copy(w, res.Body)
}

func isPrivateHost(host string) bool {
	host = strings.Trim(host, "[]")
	ips, err := net.LookupIP(host)
	if err != nil {
		return true
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return true
		}
	}
	return false
}

func handleURLProxy(w http.ResponseWriter, r *http.Request) {
	rawURL := strings.TrimSpace(r.URL.Query().Get("url"))
	if rawURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "missing url query parameter"})
		return
	}
	target, err := url.Parse(rawURL)
	if err != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "url must be absolute http(s) URL"})
		return
	}
	if !strings.EqualFold(getenv("ALLOW_PRIVATE_PROXY_TARGETS", "false"), "true") && isPrivateHost(target.Hostname()) {
		writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "private proxy targets are disabled"})
		return
	}
	streamHTTP(w, r, target.String())
}

func dockerHubPath(path string) string {
	if !strings.HasPrefix(path, "/v2/") {
		return path
	}
	trimmed := strings.TrimPrefix(path, "/v2/")
	parts := strings.Split(trimmed, "/")
	if len(parts) >= 3 && (parts[1] == "manifests" || parts[1] == "blobs") {
		return "/v2/library/" + trimmed
	}
	return path
}

func registryTargetURL(r *http.Request, registryBase string) string {
	base := strings.TrimRight(registryBase, "/")
	path := dockerHubPath(r.URL.Path)
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	return base + path
}

func handleRegistryProxy(w http.ResponseWriter, r *http.Request, registryBase string) {
	streamHTTP(w, r, registryTargetURL(r, registryBase))
}

func normalizePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}

func isPrefixPath(requestPath, prefix string) bool {
	prefix = normalizePath(prefix)
	if prefix == "/" {
		return true
	}
	return requestPath == prefix || strings.HasPrefix(requestPath, prefix+"/")
}

func usagePage(w http.ResponseWriter, registryBase string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, "ok\n\nURL relay:\n  /proxy?url=https://example.com/file\n\nDocker registry relay:\n  /v2/... -> %s\n", registryBase)
}

func main() {
	port := getenv("PORT", "8080")
	serviceName := getenv("SERVICE_NAME", "stream-proxy-xray-tm")
	version := getenv("APP_VERSION", "dev")
	wsPath := normalizePath(getenv("VLESS_WS_PATH", "/ws"))
	registryBase := strings.TrimRight(getenv("DOCKER_REGISTRY_BASE", "https://registry-1.docker.io"), "/")

	startXray()
	startTraffmonetizer()

	xrayTarget := "http://127.0.0.1:" + getenv("XRAY_PORT", "10000")
	xrayProxy := proxyTo(xrayTarget)

	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	http.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, response(serviceName, version, wsPath, registryBase))
	})
	http.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, response(serviceName, version, wsPath, registryBase))
	})
	http.HandleFunc("/proxy", handleURLProxy)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if isPrefixPath(r.URL.Path, wsPath) {
			xrayProxy.ServeHTTP(w, r)
			return
		}
		if isPrefixPath(r.URL.Path, "/v2") {
			handleRegistryProxy(w, r, registryBase)
			return
		}
		usagePage(w, registryBase)
	})

	addr := "0.0.0.0:" + port
	log.Printf("%s listening on %s; /proxy URL relay; /v2 -> %s; %s -> %s; /healthz local", serviceName, addr, registryBase, wsPath, xrayTarget)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal(err)
	}
}
