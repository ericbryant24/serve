package main

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
// fSafe, fPath, fInt or fErr — never as a literal — so redaction is not
// something a call site can forget.
type Field struct {
	Key string `json:"key"`
	Val string `json:"val"`
}

// fSafe records a value known not to carry user content: an enum, a count, a
// literal. Never pass err.Error() here — use fErr, which redacts paths.
func fSafe(k, v string) Field { return Field{Key: k, Val: v} }

func fInt(k string, v int) Field { return Field{Key: k, Val: fmt.Sprint(v)} }

// fPath records a filesystem path, always shape-redacted.
func fPath(k, p string) Field { return Field{Key: k, Val: redactPath(p, PathShape)} }

// fErr records an error message with every path-shaped run redacted. Go's
// *PathError puts an absolute path straight into Error(), which is exactly the
// leak this exists to close.
func fErr(err error) Field {
	if err == nil {
		return Field{Key: "err", Val: ""}
	}
	return Field{Key: "err", Val: redactTextPaths(err.Error())}
}

type LogEvent struct {
	Time   string  `json:"time"`
	Level  string  `json:"level"`
	Msg    string  `json:"msg"`
	Fields []Field `json:"fields,omitempty"`
}

type logRing struct {
	mu   sync.Mutex
	buf  []LogEvent
	next int
	full bool
}

var theLog = &logRing{buf: make([]LogEvent, logRingSize)}

func (l *logRing) add(e LogEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf[l.next] = e
	l.next = (l.next + 1) % len(l.buf)
	if l.next == 0 {
		l.full = true
	}
}

// snapshot returns the retained events in chronological order.
func (l *logRing) snapshot() []LogEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.full {
		out := make([]LogEvent, l.next)
		copy(out, l.buf[:l.next])
		return out
	}
	out := make([]LogEvent, 0, len(l.buf))
	out = append(out, l.buf[l.next:]...)
	out = append(out, l.buf[:l.next]...)
	return out
}

func (l *logRing) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.next, l.full = 0, false
}

// logf records an event and echoes it to stderr, so console behaviour matches
// what serve printed before the ring existed.
//
// msg must be a literal. Interpolating user content into it would bypass the
// field constructors, which are the only place redaction happens.
func logf(level, msg string, fields ...Field) {
	e := LogEvent{
		Time:   time.Now().UTC().Format(time.RFC3339),
		Level:  level,
		Msg:    msg,
		Fields: fields,
	}
	theLog.add(e)

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

func logErr(msg string, err error, fields ...Field) {
	logf("error", msg, append([]Field{fErr(err)}, fields...)...)
}

// snapshotLog materializes the ring as attachable text.
func snapshotLog() string {
	events := theLog.snapshot()
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
