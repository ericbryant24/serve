package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Comment represents an inline comment on a document.
type Comment struct {
	ID              string  `json:"id"`
	Text            string  `json:"text"`
	CreatedAt       string  `json:"created_at"`
	Resolved        bool    `json:"resolved"`
	AnchorText      string  `json:"anchor_text"`
	BlockText       string  `json:"block_text"`
	SourceLineStart *int    `json:"source_line_start"`
	SourceLineEnd   *int    `json:"source_line_end"`
	ParentID        *string `json:"parent_id"`
}

// ---------------------------------------------------------------------------
// Store key — inode-based, never modifies source files
// ---------------------------------------------------------------------------

// storeKeyForFile returns a stable key for the comment store. It delegates to
// the platform-specific inodeStoreKey, which uses the file's inode+device on
// Unix (survives mv/git mv) or a path hash on Windows.
func storeKeyForFile(path string) string {
	return inodeStoreKey(path)
}

func commentStoreDir() string {
	return filepath.Join(homeDir(), ".serve", "comments")
}

func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

// ---------------------------------------------------------------------------
// Comment store
// ---------------------------------------------------------------------------

// commentsFile is the on-disk shape of a comment store JSON. The legacy shape
// was a bare []Comment; load() reads both for backward compat, save() always
// writes this wrapped form so the source path can travel with the comments.
type commentsFile struct {
	Path     string    `json:"path,omitempty"`
	Comments []Comment `json:"comments"`
}

type CommentStore struct {
	DocID      string
	path       string
	sourcePath string
	mu         sync.Mutex
}

func NewCommentStore(docID, dir string) *CommentStore {
	os.MkdirAll(dir, 0755)
	return &CommentStore{
		DocID: docID,
		path:  filepath.Join(dir, docID+".json"),
	}
}

// NewCommentStoreForFile creates a store keyed by the file's inode and tags
// future writes with the file's absolute path. Use this when the caller has
// the source path on hand; downstream tools like `serve watch` rely on it to
// emit events with a usable `file` field.
func NewCommentStoreForFile(filePath, dir string) *CommentStore {
	s := NewCommentStore(storeKeyForFile(filePath), dir)
	s.sourcePath = filePath
	return s
}

// load reads the comments JSON. It accepts both the legacy bare-array shape
// and the wrapped commentsFile shape, returning the persisted source path
// when available (empty string otherwise).
func (s *CommentStore) load() ([]Comment, string, error) {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		// Self-heal: if an external editor atomic-rewrote the source file,
		// the inode (and so the storeKey) changed, leaving the old comments
		// orphaned under a different key. The Path field persisted in
		// commentsFile lets us find them — match by absolute path, rename
		// the orphaned file to our current storeKey, continue as normal.
		if migrated, path, ok := s.tryAdoptOrphan(); ok {
			return migrated, path, nil
		}
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	// Try the wrapped shape first. json.Unmarshal into a struct will succeed
	// on a top-level array too (silently leaving fields zero), so peek at the
	// first non-whitespace byte to disambiguate.
	for _, b := range data {
		if b == ' ' || b == '\t' || b == '\n' || b == '\r' {
			continue
		}
		if b == '[' {
			var legacy []Comment
			if err := json.Unmarshal(data, &legacy); err != nil {
				return nil, "", err
			}
			return legacy, "", nil
		}
		break
	}
	var wrapped commentsFile
	if err := json.Unmarshal(data, &wrapped); err != nil {
		return nil, "", err
	}
	return wrapped.Comments, wrapped.Path, nil
}

func (s *CommentStore) save(comments []Comment) error {
	if comments == nil {
		comments = []Comment{}
	}
	path := s.sourcePath
	if path == "" {
		// Preserve any path already on disk so we don't lose it when a caller
		// without source-path knowledge (e.g. tests) writes through the store.
		if _, persisted, err := s.load(); err == nil {
			path = persisted
		}
	}
	wrapped := commentsFile{Path: path, Comments: comments}
	data, err := json.MarshalIndent(wrapped, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0644)
}

// LoadCommentsByKey reads the comments file for a given store key without
// requiring a CommentStore instance. Used by `serve watch` to enumerate all
// stored documents.
func LoadCommentsByKey(key, dir string) (path string, comments []Comment, err error) {
	s := &CommentStore{DocID: key, path: filepath.Join(dir, key+".json")}
	comments, path, err = s.load()
	return
}

func (s *CommentStore) List() ([]Comment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	comments, _, err := s.load()
	return comments, err
}

