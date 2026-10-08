// Package logx is serve's log: an in-memory ring of recent events, echoed to
// stderr, with redaction applied as each event is written.
package logx

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Structured log ring
//
// Before this existed serve had no logger at all — only scattered
// fmt.Fprintf(os.Stderr, ...) calls, none of them retained. That absence is
// what makes this design possible: rather than scrubbing a firehose after the
// fact, redaction happens at WRITE time, via the field constructors below. An
// unredacted path never enters the ring, so no later bug can leak one out of
// it.
//
// The ring is in memory only. It is never written to disk unless a report
// explicitly captures it, and the reporter sees the full text before it goes
// anywhere.
// ---------------------------------------------------------------------------

const logRingSize = 500

// Field is one key/value pair on a log event. Construct fields only through
// Safe, Path, Int or Err — never as a literal — so redaction is not
// something a call site can forget.
type Field struct {
	Key string `json:"key"`
	Val string `json:"val"`
}

// Safe records a value known not to carry user content: an enum, a count, a
// literal. Never pass err.Error() here — use Err, which redacts paths.
func Safe(k, v string) Field { return Field{Key: k, Val: v} }

func Int(k string, v int) Field { return Field{Key: k, Val: fmt.Sprint(v)} }

// Path records a filesystem path, always shape-redacted.
func Path(k, p string) Field { return Field{Key: k, Val: RedactPath(p, PathShape)} }

// Err records an error message with every path-shaped run redacted. Go's
// *PathError puts an absolute path straight into Error(), which is exactly the
// leak this exists to close.
func Err(err error) Field {
	if err == nil {
		return Field{Key: "err", Val: ""}
	}
	return Field{Key: "err", Val: RedactTextPaths(err.Error())}
}

type Event struct {
	Time   string  `json:"time"`
	Level  string  `json:"level"`
	Msg    string  `json:"msg"`
	Fields []Field `json:"fields,omitempty"`
}

type logRing struct {
	mu   sync.Mutex
	buf  []Event
	next int
	full bool
}

var ring = &logRing{buf: make([]Event, logRingSize)}

func (l *logRing) add(e Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf[l.next] = e
	l.next = (l.next + 1) % len(l.buf)
	if l.next == 0 {
		l.full = true
	}
}

// snapshot returns the retained events in chronological order.
func (l *logRing) snapshot() []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.full {
		out := make([]Event, l.next)
		copy(out, l.buf[:l.next])
		return out
	}
	out := make([]Event, 0, len(l.buf))
	out = append(out, l.buf[l.next:]...)
	out = append(out, l.buf[:l.next]...)
	return out
}

func (l *logRing) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.next, l.full = 0, false
}

// Logf records an event and echoes it to stderr, so console behaviour matches
// what serve printed before the ring existed.
//
// msg must be a literal. Interpolating user content into it would bypass the
// field constructors, which are the only place redaction happens.
func Logf(level, msg string, fields ...Field) {
	e := Event{
		Time:   time.Now().UTC().Format(time.RFC3339),
		Level:  level,
		Msg:    msg,
		Fields: fields,
	}
	ring.add(e)

	if level == "debug" {
		return
	}
	var b strings.Builder
	b.WriteString(msg)
	for _, f := range fields {
		if f.Val == "" {
			continue
		}
		b.WriteString(" ")
		b.WriteString(f.Key)
		b.WriteString("=")
		b.WriteString(f.Val)
	}
	fmt.Fprintln(os.Stderr, b.String())
}

func Error(msg string, err error, fields ...Field) {
	Logf("error", msg, append([]Field{Err(err)}, fields...)...)
}

// Snapshot materializes the ring as attachable text.
func Snapshot() string {
	events := ring.snapshot()
	if len(events) == 0 {
		return "(no log events recorded)\n"
	}
	var b strings.Builder
	for _, e := range events {
		fmt.Fprintf(&b, "%s  %-5s  %s", e.Time, e.Level, e.Msg)
		for _, f := range e.Fields {
			if f.Val == "" {
				continue
			}
			fmt.Fprintf(&b, "  %s=%s", f.Key, f.Val)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// Reset empties the ring (tests).
func Reset() { ring.reset() }

func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}
