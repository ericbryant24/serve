package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"serve/internal/daemon"
	"serve/internal/paths"
	"serve/internal/store"
)

const statusUsage = `Usage: serve status [--json]

Show the background server, the folders it serves, and any servers left
running by older versions of serve (one per folder, on ports from 8000).`

type legacy struct {
	PID  int    `json:"pid"`
	Port int    `json:"port"`
	Args string `json:"cmdline"`
	CWD  string `json:"cwd,omitempty"`
}

func cmdStatus(args []string) error {
	fs := flags("status", statusUsage)
	asJSON := fs.Bool("json", false, "")
	if _, err := parse(fs, statusUsage, args); err != nil {
		return err
	}
	info, running := daemon.Running()
	var folders []store.Folder
	if st, err := store.Open(store.DefaultPath()); err == nil {
		folders, _ = st.Folders()
		st.Close()
	}
	old := legacyServers(info)
	if *asJSON {
		out := map[string]any{"running": running, "folders": folders, "legacy": old}
		if running {
			out["pid"], out["port"], out["version"], out["url"], out["started"] = info.PID, info.Port, info.Version, info.URL("/"), info.Started
		}
		if folders == nil {
			out["folders"] = []store.Folder{}
		}
		if old == nil {
			out["legacy"] = []legacy{}
		}
		return printJSON(out)
	}
	if running {
		started := info.Started
		if t, err := time.Parse(time.RFC3339, info.Started); err == nil {
			started = t.Local().Format("Jan 2 15:04")
		}
		fmt.Printf("Running: %s  (pid %d, version %s, since %s)\n", info.URL("/"), info.PID, info.Version, started)
	} else {
		fmt.Println("Not running. 'serve <path>' starts it.")
	}
	if len(folders) > 0 {
		fmt.Println("\nOpened folders:")
		for _, f := range folders {
			fmt.Printf("  %s\n", paths.Display(f.Path))
		}
	}
	if len(old) > 0 {
		fmt.Println("\nServers from an older serve (stop them with 'serve stop --all'):")
		for _, l := range old {
			fmt.Printf("  pid %-6d port %-5d %s\n", l.PID, l.Port, l.Args)
		}
	}
	return nil
}

const stopUsage = `Usage: serve stop [--all]

Stop the background server. Open tabs reconnect when it starts again.
--all also stops servers left running by older versions of serve.`

func cmdStop(args []string) error {
	fs := flags("stop", stopUsage)
	all := fs.Bool("all", false, "")
	if _, err := parse(fs, stopUsage, args); err != nil {
		return err
	}
	info, running := daemon.Running()
	if running {
		if err := daemon.Stop(info); err != nil {
			return err
		}
		fmt.Printf("Stopped the server (pid %d).\n", info.PID)
	} else {
		fmt.Println("The server is not running.")
	}
	if *all {
		for _, l := range legacyServers(nil) {
			if p, err := os.FindProcess(l.PID); err == nil && p.Signal(syscall.SIGTERM) == nil {
				fmt.Printf("Stopped pid %d (port %d).\n", l.PID, l.Port)
			}
		}
	}
	return nil
}

const killUsage = `Usage: serve kill <pid>... | --all

Older name. 'serve kill --all' is 'serve stop --all'; 'serve kill <pid>'
stops one server left running by an older serve.`

func cmdKill(args []string) error {
	fs := flags("kill", killUsage)
	all := fs.Bool("all", false, "")
	fs.Bool("force", false, "")
	fs.Int("port", 0, "")
	pos, err := parse(fs, killUsage, args)
	if err != nil {
		return err
	}
	if *all {
		return cmdStop([]string{"--all"})
	}
	if len(pos) == 0 {
		return usageError{killUsage}
	}
	known := map[int]bool{}
	for _, l := range legacyServers(nil) {
		known[l.PID] = true
	}
	failed := false
	for _, a := range pos {
		pid, err := strconv.Atoi(a)
		if err != nil || !known[pid] {
			fmt.Fprintf(os.Stderr, "serve: %s is not a serve server\n", a)
			failed = true
			continue
		}
		if p, err := os.FindProcess(pid); err == nil && p.Signal(syscall.SIGTERM) == nil {
			fmt.Printf("Stopped pid %d.\n", pid)
		}
	}
	if failed {
		return errQuiet
	}
	return nil
}

var lsofPortRe = regexp.MustCompile(`:(\d+)\s*\(LISTEN\)`)

// legacyServers finds serve processes listening on a port that are not the
// current background server: servers started by an older serve, one per
// folder.
func legacyServers(cur *daemon.Info) []legacy {
	if runtime.GOOS == "windows" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-axo", "pid=,command=").Output()
	if err != nil {
		return nil
	}
	self := os.Getpid()
	var res []legacy
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		pidStr, cmdline, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		pid, err := strconv.Atoi(pidStr)
		if err != nil || pid == self || (cur != nil && pid == cur.PID) {
			continue
		}
		f := strings.Fields(strings.TrimSpace(cmdline))
		if len(f) == 0 {
			continue
		}
		base := filepath.Base(f[0])
		if base != "serve" && base != "serve-go" {
			continue
		}
		if len(f) > 1 && f[1] == "daemon" {
			if i, err := daemon.Read(); err == nil && i.PID == pid {
				continue
			}
		}
		port := listeningPort(ctx, pid)
		if port == 0 {
			continue
		}
		res = append(res, legacy{PID: pid, Port: port, Args: strings.TrimSpace(cmdline)})
	}
	return res
}

func listeningPort(ctx context.Context, pid int) int {
	out, err := exec.CommandContext(ctx, "lsof", "-a", "-nP", "-iTCP", "-sTCP:LISTEN", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0
	}
	min := 0
	for _, line := range strings.Split(string(out), "\n") {
		m := lsofPortRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		p, _ := strconv.Atoi(m[1])
		if min == 0 || p < min {
			min = p
		}
	}
	return min
}
