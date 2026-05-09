package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

// Instance represents a running serve process.
type Instance struct {
	PID     int    `json:"pid"`
	Port    int    `json:"port"`
	Path    string `json:"path"`
	Mode    string `json:"mode"`
	Started string `json:"started"`
	Cmdline string `json:"cmdline"`
}

func (i Instance) URL() string {
	if i.Port == 0 {
		return ""
	}
	return fmt.Sprintf("http://localhost:%d", i.Port)
}

var lsofPortRe = regexp.MustCompile(`:(\d+)\s*\(LISTEN\)`)

var subcommands = map[string]bool{
	"list": true, "ls": true, "kill": true,
	"comments": true, "resolve": true, "agent-init": true,
}

// listInstances discovers running serve-go instances via ps.
func listInstances() []Instance {
	selfPID := os.Getpid()
	out, err := exec.Command("ps", "-axo", "pid=,command=").Output()
	if err != nil {
		return nil
	}

	var instances []Instance
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pidStr, cmdline, found := strings.Cut(line, " ")
		if !found {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(pidStr))
		if err != nil || pid == selfPID {
			continue
		}
		cmdline = strings.TrimSpace(cmdline)
		if !isServeInvocation(cmdline) {
			continue
		}
		argv := strings.Fields(cmdline)
		if isSubcommand(argv) {
			continue
		}
		positional := parseServeArgv(argv)
		cwd := processCWD(pid)
		path, mode := resolveServedPath(positional, cwd)
		port := listeningPort(pid)
		started := startTime(pid)
		instances = append(instances, Instance{
			PID:     pid,
			Port:    port,
			Path:    path,
			Mode:    mode,
			Started: started,
			Cmdline: cmdline,
		})
	}

	// Sort by port then pid
	for i := 1; i < len(instances); i++ {
		for j := i; j > 0; j-- {
			a, b := instances[j-1], instances[j]
			aKey := a.Port*1000000 + a.PID
			if a.Port == 0 {
				aKey = 999999*1000000 + a.PID
			}
			bKey := b.Port*1000000 + b.PID
			if b.Port == 0 {
				bKey = 999999*1000000 + b.PID
			}
			if aKey > bKey {
				instances[j-1], instances[j] = instances[j], instances[j-1]
			} else {
				break
			}
		}
	}
	return instances
}

func isServeInvocation(cmdline string) bool {
	// Match: .../serve-go, serve-go, or serve binary
	base := filepath.Base(strings.Fields(cmdline)[0])
	return base == "serve-go" || base == "serve"
}

func isSubcommand(argv []string) bool {
	if len(argv) < 2 {
		return false
	}
	return subcommands[argv[1]]
}

func parseServeArgv(argv []string) string {
	flagsWithValue := map[string]bool{"-p": true, "--port": true, "--host": true}
	i := 1
	for i < len(argv) {
		a := argv[i]
		if flagsWithValue[a] {
			i += 2
			continue
		}
		if strings.HasPrefix(a, "-") {
			i++
			continue
		}
		return a
	}
	return ""
}

func resolveServedPath(positional, cwd string) (string, string) {
	if positional == "" {
		if cwd == "" {
			return "(default)", "?"
		}
		index := filepath.Join(cwd, "index.html")
		if fi, err := os.Stat(index); err == nil && !fi.IsDir() {
			return index, "html"
		}
		return cwd, "directory"
	}
	p := positional
	if !filepath.IsAbs(p) && cwd != "" {
		p = filepath.Join(cwd, p)
	}
	p, _ = filepath.Abs(p)
	if fi, err := os.Stat(p); err == nil && fi.IsDir() {
		return p, "directory"
	}
	ext := strings.ToLower(filepath.Ext(p))
	switch ext {
	case ".md":
		return p, "markdown"
	case ".html", ".htm":
		return p, "html"
	default:
		return p, "?"
	}
}

func listeningPort(pid int) int {
	out, err := exec.Command("lsof", "-a", "-nP", "-iTCP", "-sTCP:LISTEN",
		"-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0
	}
	minPort := 0
	for _, line := range strings.Split(string(out), "\n")[1:] {
		m := lsofPortRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		p, _ := strconv.Atoi(m[1])
		if minPort == 0 || p < minPort {
			minPort = p
		}
	}
	return minPort
}

func startTime(pid int) string {
	out, err := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func processCWD(pid int) string {
	out, err := exec.Command("lsof", "-a", "-d", "cwd", "-Fn",
		"-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "n") {
			return line[1:]
		}
	}
	return ""
}

// killInstance sends SIGTERM (or SIGKILL) to pid.
func killInstance(pid int, force bool) error {
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(sig)
}
