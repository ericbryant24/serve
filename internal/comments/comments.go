// Package comments joins the store, the renderer and the anchoring engine:
// it reads a file, carries its threads' anchors through whatever changed since
// they were last placed, and says where each thread is now, both in source
// terms (lines, for agents) and in page terms (leaf keys and offsets, for the
// browser). The CLI and the server both go through it, so they always agree
// on where a comment is.
package comments

import (
	"errors"
	"os"
	"strings"

	"serve/internal/anchor"
	"serve/internal/config"
	"serve/internal/paths"
	"serve/internal/render"
	"serve/internal/store"
)

// ErrStale means a selection no longer matches the file (it changed while
// the page was open and the text is not there any more).
var ErrStale = errors.New("the document changed; select the text again")

// Service is the comment operations.
type Service struct {
	Store  *store.Store
	Config config.Config
}

// Location is where a thread is now, in source terms.
type Location struct {
	State     string `json:"state"`
	LineStart int    `json:"line_start,omitempty"`
	LineEnd   int    `json:"line_end,omitempty"`
	Current   string `json:"current_text,omitempty"`
	Rendered  string `json:"rendered_text,omitempty"`
	Section   string `json:"section,omitempty"`
}

// State names for Location: "ok" plus the anchor states.
const (
	LocOK       = "ok"
	LocChanged  = anchor.StateChanged
	LocDeleted  = anchor.StateDeleted
	LocUnplaced = anchor.StateUnplaced
	LocPage     = "page"
	LocPageDOM  = "dom" // found in the page by selector only (scripted HTML)
)

// Placement is where a thread is now, in page terms.
type Placement struct {
	Segments []render.Segment `json:"segments,omitempty"`
	Element  string           `json:"element,omitempty"`
	Point    *render.Segment  `json:"point,omitempty"`
}

// ThreadView is a thread with its current location.
type ThreadView struct {
	*store.Thread
	Awaiting  string    `json:"awaiting"`
	Location  Location  `json:"location"`
	Placement Placement `json:"placement"`
}

// DocView is a rendered file with its threads placed.
type DocView struct {
	Path     string
	Doc      *render.Doc
	Document *store.Document
	Threads  []ThreadView
}

// Read loads and renders a file.
func (s *Service) Read(path string) (*render.Doc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) > render.MaxInlineBytes {
		return &render.Doc{Kind: render.KindBinary, Rev: anchor.Hash(string(data[:4096]))}, nil
	}
	return render.Render(path, data, s.Config.RenderOptions(path)), nil
}

// Open renders a file and places its threads. With create, a file with no
// document yet gets one; without, it has no threads.
func (s *Service) Open(path string, create bool) (*DocView, error) {
	path = paths.Abs(path)
	doc, err := s.Read(path)
	if err != nil {
		return nil, err
	}
	return s.place(path, doc, create)
}

// OpenRendered is Open for a file the caller has already rendered.
func (s *Service) OpenRendered(path string, doc *render.Doc, create bool) (*DocView, error) {
	return s.place(paths.Abs(path), doc, create)
}

func (s *Service) place(path string, doc *render.Doc, create bool) (*DocView, error) {
	dv := &DocView{Path: path, Doc: doc}
	d, err := s.Store.Document(path, create)
	if errors.Is(err, store.ErrNotFound) {
		return dv, nil
	}
	if err != nil {
		return nil, err
	}
	dv.Document = d
	threads, err := s.Store.Threads(d.ID)
	if err != nil {
		return nil, err
	}
	if err := s.remap(d, doc, threads); err != nil {
		return nil, err
	}
	for _, t := range threads {
		dv.Threads = append(dv.Threads, view(doc, t))
	}
	return dv, nil
}

// domOnly reports an element anchor that has no source range at all: an
// element on a page whose DOM is built by script, found by selector alone.
func domOnly(t *store.Thread) bool {
	a := t.Anchor
	return t.Scope == store.ScopeElement && a.Rev == "" && a.Legacy == nil && a.Quote == "" && a.Element != nil
}

