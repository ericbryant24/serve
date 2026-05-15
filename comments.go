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

type CommentStore struct {
	DocID string
	path  string
	mu    sync.Mutex
}

func NewCommentStore(docID, dir string) *CommentStore {
	os.MkdirAll(dir, 0755)
	return &CommentStore{
		DocID: docID,
		path:  filepath.Join(dir, docID+".json"),
	}
}

func (s *CommentStore) load() ([]Comment, error) {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var comments []Comment
	if err := json.Unmarshal(data, &comments); err != nil {
		return nil, err
	}
	return comments, nil
}

func (s *CommentStore) save(comments []Comment) error {
	data, err := json.MarshalIndent(comments, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0644)
}

func (s *CommentStore) List() ([]Comment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

func (s *CommentStore) Add(text, anchorText, blockText string, lineStart, lineEnd *int, parentID *string) (*Comment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	comments, err := s.load()
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

func (s *CommentStore) Update(id string, text *string, resolved *bool) (*Comment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	comments, err := s.load()
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
	comments, err := s.load()
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
