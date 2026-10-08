package cli

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"serve/internal/anchor"
	"serve/internal/comments"
	"serve/internal/paths"
	"serve/internal/store"
)

// The thread shape agents read. It keeps the field names `serve comments`
// has always had (id, text, anchor_text, source_line_start/end, resolved)
// and makes the line numbers current: they say where the passage is now,
// not where it was when the comment was made.
type cliMessage struct {
	ID        string       `json:"id"`
	Author    store.Author `json:"author"`
	Text      string       `json:"text"`
	CreatedAt string       `json:"created_at"`
	EditedAt  string       `json:"edited_at,omitempty"`
}

type cliThread struct {
	ID              string            `json:"id"`
	Status          string            `json:"status"`
	Resolved        bool              `json:"resolved"`
	Awaiting        string            `json:"awaiting,omitempty"`
	Scope           string            `json:"scope"`
	AnchorText      string            `json:"anchor_text,omitempty"`
	Text            string            `json:"text"`
	SourceLineStart int               `json:"source_line_start,omitempty"`
	SourceLineEnd   int               `json:"source_line_end,omitempty"`
	Location        comments.Location `json:"location"`
	Element         *anchor.Element   `json:"element,omitempty"`
	CreatedAt       string            `json:"created_at"`
	Messages        []cliMessage      `json:"messages"`
}

func toCLI(v comments.ThreadView) cliThread {
	t := cliThread{
		ID: v.ID, Status: v.Status, Resolved: v.Status == store.StatusResolved, Awaiting: v.Awaiting,
		Scope: v.Scope, Location: v.Location, Element: v.Anchor.Element, CreatedAt: v.CreatedAt,
		SourceLineStart: v.Location.LineStart, SourceLineEnd: v.Location.LineEnd,
	}
	t.AnchorText = v.Anchor.Quote
	if t.AnchorText == "" {
		t.AnchorText = v.Anchor.Display
	}
	for _, m := range v.Messages {
		t.Messages = append(t.Messages, cliMessage{ID: m.ID, Author: m.Author, Text: m.Body, CreatedAt: m.CreatedAt, EditedAt: m.EditedAt})
	}
	if len(t.Messages) > 0 {
		t.Text = t.Messages[0].Text
	}
	return t
}

func resolveFile(arg string) (string, error) {
	if arg == "" {
		return "", errors.New("a file is required")
	}
	p := paths.Abs(arg)
	fi, err := os.Stat(p)
	if err != nil {
		return "", fmt.Errorf("%s does not exist", arg)
	}
	if fi.IsDir() {
		return "", fmt.Errorf("%s is a folder", arg)
	}
	return p, nil
}

const commentsUsage = `Usage: serve comments <file> [--all] [--awaiting agent|human] [--format json|text]

List a document's open comment threads. Line numbers are where each passage
is now, carried through every edit since the comment was made. A thread's
location.state is "ok" (text intact), "changed" (rewritten; current_text is
what is there now, anchor_text what was commented on), "deleted" (removed;
the lines are where it was), "unplaced", or "page" (about the whole file).

The output includes a cursor: pass it to 'serve wait --since' to be woken by
anything that happens after this listing, with no gap.

Options:
  --all                     include resolved threads
  --awaiting agent|human    only threads whose last message is from the other side
  --format json|text        output format (default json)`

func cmdComments(args []string) error {
	fs := flags("comments", commentsUsage)
	all := fs.Bool("all", false, "")
	awaiting := fs.String("awaiting", "", "")
	format := fs.String("format", "json", "")
	fs.Bool("json", false, "")
	pos, err := parse(fs, commentsUsage, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageError{commentsUsage}
	}
	p, err := resolveFile(pos[0])
	if err != nil {
		return err
	}
	svc, done, err := openService()
	if err != nil {
		return err
	}
	defer done()
	cursor, _ := svc.Store.LastSeq()
	dv, err := svc.Open(p, false)
	if err != nil {
		return err
	}
	out := []cliThread{}
	for _, v := range dv.Threads {
		if !*all && v.Status != store.StatusOpen {
			continue
		}
		if *awaiting != "" && v.Awaiting != *awaiting {
			continue
		}
		out = append(out, toCLI(v))
	}
	if *format == "text" {
		printThreadsText(p, cursor, out)
		return nil
	}
	return printJSON(map[string]any{"file": p, "cursor": cursor, "threads": out})
}

