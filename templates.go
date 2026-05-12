package main

import (
	"crypto/md5"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed static/comment.css
var commentCSS string

//go:embed static/comment.js
var commentJS string

//go:embed static/vim.css
var vimCSS string

//go:embed static/vim.js
var vimJS string

//go:embed static/zoom.css
var zoomCSS string

//go:embed static/zoom.js
var zoomJS string

//go:embed static/present.css
var presentCSS string

//go:embed static/present.js
var presentJS string

//go:embed static/sidebar.css
var sidebarCSS string

//go:embed static/sidebar.js
var sidebarJS string

// ---------------------------------------------------------------------------
// Favicon generation
// ---------------------------------------------------------------------------

var faviconEmojis = []string{
	"📘", "📕", "📗", "📙", "📓",
	"📝", "📋", "📄", "📃", "📜",
	"🗞", "📰", "📚", "📖", "📔",
	"🔬", "🧪", "🧬", "🔭", "💡",
	"🎨", "🌈", "🔥", "💧", "🌱",
	"🚀", "🛸", "🌍", "🌊", "🌋",
	"🎲", "🎯", "🎰", "🎳", "🎮",
	"🦁", "🦅", "🦉", "🐙", "🦋",
}

var faviconColors = []string{
	"#264653", "#2a9d8f", "#e9c46a", "#f4a261", "#e76f51",
	"#606c38", "#283618", "#dda15e", "#bc6c25", "#6d6875",
	"#b5838d", "#e5989b", "#ffb4a2", "#457b9d", "#1d3557",
	"#a8dadc", "#2b2d42", "#8d99ae", "#ef233c", "#d90429",
}

func faviconForPath(path string) (string, string) {
	h := md5.Sum([]byte(path))
	n := 0
	for _, b := range h {
		n = n*256 + int(b)
	}
	if n < 0 {
		n = -n
	}
	emoji := faviconEmojis[n%len(faviconEmojis)]
	color := faviconColors[(n>>8)%len(faviconColors)]
	return emoji, color
}

func faviconLink(path string) string {
	emoji, bg := faviconForPath(path)
	svg := fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><rect width="100" height="100" rx="20" fill="%s"/><text x="50" y="72" font-size="60" text-anchor="middle">%s</text></svg>`,
		bg, emoji,
	)
	b64 := base64.StdEncoding.EncodeToString([]byte(svg))
	return fmt.Sprintf("  <link rel=\"icon\" href=\"data:image/svg+xml;base64,%s\">\n", b64)
}

// ---------------------------------------------------------------------------
// Link behaviour script
// ---------------------------------------------------------------------------

const linkScript = `(function() {
  document.querySelectorAll('#serve-content a[href]').forEach(function(a) {
    var href = a.getAttribute('href');
    if (href && (href.startsWith('http://') || href.startsWith('https://'))) {
      a.setAttribute('target', '_blank');
      a.setAttribute('rel', 'noopener noreferrer');
    }
  });
})();`

// ---------------------------------------------------------------------------
// Reload script (soft reload — no full-page flash)
// ---------------------------------------------------------------------------

const reloadScriptTag = `<script id="serve-reload-script">` + reloadScript + `</script>`

const reloadScript = `(function() {
  function connect() {
    var ws = new WebSocket('ws://' + location.host + '/ws');
    ws.onmessage = function(event) {
      var data = JSON.parse(event.data);
      if (data.type === 'reload') { softReload(); }
      else if (data.type === 'comments-updated') {
        softReload();
      } else if (data.type === 'filetree') {
        if (window.__updateSidebarTree) window.__updateSidebarTree(data.files);
      }
    };
    ws.onclose = function() { setTimeout(connect, 1000); };
  }
  function softReload() {
    var sc = document.getElementById('serve-content');
    if (!sc) { location.reload(); return; }
    var scrollY = window.scrollY;
    fetch(location.href, {cache: 'no-store'})
      .then(function(r) { return r.text(); })
      .then(function(html) {
        try {
          var doc = new DOMParser().parseFromString(html, 'text/html');
          var nc = doc.getElementById('serve-content');
          if (!nc) { location.reload(); return; }
          sc.innerHTML = nc.innerHTML;
          window.scrollTo(0, scrollY);
          if (window.mermaid) {
            var mels = sc.querySelectorAll('pre.mermaid:not([data-processed])');
            if (mels.length) mermaid.run({nodes: Array.from(mels)});
          }
          if (window.__refreshComments) window.__refreshComments();
        } catch(e) { location.reload(); }
      })
      .catch(function() { location.reload(); });
  }
  connect();
})();`

// ---------------------------------------------------------------------------
// Comment CSS / JS / HTML
// ---------------------------------------------------------------------------

const commentHTML = `<button id="comment-btn" style="display:none">Comment</button>
<div id="comment-badge" class="comment-count-badge" style="display:none"></div>
<div id="comment-panel" class="comment-panel">
  <div class="comment-panel-header">
    <h3>Comments</h3>
    <button class="comment-panel-close" id="panel-close">&times;</button>
  </div>
  <div class="comment-panel-body" id="panel-body"></div>
</div>`

// ---------------------------------------------------------------------------
// HTML templates
// ---------------------------------------------------------------------------

const headTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>%s</title>
%s
  <style>
    body {
      max-width: 48em;
      margin: 2em auto;
      padding: 0 1em;
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif;
      line-height: 1.6;
      color: #24292e;
      background: #fff;
    }
    h1, h2, h3, h4, h5, h6 { margin-top: 1.5em; margin-bottom: 0.5em; font-weight: 600; line-height: 1.25; }
    h1 { font-size: 2em; border-bottom: 1px solid #eaecef; padding-bottom: 0.3em; }
    h2 { font-size: 1.5em; border-bottom: 1px solid #eaecef; padding-bottom: 0.3em; }
    a { color: #0366d6; text-decoration: none; }
    a:hover { text-decoration: underline; }
    pre { background: #f6f8fa; padding: 1em; overflow-x: auto; border-radius: 6px; line-height: 1.45; }
    code { font-family: "SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace; font-size: 0.875em; }
    :not(pre) > code { background: #f6f8fa; padding: 0.2em 0.4em; border-radius: 3px; }
    img { max-width: 100%%; height: auto; }
    table { border-collapse: collapse; width: 100%%; margin: 1em 0; }
    th, td { border: 1px solid #dfe2e5; padding: 0.5em 0.75em; text-align: left; }
    th { background: #f6f8fa; font-weight: 600; }
    tr:nth-child(2n) { background: #f6f8fa; }
    blockquote { border-left: 4px solid #dfe2e5; margin: 0; padding: 0 1em; color: #6a737d; }
    hr { border: none; border-top: 1px solid #eaecef; margin: 1.5em 0; }
    .highlight { background: #f6f8fa; border-radius: 6px; }
    .highlight pre { background: transparent; margin: 0; }
    %s
  </style>
`

const headClose = `  <script type="module">
    import mermaid from 'https://cdn.jsdelivr.net/npm/mermaid@10/dist/mermaid.esm.min.mjs';
    window.mermaid = mermaid;
    mermaid.initialize({ startOnLoad: false, theme: 'default' });
    mermaid.run({ querySelector: 'pre.mermaid' });
  </script>
</head>
<body>
`

const bodyClose = `</body>
</html>`

// ---------------------------------------------------------------------------
// HTML escaping helpers
// ---------------------------------------------------------------------------

func htmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
}

func jsString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return `"` + s + `"`
}

