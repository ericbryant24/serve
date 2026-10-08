package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"serve/internal/comments"
	"serve/internal/paths"
	"serve/internal/store"
)

// Events come from the database's event log, in order, each with a sequence
// number. Every listing (`serve comments`) prints the current one as its
// cursor, and `wait`/`watch --since <cursor>` pick up from exactly there, so a
// comment that lands between reading the list and starting to wait is still
// delivered.

type eventFilter struct {
	docPath string // "" for every document
	newOnly bool
	from    string // author kind, "" for any
}

func (f eventFilter) match(e store.Event, path string) bool {
	if f.docPath != "" && path != f.docPath {
		return false
	}
	if f.newOnly && e.Type != store.EvNewComment && e.Type != store.EvNewReply {
		return false
	}
	if f.from != "" && e.Author.Kind != f.from {
		return false
	}
	return true
}

// eventView turns a stored event into the JSON line `watch` and `wait` print.
type eventView struct {
	svc   *comments.Service
	paths map[string]string
	views map[string]*comments.DocView
}

func newEventView(svc *comments.Service) *eventView {
	return &eventView{svc: svc, paths: map[string]string{}, views: map[string]*comments.DocView{}}
}

func (v *eventView) path(docID string) string {
	if p, ok := v.paths[docID]; ok {
		return p
	}
	p := ""
	if d, err := v.svc.Store.DocumentByID(docID); err == nil {
		p = d.Path
	}
	v.paths[docID] = p
	return p
}

func (v *eventView) thread(path, id string) *comments.ThreadView {
	dv, ok := v.views[path]
	if !ok {
		dv, _ = v.svc.Open(path, false)
		v.views[path] = dv
	}
	if dv == nil {
		return nil
	}
	for i := range dv.Threads {
		if dv.Threads[i].ID == id {
			return &dv.Threads[i]
		}
	}
	return nil
}

func (v *eventView) render(e store.Event) map[string]any {
	path := v.path(e.DocID)
	out := map[string]any{
		"event": e.Type, "seq": e.Seq, "cursor": e.Seq, "file": path, "thread_id": e.ThreadID,
		"timestamp": e.At, "author": e.Author,
	}
	out["comment_id"] = e.ThreadID
	if e.MessageID != "" {
		out["message_id"] = e.MessageID
	}
	if e.Type == store.EvNewReply || e.Type == store.EvEdited {
		out["comment_id"] = e.MessageID
		out["parent_id"] = e.ThreadID
	}
	var payload struct {
		Text  string `json:"text"`
		Scope string `json:"scope"`
	}
	_ = json.Unmarshal(e.Payload, &payload)
	if payload.Text != "" {
		out["text"] = payload.Text
	}
	if t := v.thread(path, e.ThreadID); t != nil {
		c := toCLI(*t)
		out["scope"] = c.Scope
		out["status"] = c.Status
		out["awaiting"] = c.Awaiting
		if c.AnchorText != "" {
			out["anchor_text"] = c.AnchorText
		}
		if c.SourceLineStart > 0 {
			out["source_line_start"], out["source_line_end"] = c.SourceLineStart, c.SourceLineEnd
		}
		out["location"] = c.Location
		if c.Element != nil {
			out["element"] = c.Element
		}
	} else if payload.Scope != "" {
		out["scope"] = payload.Scope
	}
	return out
}

func (v *eventView) reset() {
	v.views = map[string]*comments.DocView{}
}

func parseCursor(s string) (int64, error) {
	if s == "" {
		return -1, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("--since takes a cursor (a number printed by 'serve comments')")
	}
	return n, nil
}

const waitUsage = `Usage: serve wait [file] [--since CURSOR] [--new] [--from human|agent] [--timeout SECONDS]

Block until a comment event matches, print it as one JSON line, and exit.
With --since, an event that already happened after that cursor counts, so
the usual loop has no gap:

  serve comments doc.md            # note "cursor"
  ...address the comments...
  serve wait doc.md --since 1842 --from human

Options:
  --since N        wake for events after cursor N (default: from now)
  --new            only new comments and replies
  --from KIND      only events by a human, or by an agent
  --timeout N      give up after N seconds and exit 124

Exit codes: 0 an event was printed, 124 timed out, 130 interrupted.`