func printThreadsText(p string, cursor int64, ts []cliThread) {
	fmt.Printf("%s · %d thread%s · cursor %d\n", paths.Display(p), len(ts), plural(len(ts)), cursor)
	for _, t := range ts {
		where := ""
		switch t.Location.State {
		case comments.LocPage:
			where = "whole page"
		case comments.LocUnplaced:
			where = "couldn't place"
			if t.Location.Section != "" {
				where += " (was in " + t.Location.Section + ")"
			}
		default:
			if t.SourceLineStart == t.SourceLineEnd {
				where = fmt.Sprintf("line %d", t.SourceLineStart)
			} else {
				where = fmt.Sprintf("lines %d–%d", t.SourceLineStart, t.SourceLineEnd)
			}
			if t.Location.State == comments.LocChanged {
				where += " (text changed)"
			} else if t.Location.State == comments.LocDeleted {
				where += " (text deleted)"
			}
		}
		status := t.Status
		if t.Awaiting != "" {
			status += " · awaiting " + t.Awaiting
		}
		fmt.Printf("\n%s  %s · %s\n", t.ID, status, where)
		if t.AnchorText != "" {
			fmt.Printf("  quote: %q\n", oneLine(t.AnchorText, 100))
			if t.Location.State == comments.LocChanged {
				fmt.Printf("  now:   %q\n", oneLine(t.Location.Current, 100))
			}
		}
		for _, m := range t.Messages {
			fmt.Printf("  %s: %s\n", m.Author.Name, oneLine(m.Text, 200))
		}
	}
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		s = s[:n] + "…"
	}
	return s
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// threadFor finds the thread (and message) an id or id prefix names in a file.
func threadFor(svc *comments.Service, p, id string) (string, string, error) {
	d, err := svc.Store.Document(p, false)
	if err != nil {
		return "", "", fmt.Errorf("%s has no comments", paths.Display(p))
	}
	tid, mid, err := svc.Store.ResolveID(d.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		return "", "", fmt.Errorf("no comment %q on %s", id, paths.Display(p))
	}
	return tid, mid, err
}

const replyUsage = `Usage: serve reply <file> <id> <text...> [--as agent|human[:name]]

Reply to a thread. <id> is the thread's id, the id of any message in it, or
a unique prefix of either; the reply always goes at the end of the thread.
Use "-" as the text to read it from stdin. Replies are signed as the agent
($SERVE_AUTHOR, default "agent") unless --as says otherwise.`

func cmdReply(args []string) error {
	fs := flags("reply", replyUsage)
	as := fs.String("as", "", "")
	pos, err := parse(fs, replyUsage, args)
	if err != nil {
		return err
	}
	if len(pos) < 3 {
		return usageError{replyUsage}
	}
	p, err := resolveFile(pos[0])
	if err != nil {
		return err
	}
	text, err := readText(pos[2:])
	if err != nil {
		return err
	}
	if text == "" {
		return errors.New("reply text is required")
	}
	svc, done, err := openService()
	if err != nil {
		return err
	}
	defer done()
	author, err := parseAuthor(*as, svc.Config)
	if err != nil {
		return err
	}
	tid, _, err := threadFor(svc, p, pos[1])
	if err != nil {
		return err
	}
	m, err := svc.Reply(tid, author, text)
	if err != nil {
		return err
	}
	return printJSON(cliMessage{ID: m.ID, Author: m.Author, Text: m.Body, CreatedAt: m.CreatedAt})
}