// remap carries every anchor not yet placed against the current text through
// to it, and saves the results.
func (s *Service) remap(d *store.Document, doc *render.Doc, threads []*store.Thread) error {
	if doc.Source == "" && doc.Kind != render.KindMarkdown && doc.Kind != render.KindCode && doc.Kind != render.KindHTML {
		return nil
	}
	text := doc.Source
	var ups []store.AnchorUpdate
	var opt *anchor.Options
	for _, t := range threads {
		if t.Scope == store.ScopePage || domOnly(t) {
			continue
		}
		a := t.Anchor
		if a.Rev == doc.Rev && a.State != anchor.StateUnplaced {
			continue
		}
		if opt == nil {
			o := doc.AnchorOptions()
			opt = &o
		}
		var na anchor.Anchor
		if old, ok := s.Store.Revision(d.ID, a.Rev); ok && a.Rev != "" && a.State != anchor.StateUnplaced {
			na = anchor.Map(old, text, a, *opt)
		} else {
			na = anchor.Locate(text, a, *opt)
		}
		if na.State != anchor.StateUnplaced {
			// Placed against a known revision, the legacy fields have done
			// their job.
			na.Legacy = nil
			if na.Section == "" {
				na.Section = doc.SectionAt(na.Start)
			}
		}
		if na == a {
			continue
		}
		ups = append(ups, store.AnchorUpdate{ThreadID: t.ID, FromRev: a.Rev, Anchor: na})
		t.Anchor = na
	}
	if len(ups) == 0 {
		if d.ContentHash != doc.Rev {
			_ = s.Store.SeeDocument(d.ID, doc.Rev)
		}
		return nil
	}
	return s.Store.UpdateAnchors(d.ID, doc.Rev, text, ups, true)
}

// view describes where a thread is in the current document.
func view(doc *render.Doc, t *store.Thread) ThreadView {
	v := ThreadView{Thread: t, Awaiting: t.Awaiting()}
	a := t.Anchor
	switch {
	case t.Scope == store.ScopePage:
		v.Location.State = LocPage
		return v
	case domOnly(t):
		v.Location.State = LocPageDOM
		return v
	case a.State == anchor.StateUnplaced:
		v.Location = Location{State: LocUnplaced, Section: a.Section}
		return v
	}
	text := doc.Source
	if a.Start < 0 || a.End > len(text) || a.Start > a.End {
		v.Location = Location{State: LocUnplaced, Section: a.Section}
		return v
	}
	v.Location.State = LocOK
	if a.State != "" {
		v.Location.State = a.State
	}
	v.Location.LineStart, v.Location.LineEnd = anchor.Lines(text, a.Start, a.End)
	v.Location.Current = text[a.Start:a.End]
	v.Location.Section = a.Section
	if a.State == anchor.StateDeleted || a.Start == a.End {
		if p, ok := doc.PointAt(a.Start); ok {
			v.Placement.Point = &p
		}
		return v
	}
	if t.Scope == store.ScopeElement {
		if doc.Kind == render.KindHTML {
			// No blocks on an HTML page: the script in the frame finds the
			// element from its text range.
			v.Placement.Segments = doc.Segments(a.Start, a.End)
		} else if b, ok := doc.ElementFor(a.Start, a.End); ok {
			v.Placement.Element = b.Key
		}
		v.Location.Rendered = doc.RenderedText(a.Start, a.End)
		return v
	}
	v.Placement.Segments = doc.Segments(a.Start, a.End)
	v.Location.Rendered = doc.RenderedText(a.Start, a.End)
	return v
}

// --- creating threads -------------------------------------------------------------

// Selection is a text selection made in the browser.
type Selection struct {
	StartKey    string `json:"start_key"`
	StartOffset int    `json:"start_offset"`
	EndKey      string `json:"end_key"`
	EndOffset   int    `json:"end_offset"`
	Quote       string `json:"quote"`
	Prefix      string `json:"prefix,omitempty"`
	Suffix      string `json:"suffix,omitempty"`
}

// CreateText starts a thread on a text selection.
func (s *Service) CreateText(path string, sel Selection, author store.Author, body string) (*ThreadView, error) {
	dv, err := s.Open(path, true)
	if err != nil {
		return nil, err
	}
	doc := dv.Doc
	st, en, ok := doc.SourceFromSelection(sel.StartKey, sel.StartOffset, sel.EndKey, sel.EndOffset)
	if ok && sel.Quote != "" && !sameText(doc.RenderedText(st, en), sel.Quote) {
		ok = false
	}
	if !ok {
		st, en, ok = findRendered(doc, sel)
	}
	if !ok || en <= st {
		return nil, ErrStale
	}
	a := anchor.New(doc.Source, st, en, doc.SectionAt(st))
	a.Display = sel.Quote
	t, err := s.Store.CreateThread(store.NewThread{DocID: dv.Document.ID, Scope: store.ScopeText, Anchor: a, Author: author, Body: body, Text: doc.Source})
	if err != nil {
		return nil, err
	}
	v := view(doc, t)
	return &v, nil
}

