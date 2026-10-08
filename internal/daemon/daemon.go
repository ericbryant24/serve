// Package daemon is how the CLI finds, starts, stops and talks to the
// background server. The server records itself in ~/.serve/daemon.json
// (mode 0600, since it holds the token that every state-changing request to
// the server needs).
package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"serve/internal/paths"
)

// Info is what the running server records about itself.
type Info struct {
	PID         int    `json:"pid"`
	Port        int    `json:"port"`
	ContentPort int    `json:"content_port"`
	Host        string `json:"host"`
	Token       string `json:"token"`
	ShareToken  string `json:"share_token,omitempty"`
	Version     string `json:"version"`
	Started     string `json:"started"`
}

// Path is ~/.serve/daemon.json.
func Path() string { return filepath.Join(paths.StateDir(), "daemon.json") }

// LogPath is where a background server started by the CLI writes its output.
func LogPath() string { return filepath.Join(paths.StateDir(), "daemon.log") }

// Read loads daemon.json.
func Read() (*Info, error) {
	data, err := os.ReadFile(Path())
	if err != nil {
		return nil, err
	}
	var i Info
	if err := json.Unmarshal(data, &i); err != nil {
		return nil, err
	}
	return &i, nil
}

// Write saves daemon.json atomically with mode 0600.
func Write(i Info) error {
	if err := os.MkdirAll(paths.StateDir(), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(i, "", "  ")
	tmp := Path() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, Path())
}

// Remove deletes daemon.json if it still describes process pid.
func Remove(pid int) {
	if i, err := Read(); err == nil && i.PID == pid {
		_ = os.Remove(Path())
	}
}

// BrowserHost is the host name to put in URLs for this server.
func (i *Info) BrowserHost() string {
	switch i.Host {
	case "", "localhost", "127.0.0.1", "::1", "0.0.0.0", "::":
		return "localhost"
	}
	return i.Host
}

// URL is the address of a path on the server.
func (i *Info) URL(path string) string {
	return "http://" + net.JoinHostPort(i.BrowserHost(), strconv.Itoa(i.Port)) + path
}

var client = &http.Client{Timeout: 5 * time.Second}

// Health asks the server for its version.
func (i *Info) Health(ctx context.Context) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+net.JoinHostPort("127.0.0.1", strconv.Itoa(i.Port))+"/_serve/health", nil)
	req.Host = net.JoinHostPort("localhost", strconv.Itoa(i.Port))
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var h struct {
		Version string `json:"version"`
		PID     int    `json:"pid"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		return "", err
	}
	if h.PID != i.PID {
		return "", errors.New("a different server is on the port")
	}
	return h.Version, nil
}

// Call makes an API request with the token.
func (i *Info) Call(method, apiPath string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, "http://"+net.JoinHostPort("127.0.0.1", strconv.Itoa(i.Port))+"/_serve/api/"+apiPath, r)
	req.Host = net.JoinHostPort("localhost", strconv.Itoa(i.Port))
	req.Header.Set("X-Serve-Token", i.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server said %s: %s", resp.Status, bytes.TrimSpace(msg))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// Running returns the running server, if there is one that answers.
func Running() (*Info, bool) {
	i, err := Read()
	if err != nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := i.Health(ctx); err != nil {
		return nil, false
	}
	return i, true
}

// Options say how to start a server.
type Options struct {
	Exe     string // the serve binary
	Version string
	Port    int
	Host    string
}

// Ensure returns a running server of this version, starting one (or
// replacing an older one) if needed.
func Ensure(o Options) (*Info, error) {
	if i, ok := Running(); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		v, _ := i.Health(ctx)
		cancel()
		// A server of this version is reused whatever port was asked for:
		// stopping it would take down every tab and app using it. Only an
		// upgrade, or an explicit change of host (sharing), replaces it.
		sameHost := o.Host == "" || o.Host == i.Host || (isLoopback(o.Host) && isLoopback(i.Host))
		if v == o.Version && sameHost {
			return i, nil
		}
		if err := Stop(i); err != nil {
			return nil, fmt.Errorf("stopping the old server: %w", err)
		}
	}
	if err := spawn(o); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if i, ok := Running(); ok {
			return i, nil
		}
		time.Sleep(60 * time.Millisecond)
	}
	return nil, fmt.Errorf("the server did not start; see %s", LogPath())
}

func isLoopback(h string) bool {
	switch h {
	case "", "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// Stop asks a server to shut down and waits for it to go.
func Stop(i *Info) error {
	_ = i.Call("POST", "shutdown", map[string]any{}, nil)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !alive(i.PID) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	if p, err := os.FindProcess(i.PID); err == nil {
		_ = p.Kill()
	}
	return nil
}

func spawn(o Options) error {
	args := []string{"daemon"}
	if o.Port != 0 {
		args = append(args, "--port", strconv.Itoa(o.Port))
	}
	if o.Host != "" {
		args = append(args, "--host", o.Host)
	}
	if err := os.MkdirAll(paths.StateDir(), 0o700); err != nil {
		return err
	}
	logf, err := os.OpenFile(LogPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(o.Exe, args...)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.Stdin = nil
	cmd.SysProcAttr = detached()
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
