package render

import (
	"bytes"
	stdhtml "html"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Raw HTML inside markdown is filtered to roughly what GitHub allows:
// formatting, tables, images, links, details. Scripts, styles, frames, forms
// and event-handler attributes go. A folder can opt out in config, for
// documents that embed their own widgets.

var allowedTags = map[string]bool{
	"a": true, "abbr": true, "b": true, "bdi": true, "bdo": true, "blockquote": true, "br": true,
	"caption": true, "center": true, "cite": true, "code": true, "col": true, "colgroup": true,
	"dd": true, "del": true, "details": true, "dfn": true, "div": true, "dl": true, "dt": true,
	"em": true, "figcaption": true, "figure": true, "h1": true, "h2": true, "h3": true, "h4": true,
	"h5": true, "h6": true, "hr": true, "i": true, "img": true, "ins": true, "kbd": true, "li": true,
	"mark": true, "ol": true, "p": true, "picture": true, "pre": true, "q": true, "rp": true, "rt": true,
	"ruby": true, "s": true, "samp": true, "small": true, "source": true, "span": true, "strike": true,
	"strong": true, "sub": true, "summary": true, "sup": true, "table": true, "tbody": true, "td": true,
	"tfoot": true, "th": true, "thead": true, "time": true, "tr": true, "tt": true, "u": true, "ul": true,
	"var": true, "video": true, "audio": true, "wbr": true, "track": true,
}

// droppedWithContent are elements removed together with everything in them.
var droppedWithContent = map[string]bool{
	"script": true, "style": true, "iframe": true, "object": true, "embed": true, "noscript": true,
	"template": true, "frame": true, "frameset": true, "applet": true, "title": true, "head": true,
	"svg": true, "math": true, "link": true, "meta": true, "base": true,
}

var allowedAttrs = map[string]bool{
	"class": true, "title": true, "lang": true, "dir": true, "align": true, "valign": true,
	"width": true, "height": true, "colspan": true, "rowspan": true, "start": true, "type": true,
	"open": true, "alt": true, "src": true, "srcset": true, "href": true, "name": true, "id": true,
	"border": true, "datetime": true, "cite": true, "controls": true, "media": true, "sizes": true,
	"loop": true, "muted": true, "poster": true, "reversed": true, "scope": true, "span": true,
	"abbr": true, "kind": true, "label": true, "srclang": true, "target": true, "rel": true,
}

var urlAttrs = map[string]bool{"href": true, "src": true, "srcset": true, "cite": true, "poster": true}

func dangerousURL(u string) bool {
	l := strings.ToLower(strings.TrimSpace(u))
	l = strings.Map(func(r rune) rune {
		if r < 0x20 {
			return -1
		}
		return r
	}, l)
	if strings.HasPrefix(l, "data:image/") {
		for _, ok := range []string{"png;", "gif;", "jpeg;", "jpg;", "webp;"} {
			if strings.HasPrefix(l[11:], ok) {
				return false
			}
		}
		return true
	}
	return strings.HasPrefix(l, "javascript:") || strings.HasPrefix(l, "vbscript:") || strings.HasPrefix(l, "data:")
}

// cleanTag rewrites one start or end tag, or returns "" to drop it.
func cleanTag(tt html.TokenType, t html.Token) string {
	name := t.Data
	if !allowedTags[name] {
		return ""
	}
	if tt == html.EndTagToken {
		return "</" + name + ">"
	}
	var b strings.Builder
	b.WriteString("<" + name)
	for _, a := range t.Attr {
		k := strings.ToLower(a.Key)
		if a.Namespace != "" || !allowedAttrs[k] {
			continue
		}
		if urlAttrs[k] && dangerousURL(a.Val) {
			continue
		}
		b.WriteString(" " + k + `="` + stdhtml.EscapeString(a.Val) + `"`)
	}
	if tt == html.SelfClosingTagToken {
		b.WriteString(" /")
	}
	b.WriteString(">")
	return b.String()
}

// htmlFragment renders a fragment of raw HTML from source[s:e], filtered
// unless unsafe, recording its text in lb (which may be nil). Text inside
// dropped elements is dropped too, so lb holds exactly the text the browser
// will show.
func htmlFragment(source []byte, s, e int, lb *leafBuilder, unsafe bool) string {
	var out strings.Builder
	z := html.NewTokenizer(bytes.NewReader(source[s:e]))
	pos := s
	skipDepth := 0
	skipName := ""
	afterPre := false
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		raw := z.Raw()
		start := pos
		pos += len(raw)
		switch tt {
		case html.TextToken:
			if skipDepth > 0 {
				continue
			}
			// The parser drops a newline straight after <pre>; the raw text
			// keeps it (so the browser can drop it) and the record skips it.
			ts := start
			if afterPre && len(raw) > 0 && raw[0] == '\n' {
				ts++
			}
			afterPre = false
			recordHTMLText(source, ts, start+len(raw), lb)
			out.Write(raw)
		case html.StartTagToken, html.SelfClosingTagToken, html.EndTagToken:
			tok := z.Token()
			name := tok.Data
			afterPre = false
			if skipDepth > 0 {
				if name == skipName {
					if tt == html.StartTagToken {
						skipDepth++
					} else if tt == html.EndTagToken {
						skipDepth--
					}
				}
				continue
			}
			if !unsafe && droppedWithContent[name] {
				if tt == html.StartTagToken && !isVoid(name) {
					skipDepth, skipName = 1, name
				}
				continue
			}
			if tt == html.StartTagToken && (name == "pre" || name == "textarea" || name == "listing") {
				afterPre = true
			}
			if unsafe {
				out.Write(raw)
			} else {
				out.WriteString(cleanTag(tt, tok))
			}
		case html.CommentToken, html.DoctypeToken:
			if unsafe {
				out.Write(raw)
			}
		}
	}
	return out.String()
}

