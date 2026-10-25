package web

import (
	"html"
	"html/template"
	"net/http"
	"strings"
)

// The server stores nothing about visitors and reads no cookie. Every text is sent in both
// English and German, each in a <span lang="..">, and the stylesheet shows only the spans of
// the page's language. The visitor's choice of language, like all other settings, lives in the
// browser (localStorage): static/boot.js sets the page's language before anything is drawn.

// browserLang is the page's language until the visitor picks one: German if the browser
// prefers it, else English. Only search engines and visitors without JavaScript keep it.
func browserLang(r *http.Request) string {
	if strings.HasPrefix(strings.ToLower(r.Header.Get("Accept-Language")), "de") {
		return "de"
	}
	return "en"
}

// both returns a text in English and German as HTML: two spans, of which the stylesheet shows
// the one in the page's language, or the text alone when both are the same. Both texts are
// escaped here, since the result is not escaped again.
func both(en, de string) template.HTML {
	if en == "" {
		en = de
	}
	if de == "" {
		de = en
	}
	if en == de {
		return template.HTML(html.EscapeString(en))
	}
	return template.HTML(`<span lang="en">` + html.EscapeString(en) + `</span><span lang="de">` + html.EscapeString(de) + `</span>`)
}

// tr is an interface text in both languages, formatted with args like fmt.Sprintf.
func tr(key string, args ...any) template.HTML {
	return both(translate("en", key, args...), translate("de", key, args...))
}
