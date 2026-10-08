// Package render turns a document's source into the HTML serve shows, and
// keeps a map from every rendered character back to the byte of source it came
// from.
//
// The map is what lets comments live in source coordinates. Each block that
// holds text directly (a paragraph, a heading, a table cell, a line of code) is
// a "leaf" with a content-derived key, written into the HTML as data-b. For a
// leaf, Runs record which stretch of its rendered text (the browser's
// textContent, counted in UTF-16 units as JavaScript counts them) came from
// which source bytes. A browser selection arrives as (leaf key, offset), is
// turned into a source range here, and an anchor's source range goes back out
// as (leaf key, start, end) segments for the browser to wrap in <mark>s. No
// text is ever searched for.
package render

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"serve/internal/anchor"
)

// Kinds of document.
const (
	KindMarkdown = "markdown"
	KindCode     = "code"
	KindText     = "text"
	KindHTML     = "html"
	KindPDF      = "pdf"
	KindImage    = "image"
	KindBinary   = "binary"
)

// Run maps one stretch of a leaf's rendered text to source.
type Run struct {
	R    int    // start, in UTF-16 units from the start of the leaf's text
	RLen int    // length in UTF-16 units
	S, E int    // source byte range
	Text string // the rendered text of this run
	// Literal runs render exactly their source bytes, so positions inside
	// them map character by character. Other runs (an escape, an entity, a
	// line break) map only as a whole.
	Literal bool
}

// Block is one block element of the rendered page.
type Block struct {
	Key    string `json:"key"`
	Kind   string `json:"kind"`
	Start  int    `json:"start"`
	End    int    `json:"end"`
	Leaf   bool   `json:"leaf,omitempty"`
	Parent int    `json:"-"`
	Text   string `json:"-"` // a leaf's rendered text
	Runs   []Run  `json:"-"`
	RLen   int    `json:"-"` // utf16 length of Text
}

// Top is one top-level block's HTML, the unit a live update replaces.
type Top struct {
	Key  string `json:"key"`  // hash of the HTML
	Src  string `json:"src"`  // hash of the source text, to tell an edit from a renumbering
	HTML string `json:"html"` // rendered HTML
}

// Heading is a heading and the section it opens.
type Heading struct {
	Level int    `json:"level"`
	Text  string `json:"text"`
	ID    string `json:"id"`
	Start int    `json:"start"`
	Path  string `json:"path"`
}

