package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	_ "embed"
)

//go:embed static/home.html
var homeHTML []byte

func cmdHome(args []string) {
	port := 7070
	noOpen := false

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-p" || a == "--port":
			if i+1 < len(args) {
				p, err := strconv.Atoi(args[i+1])
				if err != nil {
					fmt.Fprintln(os.Stderr, "Error: invalid port")
					os.Exit(1)
				}
				port = p
				i++
			}
		case strings.HasPrefix(a, "--port="):
			p, err := strconv.Atoi(strings.TrimPrefix(a, "--port="))
			if err != nil {
				fmt.Fprintln(os.Stderr, "Error: invalid port")
				os.Exit(1)
			}
			port = p
		case a == "--no-open":
			noOpen = true
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/instances", handleHomeInstances)
	mux.HandleFunc("/api/kill/", handleHomeKill)
	mux.HandleFunc("/", handleHomeDashboard)

	addr := fmt.Sprintf("localhost:%d", port)
	url := fmt.Sprintf("http://localhost:%d", port)
	fmt.Printf("serve home → %s\n", url)

	if !noOpen {
		_ = exec.Command("open", url).Start()
	}

	srv := &http.Server{Addr: addr, Handler: mux}

	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nStopped")
		_ = srv.Shutdown(context.Background())
		cancel()
	}()

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func handleHomeDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(homeHTML)
}

func handleHomeInstances(w http.ResponseWriter, r *http.Request) {
	instances := listInstances()
	type instJSON struct {
		PID     int    `json:"pid"`
		Port    int    `json:"port"`
		Path    string `json:"path"`
		Mode    string `json:"mode"`
		Started string `json:"started"`
		URL     string `json:"url"`
	}
	out := make([]instJSON, len(instances))
	for i, inst := range instances {
		out[i] = instJSON{
			PID:     inst.PID,
			Port:    inst.Port,
			Path:    inst.Path,
			Mode:    inst.Mode,
			Started: inst.Started,
			URL:     inst.URL(),
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func handleHomeKill(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	pidStr := strings.TrimPrefix(r.URL.Path, "/api/kill/")
	pid, err := strconv.Atoi(pidStr)
	if err != nil || pid <= 0 {
		http.Error(w, "invalid pid", http.StatusBadRequest)
		return
	}
	if err := killInstance(pid, false); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true,"pid":%d}`, pid)
}