const resolveUsage = `Usage: serve resolve <file> <id>... [--note <text>] [--as agent|human[:name]]

Resolve threads. With --note, the note is added as a reply first, so the
reviewer sees what was done ("Changed to four, per Stripe's guidance").`

func cmdResolve(args []string) error { return setStatus(args, store.StatusResolved, resolveUsage) }

const reopenUsage = `Usage: serve reopen <file> <id>... [--note <text>]

Reopen resolved threads.`

func cmdReopen(args []string) error { return setStatus(args, store.StatusOpen, reopenUsage) }

func setStatus(args []string, status, usage string) error {
	fs := flags("resolve", usage)
	note := fs.String("note", "", "")
	as := fs.String("as", "", "")
	pos, err := parse(fs, usage, args)
	if err != nil {
		return err
	}
	if len(pos) < 2 {
		return usageError{usage}
	}
	p, err := resolveFile(pos[0])
	if err != nil {
		return err
	}
	svc, done, err := openService()
	if err != nil {
		return err
	}
	defer done()
	author, err := parseAuthor(*as, svc.Config)
	if err != nil {
		return err
	}
	failed := false
	for _, id := range pos[1:] {
		tid, _, err := threadFor(svc, p, id)
		if err != nil {
			fmt.Fprintln(os.Stderr, "serve:", err)
			failed = true
			continue
		}
		if strings.TrimSpace(*note) != "" {
			if _, err := svc.Reply(tid, author, *note); err != nil {
				fmt.Fprintln(os.Stderr, "serve:", err)
				failed = true
				continue
			}
		}
		if _, err := svc.Store.SetStatus(tid, status, author); err != nil {
			fmt.Fprintln(os.Stderr, "serve:", err)
			failed = true
			continue
		}
		if status == store.StatusResolved {
			fmt.Println("Resolved:", tid)
		} else {
			fmt.Println("Reopened:", tid)
		}
	}
	if failed {
		return errQuiet
	}
	return nil
}

const editUsage = `Usage: serve edit <file> <message-id> <text...>

Change the text of a message ("-" reads it from stdin). A thread id edits
the thread's first message.`

func cmdEdit(args []string) error {
	fs := flags("edit", editUsage)
	as := fs.String("as", "", "")
	pos, err := parse(fs, editUsage, args)
	if err != nil {
		return err
	}
	if len(pos) < 3 {
		return usageError{editUsage}
	}
	p, err := resolveFile(pos[0])
	if err != nil {
		return err
	}
	text, err := readText(pos[2:])
	if err != nil || text == "" {
		return errors.New("text is required")
	}
	svc, done, err := openService()
	if err != nil {
		return err
	}
	defer done()
	author, err := parseAuthor(*as, svc.Config)
	if err != nil {
		return err
	}
	tid, mid, err := threadFor(svc, p, pos[1])
	if err != nil {
		return err
	}
	if mid == "" {
		t, err := svc.Store.Thread(tid)
		if err != nil {
			return err
		}
		mid = t.Messages[0].ID
	}
	m, err := svc.Store.EditMessage(mid, author, text)
	if err != nil {
		return err
	}
	return printJSON(cliMessage{ID: m.ID, Author: m.Author, Text: m.Body, CreatedAt: m.CreatedAt, EditedAt: m.EditedAt})
}

const deleteUsage = `Usage: serve delete <file> <id>

Delete a thread (given its id) or one message (given a message id).
Deleting a thread's first message deletes the thread.`

