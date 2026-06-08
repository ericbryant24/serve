package main

import (
	"crypto/md5"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
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

//go:embed static/edit.css
var editCSS string

//go:embed static/edit.js
var editJS string

//go:embed static/page.gohtml
var pageTemplateSource string

var pageTemplate = template.Must(template.New("page").Parse(pageTemplateSource))

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
      if (data.type === 'reload') {
        if (window.__serveEditMode) {
          if (window.__serveOnReload) window.__serveOnReload();
        } else {
          softReload();
        }
      } else if (data.type === 'comments-updated') {
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

// ---------------------------------------------------------------------------
// Wrap options and page data
// ---------------------------------------------------------------------------

type wrapOptions struct {
	sidebar     *[2]string // [dirName, currentPath]
	fileTree    []FileNode
	faviconPath string
	fileMetaJS  string // JSON for window.__serveFile (current file name + dates)
	isMarp      bool
	extraCSS    string
	showEdit    bool
}

type pageData struct {
	Title          string
	FaviconLink    template.HTML
	ChromaCSS      template.CSS
	ExtraCSS       template.CSS
	CommentCSS     template.CSS
	VimCSS         template.CSS
	ZoomCSS        template.CSS
	SidebarCSS     template.CSS
	PresentCSS     template.CSS
	EditCSS        template.CSS
	ShowComments   bool
	ShowEdit       bool
	IsMarp         bool
	Sidebar        bool
	SidebarDirName string
	SidebarPath    template.JS
	FileTree       template.JS
	CurrentFile    template.JS
	Content        template.HTML
	ReloadScript   template.JS
	CommentJS      template.JS
	LinkScript     template.JS
	VimJS          template.JS
	ZoomJS         template.JS
	SidebarJS      template.JS
	PresentJS      template.JS
	EditJS         template.JS
}

func buildPageData(title string, opts wrapOptions, showComments bool) pageData {
	d := pageData{
		Title:        title,
		FaviconLink:  template.HTML(faviconLink(opts.faviconPath)),
		ChromaCSS:    template.CSS(chromaCSS),
		ExtraCSS:     template.CSS(opts.extraCSS),
		CommentCSS:   template.CSS(commentCSS),
		VimCSS:       template.CSS(vimCSS),
		ZoomCSS:      template.CSS(zoomCSS),
		ShowComments: showComments,
		IsMarp:       opts.isMarp,
		ReloadScript: template.JS(reloadScript),
	}
	if showComments {
		d.CommentJS  = template.JS(commentJS)
		d.LinkScript = template.JS(linkScript)
		d.VimJS      = template.JS(vimJS)
		d.ZoomJS     = template.JS(zoomJS)
	}
	if opts.isMarp {
		d.PresentCSS = template.CSS(presentCSS)
		d.PresentJS  = template.JS(presentJS)
	}
	if opts.showEdit {
		d.ShowEdit = true
		d.EditCSS  = template.CSS(editCSS)
		d.EditJS   = template.JS(editJS)
	}
	if opts.sidebar != nil {
		tree := opts.fileTree
		if tree == nil {
			tree = []FileNode{}
		}
		treeJSON, _ := json.Marshal(tree)
		d.Sidebar        = true
		d.SidebarDirName = opts.sidebar[0]
		d.SidebarPath    = template.JS(jsString(opts.sidebar[1]))
		d.FileTree       = template.JS(string(treeJSON))
		d.CurrentFile    = template.JS("null")
		if opts.fileMetaJS != "" {
			d.CurrentFile = template.JS(opts.fileMetaJS)
		}
		d.SidebarCSS     = template.CSS(sidebarCSS)
		d.SidebarJS      = template.JS(sidebarJS)
	}
	return d
}

func renderPage(data pageData) string {
	var b strings.Builder
	if err := pageTemplate.Execute(&b, data); err != nil {
		return "<!-- template error: " + htmlEscape(err.Error()) + " -->"
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Wrap functions — produce full HTML pages
// ---------------------------------------------------------------------------

func wrapMarkdown(title, content string, opts wrapOptions) string {
	data := buildPageData(title, opts, true)
	data.Content = template.HTML(`<div id="serve-content">` + content + `</div>`)
	return renderPage(data)
}

func wrapCode(title, highlightedHTML string, opts wrapOptions) string {
	data := buildPageData(title, opts, false)
	data.Content = template.HTML(`<div id="serve-content">` + highlightedHTML + `</div>`)
	return renderPage(data)
}

func wrapPlain(title, text string, opts wrapOptions) string {
	data := buildPageData(title, opts, false)
	data.Content = template.HTML(
		`<div id="serve-content"><pre style="white-space:pre-wrap;word-break:break-word;">` +
			htmlEscape(text) + `</pre></div>`,
	)
	return renderPage(data)
}

func wrapPDF(title, pdfURL string, opts wrapOptions) string {
	opts.extraCSS += "\n    body { margin: 0; padding: 0; }\n    body.has-sidebar { margin-left: 260px; }\n    body.sidebar-collapsed { margin-left: 0; }\n    embed { width: 100%; height: 100vh; border: none; }"
	data := buildPageData(title, opts, false)
	data.Content = template.HTML(fmt.Sprintf(`<embed src="/%s?raw=1" type="application/pdf">`, htmlEscape(pdfURL)))
	return renderPage(data)
}

func wrapImage(title, imageURL string, opts wrapOptions) string {
	opts.extraCSS += "\n    img.serve-image { max-width: 100%; height: auto; display: block; margin: 1em auto; border-radius: 4px; box-shadow: 0 2px 8px rgba(0,0,0,0.1); }"
	data := buildPageData(title, opts, false)
	data.Content = template.HTML(fmt.Sprintf(
		`<img class="serve-image" src="/%s?raw=1" alt="%s">`,
		htmlEscape(imageURL), htmlEscape(title),
	))
	return renderPage(data)
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
	data := buildPageData(title, opts, false)
	rawURL := "/" + htmlEscape(fileURL) + "?raw=1"
	data.Content = template.HTML(fmt.Sprintf(
		`<div class="file-info"><div class="icon">&#128196;</div><h2>%s</h2>`+
			`<div class="meta">%s &middot; %s</div>`+
			`<div class="actions">`+
			`<a class="btn-download" href="%s" download="%s">Download</a>`+
			`<a class="btn-open" href="%s" target="_blank">Open in new tab</a>`+
			`</div></div>`,
		htmlEscape(title), htmlEscape(ext), formatSize(size),
		rawURL, htmlEscape(title), rawURL,
	))
	return renderPage(data)
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
func injectReloadScript(html string, sidebar *[2]string, fileTree []FileNode, faviconPath, fileMetaJS string, annotate, bare bool) string {
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
			tree := fileTree
			if tree == nil {
				tree = []FileNode{}
			}
			treeJSON, _ := json.Marshal(tree)
			fileGlobal := "null"
			if fileMetaJS != "" {
				fileGlobal = fileMetaJS
			}
			scriptParts.WriteString(fmt.Sprintf(
				"<script>window.__servePath = %s; window.__serveFileTree = %s; window.__serveFile = %s;</script>\n",
				jsString(sidebar[1]), string(treeJSON), fileGlobal,
			))
			scriptParts.WriteString(
				`<nav id="serve-sidebar">` +
					`<div id="serve-sidebar-header"><span class="dir-name">` + htmlEscape(sidebar[0]) + `</span></div>` +
					`<div id="serve-sidebar-tree"></div>` +
					`</nav>` +
					`<button id="serve-sidebar-toggle">&lsaquo;</button>` + "\n",
			)
		}
		scriptParts.WriteString(
			`<button id="comment-btn" style="display:none">Comment</button>` + "\n" +
				`<div id="comment-badge" class="comment-count-badge" style="display:none"></div>` + "\n" +
				`<div id="comment-panel" class="comment-panel">` + "\n" +
				`  <div class="comment-panel-header"><h3>Comments</h3>` +
				`<button class="comment-panel-close" id="panel-close">&times;</button></div>` + "\n" +
				`  <div class="comment-panel-body" id="panel-body"></div>` + "\n" +
				`</div>` + "\n",
		)
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
