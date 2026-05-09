package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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

var storeDir = filepath.Join(homeDir(), ".serve", "comments")

func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

// ---------------------------------------------------------------------------
// Document ID management
// ---------------------------------------------------------------------------

var (
	mdFrontmatterRe = regexp.MustCompile(`(?s)^---\s*\n(.*?\n)---\s*\n`)
	htmlCommentIDRe = regexp.MustCompile(`(?i)<meta\s+name=["']comment-id["']\s+content=["']([^"']+)["']`)
)

func getDocumentID(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	content := string(data)
	ext := strings.ToLower(filepath.Ext(path))

	if ext == ".md" {
		m := mdFrontmatterRe.FindStringSubmatch(content)
		if m != nil {
			for _, line := range strings.Split(m[1], "\n") {
				if strings.HasPrefix(line, "comment-id:") {
					return strings.TrimSpace(strings.TrimPrefix(line, "comment-id:"))
				}
			}
		}
		return ""
	}

	m := htmlCommentIDRe.FindStringSubmatch(content)
	if m != nil {
		return m[1]
	}
	return ""
}

func setDocumentID(path, docID string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	content := string(data)
	ext := strings.ToLower(filepath.Ext(path))

	if ext == ".md" {
		m := mdFrontmatterRe.FindStringSubmatchIndex(content)
		if m != nil {
			// Append to existing frontmatter
			fmEnd := m[1]
			fmBody := content[m[2]:m[3]]
			newFM := fmt.Sprintf("---\n%scomment-id: %s\n---\n", fmBody, docID)
			content = newFM + content[fmEnd:]
		} else {
			content = fmt.Sprintf("---\ncomment-id: %s\n---\n\n%s", docID, content)
		}
	} else {
		tag := fmt.Sprintf(`<meta name="comment-id" content="%s">`, docID)
		switch {
		case strings.Contains(content, "<head>"):
			content = strings.Replace(content, "<head>", "<head>\n  "+tag, 1)
		case strings.Contains(content, "<HEAD>"):
			content = strings.Replace(content, "<HEAD>", "<HEAD>\n  "+tag, 1)
		default:
			content = tag + "\n" + content
		}
	}

	return os.WriteFile(path, []byte(content), 0644)
}

func ensureDocumentID(path string) (string, error) {
	if id := getDocumentID(path); id != "" {
		return id, nil
	}
	id := generateShortID()
	if err := setDocumentID(path, id); err != nil {
		return "", err
	}
	return id, nil
}

func generateShortID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())[:8]
	}
	return hex.EncodeToString(b)
}

// ---------------------------------------------------------------------------
// Comment store
// ---------------------------------------------------------------------------

type CommentStore struct {
	DocID string
	path  string
}

func NewCommentStore(docID string) *CommentStore {
	os.MkdirAll(storeDir, 0755)
	return &CommentStore{
		DocID: docID,
		path:  filepath.Join(storeDir, docID+".json"),
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
	return s.load()
}

func (s *CommentStore) Add(text, anchorText, blockText string, lineStart, lineEnd *int, parentID *string) (*Comment, error) {
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
	comments, err := s.load()
	if err != nil {
		return false, err
	}
	var filtered []Comment
	found := false
	for _, c := range comments {
		if c.ID == id {
			found = true
			continue
		}
		if c.ParentID != nil && *c.ParentID == id {
			continue // cascade delete replies
		}
		filtered = append(filtered, c)
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