// ---------------------------------------------------------------------------
// FileNode for sidebar tree
// ---------------------------------------------------------------------------

type FileNode struct {
	Name     string     `json:"name"`
	Path     string     `json:"path"`
	Type     string     `json:"type"` // "file" or "dir"
	Children []FileNode `json:"children,omitempty"`
}

func sidebarHTML(dirName, currentPath string, fileTree []FileNode) string {
	treeJSON, _ := json.Marshal(fileTree)
	return fmt.Sprintf(
		"<script>window.__servePath = %s; window.__serveFileTree = %s;</script>\n"+
			`<nav id="serve-sidebar">`+
			`<div id="serve-sidebar-header"><span class="dir-name">%s</span></div>`+
			`<div id="serve-sidebar-tree"></div>`+
			`</nav>`+
			`<button id="serve-sidebar-toggle">&lsaquo;</button>`+"\n",
		jsString(currentPath), string(treeJSON), htmlEscape(dirName),
	)
}

// ---------------------------------------------------------------------------
// Wrap functions — produce full HTML pages
// ---------------------------------------------------------------------------

type wrapOptions struct {
	sidebar     *[2]string // [dirName, currentPath]
	fileTree    []FileNode
	faviconPath string
	isMarp      bool
	extraCSS    string
}

func buildHead(title string, opts wrapOptions) string {
	var extra strings.Builder
	extra.WriteString(commentCSS)
	extra.WriteString(vimCSS)
	extra.WriteString(zoomCSS)
	if opts.isMarp {
		extra.WriteString(presentCSS)
	}
	if opts.sidebar != nil {
		extra.WriteString(sidebarCSS)
	}
	fav := faviconLink(opts.faviconPath)
	return fmt.Sprintf(headTemplate, htmlEscape(title), fav, chromaCSS+opts.extraCSS+extra.String())
}

