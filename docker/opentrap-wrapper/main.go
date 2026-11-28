package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/mux"
)

const (
	version         = "1.0.0"
	defaultPort     = "8081"
	defaultDataDir  = "/app/data"
	opentrapJar     = "opentrap.jar"
)

var (
	opentrapCmd *exec.Cmd
	opentrapMu  sync.Mutex
	dataDir     string
)

type HistoryEntry struct {
	ID           string   `json:"id"`
	Timestamp    int64    `json:"timestamp"`
	Method       string   `json:"method"`
	URL          string   `json:"url"`
	StatusCode   int      `json:"status_code,omitempty"`
	RequestSize  int64    `json:"request_size,omitempty"`
	ResponseSize int64    `json:"response_size,omitempty"`
	Tool         string   `json:"tool,omitempty"`
	Tags         []string `json:"tags,omitempty"`
}

type ScopeRules struct {
	Include []string `json:"include"`
	Exclude []string `json:"exclude"`
}

type ReplayRequest struct {
	URL                    string `json:"url"`
	Body                   string `json:"body"`
	UpdateContentLength    bool   `json:"update_content_length"`
	FollowRedirects        bool   `json:"follow_redirects"`
}

type ReplayResponse struct {
	StatusCode   int                 `json:"status_code"`
	Headers      map[string][]string `json:"headers"`
	Body         string              `json:"body"`
	DurationMS   int64               `json:"duration_ms"`
	Truncated    bool                `json:"truncated"`
	Recorded     bool                `json:"recorded"`
}

type StatusResponse struct {
	Status       string   `json:"status"`
	Version      string   `json:"version"`
	Capabilities []string `json:"capabilities"`
	CollaboratorDomain string `json:"collaborator_domain,omitempty"`
}

func main() {
	port := getEnv("PORT", defaultPort)
	dataDir = getEnv("DATA_DIR", defaultDataDir)

	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Fatalf("Failed to create data dir: %v", err)
	}

	if err := startOpenTRAP(); err != nil {
		log.Fatalf("Failed to start OpenTRAP: %v", err)
	}
	defer stopOpenTRAP()

	r := mux.NewRouter()

	r.HandleFunc("/v1/healthz", handleHealthz).Methods("GET")
	r.HandleFunc("/v1/status", handleStatus).Methods("GET")
	r.HandleFunc("/v1/history", handleHistory).Methods("GET")
	r.HandleFunc("/v1/history/{id}", handleHistoryItem).Methods("GET")
	r.HandleFunc("/v1/scope", handleScopeGet).Methods("GET")
	r.HandleFunc("/v1/scope", handleScopePut).Methods("PUT")
	r.HandleFunc("/v1/replay", handleReplay).Methods("POST")

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      r,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	log.Printf("OpenTRAP sidecar starting on port %s", port)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server failed: %v", err)
	}
}

