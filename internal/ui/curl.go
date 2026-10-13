package ui

import (
	"net/http"
	"strings"
)

// curlCommand renders a curl command line, quoted for a POSIX shell, that
// sends method, the headers in h and body to url. Headers that curl sets
// itself are left out. -g (globoff) keeps curl from treating [...] or
// {...} in the URL as its own glob syntax.
func curlCommand(method, url string, h http.Header, body []byte) string {
	var b strings.Builder
	b.WriteString("curl -g -X " + shellQuote(method) + " " + shellQuote(url))
	for _, l := range headerLines(h) {
		if curlSets[l.Name] {
			continue
		}
		b.WriteString(" \\\n  -H " + shellQuote(l.Name+": "+l.Value))
	}
	if len(body) > 0 {
		b.WriteString(" \\\n  --data-binary " + shellQuote(string(body)))
	}
	return b.String()
}

var curlSets = map[string]bool{"Content-Length": true, "Connection": true, "Transfer-Encoding": true}

// shellQuote quotes s for a POSIX shell: single quotes keep every byte
// literal, and a single quote inside is closed, escaped and reopened. A
// carriage return is never written as a literal byte: the command reaches
// the clipboard through a <pre> element's textContent, and an HTML parser
// turns a raw CR into a line feed on its way there. Instead, it is closed
// out to a command substitution that has printf produce the CR at
// shell-run time.
func shellQuote(s string) string {
	var b strings.Builder
	b.WriteByte('\'')
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\'':
			b.WriteString(`'"'"'`)
		case '\r':
			b.WriteString(`'"$(printf '\r')"'`)
		default:
			b.WriteByte(s[i])
		}
	}
	b.WriteByte('\'')
	return b.String()
}
