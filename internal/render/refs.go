package render

import (
	"strings"

	"golang.org/x/net/html"
)

// Refs lists the URLs a page loads as it is shown, as written: src, srcset
// and poster attributes, <link href>, and SVG <image href> and <use href>.
// Links a reader follows (<a href>) are not loads.
func Refs(page string) []string {
	z := html.NewTokenizer(strings.NewReader(page))
	seen := map[string]bool{}
	var out []string
	add := func(u string) {
		u = strings.TrimSpace(u)
		if u != "" && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return out
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		name, more := z.TagName()
		tag := string(name)
		for more {
			var k, v []byte
			k, v, more = z.TagAttr()
			switch string(k) {
			case "src", "poster":
				add(string(v))
			case "srcset":
				for _, c := range strings.Split(string(v), ",") {
					if f := strings.Fields(c); len(f) > 0 {
						add(f[0])
					}
				}
			case "href", "xlink:href":
				if tag == "link" || tag == "image" || tag == "use" {
					add(string(v))
				}
			}
		}
	}
}
