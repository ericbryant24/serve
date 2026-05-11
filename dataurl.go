package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	imgSrcRe      = regexp.MustCompile(`(?i)(<img\s[^>]*?src=["'])([^"']+)(["'])`)
	reloadScriptRe = regexp.MustCompile(`(?s)<script>\(function\(\)\s*\{\s*function connect\(\).*?</script>`)
)

func inlineImages(htmlStr, baseDir string) string {
	return imgSrcRe.ReplaceAllStringFunc(htmlStr, func(match string) string {
		sub := imgSrcRe.FindStringSubmatch(match)
		if len(sub) < 4 {
			return match
		}
		prefix, src, suffix := sub[1], sub[2], sub[3]
		if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") ||
			strings.HasPrefix(src, "data:") || strings.HasPrefix(src, "//") {
			return match
		}
		imgPath := filepath.Join(baseDir, src)
		imgPath, _ = filepath.Abs(imgPath)
		data, err := os.ReadFile(imgPath)
		if err != nil {
			return match
		}
		mimeType := mimeTypeFor(imgPath)
		encoded := base64.StdEncoding.EncodeToString(data)
		return prefix + "data:" + mimeType + ";base64," + encoded + suffix
	})
}

func generateDataURL(filePath, mode string) (string, error) {
	filePath, _ = filepath.Abs(filePath)
	baseDir := filepath.Dir(filePath)

	var htmlStr string
	if mode == "markdown" {
		rendered, err := renderMarkdown(filePath, wrapOptions{faviconPath: filePath})
		if err != nil {
			return "", err
		}
		htmlStr = rendered
	} else {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return "", err
		}
		htmlStr = string(data)
	}

	// Strip live-reload WebSocket script
	htmlStr = reloadScriptRe.ReplaceAllString(htmlStr, "")

	htmlStr = inlineImages(htmlStr, baseDir)

	encoded := base64.StdEncoding.EncodeToString([]byte(htmlStr))
	return "data:text/html;base64," + encoded, nil
}

func mimeTypeFor(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".svg":
		return "image/svg+xml"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".ico":
		return "image/x-icon"
	default:
		return "application/octet-stream"
	}
}
