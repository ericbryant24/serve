package render

import (
	stdhtml "html"
	"regexp"
	"strings"
)

var frontmatterRe = regexp.MustCompile(`(?s)\A---[ \t]*\r?\n(.*?\r?\n)?---[ \t]*(\r?\n|\z)`)

// parseFrontmatter finds YAML frontmatter at the top of a markdown file and
// returns its top-level key/value pairs and the byte offset where it ends.
func parseFrontmatter(source string) ([][2]string, int) {
	loc := frontmatterRe.FindStringSubmatchIndex(source)
	if loc == nil {
		return nil, 0
	}
	var out [][2]string
	if loc[2] >= 0 {
		body := source[loc[2]:loc[3]]
		var key string
		var val strings.Builder
		flush := func() {
			if key != "" {
				out = append(out, [2]string{key, strings.TrimSpace(val.String())})
			}
			key = ""
			val.Reset()
		}
		for _, line := range strings.Split(body, "\n") {
			line = strings.TrimRight(line, "\r")
			if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			if line[0] != ' ' && line[0] != '\t' && line[0] != '-' && strings.Contains(line, ":") {
				flush()
				k, v, _ := strings.Cut(line, ":")
				key = strings.TrimSpace(k)
				val.WriteString(strings.Trim(strings.TrimSpace(v), `"'`))
				continue
			}
			if key != "" {
				if val.Len() > 0 {
					val.WriteString(" ")
				}
				val.WriteString(strings.TrimSpace(line))
			}
		}
		flush()
	}
	return out, loc[1]
}

func isMarp(fm [][2]string) bool {
	for _, kv := range fm {
		if strings.EqualFold(kv[0], "marp") {
			v := strings.ToLower(kv[1])
			return v == "true" || v == "yes" || v == "on" || v == "1"
		}
	}
	return false
}

func frontmatterHTML(fm [][2]string, key string) string {
	var b strings.Builder
	b.WriteString(`<details class="frontmatter" data-b="` + key + `"><summary>Frontmatter</summary><table>`)
	for _, kv := range fm {
		b.WriteString("<tr><th>" + stdhtml.EscapeString(kv[0]) + "</th><td>" + stdhtml.EscapeString(kv[1]) + "</td></tr>")
	}
	b.WriteString("</table></details>\n")
	return b.String()
}
