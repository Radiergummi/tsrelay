package ui

import (
	"net/http"
	"strings"
)

// curlCommand renders a curl command line, quoted for a POSIX shell, that
// sends method, the headers in h and body to url. Headers that curl sets
// itself are left out.
func curlCommand(method, url string, h http.Header, body []byte) string {
	var b strings.Builder
	b.WriteString("curl -X " + shellQuote(method) + " " + shellQuote(url))
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
// literal, and a single quote inside is closed, escaped and reopened.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
