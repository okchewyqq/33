package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
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
	Routes    map[string]string    `json:"routes"`
	OpenList  map[string]string    `json:"openlist"`
	Processes map[string]ProcState `json:"processes"`
}

type DLRequestInput struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Body    string            `json:"body"`
	Headers map[string]string `json:"headers"`
	Token   string            `json:"token"`
	MaxAge  int64             `json:"max_age"`
}

type DLStoredRequest struct {
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Body      string            `json:"body,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	ExpiresAt time.Time         `json:"expires_at"`
}

var (
	procMu sync.RWMutex
	procs  = map[string]ProcState{}

	dlStoreMu sync.RWMutex
	dlStore   = map[string]DLStoredRequest{}
)

var dlRequestHeadersExposed = []string{
	"accept", "accept-encoding", "accept-language", "cache-control", "range", "user-agent",
}

var dlResponseHeadersExposed = []string{
	"Accept-Ranges", "Cache-Control", "Connection", "Content-Disposition", "Content-Encoding", "Content-Length", "Content-Range", "Content-Type", "Date",
}

var dlHTTPClient = &http.Client{
	Timeout: 5 * time.Minute,
	Transport: func() http.RoundTripper {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.DisableCompression = true
		return transport
	}(),
}

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

	listen := getenv("XRAY_LISTEN", "0.0.0.0")
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

func openListArgs() []string {
	if text := strings.TrimSpace(os.Getenv("OPENLIST_ARGS")); text != "" {
		return splitArgs(text)
	}
	return []string{"server"}
}

func ensureOpenListConfig() {
	dataDir := getenv("OPENLIST_DATA_DIR", "/opt/openlist/data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		setProc("openlist", "error", err.Error(), false)
		return
	}
	_ = os.Setenv("SITE_URL", strings.TrimRight(getenv("OPENLIST_SITE_URL", "/op"), "/"))
	_ = os.Setenv("HTTP_PORT", getenv("OPENLIST_PORT", "5244"))
	_ = os.Setenv("ADDRESS", getenv("OPENLIST_LISTEN", "0.0.0.0"))
	_ = os.Setenv("OPENLIST_ADMIN_PASSWORD", getenv("OPENLIST_ADMIN_PASSWORD", "114514"))
}

func startOpenList() {
	if strings.EqualFold(getenv("OPENLIST_ENABLED", "true"), "false") {
		setProc("openlist", "disabled", "", false)
		return
	}
	ensureOpenListConfig()
	bin := findExecutable([]string{"/usr/local/bin/openlist", "/usr/bin/openlist", "/bin/openlist"}, []string{"openlist"})
	// OPENLIST_ADMIN_PASSWORD sets the initial admin password in OpenList Docker builds.
	// The default admin username is usually admin; OPENLIST_ADMIN_USERNAME is kept as
	// metadata/status because upstream does not expose a documented username env var.
	supervise("openlist", bin, openListArgs(), "server")
}

func response(serviceName, version, opPath, wsPath, dlPath, openListTarget, xrayTarget string) Response {
	return Response{
		OK:        true,
		Service:   serviceName,
		Version:   version,
		Timestamp: time.Now().UTC(),
		Routes: map[string]string{
			"/":        "local ok page",
			opPath:     openListTarget,
			wsPath:     xrayTarget,
			dlPath:     "download proxy",
			"/healthz": "local health check",
			"/readyz":  "local status",
			"/status":  "local status",
		},
		OpenList: map[string]string{
			"path":     opPath,
			"username": getenv("OPENLIST_ADMIN_USERNAME", "Neu"),
			"password": getenv("OPENLIST_ADMIN_PASSWORD", "114514"),
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

func openListProxyTo(target, opPath string) *httputil.ReverseProxy {
	p := proxyTo(target)
	p.ModifyResponse = func(resp *http.Response) error {
		prefix := strings.TrimRight(normalizePath(opPath), "/")
		if prefix == "" {
			prefix = "/"
		}
		if loc := resp.Header.Get("Location"); prefix != "/" && loc != "" {
			if rewritten := prefixRelativeLocation(loc, prefix); rewritten != loc {
				resp.Header.Set("Location", rewritten)
			}
		}
		if prefix == "/" || !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/html") || resp.Body == nil {
			return nil
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		body = rewriteOpenListHTML(body, prefix)
		resp.Body = io.NopCloser(bytes.NewReader(body))
		resp.ContentLength = int64(len(body))
		resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
		return nil
	}
	return p
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

func stripPrefixPath(requestPath, prefix string) string {
	prefix = strings.TrimRight(normalizePath(prefix), "/")
	if prefix == "" || prefix == "/" {
		return requestPath
	}
	if requestPath == prefix {
		return "/"
	}
	stripped := strings.TrimPrefix(requestPath, prefix)
	if stripped == "" {
		return "/"
	}
	if !strings.HasPrefix(stripped, "/") {
		return "/" + stripped
	}
	return stripped
}

func withStrippedPrefix(r *http.Request, prefix string) *http.Request {
	out := r.Clone(r.Context())
	out.URL.Path = stripPrefixPath(r.URL.Path, prefix)
	out.URL.RawPath = ""
	out.Header.Del("Accept-Encoding")
	out.Header.Set("X-Forwarded-Prefix", strings.TrimRight(normalizePath(prefix), "/"))
	if out.Header.Get("X-Forwarded-Host") == "" {
		out.Header.Set("X-Forwarded-Host", r.Host)
	}
	if out.Header.Get("X-Forwarded-Proto") == "" {
		if r.TLS != nil {
			out.Header.Set("X-Forwarded-Proto", "https")
		} else {
			out.Header.Set("X-Forwarded-Proto", "http")
		}
	}
	return out
}

func prefixPath(prefix, path string) string {
	prefix = strings.TrimRight(normalizePath(prefix), "/")
	if prefix == "" || prefix == "/" {
		return path
	}
	if path == "" || path == "/" {
		return prefix + "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if isPrefixPath(path, prefix) {
		return path
	}
	return prefix + path
}

func prefixRelativeLocation(location, prefix string) string {
	if strings.HasPrefix(location, "//") || !strings.HasPrefix(location, "/") {
		return location
	}
	u, err := url.Parse(location)
	if err != nil || u.Path == "" || isPrefixPath(u.Path, prefix) {
		return location
	}
	u.Path = prefixPath(prefix, u.Path)
	return u.String()
}

func rewriteOpenListHTML(body []byte, prefix string) []byte {
	replacements := [][2]string{
		{"base_path: '/',", "base_path: '" + prefix + "',"},
		{"base_path: \"/\",", "base_path: \"" + prefix + "\","},
		{"href=\"/manifest.json\"", "href=\"" + prefix + "/manifest.json\""},
		{"href='/manifest.json'", "href='" + prefix + "/manifest.json'"},
		{"\"/assets/", "\"" + prefix + "/assets/"},
		{"'/assets/", "'" + prefix + "/assets/"},
	}
	out := body
	for _, replacement := range replacements {
		out = bytes.ReplaceAll(out, []byte(replacement[0]), []byte(replacement[1]))
	}
	return out
}

func dlIndexHTML(prefix string) string {
	prefix = strings.TrimRight(normalizePath(prefix), "/")
	if prefix == "" {
		prefix = "/"
	}
	return `<!doctype html>
<html>
<head>
    <meta charset="utf-8">
    <title>Download Proxy</title>
</head>
<body>
<input type="url" placeholder="url" id="url" style="height: 20px;width: 80%; display: block;">
<input type="submit" id="submit" value="submit"/>
<div><a id="a" href=""></a></div>
<p>注:该工具只针对直链有效</p>
<hr>
<h3>代理下载</h3>
<p>GET ` + prefix + `/down/http://example.com</p>
<p>直接将网址放到url后面即可</p>
<hr>
<h3>身份匿名授权(dev)</h3>
<p>适用场景:a访问b网址下载文件需要提供token,a想要c能够下载b网址的文件,但又不想让c知道token内容.
    a可以通过提交token给服务,服务生成临时链接供c使用,c无法得知token的内容.</p>
<p>获取临时链接</p>
<p>POST ` + prefix + `/request/ {method = 'GET', url, body = '', headers = {}, token, max_age = 12*3600}</p>
<p>使用key</p>
<div>GET ` + prefix + `/request/ODAzMzUyMzY1MjY1MDQw</div>
<script>
    document.getElementById('submit').onclick = function () {
        const url = document.getElementById('url').value;
        const a = document.getElementById('a');
        if (!url || !url.startsWith('http')) {
            a.textContent = "链接不合法: " + url;
            a.href = 'javascript:void(0)';
        } else {
            a.href = a.textContent = (new URL(window.location.href)).origin + '` + prefix + `/down/' + url;
        }
    };
</script>
</body>
</html>
`
}

func handleDL(w http.ResponseWriter, r *http.Request, prefix string) {
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Origin", corsHeader(r, "Origin"))
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,TRACE,DELETE,HEAD,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", corsHeader(r, "Access-Control-Request-Headers"))
		w.Header().Set("Access-Control-Max-Age", "86400")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	path := stripPrefixPath(r.URL.Path, prefix)
	if path == "/" || path == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(dlIndexHTML(prefix)))
		return
	}

	requestPath := "/request/"
	if strings.HasPrefix(path, requestPath) {
		handleDLRequest(w, r, strings.TrimPrefix(path, requestPath))
		return
	}

	downPath := "/down/"
	if strings.HasPrefix(path, downPath) {
		target := normalizeDLTarget(strings.TrimPrefix(path, downPath), r.URL.RawQuery)
		handleDLDown(w, r, target)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(dlIndexHTML(prefix)))
}

func corsHeader(r *http.Request, key string) string {
	if value := r.Header.Get(key); value != "" {
		return value
	}
	return "*"
}

func handleDLRequest(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodPost:
		body, err := io.ReadAll(io.LimitReader(r.Body, 2048))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": 400, "message": err.Error()})
			return
		}
		if len(body) > 1024 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": 400, "message": "your request data is too much long"})
			return
		}
		var input DLRequestInput
		if err := json.Unmarshal(body, &input); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": 400, "message": err.Error()})
			return
		}
		if err := validateDLToken(input.Token); err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": 401, "message": err.Error()})
			return
		}
		stored, err := normalizeDLRequest(input)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": 400, "message": err.Error()})
			return
		}
		key, err := randKey()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": 500, "message": err.Error()})
			return
		}
		dlStoreMu.Lock()
		dlStore[key] = stored
		dlStoreMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]string{"key": key})
	case http.MethodGet:
		if id == "" || strings.Contains(id, "/") {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": 400, "message": "invalid url, eg: /dl/request/ODAzMzUyMzY1MjY1MDQw"})
			return
		}
		stored, ok := getDLStoredRequest(id)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": 404, "message": "no such key"})
			return
		}
		proxyDLFetch(w, r, stored.URL, stored.Method, stored.Body, stored.Headers)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": 405, "message": "method not allowed"})
	}
}

func validateDLToken(token string) error {
	required := strings.TrimSpace(os.Getenv("DL_REQUEST_TOKEN"))
	if required != "" && token != required {
		return fmt.Errorf("unauthorized key")
	}
	return nil
}

func normalizeDLRequest(input DLRequestInput) (DLStoredRequest, error) {
	method := strings.ToUpper(strings.TrimSpace(input.Method))
	if method == "" {
		method = http.MethodGet
	}
	if _, err := url.ParseRequestURI(input.URL); err != nil || !isHTTPURL(input.URL) {
		return DLStoredRequest{}, fmt.Errorf("url is invalid:%s", input.URL)
	}
	maxAge := input.MaxAge
	if maxAge <= 0 {
		maxAge = 12 * 3600
	}
	if maxAge > 7*24*3600 {
		maxAge = 7 * 24 * 3600
	}
	return DLStoredRequest{
		Method:    method,
		URL:       input.URL,
		Body:      input.Body,
		Headers:   input.Headers,
		ExpiresAt: time.Now().UTC().Add(time.Duration(maxAge) * time.Second),
	}, nil
}

func getDLStoredRequest(key string) (DLStoredRequest, bool) {
	now := time.Now().UTC()
	dlStoreMu.RLock()
	stored, ok := dlStore[key]
	dlStoreMu.RUnlock()
	if !ok {
		return DLStoredRequest{}, false
	}
	if now.After(stored.ExpiresAt) {
		dlStoreMu.Lock()
		delete(dlStore, key)
		dlStoreMu.Unlock()
		return DLStoredRequest{}, false
	}
	return stored, true
}

func randKey() (string, error) {
	b := make([]byte, 15)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func handleDLDown(w http.ResponseWriter, r *http.Request, target string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": 405, "message": "method not allowed"})
		return
	}
	proxyDLFetch(w, r, target, r.Method, "", map[string]string{})
}

func proxyDLFetch(w http.ResponseWriter, r *http.Request, target, method, body string, extraHeaders map[string]string) {
	if !isHTTPURL(target) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": 400, "message": "url is invalid:" + target})
		return
	}
	if method == "" {
		method = http.MethodGet
	}
	var reqBody io.Reader
	if body != "" && method != http.MethodGet && method != http.MethodHead {
		reqBody = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(r.Context(), method, target, reqBody)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": 400, "message": err.Error()})
		return
	}
	for _, key := range dlRequestHeadersExposed {
		if value := r.Header.Get(key); value != "" {
			req.Header.Set(key, value)
		}
	}
	for key, value := range extraHeaders {
		if strings.TrimSpace(key) != "" {
			req.Header.Set(key, value)
		}
	}
	resp, err := dlHTTPClient.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": 502, "message": err.Error()})
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Access-Control-Allow-Origin", "*")
	for _, key := range dlResponseHeadersExposed {
		if value := resp.Header.Get(key); value != "" {
			w.Header().Set(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if method != http.MethodHead {
		_, _ = io.Copy(w, resp.Body)
	}
}

func isHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func normalizeDLTarget(pathValue, rawQuery string) string {
	target := pathValue
	for _, scheme := range []string{"http", "https"} {
		prefix := scheme + ":"
		if strings.HasPrefix(target, prefix) {
			target = prefix + "//" + strings.TrimLeft(target[len(prefix):], "/")
			break
		}
	}
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	return target
}

func redirectDLReferer(w http.ResponseWriter, r *http.Request, prefix string) bool {
	referer := r.Header.Get("Referer")
	if referer == "" {
		return false
	}
	u, err := url.Parse(referer)
	if err != nil {
		return false
	}
	path := stripPrefixPath(u.Path, prefix)
	const downPath = "/down/"
	if !strings.HasPrefix(path, downPath) {
		return false
	}
	target := normalizeDLTarget(strings.TrimPrefix(path, downPath), "")
	targetURL, err := url.Parse(target)
	if err != nil || targetURL.Scheme == "" || targetURL.Host == "" {
		return false
	}
	location := strings.TrimRight(normalizePath(prefix), "/") + downPath + targetURL.Scheme + "://" + targetURL.Host + r.URL.Path
	if r.URL.RawQuery != "" {
		location += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, location, http.StatusMovedPermanently)
	return true
}

func main() {
	port := getenv("PORT", "8080")
	serviceName := getenv("SERVICE_NAME", "openlist-xray-tm")
	version := getenv("APP_VERSION", "dev")
	wsPath := normalizePath(getenv("VLESS_WS_PATH", "/ws"))
	opPath := normalizePath(getenv("OPENLIST_PATH", "/op"))
	dlPath := normalizePath(getenv("DL_PATH", "/dl"))

	startOpenList()
	startXray()
	startTraffmonetizer()

	xrayTarget := "http://127.0.0.1:" + getenv("XRAY_PORT", "10000")
	openListTarget := "http://127.0.0.1:" + getenv("OPENLIST_PORT", "5244")
	xrayProxy := proxyTo(xrayTarget)
	openListProxy := openListProxyTo(openListTarget, opPath)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
			return
		case "/readyz":
			writeJSON(w, http.StatusOK, response(serviceName, version, opPath, wsPath, dlPath, openListTarget, xrayTarget))
			return
		case "/status":
			writeJSON(w, http.StatusOK, response(serviceName, version, opPath, wsPath, dlPath, openListTarget, xrayTarget))
			return
		}

		if isPrefixPath(r.URL.Path, wsPath) {
			xrayProxy.ServeHTTP(w, r)
			return
		}
		if isPrefixPath(r.URL.Path, opPath) {
			openListProxy.ServeHTTP(w, withStrippedPrefix(r, opPath))
			return
		}
		if isPrefixPath(r.URL.Path, dlPath) {
			handleDL(w, r, dlPath)
			return
		}
		if redirectDLReferer(w, r, dlPath) {
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	addr := "0.0.0.0:" + port
	log.Printf("%s listening on %s; / -> ok; %s -> %s; %s -> %s; %s download proxy; /healthz local", serviceName, addr, opPath, openListTarget, wsPath, xrayTarget, dlPath)
	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatal(err)
	}
}