// Segment is part of an anchor's range inside one leaf, in the leaf's
// rendered-text coordinates.
type Segment struct {
	Key   string `json:"key"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	Text  string `json:"text"`
}

// Doc is a rendered document.
type Doc struct {
	Kind        string
	Source      string
	Rev         string
	HTML        string
	Tops        []Top
	Blocks      []Block
	Headings    []Heading
	Title       string
	HasMermaid  bool
	IsMarp      bool
	Frontmatter [][2]string
	Lang        string   // code files: the lexer name
	Refs        []string // URLs the page loads (images, media, stylesheets, scripts), as written
	legacy      []anchor.Block
	byKey       map[string]int
}

func (d *Doc) index() {
	d.byKey = make(map[string]int, len(d.Blocks))
	for i, b := range d.Blocks {
		d.byKey[b.Key] = i
	}
}

// Block returns the block with key k.
func (d *Doc) Block(k string) (*Block, bool) {
	if d.byKey == nil {
		d.index()
	}
	i, ok := d.byKey[k]
	if !ok {
		return nil, false
	}
	return &d.Blocks[i], true
}

// Leaves returns the leaf blocks in document order.
func (d *Doc) Leaves() []*Block {
	var out []*Block
	for i := range d.Blocks {
		if d.Blocks[i].Leaf {
			out = append(out, &d.Blocks[i])
		}
	}
	return out
}

// SourceFromSelection converts a selection made in the browser, as leaf keys
// and offsets in their rendered text, into a source byte range.
func (d *Doc) SourceFromSelection(startKey string, startOff int, endKey string, endOff int) (int, int, bool) {
	sb, ok1 := d.Block(startKey)
	eb, ok2 := d.Block(endKey)
	if !ok1 || !ok2 || !sb.Leaf || !eb.Leaf {
		return 0, 0, false
	}
	s := sb.toSourceStart(startOff)
	e := eb.toSourceEnd(endOff)
	if e < s {
		s, e = e, s
	}
	return s, e, true
}

// RenderedText returns what a source range looks like rendered: the rendered
// text of each leaf it touches, joined by newlines.
func (d *Doc) RenderedText(start, end int) string {
	var parts []string
	for _, seg := range d.Segments(start, end) {
		parts = append(parts, seg.Text)
	}
	return strings.Join(parts, "\n")
}

// Segments splits a source range into per-leaf rendered ranges.
func (d *Doc) Segments(start, end int) []Segment {
	var out []Segment
	for _, b := range d.Leaves() {
		if b.End <= start || b.Start >= end || len(b.Runs) == 0 {
			continue
		}
		rs := b.toRenderedStart(max(start, b.Start))
		re := b.toRenderedEnd(min(end, b.End))
		if re <= rs {
			continue
		}
		out = append(out, Segment{Key: b.Key, Start: rs, End: re, Text: utf16Slice(b.Text, rs, re)})
	}
	return out
}

// PointAt places a collapsed anchor: the leaf at or after a source offset,
// and the rendered offset within it.
func (d *Doc) PointAt(pos int) (Segment, bool) {
	var last *Block
	for _, b := range d.Leaves() {
		if pos < b.End || (pos >= b.Start && pos <= b.End) {
			off := 0
			if pos > b.Start {
				off = b.toRenderedStart(pos)
			}
			return Segment{Key: b.Key, Start: off, End: off}, true
		}
		last = b
	}
	if last != nil {
		return Segment{Key: last.Key, Start: last.RLen, End: last.RLen}, true
	}
	return Segment{}, false
}

// ElementFor finds the block an element anchor names: the one whose source
// range matches exactly, else the smallest one containing the range.
func (d *Doc) ElementFor(start, end int) (*Block, bool) {
	var best *Block
	for i := range d.Blocks {
		b := &d.Blocks[i]
		if b.Start == start && b.End == end {
			return b, true
		}
		if b.Start <= start && b.End >= end && (best == nil || b.End-b.Start < best.End-best.Start) {
			best = b
		}
	}
	return best, best != nil
}

// SectionAt returns the heading path of the section a source offset is in.
func (d *Doc) SectionAt(pos int) string {
	path := ""
	for _, h := range d.Headings {
		if h.Start > pos {
			break
		}
		path = h.Path
	}
	return path
}

// AnchorOptions describes the document's structure for anchor.Map and
// anchor.Locate.
func (d *Doc) AnchorOptions() anchor.Options {
	opt := anchor.Options{Blocks: d.legacy}
	if opt.Blocks == nil {
		for _, b := range d.Leaves() {
			text := anchor.Normalize(b.Text)
			opt.Blocks = append(opt.Blocks, anchor.Block{
				Start: b.Start, End: b.End, Text: text, Hash: anchor.HashBlockText(text), Leaf: true,
				Find: d.finder(b.Start, b.End),
			})
		}
	}
	for _, h := range d.Headings {
		opt.Sections = append(opt.Sections, anchor.Section{Path: h.Path, Start: h.Start})
	}
	return opt
}

// finder searches a phrase, as rendered text, in the leaves inside a source
// range and returns its source range.
func (d *Doc) finder(start, end int) func(string) (int, int, bool) {
	return func(phrase string) (int, int, bool) {
		want := anchor.Normalize(phrase)
		if want == "" {
			return 0, 0, false
		}
		for _, b := range d.Leaves() {
			if b.Start < start || b.End > end {
				continue
			}
			if i := strings.Index(b.Text, phrase); i >= 0 {
				rs := utf16Len(b.Text[:i])
				return b.toSourceStart(rs), b.toSourceEnd(rs + utf16Len(phrase)), true
			}
			if s, e, ok := normalizedFind(b.Text, want); ok {
				rs, re := utf16Len(b.Text[:s]), utf16Len(b.Text[:e])
				return b.toSourceStart(rs), b.toSourceEnd(re), true
			}
		}
		return 0, 0, false
	}
}

// normalizedFind finds want (already normalized) in text, comparing both in
// normalized form, and returns byte offsets in text.
func normalizedFind(text, want string) (int, int, bool) {
	// Normalize character by character, remembering where each output byte
	// came from.
	var norm strings.Builder
	var from []int
	for i, r := range text {
		n := anchor.Normalize(string(r))
		if n == "" {
			n = " "
		}
		for j := 0; j < len(n); j++ {
			from = append(from, i)
		}
		norm.WriteString(n)
	}
	ns := norm.String()
	// Collapse runs of spaces the same way Normalize would.
	var collapsed strings.Builder
	var cfrom []int
	prevSpace := true
	for i := 0; i < len(ns); i++ {
		if ns[i] == ' ' {
			if prevSpace {
				continue
			}
			prevSpace = true
		} else {
			prevSpace = false
		}
		collapsed.WriteByte(ns[i])
		cfrom = append(cfrom, from[i])
	}
	cs := collapsed.String()
	k := strings.Index(cs, want)
	if k < 0 {
		return 0, 0, false
	}
	s := cfrom[k]
	last := cfrom[k+len(want)-1]
	_, size := utf8.DecodeRuneInString(text[last:])
	return s, last + size, true
}

// --- run mapping --------------------------------------------------------

func (b *Block) toSourceStart(r int) int {
	for _, run := range b.Runs {
		if r < run.R+run.RLen {
			if r <= run.R {
				return run.S
			}
			if run.Literal {
				return run.S + utf16ToByte(run.Text, r-run.R)
			}
			return run.S
		}
	}
	if n := len(b.Runs); n > 0 {
		return b.Runs[n-1].E
	}
	return b.Start
}

func (b *Block) toSourceEnd(r int) int {
	for i := len(b.Runs) - 1; i >= 0; i-- {
		run := b.Runs[i]
		if r > run.R {
			if r >= run.R+run.RLen {
				return run.E
			}
			if run.Literal {
				return run.S + utf16ToByte(run.Text, r-run.R)
			}
			return run.E
		}
	}
	if len(b.Runs) > 0 {
		return b.Runs[0].S
	}
	return b.Start
}

func (b *Block) toRenderedStart(s int) int {
	for _, run := range b.Runs {
		if s < run.E {
			if s <= run.S {
				return run.R
			}
			if run.Literal {
				return run.R + utf16Len(run.Text[:s-run.S])
			}
			return run.R
		}
	}
	return b.RLen
}

func (b *Block) toRenderedEnd(s int) int {
	for i := len(b.Runs) - 1; i >= 0; i-- {
		run := b.Runs[i]
		if s > run.S {
			if s >= run.E {
				return run.R + run.RLen
			}
			if run.Literal {
				return run.R + utf16Len(run.Text[:s-run.S])
			}
			return run.R + run.RLen
		}
	}
	return 0
}

// --- leaf construction --------------------------------------------------

// leafBuilder accumulates a leaf's rendered text and runs as it is written.
type leafBuilder struct {
	text    strings.Builder
	runs    []Run
	r       int // utf16 length so far
	lastE   int // source end of the last mapped run
	pending []int
}

// literal records rendered text that is exactly source[s:e].
func (l *leafBuilder) literal(s, e int, text string) {
	if text == "" {
		return
	}
	l.settle(s)
	n := utf16Len(text)
	l.runs = append(l.runs, Run{R: l.r, RLen: n, S: s, E: e, Text: text, Literal: true})
	l.text.WriteString(text)
	l.r += n
	l.lastE = e
}

// atomic records rendered text that came from source[s:e] as a whole.
func (l *leafBuilder) atomic(s, e int, text string) {
	if text == "" {
		return
	}
	l.settle(s)
	n := utf16Len(text)
	l.runs = append(l.runs, Run{R: l.r, RLen: n, S: s, E: e, Text: text})
	l.text.WriteString(text)
	l.r += n
	l.lastE = e
}

// unmapped records rendered text with no source of its own (a line break, a
// footnote number). It is pinned between the source around it once the next
// mapped run says where that is.
func (l *leafBuilder) unmapped(text string) {
	if text == "" {
		return
	}
	n := utf16Len(text)
	l.runs = append(l.runs, Run{R: l.r, RLen: n, S: l.lastE, E: l.lastE, Text: text})
	l.pending = append(l.pending, len(l.runs)-1)
	l.text.WriteString(text)
	l.r += n
}

func (l *leafBuilder) settle(next int) {
	for _, i := range l.pending {
		if next >= l.runs[i].S {
			l.runs[i].E = next
		}
	}
	l.pending = l.pending[:0]
}

func (l *leafBuilder) finish(b *Block) {
	l.pending = l.pending[:0]
	b.Text = l.text.String()
	b.Runs = l.runs
	b.RLen = l.r
	b.Leaf = true
}

// --- helpers -----------------------------------------------------------

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

// utf16ToByte returns the byte offset in s of UTF-16 offset u.
func utf16ToByte(s string, u int) int {
	n := 0
	for i, r := range s {
		if n >= u {
			return i
		}
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return len(s)
}

func utf16Slice(s string, a, b int) string {
	u := utf16.Encode([]rune(s))
	a, b = min(max(a, 0), len(u)), min(max(b, 0), len(u))
	if a >= b {
		return ""
	}
	return string(utf16.Decode(u[a:b]))
}

func shortHash(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)[:5])
}

// keyer hands out content-derived block keys, numbering repeats so that every
// key on a page is unique.
type keyer map[string]int

func (k keyer) key(kind, text string) string {
	base := shortHash(kind, text)
	n := k[base]
	k[base] = n + 1
	if n == 0 {
		return base
	}
	return base + "." + itoaN(n)
}

func itoaN(n int) string {
	if n == 0 {
		return "0"
	}
	var d [20]byte
	i := len(d)
	for n > 0 {
		i--
		d[i] = byte('0' + n%10)
		n /= 10
	}
	return string(d[i:])
}

// sortBlocks puts blocks in source order (parents before children).
func sortBlocks(bs []Block) {
	sort.SliceStable(bs, func(i, j int) bool {
		if bs[i].Start != bs[j].Start {
			return bs[i].Start < bs[j].Start
		}
		return bs[i].End > bs[j].End
	})
}
