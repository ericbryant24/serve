// Package images keeps the images pasted into comments, in ~/.serve/images.
// A file is named for a hash of its bytes, so pasting the same image twice
// keeps one copy. A message refers to an image by its URL path
// (/_serve/images/<name>) in a markdown image, and Refs turns those back into
// files for agents, who read the message text from the CLI.
package images

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"serve/internal/paths"
)

// URLPrefix is where the server serves images.
const URLPrefix = "/_serve/images/"

// MaxSize is the largest image accepted.
const MaxSize = 10 << 20

// The types a browser shows in an <img> and none of which can run script
// (no SVG), keyed by the type the bytes sniff as.
var exts = map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp"}

var nameRe = regexp.MustCompile(`^[0-9a-f]{32}\.(png|jpg|gif|webp)$`)
var refRe = regexp.MustCompile(regexp.QuoteMeta(URLPrefix) + `([0-9a-f]{32}\.(?:png|jpg|gif|webp))`)

// ErrType is returned for bytes that are not a PNG, JPEG, GIF or WebP image.
var ErrType = errors.New("only PNG, JPEG, GIF and WebP images can be pasted")

// ErrSize is returned for an image over MaxSize.
var ErrSize = errors.New("the image is larger than 10 MB")

// Dir is ~/.serve/images.
func Dir() string { return filepath.Join(paths.StateDir(), "images") }

// Save stores an image and returns its name. Mode 0700/0600: a pasted image
// may be a screenshot of something private.
func Save(data []byte) (string, error) {
	if len(data) > MaxSize {
		return "", ErrSize
	}
	ext, ok := exts[http.DetectContentType(data)]
	if !ok {
		return "", ErrType
	}
	sum := sha256.Sum256(data)
	name := hex.EncodeToString(sum[:16]) + ext
	dir := Dir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	p := filepath.Join(dir, name)
	if _, err := os.Stat(p); err == nil {
		return name, nil
	}
	tmp, err := os.CreateTemp(dir, ".paste-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return "", err
	}
	return name, os.Rename(tmp.Name(), p)
}

// Path is the file for an image name, or false for a name Save would never
// have made (which keeps a request path from reaching outside Dir).
func Path(name string) (string, bool) {
	if !nameRe.MatchString(name) {
		return "", false
	}
	return filepath.Join(Dir(), name), true
}

// Refs lists the files of the images a message's text refers to, in order,
// each once.
func Refs(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range refRe.FindAllStringSubmatch(text, -1) {
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		p, _ := Path(m[1])
		out = append(out, p)
	}
	return out
}
