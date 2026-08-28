package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func printReportUsage() {
	fmt.Print(`Usage: serve report [subcommand]

Bug reports and feature requests captured from the browser. Reports are stored
under ~/.serve/reports/ and are never sent anywhere on their own.

Subcommands:
  serve report                  List stored reports (also: --json)
  serve report show <id>        Print one report as JSON
  serve report export <id>      Print the issue markdown to stdout
  serve report open <id>        Open the report folder
  serve report file <id>        File the report as a GitHub issue
  serve report login            Authorize this machine with GitHub (--force to redo)
  serve report logout           Forget the stored GitHub token
  serve report rm <id>...       Delete reports and their attachments
`)
}

func cmdReport(args []string) {
	if len(args) == 0 {
		reportList(false)
		return
	}
	switch args[0] {
	case "-h", "--help":
		printReportUsage()
	case "--json":
		reportList(true)
	case "show":
		reportShow(args[1:])
	case "export":
		reportExport(args[1:])
	case "open":
		reportOpen(args[1:])
	case "file":
		reportFile(args[1:])
	case "login":
		reportLogin(args[1:])
	case "logout":
		reportLogout()
	case "rm":
		reportRemove(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown subcommand: %s\n\n", args[0])
		printReportUsage()
		os.Exit(1)
	}
}

func reportArg(args []string, what string) string {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: serve report %s <id>\n", what)
		os.Exit(1)
	}
	return args[0]
}

func mustGet(id string) (*ReportStore, *Report) {
	store := NewReportStore()
	rep, err := store.Get(id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "No report with id %s\n", id)
		os.Exit(1)
	}
	return store, rep
}

func reportList(asJSON bool) {
	reports, err := NewReportStore().List()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	if asJSON {
		out, _ := json.MarshalIndent(reports, "", "  ")
		fmt.Println(string(out))
		return
	}
	if len(reports) == 0 {
		fmt.Println("No reports yet. Use the Report button in the browser to capture one.")
		return
	}
	for _, r := range reports {
		status := "local"
		if r.Filed() {
			status = "filed"
		}
		date := r.CreatedAt
		if t, err := time.Parse(time.RFC3339, r.CreatedAt); err == nil {
			date = t.Local().Format("2006-01-02 15:04")
		}
		title := r.Title
		if len(title) > 52 {
			title = title[:51] + "…"
		}
		fmt.Printf("%-12s  %-7s  %-5s  %-16s  %s\n", r.ID, r.Kind, status, date, title)
	}
	fmt.Printf("\n%d report(s) in %s\n", len(reports), reportsDir())
}

func reportShow(args []string) {
	_, rep := mustGet(reportArg(args, "show"))
	out, _ := json.MarshalIndent(rep, "", "  ")
	fmt.Println(string(out))
}

// reportExport prints the issue markdown. This is the path that needs no
// credentials and no network, so an organisation that routes feedback its own
// way can still use every other part of the feature.
func reportExport(args []string) {
	store, rep := mustGet(reportArg(args, "export"))
	fmt.Printf("# %s\n\n", rep.IssueTitle())
	fmt.Println(rep.Markdown())

	if att := rep.IncludedAttachments(); len(att) > 0 {
		fmt.Println()
		fmt.Println("<!-- attachments to add by hand:")
		for _, a := range att {
			if p, err := store.AttachmentPath(rep.ID, a.ID); err == nil {
				fmt.Printf("     %s\n", p)
			}
		}
		fmt.Println("-->")
	}
}

func reportOpen(args []string) {
	store, rep := mustGet(reportArg(args, "open"))
	dir, _ := store.Dir(rep.ID)
	if err := openInFileManager(dir); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		fmt.Println(dir)
		os.Exit(1)
	}
}

