// Package store keeps serve's comments in one SQLite database at
// ~/.serve/serve.db.
//
// Every process that touches comments (the background server, `serve reply`
// from an agent, `serve resolve` from a terminal) opens the same database.
// Writes are transactions, so two of them at once cannot drop each other's
// change, and every change appends a row to events with an increasing
// sequence number. That number is the cursor `serve wait --since` and
// `serve watch --since` resume from, and what the server polls to learn about
// changes other processes made.
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"serve/internal/anchor"
	"serve/internal/paths"
)

// ErrNotFound is returned for an id that matches nothing.
var ErrNotFound = errors.New("not found")

// ErrAmbiguous is returned for an id prefix that matches more than one thing.
var ErrAmbiguous = errors.New("id prefix matches more than one comment")

// Thread scopes.
const (
	ScopeText    = "text"
	ScopeElement = "element"
	ScopePage    = "page"
)

// Thread statuses.
const (
	StatusOpen     = "open"
	StatusResolved = "resolved"
)

// Author kinds.
const (
	Human = "human"
	Agent = "agent"
)

// Event types. The names are the ones `serve watch` has always emitted.
const (
	EvNewComment = "new_comment"
	EvNewReply   = "new_reply"
	EvEdited     = "edited"
	EvResolved   = "resolved"
	EvUnresolved = "unresolved"
	EvDeleted    = "deleted"
)

// Author is who wrote a message.
type Author struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// Document is a file serve has comments for.
type Document struct {
	ID          string `json:"id"`
	Path        string `json:"path"`
	Dev         uint64 `json:"-"`
	Ino         uint64 `json:"-"`
	ContentHash string `json:"-"`
	LastSeen    string `json:"last_seen"`
}

// Message is one entry in a thread.
type Message struct {
	ID        string `json:"id"`
	ThreadID  string `json:"thread_id"`
	Author    Author `json:"author"`
	Body      string `json:"text"`
	CreatedAt string `json:"created_at"`
	EditedAt  string `json:"edited_at,omitempty"`
	Rev       string `json:"-"`
}

// Thread is a comment and its replies.
type Thread struct {
	ID         string        `json:"id"`
	DocID      string        `json:"doc_id"`
	Scope      string        `json:"scope"`
	Anchor     anchor.Anchor `json:"anchor"`
	Status     string        `json:"status"`
	CreatedAt  string        `json:"created_at"`
	ResolvedAt string        `json:"resolved_at,omitempty"`
	ResolvedBy *Author       `json:"resolved_by,omitempty"`
	UpdatedAt  string        `json:"updated_at"`
	Messages   []Message     `json:"messages"`
}

// Awaiting says whose turn it is on an open thread: the side that did not
// write the last message. Resolved threads await nobody.
func (t *Thread) Awaiting() string {
	if t.Status != StatusOpen || len(t.Messages) == 0 {
		return ""
	}
	if t.Messages[len(t.Messages)-1].Author.Kind == Agent {
		return Human
	}
	return Agent
}