func buildScripts(opts wrapOptions) string {
	var b strings.Builder
	b.WriteString(reloadScriptTag + "\n")
	b.WriteString("<script>" + commentJS + "</script>\n")
	b.WriteString("<script>" + linkScript + "</script>\n")
	b.WriteString("<script>" + vimJS + "</script>\n")
	b.WriteString("<script>" + zoomJS + "</script>\n")
	if opts.isMarp {
		b.WriteString("<script>" + presentJS + "</script>\n")
	}
	if opts.sidebar != nil {
		b.WriteString("<script>" + sidebarJS + "</script>\n")
	}
	return b.String()
}

func wrapMarkdown(title, content string, opts wrapOptions) string {
	var b strings.Builder
	b.WriteString(buildHead(title, opts))
	b.WriteString(headClose)
	if opts.sidebar != nil {
		b.WriteString(sidebarHTML(opts.sidebar[0], opts.sidebar[1], opts.fileTree))
	}
	b.WriteString(`<div id="serve-content">`)
	b.WriteString(content)
	b.WriteString("</div>\n")
	b.WriteString(commentHTML + "\n")
	b.WriteString(buildScripts(opts))
	b.WriteString(bodyClose)
	return b.String()
}

func wrapCode(title, highlightedHTML string, opts wrapOptions) string {
	var b strings.Builder
	b.WriteString(buildHead(title, opts))
	b.WriteString(headClose)
	if opts.sidebar != nil {
		b.WriteString(sidebarHTML(opts.sidebar[0], opts.sidebar[1], opts.fileTree))
	}
	b.WriteString(`<div id="serve-content">`)
	b.WriteString(highlightedHTML)
	b.WriteString("</div>\n")
	b.WriteString(reloadScriptTag + "\n")
	if opts.sidebar != nil {
		b.WriteString("<script>" + sidebarJS + "</script>\n")
	}
	b.WriteString(bodyClose)
	return b.String()
}

