package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestURLProxyStreamsResponseAndPreservesHeaders(t *testing.T) {
	t.Setenv("ALLOW_PRIVATE_PROXY_TARGETS", "true")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Range"); got != "bytes=0-3" {
			t.Fatalf("Range header = %q, want bytes=0-3", got)
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Disposition", `attachment; filename="demo.txt"`)
		w.Header().Set("Accept-Ranges", "bytes")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("demo"))
	}))
	defer upstream.Close()

	req := httptest.NewRequest(http.MethodGet, "/proxy?url="+upstream.URL+"/file.txt", nil)
	req.Header.Set("Range", "bytes=0-3")
	rr := httptest.NewRecorder()

	handleURLProxy(rr, req)

	res := rr.Result()
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusPartialContent)
	}
	if string(body) != "demo" {
		t.Fatalf("body = %q, want demo", string(body))
	}
	if got := res.Header.Get("Content-Type"); got != "text/plain" {
		t.Fatalf("Content-Type = %q, want text/plain", got)
	}
	if got := res.Header.Get("Content-Disposition"); !strings.Contains(got, "demo.txt") {
		t.Fatalf("Content-Disposition = %q, want filename", got)
	}
}

func TestURLProxyRejectsMissingURL(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/proxy", nil)
	rr := httptest.NewRecorder()

	handleURLProxy(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestURLProxyRejectsPrivateNetworkByDefault(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/proxy?url=http://127.0.0.1/file", nil)
	rr := httptest.NewRecorder()

	handleURLProxy(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
}

func TestRegistryProxyRewritesDockerHubPathAndPreservesAuthChallenge(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/library/alpine/manifests/latest" {
			t.Fatalf("path = %q, want /v2/library/alpine/manifests/latest", r.URL.Path)
		}
		if got := r.Header.Get("Accept"); got != "application/vnd.docker.distribution.manifest.v2+json" {
			t.Fatalf("Accept = %q", got)
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="https://auth.docker.io/token",service="registry.docker.io"`)
		w.Header().Set("Docker-Distribution-Api-Version", "registry/2.0")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer upstream.Close()

	req := httptest.NewRequest(http.MethodGet, "/v2/alpine/manifests/latest", nil)
	req.Header.Set("Accept", "application/vnd.docker.distribution.manifest.v2+json")
	rr := httptest.NewRecorder()

	handleRegistryProxy(rr, req, upstream.URL)

	res := rr.Result()
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusUnauthorized)
	}
	if got := res.Header.Get("WWW-Authenticate"); !strings.Contains(got, "Bearer") {
		t.Fatalf("WWW-Authenticate = %q, want Bearer challenge", got)
	}
	if got := res.Header.Get("Docker-Distribution-Api-Version"); got != "registry/2.0" {
		t.Fatalf("Docker-Distribution-Api-Version = %q", got)
	}
}

func TestRegistryProxyStreamsBlobRange(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Range"); got != "bytes=0-2" {
			t.Fatalf("Range = %q, want bytes=0-2", got)
		}
		w.Header().Set("Content-Range", "bytes 0-2/6")
		w.Header().Set("Docker-Content-Digest", "sha256:abc")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("abc"))
	}))
	defer upstream.Close()

	req := httptest.NewRequest(http.MethodGet, "/v2/library/demo/blobs/sha256:abc", nil)
	req.Header.Set("Range", "bytes=0-2")
	rr := httptest.NewRecorder()

	handleRegistryProxy(rr, req, upstream.URL)

	res := rr.Result()
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusPartialContent)
	}
	if string(body) != "abc" {
		t.Fatalf("body = %q, want abc", string(body))
	}
	if got := res.Header.Get("Docker-Content-Digest"); got != "sha256:abc" {
		t.Fatalf("Docker-Content-Digest = %q", got)
	}
}