// Event is one change, in order.
type Event struct {
	Seq       int64           `json:"seq"`
	DocID     string          `json:"doc_id"`
	ThreadID  string          `json:"thread_id"`
	MessageID string          `json:"message_id,omitempty"`
	Type      string          `json:"type"`
	Author    Author          `json:"author"`
	At        string          `json:"at"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// Folder is a folder the user has opened in serve.
type Folder struct {
	Path     string `json:"path"`
	OpenedAt string `json:"opened_at"`
	LastUsed string `json:"last_used"`
}

// Store is an open database.
type Store struct {
	db   *sql.DB
	Path string
}

// DefaultPath is ~/.serve/serve.db.
func DefaultPath() string { return filepath.Join(paths.StateDir(), "serve.db") }

// Open opens (creating if needed) the database at path.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	s := &Store{db: db, Path: path}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

const schema = `
CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS documents (
	id TEXT PRIMARY KEY,
	path TEXT NOT NULL,
	dev INTEGER NOT NULL DEFAULT 0,
	ino INTEGER NOT NULL DEFAULT 0,
	content_hash TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	last_seen TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS documents_path ON documents(path);
CREATE INDEX IF NOT EXISTS documents_inode ON documents(dev, ino);
CREATE TABLE IF NOT EXISTS revisions (
	doc_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
	hash TEXT NOT NULL,
	text BLOB NOT NULL,
	created_at TEXT NOT NULL,
	PRIMARY KEY (doc_id, hash)
);
CREATE TABLE IF NOT EXISTS threads (
	id TEXT PRIMARY KEY,
	doc_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
	scope TEXT NOT NULL,
	anchor TEXT NOT NULL DEFAULT '{}',
	status TEXT NOT NULL DEFAULT 'open',
	created_at TEXT NOT NULL,
	resolved_at TEXT NOT NULL DEFAULT '',
	resolved_by TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS threads_doc ON threads(doc_id);
CREATE TABLE IF NOT EXISTS messages (
	id TEXT PRIMARY KEY,
	thread_id TEXT NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
	author_kind TEXT NOT NULL DEFAULT '',
	author_name TEXT NOT NULL DEFAULT '',
	body TEXT NOT NULL,
	created_at TEXT NOT NULL,
	edited_at TEXT NOT NULL DEFAULT '',
	rev TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS messages_thread ON messages(thread_id, created_at);
CREATE TABLE IF NOT EXISTS events (
	seq INTEGER PRIMARY KEY AUTOINCREMENT,
	doc_id TEXT NOT NULL,
	thread_id TEXT NOT NULL DEFAULT '',
	message_id TEXT NOT NULL DEFAULT '',
	type TEXT NOT NULL,
	author_kind TEXT NOT NULL DEFAULT '',
	author_name TEXT NOT NULL DEFAULT '',
	at TEXT NOT NULL,
	payload TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS events_doc ON events(doc_id, seq);
CREATE TABLE IF NOT EXISTS folders (
	path TEXT PRIMARY KEY,
	opened_at TEXT NOT NULL,
	last_used TEXT NOT NULL
);
`

func (s *Store) migrate() error {
	_, err := s.db.Exec(schema)
	if err != nil {
		return fmt.Errorf("creating the comment database: %w", err)
	}
	_, err = s.db.Exec(`INSERT OR IGNORE INTO meta(key, value) VALUES ('schema', '1')`)
	return err
}

// Meta reads a value from the meta table.
func (s *Store) Meta(key string) (string, bool) {
	var v string
	if err := s.db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&v); err != nil {
		return "", false
	}
	return v, true
}

// SetMeta writes a value to the meta table.
func (s *Store) SetMeta(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO meta(key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// --- ids and time -----------------------------------------------------------

const idAlphabet = "0123456789abcdefghjkmnpqrstvwxyz"

// NewID returns an 8-character id.
func NewID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = idAlphabet[int(b[i])%len(idAlphabet)]
	}
	return string(b)
}

// Now is the timestamp format stored everywhere: UTC with milliseconds, so
// messages written in the same second still sort in order.
func Now() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000Z") }

// --- transactions ----------------------------------------------------------

func (s *Store) tx(f func(*sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if err := f(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func addEvent(tx *sql.Tx, docID, threadID, messageID, typ string, a Author, payload any) error {
	p := []byte("{}")
	if payload != nil {
		var err error
		if p, err = json.Marshal(payload); err != nil {
			return err
		}
	}
	_, err := tx.Exec(`INSERT INTO events(doc_id, thread_id, message_id, type, author_kind, author_name, at, payload) VALUES (?,?,?,?,?,?,?,?)`,
		docID, threadID, messageID, typ, a.Kind, a.Name, Now(), string(p))
	return err
}

// --- documents --------------------------------------------------------------

func scanDoc(row interface{ Scan(...any) error }) (*Document, error) {
	var d Document
	var dev, ino int64
	if err := row.Scan(&d.ID, &d.Path, &dev, &ino, &d.ContentHash, &d.LastSeen); err != nil {
		return nil, err
	}
	d.Dev, d.Ino = uint64(dev), uint64(ino)
	return &d, nil
}

const docCols = `id, path, dev, ino, content_hash, last_seen`

// Document finds the document for a file, following it through moves (same
// inode, new path) and atomic saves (same path, new inode). With create, a
// file seen for the first time gets a new document; without, it returns
// ErrNotFound.
func (s *Store) Document(path string, create bool) (*Document, error) {
	abs := paths.Abs(path)
	dev, ino, exists := paths.FileID(abs)
	var out *Document
	err := s.tx(func(tx *sql.Tx) error {
		if d, err := scanDoc(tx.QueryRow(`SELECT `+docCols+` FROM documents WHERE path = ? ORDER BY last_seen DESC LIMIT 1`, abs)); err == nil {
			if exists && (d.Dev != dev || d.Ino != ino) {
				if _, err := tx.Exec(`UPDATE documents SET dev = ?, ino = ? WHERE id = ?`, int64(dev), int64(ino), d.ID); err != nil {
					return err
				}
				d.Dev, d.Ino = dev, ino
			}
			out = d
			return nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if exists && ino != 0 {
			rows, err := tx.Query(`SELECT `+docCols+` FROM documents WHERE dev = ? AND ino = ?`, int64(dev), int64(ino))
			if err != nil {
				return err
			}
			var cands []*Document
			for rows.Next() {
				d, err := scanDoc(rows)
				if err != nil {
					rows.Close()
					return err
				}
				cands = append(cands, d)
			}
			rows.Close()
			for _, d := range cands {
				// Moved: the inode is the same and the old path is gone.
				if _, err := os.Stat(d.Path); errors.Is(err, os.ErrNotExist) {
					if _, err := tx.Exec(`UPDATE documents SET path = ? WHERE id = ?`, abs, d.ID); err != nil {
						return err
					}
					d.Path = abs
					out = d
					return nil
				}
			}
		}
		if !create {
			return ErrNotFound
		}
		now := Now()
		d := &Document{ID: NewID(), Path: abs, Dev: dev, Ino: ino, LastSeen: now}
		_, err := tx.Exec(`INSERT INTO documents(id, path, dev, ino, content_hash, created_at, last_seen) VALUES (?,?,?,?,?,?,?)`,
			d.ID, d.Path, int64(dev), int64(ino), "", now, now)
		out = d
		return err
	})
	return out, err
}

// DocumentByID returns a document by id.
func (s *Store) DocumentByID(id string) (*Document, error) {
	d, err := scanDoc(s.db.QueryRow(`SELECT `+docCols+` FROM documents WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

// Documents lists every document that has at least one thread.
func (s *Store) Documents() ([]Document, error) {
	rows, err := s.db.Query(`SELECT ` + docCols + ` FROM documents WHERE id IN (SELECT DISTINCT doc_id FROM threads) ORDER BY path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Document
	for rows.Next() {
		d, err := scanDoc(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// SeeDocument records the current hash of a document's text.
func (s *Store) SeeDocument(id, hash string) error {
	_, err := s.db.Exec(`UPDATE documents SET content_hash = ?, last_seen = ? WHERE id = ?`, hash, Now(), id)
	return err
}

// Relink moves every thread from one document to another, for a file the
// user says is the same document under a new name.
func (s *Store) Relink(fromID, toID string, a Author) error {
	return s.tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE threads SET doc_id = ? WHERE doc_id = ?`, toID, fromID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE OR IGNORE revisions SET doc_id = ? WHERE doc_id = ?`, toID, fromID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE events SET doc_id = ? WHERE doc_id = ?`, toID, fromID); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM documents WHERE id = ?`, fromID)
		return err
	})
}

// DeleteDocument removes a document and everything attached to it.
func (s *Store) DeleteDocument(id string) error {
	return s.tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM events WHERE doc_id = ?`, id); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM documents WHERE id = ?`, id)
		return err
	})
}

// --- threads and messages ----------------------------------------------------

const threadCols = `id, doc_id, scope, anchor, status, created_at, resolved_at, resolved_by, updated_at`

func scanThread(row interface{ Scan(...any) error }) (*Thread, error) {
	var t Thread
	var a, rb string
	if err := row.Scan(&t.ID, &t.DocID, &t.Scope, &a, &t.Status, &t.CreatedAt, &t.ResolvedAt, &rb, &t.UpdatedAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(a), &t.Anchor); err != nil {
		return nil, fmt.Errorf("thread %s: bad anchor: %w", t.ID, err)
	}
	if rb != "" {
		var au Author
		if json.Unmarshal([]byte(rb), &au) == nil {
			t.ResolvedBy = &au
		}
	}
	return &t, nil
}

func (s *Store) loadMessages(threads []*Thread) error {
	if len(threads) == 0 {
		return nil
	}
	byID := map[string]*Thread{}
	ids := make([]any, len(threads))
	ph := make([]string, len(threads))
	for i, t := range threads {
		byID[t.ID] = t
		t.Messages = nil
		ids[i] = t.ID
		ph[i] = "?"
	}
	rows, err := s.db.Query(`SELECT id, thread_id, author_kind, author_name, body, created_at, edited_at, rev FROM messages WHERE thread_id IN (`+strings.Join(ph, ",")+`) ORDER BY created_at, rowid`, ids...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.ThreadID, &m.Author.Kind, &m.Author.Name, &m.Body, &m.CreatedAt, &m.EditedAt, &m.Rev); err != nil {
			return err
		}
		if t := byID[m.ThreadID]; t != nil {
			t.Messages = append(t.Messages, m)
		}
	}
	return rows.Err()
}

// Threads returns a document's threads, oldest first, with their messages.
func (s *Store) Threads(docID string) ([]*Thread, error) {
	rows, err := s.db.Query(`SELECT `+threadCols+` FROM threads WHERE doc_id = ? ORDER BY created_at, rowid`, docID)
	if err != nil {
		return nil, err
	}
	var out []*Thread
	for rows.Next() {
		t, err := scanThread(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, s.loadMessages(out)
}

// Thread returns one thread with its messages.
func (s *Store) Thread(id string) (*Thread, error) {
	t, err := scanThread(s.db.QueryRow(`SELECT `+threadCols+` FROM threads WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return t, s.loadMessages([]*Thread{t})
}

// OpenThreads returns every open thread across all documents, newest
// activity first.
func (s *Store) OpenThreads() ([]*Thread, error) {
	rows, err := s.db.Query(`SELECT ` + threadCols + ` FROM threads WHERE status = 'open' ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	var out []*Thread
	for rows.Next() {
		t, err := scanThread(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, t)
	}
	rows.Close()
	return out, s.loadMessages(out)
}

// OpenCounts returns the number of open threads per document path.
func (s *Store) OpenCounts() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT d.path, COUNT(*) FROM threads t JOIN documents d ON d.id = t.doc_id WHERE t.status = 'open' GROUP BY d.path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var p string
		var n int
		if err := rows.Scan(&p, &n); err != nil {
			return nil, err
		}
		out[p] += n
	}
	return out, rows.Err()
}

// NewThread is what CreateThread needs.
type NewThread struct {
	DocID  string
	Scope  string
	Anchor anchor.Anchor
	Author Author
	Body   string
	// Text is the document text the anchor's Rev names; it is kept so the
	// anchor can later be carried through edits.
	Text string
	// CreatedAt overrides the timestamp (for imports).
	CreatedAt string
}

// CreateThread starts a thread with its first message.
func (s *Store) CreateThread(n NewThread) (*Thread, error) {
	if strings.TrimSpace(n.Body) == "" {
		return nil, errors.New("comment text is required")
	}
	switch n.Scope {
	case ScopeText, ScopeElement, ScopePage:
	default:
		return nil, fmt.Errorf("unknown scope %q", n.Scope)
	}
	now := n.CreatedAt
	if now == "" {
		now = Now()
	}
	t := &Thread{ID: NewID(), DocID: n.DocID, Scope: n.Scope, Anchor: n.Anchor, Status: StatusOpen, CreatedAt: now, UpdatedAt: now}
	m := Message{ID: NewID(), ThreadID: t.ID, Author: n.Author, Body: n.Body, CreatedAt: now, Rev: n.Anchor.Rev}
	a, err := json.Marshal(n.Anchor)
	if err != nil {
		return nil, err
	}
	err = s.tx(func(tx *sql.Tx) error {
		if n.Anchor.Rev != "" && n.Text != "" {
			if err := putRevision(tx, n.DocID, n.Anchor.Rev, n.Text); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`INSERT INTO threads(id, doc_id, scope, anchor, status, created_at, updated_at) VALUES (?,?,?,?,?,?,?)`,
			t.ID, t.DocID, t.Scope, string(a), t.Status, t.CreatedAt, t.UpdatedAt); err != nil {
			return err
		}
		if err := insertMessage(tx, m); err != nil {
			return err
		}
		return addEvent(tx, t.DocID, t.ID, m.ID, EvNewComment, n.Author, map[string]any{"text": n.Body, "scope": n.Scope})
	})
	if err != nil {
		return nil, err
	}
	t.Messages = []Message{m}
	return t, nil
}

func insertMessage(tx *sql.Tx, m Message) error {
	_, err := tx.Exec(`INSERT INTO messages(id, thread_id, author_kind, author_name, body, created_at, rev) VALUES (?,?,?,?,?,?,?)`,
		m.ID, m.ThreadID, m.Author.Kind, m.Author.Name, m.Body, m.CreatedAt, m.Rev)
	return err
}

// Reply appends a message to a thread.
func (s *Store) Reply(threadID string, a Author, body, rev string) (*Message, error) {
	if strings.TrimSpace(body) == "" {
		return nil, errors.New("reply text is required")
	}
	var m Message
	err := s.tx(func(tx *sql.Tx) error {
		var docID string
		if err := tx.QueryRow(`SELECT doc_id FROM threads WHERE id = ?`, threadID).Scan(&docID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		m = Message{ID: NewID(), ThreadID: threadID, Author: a, Body: body, CreatedAt: Now(), Rev: rev}
		if err := insertMessage(tx, m); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE threads SET updated_at = ? WHERE id = ?`, m.CreatedAt, threadID); err != nil {
			return err
		}
		return addEvent(tx, docID, threadID, m.ID, EvNewReply, a, map[string]any{"text": body})
	})
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// EditMessage changes a message's text.
func (s *Store) EditMessage(id string, a Author, body string) (*Message, error) {
	if strings.TrimSpace(body) == "" {
		return nil, errors.New("text cannot be empty")
	}
	var m Message
	err := s.tx(func(tx *sql.Tx) error {
		err := tx.QueryRow(`SELECT id, thread_id, author_kind, author_name, created_at FROM messages WHERE id = ?`, id).
			Scan(&m.ID, &m.ThreadID, &m.Author.Kind, &m.Author.Name, &m.CreatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		m.Body, m.EditedAt = body, Now()
		if _, err := tx.Exec(`UPDATE messages SET body = ?, edited_at = ? WHERE id = ?`, body, m.EditedAt, id); err != nil {
			return err
		}
		var docID string
		if err := tx.QueryRow(`SELECT doc_id FROM threads WHERE id = ?`, m.ThreadID).Scan(&docID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE threads SET updated_at = ? WHERE id = ?`, m.EditedAt, m.ThreadID); err != nil {
			return err
		}
		return addEvent(tx, docID, m.ThreadID, id, EvEdited, a, map[string]any{"text": body})
	})
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// DeleteMessage removes one message. Deleting a thread's first message
// deletes the whole thread; the bool reports that.
func (s *Store) DeleteMessage(id string, a Author) (bool, error) {
	var threadGone bool
	err := s.tx(func(tx *sql.Tx) error {
		var threadID, docID string
		err := tx.QueryRow(`SELECT m.thread_id, t.doc_id FROM messages m JOIN threads t ON t.id = m.thread_id WHERE m.id = ?`, id).Scan(&threadID, &docID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		var first string
		if err := tx.QueryRow(`SELECT id FROM messages WHERE thread_id = ? ORDER BY created_at, rowid LIMIT 1`, threadID).Scan(&first); err != nil {
			return err
		}
		if first == id {
			threadGone = true
			return deleteThread(tx, threadID, docID, a)
		}
		if _, err := tx.Exec(`DELETE FROM messages WHERE id = ?`, id); err != nil {
			return err
		}
		return addEvent(tx, docID, threadID, id, EvDeleted, a, nil)
	})
	return threadGone, err
}

// DeleteThread removes a thread and its messages.
func (s *Store) DeleteThread(id string, a Author) error {
	return s.tx(func(tx *sql.Tx) error {
		var docID string
		err := tx.QueryRow(`SELECT doc_id FROM threads WHERE id = ?`, id).Scan(&docID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		return deleteThread(tx, id, docID, a)
	})
}

func deleteThread(tx *sql.Tx, id, docID string, a Author) error {
	if _, err := tx.Exec(`DELETE FROM threads WHERE id = ?`, id); err != nil {
		return err
	}
	return addEvent(tx, docID, id, "", EvDeleted, a, nil)
}

// SetStatus resolves or reopens a thread. Setting the status it already has
// changes nothing and records no event.
func (s *Store) SetStatus(id, status string, a Author) (*Thread, error) {
	if status != StatusOpen && status != StatusResolved {
		return nil, fmt.Errorf("unknown status %q", status)
	}
	err := s.tx(func(tx *sql.Tx) error {
		var docID, cur string
		err := tx.QueryRow(`SELECT doc_id, status FROM threads WHERE id = ?`, id).Scan(&docID, &cur)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if cur == status {
			return nil
		}
		now := Now()
		by := ""
		at := ""
		typ := EvUnresolved
		if status == StatusResolved {
			b, _ := json.Marshal(a)
			by, at, typ = string(b), now, EvResolved
		}
		if _, err := tx.Exec(`UPDATE threads SET status = ?, resolved_at = ?, resolved_by = ?, updated_at = ? WHERE id = ?`, status, at, by, now, id); err != nil {
			return err
		}
		return addEvent(tx, docID, id, "", typ, a, nil)
	})
	if err != nil {
		return nil, err
	}
	return s.Thread(id)
}

// ResolveID turns an id or unique id prefix into a thread id, and a message
// id when the prefix named a message. docID, when set, limits the search to
// one document.
func (s *Store) ResolveID(docID, prefix string) (threadID, messageID string, err error) {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if prefix == "" {
		return "", "", ErrNotFound
	}
	like := prefix + "%"
	type hit struct{ t, m string }
	var hits []hit
	q := `SELECT id FROM threads WHERE (id = ? OR id LIKE ?)`
	args := []any{prefix, like}
	if docID != "" {
		q += ` AND doc_id = ?`
		args = append(args, docID)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return "", "", err
	}
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		hits = append(hits, hit{t: id})
	}
	rows.Close()
	q = `SELECT m.id, m.thread_id FROM messages m JOIN threads t ON t.id = m.thread_id WHERE (m.id = ? OR m.id LIKE ?)`
	if docID != "" {
		q += ` AND t.doc_id = ?`
	}
	rows, err = s.db.Query(q, args...)
	if err != nil {
		return "", "", err
	}
	for rows.Next() {
		var mid, tid string
		_ = rows.Scan(&mid, &tid)
		hits = append(hits, hit{t: tid, m: mid})
	}
	rows.Close()
	// An exact match wins over prefix matches.
	for _, h := range hits {
		if (h.t == prefix && h.m == "") || h.m == prefix {
			return h.t, h.m, nil
		}
	}
	switch len(hits) {
	case 0:
		return "", "", ErrNotFound
	case 1:
		return hits[0].t, hits[0].m, nil
	}
	// Several hits inside one thread (its id and a message's) name that thread.
	for _, h := range hits[1:] {
		if h.t != hits[0].t {
			return "", "", ErrAmbiguous
		}
	}
	return hits[0].t, "", nil
}

// --- anchors and revisions -----------------------------------------------------

func putRevision(tx *sql.Tx, docID, hash, text string) error {
	_, err := tx.Exec(`INSERT OR IGNORE INTO revisions(doc_id, hash, text, created_at) VALUES (?,?,?,?)`, docID, hash, []byte(text), Now())
	return err
}

// Revision returns a stored revision of a document's text.
func (s *Store) Revision(docID, hash string) (string, bool) {
	var b []byte
	if err := s.db.QueryRow(`SELECT text FROM revisions WHERE doc_id = ? AND hash = ?`, docID, hash).Scan(&b); err != nil {
		return "", false
	}
	return string(b), true
}

// AnchorUpdate is a remapped anchor for one thread, applied only if the
// thread's anchor is still at FromRev (another process may have got there
// first).
type AnchorUpdate struct {
	ThreadID string
	FromRev  string
	Anchor   anchor.Anchor
}

// UpdateAnchors stores remapped anchors and the revision they now refer to,
// then drops revisions nothing refers to any more.
func (s *Store) UpdateAnchors(docID, rev, text string, ups []AnchorUpdate, keepText bool) error {
	return s.tx(func(tx *sql.Tx) error {
		if keepText && rev != "" {
			if err := putRevision(tx, docID, rev, text); err != nil {
				return err
			}
		}
		for _, u := range ups {
			var cur string
			if err := tx.QueryRow(`SELECT anchor FROM threads WHERE id = ?`, u.ThreadID).Scan(&cur); err != nil {
				continue
			}
			var a anchor.Anchor
			if json.Unmarshal([]byte(cur), &a) != nil || a.Rev != u.FromRev {
				continue
			}
			b, err := json.Marshal(u.Anchor)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE threads SET anchor = ? WHERE id = ?`, string(b), u.ThreadID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`UPDATE documents SET content_hash = ?, last_seen = ? WHERE id = ?`, rev, Now(), docID); err != nil {
			return err
		}
		return gcRevisions(tx, docID)
	})
}

// gcRevisions keeps the revisions open anchors refer to and the one the
// latest human message was written against (for "changes since my last
// comment"), and drops the rest.
func gcRevisions(tx *sql.Tx, docID string) error {
	keep := map[string]bool{}
	rows, err := tx.Query(`SELECT anchor FROM threads WHERE doc_id = ?`, docID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		var a anchor.Anchor
		if json.Unmarshal([]byte(s), &a) == nil && a.Rev != "" {
			keep[a.Rev] = true
		}
	}
	rows.Close()
	var rev string
	if err := tx.QueryRow(`SELECT m.rev FROM messages m JOIN threads t ON t.id = m.thread_id WHERE t.doc_id = ? AND m.author_kind = 'human' AND m.rev != '' ORDER BY m.created_at DESC LIMIT 1`, docID).Scan(&rev); err == nil {
		keep[rev] = true
	}
	rows, err = tx.Query(`SELECT hash FROM revisions WHERE doc_id = ?`, docID)
	if err != nil {
		return err
	}
	var drop []string
	for rows.Next() {
		var h string
		_ = rows.Scan(&h)
		if !keep[h] {
			drop = append(drop, h)
		}
	}
	rows.Close()
	for _, h := range drop {
		if _, err := tx.Exec(`DELETE FROM revisions WHERE doc_id = ? AND hash = ?`, docID, h); err != nil {
			return err
		}
	}
	return nil
}

// PutRevision stores a revision outside of an anchor update.
func (s *Store) PutRevision(docID, hash, text string) error {
	return s.tx(func(tx *sql.Tx) error { return putRevision(tx, docID, hash, text) })
}

// LastHumanRev is the revision the latest human message on a document was
// written against.
func (s *Store) LastHumanRev(docID string) (string, string, bool) {
	var rev, at string
	err := s.db.QueryRow(`SELECT m.rev, m.created_at FROM messages m JOIN threads t ON t.id = m.thread_id WHERE t.doc_id = ? AND m.author_kind = 'human' AND m.rev != '' ORDER BY m.created_at DESC LIMIT 1`, docID).Scan(&rev, &at)
	return rev, at, err == nil
}

// --- events ------------------------------------------------------------------------

// LastSeq is the newest event's sequence number (0 when there are none).
func (s *Store) LastSeq() (int64, error) {
	var n sql.NullInt64
	err := s.db.QueryRow(`SELECT MAX(seq) FROM events`).Scan(&n)
	return n.Int64, err
}

// Events returns events after a sequence number, oldest first, optionally
// for one document.
func (s *Store) Events(after int64, docID string, limit int) ([]Event, error) {
	q := `SELECT seq, doc_id, thread_id, message_id, type, author_kind, author_name, at, payload FROM events WHERE seq > ?`
	args := []any{after}
	if docID != "" {
		q += ` AND doc_id = ?`
		args = append(args, docID)
	}
	q += ` ORDER BY seq`
	if limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var p string
		if err := rows.Scan(&e.Seq, &e.DocID, &e.ThreadID, &e.MessageID, &e.Type, &e.Author.Kind, &e.Author.Name, &e.At, &p); err != nil {
			return nil, err
		}
		e.Payload = json.RawMessage(p)
		out = append(out, e)
	}
	return out, rows.Err()
}

// RecentDocuments lists documents by their latest comment activity.
func (s *Store) RecentDocuments(limit int) ([]Document, error) {
	rows, err := s.db.Query(`SELECT d.id, d.path, d.dev, d.ino, d.content_hash, MAX(t.updated_at) FROM documents d JOIN threads t ON t.doc_id = d.id GROUP BY d.id ORDER BY MAX(t.updated_at) DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Document
	for rows.Next() {
		d, err := scanDoc(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// --- folders -----------------------------------------------------------------------

// AddFolder records a folder as opened.
func (s *Store) AddFolder(path string) error {
	now := Now()
	_, err := s.db.Exec(`INSERT INTO folders(path, opened_at, last_used) VALUES (?,?,?) ON CONFLICT(path) DO UPDATE SET last_used = excluded.last_used`, path, now, now)
	return err
}

// TouchFolder marks a folder as just used.
func (s *Store) TouchFolder(path string) {
	_, _ = s.db.Exec(`UPDATE folders SET last_used = ? WHERE path = ?`, Now(), path)
}

// RemoveFolder forgets a folder.
func (s *Store) RemoveFolder(path string) error {
	_, err := s.db.Exec(`DELETE FROM folders WHERE path = ?`, path)
	return err
}

// Folders lists opened folders, most recently used first.
func (s *Store) Folders() ([]Folder, error) {
	rows, err := s.db.Query(`SELECT path, opened_at, last_used FROM folders ORDER BY last_used DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Folder
	for rows.Next() {
		var f Folder
		if err := rows.Scan(&f.Path, &f.OpenedAt, &f.LastUsed); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ImportThread is one thread brought in from the old JSON store.
type ImportThread struct {
	Thread Thread
	// Text is the document text the anchor's Rev names, when it has one.
	Text string
}

// Import adds threads from the old store in one transaction, without
// recording events (an import is not news to anything watching), and marks
// the import done. It returns false if the import had already been done.
func (s *Store) Import(marker string, threads []ImportThread) (bool, error) {
	done := false
	err := s.tx(func(tx *sql.Tx) error {
		var v string
		if err := tx.QueryRow(`SELECT value FROM meta WHERE key = ?`, marker).Scan(&v); err == nil {
			return nil
		}
		for _, it := range threads {
			t := it.Thread
			if t.Anchor.Rev != "" && it.Text != "" {
				if err := putRevision(tx, t.DocID, t.Anchor.Rev, it.Text); err != nil {
					return err
				}
			}
			a, err := json.Marshal(t.Anchor)
			if err != nil {
				return err
			}
			by := ""
			if t.ResolvedBy != nil {
				b, _ := json.Marshal(t.ResolvedBy)
				by = string(b)
			}
			if _, err := tx.Exec(`INSERT INTO threads(id, doc_id, scope, anchor, status, created_at, resolved_at, resolved_by, updated_at) VALUES (?,?,?,?,?,?,?,?,?)`,
				t.ID, t.DocID, t.Scope, string(a), t.Status, t.CreatedAt, t.ResolvedAt, by, t.UpdatedAt); err != nil {
				return err
			}
			for _, m := range t.Messages {
				m.ThreadID = t.ID
				if err := insertMessage(tx, m); err != nil {
					return err
				}
			}
		}
		_, err := tx.Exec(`INSERT INTO meta(key, value) VALUES (?, ?)`, marker, Now())
		done = true
		return err
	})
	return done, err
}