func wrapPlain(title, text string, opts wrapOptions) string {
	var b strings.Builder
	b.WriteString(buildHead(title, opts))
	b.WriteString(headClose)
	if opts.sidebar != nil {
		b.WriteString(sidebarHTML(opts.sidebar[0], opts.sidebar[1], opts.fileTree))
	}
	b.WriteString(`<div id="serve-content">`)
	b.WriteString(`<pre style="white-space:pre-wrap;word-break:break-word;">`)
	b.WriteString(htmlEscape(text))
	b.WriteString("</pre></div>\n")
	b.WriteString(reloadScriptTag + "\n")
	if opts.sidebar != nil {
		b.WriteString("<script>" + sidebarJS + "</script>\n")
	}
	b.WriteString(bodyClose)
	return b.String()
}

func wrapPDF(title, pdfURL string, opts wrapOptions) string {
	opts.extraCSS += "\n    body { margin: 0; padding: 0; }\n    body.has-sidebar { margin-left: 260px; }\n    body.sidebar-collapsed { margin-left: 0; }\n    embed { width: 100%; height: 100vh; border: none; }"
	var b strings.Builder
	b.WriteString(buildHead(title, opts))
	b.WriteString(headClose)
	if opts.sidebar != nil {
		b.WriteString(sidebarHTML(opts.sidebar[0], opts.sidebar[1], opts.fileTree))
	}
	b.WriteString(fmt.Sprintf(`<embed src="/%s?raw=1" type="application/pdf">`, htmlEscape(pdfURL)))
	b.WriteString("\n" + reloadScriptTag + "\n")
	if opts.sidebar != nil {
		b.WriteString("<script>" + sidebarJS + "</script>\n")
	}
	b.WriteString(bodyClose)
	return b.String()
}

func wrapImage(title, imageURL string, opts wrapOptions) string {
	opts.extraCSS += "\n    img.serve-image { max-width: 100%; height: auto; display: block; margin: 1em auto; border-radius: 4px; box-shadow: 0 2px 8px rgba(0,0,0,0.1); }"
	var b strings.Builder
	b.WriteString(buildHead(title, opts))
	b.WriteString(headClose)
	if opts.sidebar != nil {
		b.WriteString(sidebarHTML(opts.sidebar[0], opts.sidebar[1], opts.fileTree))
	}
	b.WriteString(fmt.Sprintf(`<img class="serve-image" src="/%s?raw=1" alt="%s">`, htmlEscape(imageURL), htmlEscape(title)))
	b.WriteString("\n" + reloadScriptTag + "\n")
	if opts.sidebar != nil {
		b.WriteString("<script>" + sidebarJS + "</script>\n")
	}
	b.WriteString(bodyClose)
	return b.String()
}

func formatSize(size int64) string {
	units := []string{"B", "KB", "MB", "GB"}
	f := float64(size)
	for _, u := range units {
		if f < 1024 {
			if u == "B" {
				return fmt.Sprintf("%.0f %s", f, u)
			}
			return fmt.Sprintf("%.1f %s", f, u)
		}
		f /= 1024
	}
	return fmt.Sprintf("%.1f TB", f)
}