func reportRemove(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: serve report rm <id>...")
		os.Exit(1)
	}
	store := NewReportStore()
	failed := 0
	for _, id := range args {
		if err := store.Delete(id); err != nil {
			fmt.Fprintf(os.Stderr, "Not found: %s\n", id)
			failed++
			continue
		}
		fmt.Printf("Deleted %s\n", id)
	}
	if failed > 0 {
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// GitHub
// ---------------------------------------------------------------------------

// authorize runs the device flow and caches the token. The reporter approves
// on github.com, so the issue is filed under their own account.
func authorize(ctx context.Context) (string, error) {
	clientID := githubClientID()
	if clientID == "" {
		return "", errNoClientID
	}
	c := newGHClient()
	dc, err := c.DeviceStart(ctx, clientID)
	if err != nil {
		return "", err
	}
	fmt.Printf("\n  Open %s\n  and enter this code:\n\n      %s\n\n",
		dc.VerificationURI, dc.UserCode)
	fmt.Println("  Waiting for approval…")

	token, err := c.DevicePoll(ctx, clientID, dc)
	if err != nil {
		return "", err
	}
	if err := saveGHToken(token); err != nil {
		fmt.Fprintln(os.Stderr, "Warning: could not cache the token:", err)
	}
	return token, nil
}

func reportLogin(args []string) {
	if !uploadAllowed() {
		fmt.Fprintln(os.Stderr, uploadBlockedReason())
		os.Exit(1)
	}
	force := false
	for _, a := range args {
		if a == "--force" || a == "-f" {
			force = true
		}
	}
	// Without this, re-running login on an already-authorized machine starts a
	// second device flow and sits there polling for a code nobody asked for.
	if !force && loadGHToken() != "" {
		fmt.Println("Already signed in; the token is stored at", ghTokenPath())
		fmt.Println("Use `serve report login --force` to authorize again, or `serve report logout` to sign out.")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	if _, err := authorize(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	fmt.Println("Authorized. The token is stored at", ghTokenPath())
}

func reportLogout() {
	if err := clearGHToken(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	fmt.Println("Signed out.")
}

func reportFile(args []string) {
	if !uploadAllowed() {
		fmt.Fprintln(os.Stderr, uploadBlockedReason())
		os.Exit(1)
	}
	assumeYes := false
	var rest []string
	for _, a := range args {
		if a == "--yes" || a == "-y" {
			assumeYes = true
			continue
		}
		rest = append(rest, a)
	}
	store, rep := mustGet(reportArg(rest, "file"))

	if rep.Filed() {
		fmt.Printf("Already filed: %s\n", rep.Upload.IssueURL)
		return
	}

	owner, repo := githubTarget()
	fmt.Printf("Filing to github.com/%s/%s as a public issue.\n\n", owner, repo)
	fmt.Printf("  %s\n\n", rep.IssueTitle())
	fmt.Println(indent(rep.Markdown(), "  "))

	if hits := scanSecrets(rep.Title + "\n" + rep.Body); len(hits) > 0 {
		fmt.Printf("\n  %d item(s) in this report look like credentials:\n", len(hits))
		for _, h := range hits {
			fmt.Printf("    %s: %s\n", h.Kind, h.Excerpt)
		}
	}
	if !assumeYes && !confirm("\nFile this issue?") {
		fmt.Println("Not filed.")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	token := loadGHToken()
	if token == "" {
		var err error
		if token, err = authorize(ctx); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}
	}

	url, err := newGHClient().CreateIssue(ctx, token, owner, repo, rep)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	if _, err := store.Update(rep.ID, func(r *Report) error {
		r.Upload = &UploadState{
			Status:   UploadFiled,
			IssueURL: url,
			FiledAt:  time.Now().UTC().Format(time.RFC3339),
		}
		return nil
	}); err != nil {
		fmt.Fprintln(os.Stderr, "Warning: filed, but the report could not be updated:", err)
	}

	fmt.Println("\nFiled:", url)
	if att := rep.IncludedAttachments(); len(att) > 0 {
		// The GitHub REST API has no endpoint for issue attachments, so the
		// files have to be dropped into the issue by hand.
		fmt.Println("\nTo attach the files, drag these into the issue:")
		dir, _ := store.Dir(rep.ID)
		for _, a := range att {
			fmt.Println("  " + filepath.Join(dir, a.File))
		}
	}
}

// confirm asks before an irreversible, outward-facing action. A public issue
// cannot be meaningfully retracted, so this never auto-answers: without a
// terminal the caller has to pass --yes explicitly.
func confirm(prompt string) bool {
	fi, err := os.Stdin.Stat()
	if err != nil || (fi.Mode()&os.ModeCharDevice) == 0 {
		fmt.Fprintln(os.Stderr,
			"\nRefusing to file without confirmation. Re-run with --yes if you meant to.")
		return false
	}
	fmt.Printf("%s [y/N] ", prompt)
	reader := bufio.NewReader(os.Stdin)
	ans, _ := reader.ReadString('\n')
	ans = strings.ToLower(strings.TrimSpace(ans))
	return ans == "y" || ans == "yes"
}

func indent(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}