func cmdWait(args []string) error {
	fs := flags("wait", waitUsage)
	since := fs.String("since", "", "")
	newOnly := fs.Bool("new", false, "")
	from := fs.String("from", "", "")
	timeout := fs.Int("timeout", 0, "")
	pos, err := parse(fs, waitUsage, args)
	if err != nil {
		return err
	}
	if len(pos) > 1 {
		return usageError{waitUsage}
	}
	f := eventFilter{newOnly: *newOnly, from: *from}
	if len(pos) == 1 {
		if f.docPath, err = resolveFile(pos[0]); err != nil {
			return err
		}
	}
	cursor, err := parseCursor(*since)
	if err != nil {
		return err
	}
	svc, done, err := openService()
	if err != nil {
		return err
	}
	defer done()
	if cursor < 0 {
		cursor, _ = svc.Store.LastSeq()
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if *timeout > 0 {
		var c2 context.CancelFunc
		ctx, c2 = context.WithTimeout(ctx, time.Duration(*timeout)*time.Second)
		defer c2()
	}
	view := newEventView(svc)
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		evs, err := svc.Store.Events(cursor, "", 500)
		if err != nil {
			return err
		}
		for _, e := range evs {
			cursor = e.Seq
			if f.match(e, view.path(e.DocID)) {
				return printLine(os.Stdout, view.render(e))
			}
		}
		select {
		case <-ctx.Done():
			if ctx.Err() == context.DeadlineExceeded {
				return exitCode(124)
			}
			return exitCode(130)
		case <-tick.C:
		}
	}
}

func printLine(w *os.File, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

const watchUsage = `Usage: serve watch [file] [--since CURSOR] [--new] [--from human|agent]

Stream comment events as JSON lines: new_comment, new_reply, edited,
resolved, unresolved, deleted. Without --new or --since it first prints an
"initial" line for every open thread. It exits when whatever reads its
output goes away.

For an agent that is re-invoked only when a background command exits,
'serve wait' is the better fit.`

func cmdWatch(args []string) error {
	fs := flags("watch", watchUsage)
	since := fs.String("since", "", "")
	newOnly := fs.Bool("new", false, "")
	from := fs.String("from", "", "")
	pos, err := parse(fs, watchUsage, args)
	if err != nil {
		return err
	}
	if len(pos) > 1 {
		return usageError{watchUsage}
	}
	f := eventFilter{newOnly: *newOnly, from: *from}
	if len(pos) == 1 {
		if f.docPath, err = resolveFile(pos[0]); err != nil {
			return err
		}
	}
	cursor, err := parseCursor(*since)
	if err != nil {
		return err
	}
	svc, done, err := openService()
	if err != nil {
		return err
	}
	defer done()
	out := bufio.NewWriter(os.Stdout)
	emit := func(v any) error {
		b, _ := json.Marshal(v)
		out.Write(b)
		out.WriteByte('\n')
		return out.Flush()
	}
	start, _ := svc.Store.LastSeq()
	if cursor < 0 {
		cursor = start
		if !*newOnly {
			if err := emitInitial(svc, f, emit); err != nil {
				return nil
			}
		}
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	view := newEventView(svc)
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		evs, err := svc.Store.Events(cursor, "", 500)
		if err != nil {
			return err
		}
		view.reset()
		for _, e := range evs {
			cursor = e.Seq
			if f.match(e, view.path(e.DocID)) {
				if emit(view.render(e)) != nil {
					return nil
				}
			}
		}
		if stdoutGone() {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

func emitInitial(svc *comments.Service, f eventFilter, emit func(any) error) error {
	var docs []string
	if f.docPath != "" {
		docs = []string{f.docPath}
	} else {
		ds, _ := svc.Store.Documents()
		for _, d := range ds {
			docs = append(docs, d.Path)
		}
	}
	now := store.Now()
	for _, p := range docs {
		dv, err := svc.Open(p, false)
		if err != nil {
			continue
		}
		for _, t := range dv.Threads {
			if t.Status != store.StatusOpen {
				continue
			}
			if f.from != "" && len(t.Messages) > 0 && t.Messages[len(t.Messages)-1].Author.Kind != f.from {
				continue
			}
			c := toCLI(t)
			ev := map[string]any{"event": "initial", "file": p, "thread_id": t.ID, "comment_id": t.ID, "timestamp": now,
				"text": c.Text, "anchor_text": c.AnchorText, "scope": c.Scope, "status": c.Status, "awaiting": c.Awaiting,
				"location": c.Location, "messages": c.Messages}
			if c.SourceLineStart > 0 {
				ev["source_line_start"], ev["source_line_end"] = c.SourceLineStart, c.SourceLineEnd
			}
			if err := emit(ev); err != nil {
				return err
			}
		}
	}
	_ = paths.Display
	return nil
}