func wrapFileInfo(title, fileURL string, size int64, opts wrapOptions) string {
	ext := "FILE"
	if idx := strings.LastIndex(title, "."); idx >= 0 {
		ext = strings.ToUpper(title[idx+1:])
	}
	opts.extraCSS += "\n    .file-info { text-align: center; padding: 4em 2em; }" +
		"\n    .file-info .icon { font-size: 64px; margin-bottom: 0.5em; }" +
		"\n    .file-info h2 { border-bottom: none; margin-bottom: 0.25em; }" +
		"\n    .file-info .meta { color: #656d76; font-size: 14px; margin-bottom: 1.5em; }" +
		"\n    .file-info .actions { display: flex; gap: 12px; justify-content: center; }" +
		"\n    .file-info .actions a { display: inline-block; padding: 8px 20px; border-radius: 6px; font-size: 14px; font-weight: 500; text-decoration: none; }" +
		"\n    .file-info .btn-download { background: #0078d4; color: #fff; }" +
		"\n    .file-info .btn-download:hover { background: #106ebe; text-decoration: none; }" +
		"\n    .file-info .btn-open { background: #f0f0f0; color: #24292e; }" +
		"\n    .file-info .btn-open:hover { background: #e0e0e0; text-decoration: none; }"
	var b strings.Builder
	b.WriteString(buildHead(title, opts))
	b.WriteString(headClose)
	if opts.sidebar != nil {
		b.WriteString(sidebarHTML(opts.sidebar[0], opts.sidebar[1], opts.fileTree))
	}
	rawURL := "/" + htmlEscape(fileURL) + "?raw=1"
	b.WriteString(fmt.Sprintf(
		`<div class="file-info"><div class="icon">&#128196;</div><h2>%s</h2>`+
			`<div class="meta">%s &middot; %s</div>`+
			`<div class="actions">`+
			`<a class="btn-download" href="%s" download="%s">Download</a>`+
			`<a class="btn-open" href="%s" target="_blank">Open in new tab</a>`+
			`</div></div>`,
		htmlEscape(title), htmlEscape(ext), formatSize(size),
		rawURL, htmlEscape(title), rawURL,
	))
	b.WriteString("\n" + reloadScriptTag + "\n")
	if opts.sidebar != nil {
		b.WriteString("<script>" + sidebarJS + "</script>\n")
	}
	b.WriteString(bodyClose)
	return b.String()
}

// ---------------------------------------------------------------------------
// _annotate_html_source_lines equivalent — used for raw HTML files
// ---------------------------------------------------------------------------

// findTagEnd returns the index past the closing '>' of the tag starting at
// html[pos] (where html[pos] == '<'). It skips over quoted attribute values
// so that '>' inside an attribute does not prematurely end the tag.
// Returns -1 if the tag is unterminated.
func findTagEnd(html string, pos int) int {
	i := pos + 1
	n := len(html)
	for i < n {
		switch html[i] {
		case '>':
			return i + 1
		case '"', '\'':
			q := html[i]
			i++
			for i < n && html[i] != q {
				i++
			}
		}
		i++
	}
	return -1
}

