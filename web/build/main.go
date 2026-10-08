// Command build bundles the browser app into web/dist with esbuild's Go API,
// so building it needs Go and the npm packages in web/node_modules, but no
// Node. Run it with `go generate ./web` from the repository root.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/evanw/esbuild/pkg/api"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "build:", err)
		os.Exit(1)
	}
}

func run() error {
	if _, err := os.Stat("node_modules/preact"); err != nil {
		return fmt.Errorf("web/node_modules is missing; run `npm ci` in web/ first")
	}
	entries := []api.BuildOptions{
		{
			// The app, with the editor split into its own chunk that loads only
			// when someone presses Edit.
			EntryPoints:     []string{"src/app.tsx", "src/editor.ts"},
			Splitting:       true,
			Format:          api.FormatESModule,
			ChunkNames:      "chunk-[hash]",
			JSX:             api.JSXAutomatic,
			JSXImportSource: "preact",
		},
		{
			// The script that runs inside an HTML page's iframe: one plain file.
			EntryPoints: []string{"src/embed.ts"},
			Format:      api.FormatIIFE,
		},
		{
			// Document styles on their own, for `serve export --html`.
			EntryPoints: []string{"src/styles/doc.css"},
		},
	}
	if err := os.RemoveAll("dist"); err != nil {
		return err
	}
	for _, o := range entries {
		o.Bundle = true
		o.Outdir = "dist"
		o.Write = true
		o.MinifyWhitespace = true
		o.MinifySyntax = true
		o.MinifyIdentifiers = true
		o.Target = api.ES2020
		o.LogLevel = api.LogLevelWarning
		o.Loader = map[string]api.Loader{".svg": api.LoaderText}
		res := api.Build(o)
		if len(res.Errors) > 0 {
			return fmt.Errorf("%d errors", len(res.Errors))
		}
	}
	// Mermaid ships its own single-file build; it is loaded only on pages
	// with diagrams.
	return copyFile("node_modules/mermaid/dist/mermaid.min.js", filepath.Join("dist", "mermaid.min.js"))
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
