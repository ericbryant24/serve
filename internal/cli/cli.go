// Package cli is serve's command line.
package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"serve/internal/comments"
	"serve/internal/config"
	"serve/internal/reports"
	"serve/internal/store"
)

// Version is set by main.
var Version = "dev"

// usageError is an error whose message is the command's usage.
type usageError struct{ msg string }

func (u usageError) Error() string { return u.msg }

type command struct {
	name    string
	summary string
	run     func(args []string) error
	hidden  bool
}

var commands []command

func init() {
	commands = []command{
		{"open", "Open a file or folder in the browser (the default command)", cmdOpen, false},
		{"comments", "List a document's comment threads (JSON)", cmdComments, false},
		{"reply", "Reply to a comment thread", cmdReply, false},
		{"resolve", "Resolve threads, optionally with a closing note", cmdResolve, false},
		{"reopen", "Reopen resolved threads", cmdReopen, false},
		{"edit", "Change the text of a message", cmdEdit, false},
		{"delete", "Delete a thread or a message", cmdDelete, false},
		{"wait", "Block until the next comment event, print it, exit", cmdWait, false},
		{"watch", "Stream comment events as JSON lines", cmdWatch, false},
		{"inbox", "List threads waiting for a reply, across all documents", cmdInbox, false},
		{"export", "Export a document's threads (JSON), or the document as standalone HTML", cmdExport, false},
		{"status", "Show the background server and the folders it serves", cmdStatus, false},
		{"stop", "Stop the background server", cmdStop, false},
		{"home", "Open the start page", cmdHome, false},
		{"report", "Bug reports captured in the browser", cmdReport, false},
		{"gc", "List (or remove) comments on files that no longer exist", cmdGC, false},
		{"agent-init", "Install the serve skill for Claude Code", cmdAgentInit, false},
		{"version", "Print the version", cmdVersion, false},
		{"daemon", "Run the background server in the foreground", cmdDaemon, true},
		// Older names, kept working.
		{"list", "", cmdStatus, true},
		{"ls", "", cmdStatus, true},
		{"kill", "", cmdKill, true},
	}
}

// Main runs the command line and returns the exit code.
func Main(version string, args []string) int {
	Version = buildVersion(version)
	reports.Version = version
	if len(args) == 0 {
		return finish(cmdOpen(nil))
	}
	switch args[0] {
	case "-h", "--help", "help":
		if len(args) > 1 {
			for _, c := range commands {
				if c.name == args[1] {
					return finish(c.run([]string{"--help"}))
				}
			}
		}
		printUsage(os.Stdout)
		return 0
	case "-v", "--version":
		return finish(cmdVersion(nil))
	}
	for _, c := range commands {
		if c.name == args[0] {
			return finish(c.run(args[1:]))
		}
	}
	if strings.HasPrefix(args[0], "-") || !looksLikeCommand(args[0]) {
		return finish(cmdOpen(args))
	}
	fmt.Fprintf(os.Stderr, "serve: unknown command %q (run 'serve help')\n", args[0])
	return 2
}

// looksLikeCommand tells a mistyped command from a path to open: a bare word
// that is not a file is taken as a command.
func looksLikeCommand(a string) bool {
	if strings.ContainsAny(a, "/.\\~") {
		return false
	}
	if _, err := os.Stat(a); err == nil {
		return false
	}
	return true
}

var errQuiet = errors.New("")

func finish(err error) int {
	if err == nil {
		return 0
	}
	var ue usageError
	if errors.As(err, &ue) {
		fmt.Fprintln(os.Stderr, ue.msg)
		return 2
	}
	var ec exitCode
	if errors.As(err, &ec) {
		return int(ec)
	}
	if err != errQuiet && err.Error() != "" {
		fmt.Fprintln(os.Stderr, "serve:", err)
	}
	return 1
}

type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit %d", int(e)) }

func printUsage(w io.Writer) {
	fmt.Fprint(w, `serve — preview documents in the browser and review them with inline comments

Usage:
  serve [path] [--port N] [--host H] [--no-open]
  serve <command> [arguments]

Commands:
`)
	for _, c := range commands {
		if !c.hidden {
			fmt.Fprintf(w, "  %-11s %s\n", c.name, c.summary)
		}
	}
	fmt.Fprint(w, `
Run 'serve help <command>' for a command's options.
`)
}

// flags makes a flag set whose usage prints the given synopsis.
func flags(name, synopsis string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	fs.Bool("help", false, "")
	fs.Bool("h", false, "")
	_ = synopsis
	return fs
}

// parse parses flags that may appear before, between or after positional
// arguments, and stops at "--". Unknown flags are errors.
func parse(fs *flag.FlagSet, synopsis string, args []string) ([]string, error) {
	var pos, rest []string
	for i, a := range args {
		if a == "--" {
			rest = args[i+1:]
			args = args[:i]
			break
		}
	}
	for {
		if err := fs.Parse(args); err != nil {
			return nil, usageError{fmt.Sprintf("%v\n\n%s", err, synopsis)}
		}
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
	if h := fs.Lookup("help"); h != nil && h.Value.String() == "true" {
		fmt.Println(synopsis)
		return nil, exitCode(0)
	}
	if h := fs.Lookup("h"); h != nil && h.Value.String() == "true" {
		fmt.Println(synopsis)
		return nil, exitCode(0)
	}
	return append(pos, rest...), nil
}

// openService opens the comment database (importing the old store the first
// time) and returns the comment service.
func openService() (*comments.Service, func(), error) {
	st, err := store.Open(store.DefaultPath())
	if err != nil {
		return nil, nil, err
	}
	svc := &comments.Service{Store: st, Config: config.Load()}
	if _, err := svc.ImportV1(comments.V1Dir()); err != nil {
		fmt.Fprintln(os.Stderr, "serve: importing comments from the old store failed:", err)
	}
	return svc, func() { st.Close() }, nil
}

// parseAuthor reads --as: "agent", "human", "agent:Claude", "human:Eric".
func parseAuthor(v string, cfg config.Config) (store.Author, error) {
	if v == "" {
		return store.Author{Kind: store.Agent, Name: config.AgentName()}, nil
	}
	kind, name, _ := strings.Cut(v, ":")
	switch kind {
	case store.Agent:
		if name == "" {
			name = config.AgentName()
		}
	case store.Human:
		if name == "" {
			name = cfg.Name
		}
	default:
		return store.Author{}, fmt.Errorf("--as must be agent, human, agent:<name> or human:<name>")
	}
	return store.Author{Kind: kind, Name: name}, nil
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// readText returns the text of a message given as arguments, or read from
// stdin when the only argument is "-".
func readText(args []string) (string, error) {
	if len(args) == 1 && args[0] == "-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	}
	return strings.TrimSpace(strings.Join(args, " ")), nil
}

func openBrowser(url string) error {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", url)
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		c = exec.Command("xdg-open", url)
	}
	return c.Start()
}

// buildVersion gives a development build (version "dev") an identity from
// its binary, so the CLI notices a rebuilt serve and restarts the server.
func buildVersion(v string) string {
	if v != "dev" {
		return v
	}
	exe, err := os.Executable()
	if err != nil {
		return v
	}
	fi, err := os.Stat(exe)
	if err != nil {
		return v
	}
	return "dev+" + strconv.FormatInt(fi.ModTime().Unix(), 36)
}

func cmdVersion(args []string) error {
	fmt.Println("serve", Version)
	return nil
}
