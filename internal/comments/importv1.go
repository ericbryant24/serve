package comments

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"serve/internal/anchor"
	"serve/internal/paths"
	"serve/internal/store"
)

// The JSON-per-document store that came before the database: one file per
// document under ~/.serve/comments, a flat list of comments where a reply is
// a comment with a parent_id.

const importMarker = "import_v1"

type v1Element struct {
	Tag      string `json:"tag"`
	Selector string `json:"selector"`
	Label    string `json:"label"`
	Before   string `json:"before"`
	After    string `json:"after"`
}

type v1Comment struct {
	ID            string     `json:"id"`
	Text          string     `json:"text"`
	CreatedAt     string     `json:"created_at"`
	Resolved      bool       `json:"resolved"`
	Scope         string     `json:"scope"`
	AnchorText    string     `json:"anchor_text"`
	BlockText     string     `json:"block_text"`
	ParentID      *string    `json:"parent_id"`
	BlockHash     string     `json:"block_hash"`
	BlockPrevText string     `json:"block_prev_text"`
	BlockNextText string     `json:"block_next_text"`
	Element       *v1Element `json:"element"`
}

type v1File struct {
	Path     string      `json:"path"`
	Comments []v1Comment `json:"comments"`
}

// V1Dir is where the old store lived.
func V1Dir() string { return filepath.Join(paths.StateDir(), "comments") }

// ImportResult says what an import did.
type ImportResult struct {
	Documents, Threads, Messages, Placed int
	Skipped                              []string
}

// ImportV1 brings comments from the old store into the database, once. The
// JSON files are left as they are.
func (s *Service) ImportV1(dir string) (*ImportResult, error) {
	if _, done := s.Store.Meta(importMarker); done {
		return nil, nil
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	res := &ImportResult{}
	var batch []store.ImportThread
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var vf v1File
		if len(strings.TrimSpace(string(data))) > 0 && strings.TrimSpace(string(data))[0] == '[' {
			res.Skipped = append(res.Skipped, filepath.Base(f)+" (no path recorded)")
			continue
		}
		if err := json.Unmarshal(data, &vf); err != nil || vf.Path == "" {
			res.Skipped = append(res.Skipped, filepath.Base(f))
			continue
		}
		threads, placed, err := s.importFile(vf)
		if err != nil {
			res.Skipped = append(res.Skipped, fmt.Sprintf("%s (%v)", filepath.Base(f), err))
			continue
		}
		if len(threads) == 0 {
			continue
		}
		res.Documents++
		res.Placed += placed
		for _, t := range threads {
			res.Threads++
			res.Messages += len(t.Thread.Messages)
		}
		batch = append(batch, threads...)
	}
	done, err := s.Store.Import(importMarker, batch)
	if err != nil {
		return nil, err
	}
	if !done {
		return nil, nil
	}
	return res, nil
}

func (s *Service) importFile(vf v1File) ([]store.ImportThread, int, error) {
	path := vf.Path
	d, err := s.Store.Document(path, true)
	if err != nil {
		return nil, 0, err
	}
	var text string
	var opt anchor.Options
	exists := false
	if doc, err := s.Read(path); err == nil && (doc.Source != "" || doc.Kind == "markdown") {
		text = doc.Source
		opt = doc.AnchorOptions()
		exists = true
		defer func() { _ = s.Store.SeeDocument(d.ID, doc.Rev) }()
	}
	byID := map[string]v1Comment{}
	for _, c := range vf.Comments {
		byID[c.ID] = c
	}
	rootOf := func(c v1Comment) string {
		seen := map[string]bool{}
		for c.ParentID != nil && !seen[c.ID] {
			seen[c.ID] = true
			p, ok := byID[*c.ParentID]
			if !ok {
				return ""
			}
			c = p
		}
		return c.ID
	}
	replies := map[string][]v1Comment{}
	var roots []v1Comment
	for _, c := range vf.Comments {
		if c.ParentID == nil {
			roots = append(roots, c)
			continue
		}
		r := rootOf(c)
		if r == "" {
			// A reply whose parent is missing becomes a page comment.
			c.ParentID = nil
			c.Scope = "page"
			roots = append(roots, c)
			continue
		}
		replies[r] = append(replies[r], c)
	}
	name := s.Config.Name
	var out []store.ImportThread
	placed := 0
	for _, r := range roots {
		t := store.Thread{ID: store.NewID(), DocID: d.ID, Status: store.StatusOpen, CreatedAt: stamp(r.CreatedAt)}
		if r.Resolved {
			t.Status = store.StatusResolved
		}
		var a anchor.Anchor
		switch r.Scope {
		case "page":
			t.Scope = store.ScopePage
		case "element":
			t.Scope = store.ScopeElement
			if r.Element != nil {
				a.Element = &anchor.Element{Tag: r.Element.Tag, Label: r.Element.Label, Selector: r.Element.Selector, Before: r.Element.Before, After: r.Element.After}
				a.Display = r.Element.Label
			}
		default:
			t.Scope = store.ScopeText
			a.Quote, a.Display = r.AnchorText, r.AnchorText
		}
		if t.Scope != store.ScopePage {
			if r.BlockText != "" || r.BlockHash != "" {
				a.Legacy = &anchor.Legacy{BlockText: r.BlockText, BlockHash: r.BlockHash, PrevText: r.BlockPrevText, NextText: r.BlockNextText}
			}
			if exists {
				na := anchor.Locate(text, a, opt)
				if na.State != anchor.StateUnplaced {
					placed++
					na.Legacy = nil
					if na.State == anchor.StateOK {
						// The old quote was rendered text; from here on the
						// quote is the source it covers.
						na.Quote = text[na.Start:na.End]
						p, sfx := anchor.Context(text, na.Start, na.End)
						na.Prefix, na.Suffix = p, sfx
					}
				}
				a = na
			} else {
				a.State = anchor.StateUnplaced
			}
			if a.Element != nil && a.Rev == "" && a.Legacy == nil && a.Quote == "" && a.State == anchor.StateUnplaced {
				a.State = "" // found by selector alone
			}
		}
		t.Anchor = a
		msgs := []store.Message{{ID: store.NewID(), Author: store.Author{Kind: store.Human, Name: name}, Body: r.Text, CreatedAt: t.CreatedAt}}
		rs := replies[r.ID]
		sort.SliceStable(rs, func(i, j int) bool { return rs[i].CreatedAt < rs[j].CreatedAt })
		for _, c := range rs {
			// The browser sent the thread's anchor (or a page scope) along with
			// every reply; `serve reply` sent neither. That is how a reply's
			// author is told apart.
			au := store.Author{Kind: store.Agent, Name: "agent"}
			if c.AnchorText != "" || c.BlockText != "" || c.Scope != "" || c.Element != nil {
				au = store.Author{Kind: store.Human, Name: name}
			}
			msgs = append(msgs, store.Message{ID: store.NewID(), Author: au, Body: c.Text, CreatedAt: stamp(c.CreatedAt)})
		}
		t.Messages = msgs
		t.UpdatedAt = msgs[len(msgs)-1].CreatedAt
		if t.Status == store.StatusResolved {
			t.ResolvedAt = t.UpdatedAt
		}
		it := store.ImportThread{Thread: t}
		if t.Anchor.Rev != "" && t.Anchor.State != anchor.StateUnplaced {
			it.Text = text
		}
		out = append(out, it)
	}
	return out, placed, nil
}

// stamp converts an old RFC 3339 timestamp to the stored format.
func stamp(s string) string {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return store.Now()
	}
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}