// ElementRef is an element picked in comment mode.
type ElementRef struct {
	Key     string         `json:"key"`
	Element anchor.Element `json:"element"`
	// For an element on an HTML page: the range of its text in the page's
	// body text, when it has any.
	Range *Selection `json:"range,omitempty"`
}

// CreateElement starts a thread on a whole element.
func (s *Service) CreateElement(path string, ref ElementRef, author store.Author, body string) (*ThreadView, error) {
	if ref.Element.Tag == "" {
		return nil, errors.New("an element comment needs the element's tag")
	}
	dv, err := s.Open(path, true)
	if err != nil {
		return nil, err
	}
	doc := dv.Doc
	var a anchor.Anchor
	if b, ok := doc.Block(ref.Key); ok && ref.Key != render.BodyKey {
		a = anchor.New(doc.Source, b.Start, b.End, doc.SectionAt(b.Start))
	} else if ref.Range != nil {
		if st, en, ok := doc.SourceFromSelection(ref.Range.StartKey, ref.Range.StartOffset, ref.Range.EndKey, ref.Range.EndOffset); ok && en > st {
			a = anchor.New(doc.Source, st, en, doc.SectionAt(st))
		}
	}
	el := ref.Element
	a.Element = &el
	a.Display = el.Label
	t, err := s.Store.CreateThread(store.NewThread{DocID: dv.Document.ID, Scope: store.ScopeElement, Anchor: a, Author: author, Body: body, Text: doc.Source})
	if err != nil {
		return nil, err
	}
	v := view(doc, t)
	return &v, nil
}

// CreatePage starts a thread on the document as a whole.
func (s *Service) CreatePage(path string, author store.Author, body string) (*ThreadView, error) {
	dv, err := s.Open(path, true)
	if err != nil {
		return nil, err
	}
	t, err := s.Store.CreateThread(store.NewThread{DocID: dv.Document.ID, Scope: store.ScopePage, Author: author, Body: body, Anchor: anchor.Anchor{Rev: dv.Doc.Rev}})
	if err != nil {
		return nil, err
	}
	v := view(dv.Doc, t)
	return &v, nil
}

// Reply adds a message to a thread, recording the revision it was written
// against.
func (s *Service) Reply(threadID string, author store.Author, body string) (*store.Message, error) {
	rev := ""
	if t, err := s.Store.Thread(threadID); err == nil {
		if d, err := s.Store.DocumentByID(t.DocID); err == nil {
			if doc, err := s.Read(d.Path); err == nil {
				rev = doc.Rev
				_ = s.Store.PutRevision(d.ID, doc.Rev, doc.Source)
			}
		}
	}
	return s.Store.Reply(threadID, author, body, rev)
}

// sameText compares a selection's text with the document's, ignoring
// whitespace: the browser and the renderer put different whitespace between
// blocks.
func sameText(a, b string) bool {
	squash := func(s string) string { return strings.Join(strings.Fields(anchor.Normalize(s)), "") }
	return squash(a) == squash(b)
}

// findRendered looks for a selection's quote in the rendered text of the
// document, using its context to pick between occurrences.
func findRendered(doc *render.Doc, sel Selection) (int, int, bool) {
	q := sel.Quote
	if strings.TrimSpace(q) == "" {
		return 0, 0, false
	}
	type hit struct{ s, e, score int }
	var best *hit
	for _, b := range doc.Leaves() {
		text := b.Text
		for from := 0; ; {
			i := strings.Index(text[from:], q)
			if i < 0 {
				break
			}
			at := from + i
			score := commonSuffix(sel.Prefix, text[:at]) + commonPrefix(sel.Suffix, text[at+len(q):])
			if best == nil || score > best.score {
				rs := utf16Len(text[:at])
				st, en, ok := doc.SourceFromSelection(b.Key, rs, b.Key, rs+utf16Len(q))
				if ok {
					best = &hit{st, en, score}
				}
			}
			from = at + 1
		}
	}
	if best == nil {
		return 0, 0, false
	}
	return best.s, best.e, true
}

func commonSuffix(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[len(a)-1-n] == b[len(b)-1-n] {
		n++
	}
	return n
}

func commonPrefix(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}
