package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

var version = "dev"

func main() {
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "--version", "-v":
			fmt.Println("serve", version)
			return
		case "agent-init":
			if err := cmdAgentInit(); err != nil {
				fmt.Fprintln(os.Stderr, "Error:", err)
				os.Exit(1)
			}
			return
		case "comments":
			cmdComments(args[1:])
			return
		case "resolve":
			cmdResolve(args[1:])
			return
		case "list", "ls":
			cmdList(args[1:])
			return
		case "kill":
			cmdKill(args[1:])
			return
		case "home":
			cmdHome(args[1:])
			return
		case "watch":
			cmdWatch(args[1:])
			return
		}
	}
	cmdServe(args)
}

// ---------------------------------------------------------------------------
// serve (default) — start the server
// ---------------------------------------------------------------------------

func cmdServe(args []string) {
	host := "localhost"
	port := 8000
	noOpen := false
	dataURL := false
	var fileArg string

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
		case a == "--host":
			if i+1 < len(args) {
				host = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--host="):
			host = strings.TrimPrefix(a, "--host=")
		case a == "--no-open":
			noOpen = true
		case a == "--data-url":
			dataURL = true
		case a == "-h" || a == "--help":
			printServeUsage()
			return
		case !strings.HasPrefix(a, "-"):
			fileArg = a
		}
	}

	filePath, err := resolvePath(fileArg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}

	// Determine mode
	var mode string
	if fi, err := os.Stat(filePath); err == nil && fi.IsDir() {
		mode = "directory"
	} else {
		ext := strings.ToLower(filepath.Ext(filePath))
		switch ext {
		case ".md":
			mode = "markdown"
		case ".html", ".htm":
			mode = "html"
		default:
			fmt.Fprintf(os.Stderr, "Error: unsupported file type '%s' (use .html, .htm, or .md)\n", ext)
			os.Exit(1)
		}
	}

	if dataURL {
		if mode == "directory" {
			fmt.Fprintln(os.Stderr, "Error: --data-url is not supported for directories")
			os.Exit(1)
		}
		url, err := generateDataURL(filePath, mode)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}
		// Try pbcopy (macOS)
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(url)
		if err := cmd.Run(); err == nil {
			fmt.Printf("Data URL copied to clipboard (%s bytes)\n", commaFmt(len(url)))
		} else {
			fmt.Println(url)
		}
		return
	}

	srv := NewServer(filePath, mode, host, port)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nStopped")
		cancel()
	}()

	if !noOpen {
		srv.openBrowserFn = func(url string) {
			_ = exec.Command("open", url).Start()
		}
	}

	if err := srv.Start(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func resolvePath(arg string) (string, error) {
	if arg == "" {
		cwd, _ := os.Getwd()
		index := filepath.Join(cwd, "index.html")
		if fi, err := os.Stat(index); err == nil && !fi.IsDir() {
			return index, nil
		}
		return cwd, nil
	}
	p, _ := filepath.Abs(arg)
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("%s not found", p)
	}
	return p, nil
}

func resolveFile(arg string) (string, error) {
	if arg == "" {
		return "", fmt.Errorf("file argument required")
	}
	p, _ := filepath.Abs(arg)
	fi, err := os.Stat(p)
	if err != nil {
		return "", fmt.Errorf("%s not found", p)
	}
	if fi.IsDir() {
		return "", fmt.Errorf("%s is a directory", p)
	}
	return p, nil
}

func printServeUsage() {
	fmt.Print(`Usage: serve [file] [flags]

Serve a markdown or HTML file with live reload and inline comments,
or a directory with sidebar navigation.

Arguments:
  file         File (.html, .htm, .md) or directory to serve.
               Defaults to index.html if present, otherwise current directory.

Flags:
  -p, --port N    Port to serve on (default: 8000)
  --host HOST     Host to bind to (default: localhost)
  --no-open       Don't open the browser automatically
  --data-url      Generate a self-contained data URL (no server)

Subcommands:
  serve agent-init                   Set up agent integration
  serve comments <file>              List inline comments (JSON)
  serve resolve <file> <id> [id...]  Mark comments as resolved
  serve watch [file] [--new]         Stream comment-change events as JSONL
  serve list                         List running serve instances
  serve kill <pid>... | --all        Stop running serve instances
  serve home [--port N]              Dashboard of running instances (default port 7070)
`)
}

// ---------------------------------------------------------------------------
// comments subcommand
// ---------------------------------------------------------------------------

func cmdComments(args []string) {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Println("Usage: serve comments <file>")
		return
	}
	filePath, err := resolveFile(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	docID := storeKeyForFile(filePath)
	store := NewCommentStoreForFile(filePath, commentStoreDir())
	comments, err := store.List()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	if comments == nil {
		comments = []Comment{}
	}
	out := map[string]interface{}{
		"file":     filePath,
		"doc_id":   docID,
		"comments": comments,
	}
	data, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(data))
}

// ---------------------------------------------------------------------------
// resolve subcommand
// ---------------------------------------------------------------------------