// recordHTMLText records raw HTML text source[s:e] in lb, decoding character
// references and line endings the way an HTML parser does.
func recordHTMLText(source []byte, s, e int, lb *leafBuilder) {
	if lb == nil {
		return
	}
	lit := s
	flush := func(to int) {
		if to > lit {
			lb.literal(lit, to, string(source[lit:to]))
		}
	}
	for i := s; i < e; i++ {
		switch source[i] {
		case '\r':
			flush(i)
			if i+1 < e && source[i+1] == '\n' {
				lb.atomic(i, i+2, "\n")
				i++
			} else {
				lb.atomic(i, i+1, "\n")
			}
			lit = i + 1
		case '&':
			j := i + 1
			for j < e && j-i < 40 && (isLetter(source[j]) || isDigit(source[j]) || source[j] == '#') {
				j++
			}
			if j < e && source[j] == ';' {
				j++
			}
			cand := string(source[i:j])
			if dec := stdhtml.UnescapeString(cand); dec != cand {
				flush(i)
				lb.atomic(i, j, dec)
				lit = j
				i = j - 1
			}
		}
	}
	flush(e)
}

func isVoid(name string) bool {
	switch atom.Lookup([]byte(name)) {
	case atom.Area, atom.Base, atom.Br, atom.Col, atom.Embed, atom.Hr, atom.Img, atom.Input,
		atom.Link, atom.Meta, atom.Source, atom.Track, atom.Wbr:
		return true
	}
	return false
}

// balancedHTML reports whether every non-void element opened in the fragment
// is also closed in it. Only a balanced fragment can be wrapped in serve's own
// element without changing the document's structure.
func balancedHTML(fragment []byte) bool {
	z := html.NewTokenizer(bytes.NewReader(fragment))
	var stack []string
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			return len(stack) == 0
		case html.StartTagToken:
			name, _ := z.TagName()
			if !isVoid(string(name)) {
				stack = append(stack, string(name))
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			if len(stack) == 0 || stack[len(stack)-1] != string(name) {
				return false
			}
			stack = stack[:len(stack)-1]
		}
	}
}
