package cli

import (
	"encoding/base64"
	"fmt"
	stdhtml "html"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"serve/internal/render"
	"serve/web"
)

const exportUsage = `Usage: serve export <file> [--html] [--data-url] [-o FILE]

Without options, print every comment thread on the file as JSON, resolved
ones included, with full anchor data.

  --html       write the document as one standalone HTML file (styles and
               local images inlined) to -o, or to stdout
  --data-url   the same page as a data: URL, copied to the clipboard on
               macOS and printed otherwise`

func cmdExport(args []string) error {
	fs := flags("export", exportUsage)
	asHTML := fs.Bool("html", false, "")
	asURL := fs.Bool("data-url", false, "")
	out := fs.String("o", "", "")
	pos, err := parse(fs, exportUsage, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageError{exportUsage}
	}
	p, err := resolveFile(pos[0])
	if err != nil {
		return err
	}
	svc, done, err := openService()
	if err != nil {
		return err
	}
	defer done()
	if !*asHTML && !*asURL {
		dv, err := svc.Open(p, false)
		if err != nil {
			return err
		}
		threads := []any{}
		for _, v := range dv.Threads {
			threads = append(threads, v)
		}
		return printJSON(map[string]any{"file": p, "threads": threads})
	}
	page, err := standalonePage(svc.Read, p)
	if err != nil {
		return err
	}
	if *asURL {
		url := "data:text/html;base64," + base64.StdEncoding.EncodeToString([]byte(page))
		if runtime.GOOS == "darwin" {
			c := exec.Command("pbcopy")
			c.Stdin = strings.NewReader(url)
			if c.Run() == nil {
				fmt.Printf("Data URL copied to the clipboard (%d bytes)\n", len(url))
				return nil
			}
		}
		fmt.Println(url)
		return nil
	}
	if *out == "" {
		_, err := os.Stdout.WriteString(page)
		return err
	}
	if err := os.WriteFile(*out, []byte(page), 0o644); err != nil {
		return err
	}
	fmt.Println("Wrote", *out)
	return nil
}

var imgSrcRe = regexp.MustCompile(`(?i)(<img\s[^>]*?src=["'])([^"']+)(["'])`)

// standalonePage renders a document into one HTML file that needs nothing
// else: document styles, highlighting and local images are inlined.
func standalonePage(read func(string) (*render.Doc, error), p string) (string, error) {
	doc, err := read(p)
	if err != nil {
		return "", err
	}
	if doc.Kind == render.KindHTML {
		data, err := os.ReadFile(p)
		if err != nil {
			return "", err
		}
		return inlineImages(string(data), filepath.Dir(p)), nil
	}
	if doc.HTML == "" {
		return "", fmt.Errorf("%s cannot be exported as HTML", filepath.Base(p))
	}
	css, _ := web.Assets().Open("doc.css")
	title := doc.Title
	if title == "" {
		title = filepath.Base(p)
	}
	var b strings.Builder
	b.WriteString("<!doctype html>\n<html lang=\"en\"><head><meta charset=\"utf-8\">")
	b.WriteString(`<meta name="viewport" content="width=device-width, initial-scale=1">`)
	b.WriteString("<title>" + stdhtml.EscapeString(title) + "</title>\n<style>\n")
	b.Write(css)
	b.WriteString("\n" + render.ChromaCSS() + "\n</style>\n")
	if doc.HasMermaid {
		b.WriteString(`<script type="module">import mermaid from 'https://cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.esm.min.mjs';mermaid.initialize({startOnLoad:true});</script>` + "\n")
	}
	class := "markdown-body"
	if doc.Kind != render.KindMarkdown {
		class = "code-view"
	}
	b.WriteString("</head><body class=\"standalone\"><article class=\"" + class + "\">\n")
	b.WriteString(inlineImages(doc.HTML, filepath.Dir(p)))
	b.WriteString("</article></body></html>\n")
	return b.String(), nil
}

func inlineImages(h, base string) string {
	return imgSrcRe.ReplaceAllStringFunc(h, func(m string) string {
		sub := imgSrcRe.FindStringSubmatch(m)
		src := sub[2]
		if strings.Contains(src, "://") || strings.HasPrefix(src, "data:") || strings.HasPrefix(src, "//") {
			return m
		}
		ip := filepath.Join(base, filepath.FromSlash(src))
		rel, err := filepath.Rel(base, ip)
		if err != nil || strings.HasPrefix(rel, "..") {
			return m
		}
		data, err := os.ReadFile(ip)
		if err != nil {
			return m
		}
		mt := mime.TypeByExtension(strings.ToLower(filepath.Ext(ip)))
		if mt == "" {
			mt = "application/octet-stream"
		}
		return sub[1] + "data:" + mt + ";base64," + base64.StdEncoding.EncodeToString(data) + sub[3]
	})
}