func (s *CommentStore) Add(text, anchorText, blockText string, lineStart, lineEnd *int, parentID *string) (*Comment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	comments, _, err := s.load()
	if err != nil {
		return nil, err
	}
	c := Comment{
		ID:              generateFullUUID(),
		Text:            text,
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
		AnchorText:      anchorText,
		BlockText:       blockText,
		SourceLineStart: lineStart,
		SourceLineEnd:   lineEnd,
		ParentID:        parentID,
	}
	comments = append(comments, c)
	return &c, s.save(comments)
}

// Reply adds a reply to an existing comment, threading it under parentID.
// It returns (nil, nil) if no comment with parentID exists, so callers can
// distinguish a bad parent from a storage error.
func (s *CommentStore) Reply(parentID, text string) (*Comment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	comments, _, err := s.load()
	if err != nil {
		return nil, err
	}
	found := false
	for i := range comments {
		if comments[i].ID == parentID {
			found = true
			break
		}
	}
	if !found {
		return nil, nil
	}
	pid := parentID
	c := Comment{
		ID:        generateFullUUID(),
		Text:      text,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		ParentID:  &pid,
	}
	comments = append(comments, c)
	return &c, s.save(comments)
}

func (s *CommentStore) Update(id string, text *string, resolved *bool) (*Comment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	comments, _, err := s.load()
	if err != nil {
		return nil, err
	}
	for i := range comments {
		if comments[i].ID == id {
			if text != nil {
				comments[i].Text = *text
			}
			if resolved != nil {
				comments[i].Resolved = *resolved
			}
			if err := s.save(comments); err != nil {
				return nil, err
			}
			c := comments[i]
			return &c, nil
		}
	}
	return nil, nil
}

func (s *CommentStore) Delete(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	comments, _, err := s.load()
	if err != nil {
		return false, err
	}
	// Collect the target and all descendants (any depth)
	toDelete := map[string]bool{id: true}
	for changed := true; changed; {
		changed = false
		for _, c := range comments {
			if c.ParentID != nil && toDelete[*c.ParentID] && !toDelete[c.ID] {
				toDelete[c.ID] = true
				changed = true
			}
		}
	}
	found := false
	filtered := []Comment{}
	for _, c := range comments {
		if c.ID == id {
			found = true
		}
		if !toDelete[c.ID] {
			filtered = append(filtered, c)
		}
	}
	if !found {
		return false, nil
	}
	return true, s.save(filtered)
}

// tryAdoptOrphan scans the comments directory for a file whose persisted
// Path matches this store's sourcePath. If exactly one match exists, it is
// renamed to s.path (the current storeKey's location) and its contents are
// returned. This makes the inode-based store key self-healing across
// editors that atomic-write (write-temp + rename).
//
// Legacy bare-array stores have no Path field so they can't participate.
// That's acceptable — they predate path tagging.
func (s *CommentStore) tryAdoptOrphan() ([]Comment, string, bool) {
	if s.sourcePath == "" {
		return nil, "", false
	}
	absTarget, err := filepath.Abs(s.sourcePath)
	if err != nil {
		return nil, "", false
	}
	dir := filepath.Dir(s.path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, "", false
	}
	var bestPath string
	var bestComments []Comment
	var bestMtime time.Time
	matches := 0
	for _, ent := range entries {
		name := ent.Name()
		full := filepath.Join(dir, name)
		if full == s.path {
			continue
		}
		if filepath.Ext(name) != ".json" {
			continue
		}
		data, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		var wrapped commentsFile
		if err := json.Unmarshal(data, &wrapped); err != nil {
			continue
		}
		if wrapped.Path == "" {
			continue
		}
		abs, err := filepath.Abs(wrapped.Path)
		if err != nil || abs != absTarget {
			continue
		}
		fi, err := ent.Info()
		if err != nil {
			continue
		}
		matches++
		if bestPath == "" || fi.ModTime().After(bestMtime) {
			bestPath = full
			bestComments = wrapped.Comments
			bestMtime = fi.ModTime()
		}
	}
	if bestPath == "" {
		return nil, "", false
	}
	if matches > 1 {
		fmt.Fprintf(os.Stderr,
			"serve: %d orphaned comment files claim %s; adopting newest (%s)\n",
			matches, absTarget, filepath.Base(bestPath))
	}
	if err := os.Rename(bestPath, s.path); err != nil {
		return nil, "", false
	}
	return bestComments, s.sourcePath, true
}

func generateFullUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant bits
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%12x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