// annotateHTMLSourceLines adds data-source-lines attributes to block-level
// elements in a raw HTML string. It handles quoted attribute values (so '>'
// inside an attribute does not corrupt the parse), HTML comments, and
// script/style content containing '<'. CDATA sections and processing
// instructions are passed through unchanged.
func annotateHTMLSourceLines(html string) string {
	blockTags := map[string]bool{
		"p": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
		"div": true, "section": true, "article": true, "header": true, "footer": true,
		"nav": true, "aside": true, "main": true,
		"table": true, "thead": true, "tbody": true, "tfoot": true, "tr": true, "td": true, "th": true,
		"ul": true, "ol": true, "li": true, "dl": true, "dt": true, "dd": true,
		"blockquote": true, "pre": true, "figure": true, "figcaption": true,
		"details": true, "summary": true, "form": true, "fieldset": true,
	}

	// Build newline offset table for O(log n) line-number lookup.
	newlineOffsets := []int{-1}
	for i, ch := range html {
		if ch == '\n' {
			newlineOffsets = append(newlineOffsets, i)
		}
	}
	offsetToLine := func(offset int) int {
		lo, hi := 0, len(newlineOffsets)-1
		for lo <= hi {
			mid := (lo + hi) / 2
			if newlineOffsets[mid] < offset {
				lo = mid + 1
			} else {
				hi = mid - 1
			}
		}
		return lo
	}

	var result strings.Builder
	i := 0
	n := len(html)

	for i < n {
		if html[i] != '<' {
			result.WriteByte(html[i])
			i++
			continue
		}

		// HTML comment — pass through verbatim; '>' inside comments must not
		// be treated as a tag end.
		if strings.HasPrefix(html[i:], "<!--") {
			end := strings.Index(html[i+4:], "-->")
			if end < 0 {
				result.WriteString(html[i:])
				break
			}
			result.WriteString(html[i : i+4+end+3])
			i += 4 + end + 3
			continue
		}

		// Find the true end of this tag, skipping quoted attribute values.
		end := findTagEnd(html, i)
		if end < 0 {
			result.WriteByte(html[i])
			i++
			continue
		}

		tagContent := html[i+1 : end-1]
		isClose := strings.HasPrefix(tagContent, "/")
		if isClose {
			tagContent = tagContent[1:]
		}

		// Extract tag name (always ASCII, so byte indexing is safe).
		tagName := tagContent
		for j, ch := range tagContent {
			if ch == ' ' || ch == '\t' || ch == '\n' || ch == '/' {
				tagName = tagContent[:j]
				break
			}
		}
		tagNameLower := strings.ToLower(tagName)

		// Script and style: write the opening tag, then copy everything up to
		// the matching closing tag verbatim so that '<' in JS/CSS is not
		// misread as a new tag.
		if !isClose && (tagNameLower == "script" || tagNameLower == "style") {
			result.WriteString(html[i:end])
			i = end
			closeTag := "</" + tagNameLower + ">"
			closeIdx := strings.Index(strings.ToLower(html[i:]), closeTag)
			if closeIdx < 0 {
				result.WriteString(html[i:])
				i = n
			} else {
				result.WriteString(html[i : i+closeIdx+len(closeTag)])
				i += closeIdx + len(closeTag)
			}
			continue
		}

		// Self-closing tags (e.g. <br/>, <img ... />) — do not annotate.
		trimmed := strings.TrimRight(tagContent, " \t\n\r")
		isSelfClose := len(trimmed) > 0 && trimmed[len(trimmed)-1] == '/'

		if isClose || isSelfClose || !blockTags[tagNameLower] || strings.Contains(tagContent, "data-source-lines") {
			result.WriteString(html[i:end])
			i = end
			continue
		}

		lineNum := offsetToLine(i)
		result.WriteString(html[i : end-1])
		result.WriteString(fmt.Sprintf(` data-source-lines="%d-%d">`, lineNum, lineNum))
		i = end
	}
	return result.String()
}

// injectReloadScript injects scripts into an existing HTML document.
func injectReloadScript(html string, sidebar *[2]string, fileTree []FileNode, faviconPath string, annotate, bare bool) string {
	if annotate {
		html = annotateHTMLSourceLines(html)
	}

	favTag := faviconLink(faviconPath)

	var cssTag, scripts string
	if bare {
		scripts = reloadScriptTag
	} else {
		var cssParts strings.Builder
		cssParts.WriteString(commentCSS)
		cssParts.WriteString(vimCSS)
		if sidebar != nil {
			cssParts.WriteString(sidebarCSS)
		}
		cssTag = "<style>" + cssParts.String() + "</style>"

		var scriptParts strings.Builder
		if sidebar != nil {
			scriptParts.WriteString(sidebarHTML(sidebar[0], sidebar[1], fileTree))
		}
		scriptParts.WriteString(commentHTML + "\n")
		scriptParts.WriteString(reloadScriptTag + "\n")
		scriptParts.WriteString("<script>" + commentJS + "</script>\n")
		scriptParts.WriteString("<script>" + linkScript + "</script>\n")
		scriptParts.WriteString("<script>" + vimJS + "</script>\n")
		if sidebar != nil {
			scriptParts.WriteString("<script>" + sidebarJS + "</script>\n")
		}
		scripts = scriptParts.String()
	}

	if strings.Contains(html, "</head>") {
		html = strings.Replace(html, "</head>", favTag+"</head>", 1)
	}

	inject := cssTag + "\n" + scripts + "\n"
	if strings.Contains(html, "</body>") {
		html = strings.Replace(html, "</body>", inject+"</body>", 1)
	} else if strings.Contains(html, "</html>") {
		html = strings.Replace(html, "</html>", inject+"</html>", 1)
	} else {
		html = html + "\n" + inject
	}
	return html
}