func cmdDelete(args []string) error {
	fs := flags("delete", deleteUsage)
	pos, err := parse(fs, deleteUsage, args)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return usageError{deleteUsage}
	}
	p, err := resolveFile(pos[0])
	if err != nil {
		return err
	}
	svc, done, err := openService()
	if err != nil {
		return err
	}
	defer done()
	author := store.Author{Kind: store.Agent, Name: "agent"}
	tid, mid, err := threadFor(svc, p, pos[1])
	if err != nil {
		return err
	}
	if mid == "" {
		if err := svc.Store.DeleteThread(tid, author); err != nil {
			return err
		}
		fmt.Println("Deleted thread", tid)
		return nil
	}
	gone, err := svc.Store.DeleteMessage(mid, author)
	if err != nil {
		return err
	}
	if gone {
		fmt.Println("Deleted thread", tid)
	} else {
		fmt.Println("Deleted message", mid)
	}
	return nil
}

const inboxUsage = `Usage: serve inbox [--for agent|human] [--under DIR] [--json]

List open threads waiting for a reply, across every document: by default
the ones whose last message is from a person (waiting for the agent).
--under limits it to documents inside a folder.`

func cmdInbox(args []string) error {
	fs := flags("inbox", inboxUsage)
	who := fs.String("for", store.Agent, "")
	under := fs.String("under", "", "")
	asJSON := fs.Bool("json", false, "")
	if _, err := parse(fs, inboxUsage, args); err != nil {
		return err
	}
	svc, done, err := openService()
	if err != nil {
		return err
	}
	defer done()
	cursor, _ := svc.Store.LastSeq()
	docs, err := svc.Store.Documents()
	if err != nil {
		return err
	}
	type item struct {
		File    string      `json:"file"`
		Threads []cliThread `json:"threads"`
	}
	var out []item
	root := ""
	if *under != "" {
		root = paths.Abs(*under)
	}
	for _, d := range docs {
		if root != "" && !paths.Within(d.Path, root) {
			continue
		}
		dv, err := svc.Open(d.Path, false)
		if err != nil {
			continue
		}
		var ts []cliThread
		for _, v := range dv.Threads {
			if v.Status == store.StatusOpen && v.Awaiting == *who {
				ts = append(ts, toCLI(v))
			}
		}
		if len(ts) > 0 {
			out = append(out, item{File: d.Path, Threads: ts})
		}
	}
	if *asJSON {
		if out == nil {
			out = []item{}
		}
		return printJSON(map[string]any{"cursor": cursor, "documents": out})
	}
	if len(out) == 0 {
		fmt.Printf("Nothing waiting for the %s.\n", *who)
		return nil
	}
	for _, it := range out {
		printThreadsText(it.File, cursor, it.Threads)
		fmt.Println()
	}
	return nil
}

const gcUsage = `Usage: serve gc [--prune]

List documents with comments whose files no longer exist. With --prune,
delete those comments. A file that was moved or renamed is usually found
again on its own; check the list before pruning.`

func cmdGC(args []string) error {
	fs := flags("gc", gcUsage)
	prune := fs.Bool("prune", false, "")
	if _, err := parse(fs, gcUsage, args); err != nil {
		return err
	}
	svc, done, err := openService()
	if err != nil {
		return err
	}
	defer done()
	docs, err := svc.Store.Documents()
	if err != nil {
		return err
	}
	var gone []store.Document
	for _, d := range docs {
		if _, err := os.Stat(d.Path); errors.Is(err, os.ErrNotExist) {
			gone = append(gone, d)
		}
	}
	sort.Slice(gone, func(i, j int) bool { return gone[i].Path < gone[j].Path })
	if len(gone) == 0 {
		fmt.Println("Every document with comments still exists.")
		return nil
	}
	for _, d := range gone {
		ts, _ := svc.Store.Threads(d.ID)
		fmt.Printf("%-4d %s\n", len(ts), paths.Display(d.Path))
		if *prune {
			if err := svc.Store.DeleteDocument(d.ID); err != nil {
				return err
			}
		}
	}
	if *prune {
		fmt.Printf("Removed comments for %d missing file%s.\n", len(gone), plural(len(gone)))
	} else {
		fmt.Printf("\n%d missing file%s. Run 'serve gc --prune' to remove their comments.\n", len(gone), plural(len(gone)))
	}
	return nil
}
