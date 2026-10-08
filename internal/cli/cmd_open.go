package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"serve/internal/config"
	"serve/internal/daemon"
	"serve/internal/paths"
	"serve/internal/server"
	"serve/internal/store"
	"serve/web"
)

const openUsage = `Usage: serve [open] [path] [--port N] [--host H] [--no-open] [--json]

Open a file or folder in the browser. The first use starts serve's
background server; later ones reuse it, so the command returns at once.
A file opens with its folder in the sidebar. The URL is the file's own
path (http://localhost:7070/~/Projects/x/doc.md), so it keeps working
across restarts.

Options:
  --port N    port for the background server (default 7070, or "port" in
              ~/.serve/config.json)
  --host H    address to listen on; anything but localhost turns on share
              links (read and comment only)
  --no-open   print the URL instead of opening a browser
  --json      print {"url", "embed_url", "origin", "port"} as JSON (implies
              --no-open), for apps that show serve in a frame

A running server is reused whatever --port says; 'serve stop' first to move it.
Add ?embed=1 to a document's URL for the document and its comments alone,
without serve's own toolbar and file tree.`

func cmdOpen(args []string) error {
	fs := flags("open", openUsage)
	port := fs.Int("port", 0, "")
	fs.IntVar(port, "p", 0, "")
	host := fs.String("host", "", "")
	noOpen := fs.Bool("no-open", false, "")
	asJSON := fs.Bool("json", false, "")
	pos, err := parse(fs, openUsage, args)
	if err != nil {
		return err
	}
	if len(pos) > 1 {
		return usageError{"serve opens one path at a time\n\n" + openUsage}
	}
	target := "."
	if len(pos) == 1 {
		target = pos[0]
	}
	abs := paths.Abs(target)
	fi, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("%s does not exist", target)
	}
	folder := abs
	if !fi.IsDir() {
		folder = filepath.Dir(abs)
	}
	cfg := config.Load()
	p := cfg.Port
	if *port != 0 {
		p = *port
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	info, err := daemon.Ensure(daemon.Options{Exe: exe, Version: Version, Port: p, Host: *host})
	if err != nil {
		return err
	}
	if err := info.Call("POST", "folders", map[string]string{"path": folder}, nil); err != nil {
		return err
	}
	url := info.URL(paths.URLPath(abs))
	if *asJSON {
		return printJSON(map[string]any{"url": url, "embed_url": url + "?embed=1", "origin": info.URL(""), "port": info.Port, "path": abs})
	}
	if *port != 0 && *port != info.Port {
		fmt.Fprintf(os.Stderr, "serve: already running on port %d; 'serve stop' first to move it\n", info.Port)
	}
	fmt.Printf("%s → %s\n", paths.Display(abs), url)
	if !isLoopbackHost(info.Host) {
		if ip := lanIP(); ip != "" {
			fmt.Printf("Share (read and comment): http://%s%s?share=%s\n", net.JoinHostPort(ip, fmt.Sprint(info.Port)), paths.URLPath(abs), info.ShareToken)
		}
	}
	if !*noOpen {
		_ = openBrowser(url)
	}
	return nil
}

func isLoopbackHost(h string) bool {
	switch h {
	case "", "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

func lanIP() string {
	conn, err := net.Dial("udp", "192.0.2.1:9")
	if err != nil {
		return ""
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}

const homeUsage = `Usage: serve home

Open serve's start page: recent folders, recently commented documents, and
every thread waiting for your reply.`

func cmdHome(args []string) error {
	fs := flags("home", homeUsage)
	noOpen := fs.Bool("no-open", false, "")
	if _, err := parse(fs, homeUsage, args); err != nil {
		return err
	}
	cfg := config.Load()
	exe, _ := os.Executable()
	info, err := daemon.Ensure(daemon.Options{Exe: exe, Version: Version, Port: cfg.Port})
	if err != nil {
		return err
	}
	url := info.URL("/")
	fmt.Println(url)
	if !*noOpen {
		_ = openBrowser(url)
	}
	return nil
}

const daemonUsage = `Usage: serve daemon [--port N] [--host H]

Run the background server in the foreground. 'serve <path>' starts it on
its own; this is for running it under a supervisor or for debugging.`

func cmdDaemon(args []string) error {
	fs := flags("daemon", daemonUsage)
	port := fs.Int("port", 0, "")
	host := fs.String("host", "", "")
	if _, err := parse(fs, daemonUsage, args); err != nil {
		return err
	}
	cfg := config.Load()
	if *port == 0 {
		*port = cfg.Port
	}
	if i, ok := daemon.Running(); ok {
		return fmt.Errorf("a server is already running (pid %d, port %d)", i.PID, i.Port)
	}
	st, err := store.Open(store.DefaultPath())
	if err != nil {
		return err
	}
	defer st.Close()
	srv := server.New(server.Options{Host: *host, Port: *port, Version: Version, Store: st, Config: cfg, Assets: web.Assets()})
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := srv.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