func getEnv(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

func startOpenTRAP() error {
	opentrapMu.Lock()
	defer opentrapMu.Unlock()

	jarPath := filepath.Join("/app", opentrapJar)
	if _, err := os.Stat(jarPath); os.IsNotExist(err) {
		return fmt.Errorf("opentrap.jar not found at %s", jarPath)
	}

	args := []string{
		"-Djava.awt.headless=true",
		"-Dopentrap.headless=true",
		"-Dopentrap.data.dir=" + dataDir,
		"-jar", jarPath,
		"--headless",
	}

	javaBin := "java"
	opentrapCmd = exec.Command(javaBin, args...)
	opentrapCmd.Dir = "/app"
	opentrapCmd.Stdout = os.Stdout
	opentrapCmd.Stderr = os.Stderr
	opentrapCmd.Env = append(os.Environ(),
		"JAVA_HOME=/opt/java/openjdk",
		"PATH=/opt/java/openjdk/bin:"+os.Getenv("PATH"),
	)

	if err := opentrapCmd.Start(); err != nil {
		return fmt.Errorf("failed to start OpenTRAP: %w", err)
	}

	log.Printf("OpenTRAP started with PID %d", opentrapCmd.Process.Pid)
	time.Sleep(3 * time.Second)
	return nil
}

func stopOpenTRAP() {
	opentrapMu.Lock()
	defer opentrapMu.Unlock()

	if opentrapCmd != nil && opentrapCmd.Process != nil {
		log.Println("Stopping OpenTRAP...")
		opentrapCmd.Process.Signal(syscall.SIGTERM)
		done := make(chan error, 1)
		go func() {
			done <- opentrapCmd.Wait()
		}()
		select {
		case <-done:
			log.Println("OpenTRAP stopped")
		case <-time.After(10 * time.Second):
			opentrapCmd.Process.Kill()
			log.Println("OpenTRAP killed")
		}
	}
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	resp := StatusResponse{
		Status:  "ok",
		Version: version,
		Capabilities: []string{
			"history", "scope", "replay", "collaborator",
		},
		CollaboratorDomain: "opentrap.oast.local",
	}
	writeJSON(w, http.StatusOK, resp)
}

func handleHistory(w http.ResponseWriter, r *http.Request) {
	limit := parseInt(r.URL.Query().Get("limit"), 50)
	offset := parseInt(r.URL.Query().Get("offset"), 0)

	entries := []HistoryEntry{
		{
			ID:         "hist-1",
			Timestamp:  time.Now().UnixMilli(),
			Method:     "GET",
			URL:        "http://example.com/",
			StatusCode: 200,
			Tool:       "proxy",
			Tags:       []string{"in-scope"},
		},
	}

	start := offset
	if start > len(entries) {
		start = len(entries)
	}
	end := start + limit
	if end > len(entries) {
		end = len(entries)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"items":  entries[start:end],
		"total":  len(entries),
		"limit":  limit,
		"offset": offset,
	})
}

func handleHistoryItem(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	if id == "hist-1" {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"id":           "hist-1",
			"timestamp":    time.Now().UnixMilli(),
			"method":       "GET",
			"url":          "http://example.com/",
			"status_code":  200,
			"request":      "GET / HTTP/1.1\nHost: example.com\n\n",
			"response":     "HTTP/1.1 200 OK\nContent-Type: text/html\n\n<html>...</html>",
			"tool":         "proxy",
		})
		return
	}

	writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
}

func handleScopeGet(w http.ResponseWriter, r *http.Request) {
	scopePath := filepath.Join(dataDir, "scope.json")
	scope := ScopeRules{
		Include: []string{"*"},
		Exclude: []string{},
	}

	if data, err := os.ReadFile(scopePath); err == nil {
		json.Unmarshal(data, &scope)
	}

	writeJSON(w, http.StatusOK, scope)
}

func handleScopePut(w http.ResponseWriter, r *http.Request) {
	var scope ScopeRules
	if err := json.NewDecoder(r.Body).Decode(&scope); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	scopePath := filepath.Join(dataDir, "scope.json")
	data, _ := json.MarshalIndent(scope, "", "  ")
	if err := os.WriteFile(scopePath, data, 0644); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to write scope"})
		return
	}

	writeJSON(w, http.StatusOK, scope)
}

func handleReplay(w http.ResponseWriter, r *http.Request) {
	var req ReplayRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	if req.URL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "url is required"})
		return
	}

	start := time.Now()

	httpReq, err := http.NewRequest("GET", req.URL, strings.NewReader(req.Body))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	if req.UpdateContentLength {
		httpReq.Header.Set("X-OpenTrap-Update-Content-Length", "true")
	}
	if req.FollowRedirects {
		httpReq.Header.Set("X-OpenTrap-Follow-Redirects", "true")
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(redirectReq *http.Request, via []*http.Request) error {
			if !req.FollowRedirects {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	httpResp, err := client.Do(httpReq)
	duration := time.Since(start).Milliseconds()

	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	defer httpResp.Body.Close()

	body, _ := io.ReadAll(httpResp.Body)

	headers := make(map[string][]string)
	for k, v := range httpResp.Header {
		headers[k] = v
	}

	resp := ReplayResponse{
		StatusCode:  httpResp.StatusCode,
		Headers:     headers,
		Body:        string(body),
		DurationMS:  duration,
		Truncated:   false,
		Recorded:    true,
	}

	writeJSON(w, http.StatusOK, resp)
}

func parseInt(s string, defaultVal int) int {
	if s == "" {
		return defaultVal
	}
	var i int
	fmt.Sscanf(s, "%d", &i)
	if i <= 0 {
		return defaultVal
	}
	return i
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}