func cmdResolve(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: serve resolve <file> <id> [id...]")
		os.Exit(1)
	}
	filePath, err := resolveFile(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	store := NewCommentStoreForFile(filePath, commentStoreDir())
	failed := false
	for _, id := range args[1:] {
		resolved := true
		comment, err := store.Update(id, nil, &resolved)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error updating %s: %v\n", id, err)
			failed = true
			continue
		}
		if comment == nil {
			fmt.Fprintf(os.Stderr, "Not found: %s\n", id)
			failed = true
		} else {
			fmt.Printf("Resolved: %s\n", id)
		}
	}
	if failed {
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// list subcommand
// ---------------------------------------------------------------------------

func cmdList(args []string) {
	asJSON := false
	for _, a := range args {
		if a == "--json" {
			asJSON = true
		}
	}

	instances := listInstances()

	if asJSON {
		type instJSON struct {
			PID     int    `json:"pid"`
			Port    int    `json:"port"`
			Path    string `json:"path"`
			Mode    string `json:"mode"`
			Started string `json:"started"`
			Cmdline string `json:"cmdline"`
			URL     string `json:"url"`
		}
		var out []instJSON
		for _, i := range instances {
			out = append(out, instJSON{
				PID:     i.PID,
				Port:    i.Port,
				Path:    i.Path,
				Mode:    i.Mode,
				Started: i.Started,
				Cmdline: i.Cmdline,
				URL:     i.URL(),
			})
		}
		if out == nil {
			out = []instJSON{}
		}
		data, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(data))
		return
	}

	if len(instances) == 0 {
		fmt.Println("No serve instances running.")
		return
	}

	headers := []string{"PID", "PORT", "MODE", "STARTED", "URL", "PATH"}
	rows := make([][]string, len(instances))
	for i, inst := range instances {
		port := "?"
		if inst.Port != 0 {
			port = strconv.Itoa(inst.Port)
		}
		url := inst.URL()
		if url == "" {
			url = "?"
		}
		started := inst.Started
		if started == "" {
			started = "?"
		}
		rows[i] = []string{
			strconv.Itoa(inst.PID), port, inst.Mode, started, url, inst.Path,
		}
	}
	widths := make([]int, len(headers))
	for j, h := range headers {
		widths[j] = len(h)
	}
	for _, row := range rows {
		for j, cell := range row {
			if len(cell) > widths[j] {
				widths[j] = len(cell)
			}
		}
	}
	printRow := func(cells []string) {
		parts := make([]string, len(cells))
		for j, c := range cells {
			parts[j] = fmt.Sprintf("%-*s", widths[j], c)
		}
		fmt.Println(strings.Join(parts, "  "))
	}
	printRow(headers)
	for _, row := range rows {
		printRow(row)
	}
}

// ---------------------------------------------------------------------------
// kill subcommand
// ---------------------------------------------------------------------------

func cmdKill(args []string) {
	killAll := false
	killPort := 0
	force := false
	var pids []int

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--all":
			killAll = true
		case a == "--force":
			force = true
		case a == "--port":
			if i+1 < len(args) {
				p, _ := strconv.Atoi(args[i+1])
				killPort = p
				i++
			}
		case strings.HasPrefix(a, "--port="):
			killPort, _ = strconv.Atoi(strings.TrimPrefix(a, "--port="))
		case !strings.HasPrefix(a, "-"):
			pid, err := strconv.Atoi(a)
			if err == nil {
				pids = append(pids, pid)
			}
		}
	}

	if !killAll && killPort == 0 && len(pids) == 0 {
		fmt.Fprintln(os.Stderr, "Error: specify PID(s), --all, or --port")
		os.Exit(1)
	}

	instances := listInstances()
	byPID := map[int]Instance{}
	for _, i := range instances {
		byPID[i.PID] = i
	}

	var targets []int
	if killAll {
		for _, i := range instances {
			targets = append(targets, i.PID)
		}
	}
	if killPort != 0 {
		found := false
		for _, i := range instances {
			if i.Port == killPort {
				targets = append(targets, i.PID)
				found = true
				break
			}
		}
		if !found {
			fmt.Fprintf(os.Stderr, "No serve instance listening on port %d\n", killPort)
			os.Exit(1)
		}
	}
	targets = append(targets, pids...)

	seen := map[int]bool{}
	failed := false
	for _, pid := range targets {
		if seen[pid] {
			continue
		}
		seen[pid] = true
		inst, hasInst := byPID[pid]
		if err := killInstance(pid, force); err != nil {
			fmt.Fprintf(os.Stderr, "PID %d: %v\n", pid, err)
			failed = true
			continue
		}
		suffix := ""
		if hasInst && inst.Port != 0 {
			suffix = fmt.Sprintf(" (port %d)", inst.Port)
		}
		fmt.Printf("Killed PID %d%s\n", pid, suffix)
	}
	if failed {
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func commaFmt(n int) string {
	s := strconv.Itoa(n)
	result := make([]byte, 0, len(s)+(len(s)-1)/3)
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
}
