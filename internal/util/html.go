package util

import (
	"regexp"
	"strings"
)

var (
	htmlTagRegex    = regexp.MustCompile(`<[^>]*>`)
	whitespaceRegex = regexp.MustCompile(`\s+`)
)

var htmlEntities = map[string]string{
	"&nbsp;":   " ",
	"&amp;":    "&",
	"&lt;":     "<",
	"&gt;":     ">",
	"&quot;":   "\"",
	"&apos;":   "'",
	"&#39;":    "'",
	"&#x27;":   "'",
	"&hellip;": "…",
	"&mdash;":  "—",
	"&ndash;":  "–",
	"&laquo;":  "«",
	"&raquo;":  "»",
}

func decodeEntities(s string) string {
	for entity, replacement := range htmlEntities {
		s = strings.ReplaceAll(s, entity, replacement)
	}
	return s
}

// StripHTML removes all HTML tags, decodes entities, collapses whitespace.
func StripHTML(s string) string {
	s = htmlTagRegex.ReplaceAllString(s, " ")
	s = decodeEntities(s)
	s = whitespaceRegex.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// HTMLToText converts HTML to readable plain text, preserving block structure.
func HTMLToText(html string) string {
	html = strings.ReplaceAll(html, "<br>", "\n")
	html = strings.ReplaceAll(html, "<br/>", "\n")
	html = strings.ReplaceAll(html, "<br />", "\n")
	html = strings.ReplaceAll(html, "</p>", "\n\n")
	html = strings.ReplaceAll(html, "</div>", "\n")
	html = strings.ReplaceAll(html, "</li>", "\n")
	html = strings.ReplaceAll(html, "<li>", "  • ")

	text := htmlTagRegex.ReplaceAllString(html, "")
	text = decodeEntities(text)

	for strings.Contains(text, "\n\n\n") {
		text = strings.ReplaceAll(text, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(text)
}
