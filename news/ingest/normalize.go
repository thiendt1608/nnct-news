// Package ingest fetches feeds and normalizes items. It is pure: no DB access.
package ingest

import (
	"crypto/sha1"
	"encoding/hex"
	"html"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// Item is one normalized feed entry.
type Item struct {
	SourceID     string
	URLCanonical string
	Title        string
	Snippet      string
	Lang         string
	PublishedAt  time.Time
	Engagement   map[string]int // e.g. {"points": 321, "comments": 80}
	TitleHash    string
}

var trackingParams = map[string]bool{"fbclid": true, "gclid": true, "mc_cid": true, "mc_eid": true, "ref": true, "ref_src": true, "igshid": true, "amp": true}

// CanonicalURL strips tracking params, fragments, amp/mobile markers and trailing slashes.
func CanonicalURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme == "http" {
		u.Scheme = "https"
	}
	host := strings.ToLower(u.Hostname())
	for _, p := range []string{"www.", "m.", "amp."} {
		host = strings.TrimPrefix(host, p)
	}
	u.Host = host
	u.Fragment = ""
	q := u.Query()
	for k := range q {
		if strings.HasPrefix(strings.ToLower(k), "utm_") || trackingParams[strings.ToLower(k)] {
			q.Del(k)
		}
	}
	u.RawQuery = q.Encode()
	p := u.Path
	p = strings.TrimSuffix(p, "/amp")
	p = strings.TrimSuffix(p, ".amp")
	if strings.HasSuffix(p, "/amp/") {
		p = strings.TrimSuffix(p, "/amp/")
	}
	if len(p) > 1 {
		p = strings.TrimRight(p, "/")
	}
	u.Path = p
	u.RawPath = ""
	return u.String(), nil
}

// TitleHash hashes a title after lowercasing and dropping punctuation/whitespace runs.
func TitleHash(title string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(title) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteRune(r)
			space = false
		} else {
			space = true
		}
	}
	sum := sha1.Sum([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

var tagRe = regexp.MustCompile(`(?s)<[^>]*>`)
var wsRe = regexp.MustCompile(`\s+`)

// CleanSnippet strips HTML, unescapes entities and truncates to max runes.
func CleanSnippet(s string, max int) string {
	s = tagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = strings.TrimSpace(wsRe.ReplaceAllString(s, " "))
	r := []rune(s)
	if len(r) > max {
		s = string(r[:max])
	}
	return s
}

// Build fills canonical URL/hash/snippet; returns ok=false for unusable entries.
func Build(src, lang, rawURL, title, snippet string, pub time.Time) (Item, bool) {
	title = strings.TrimSpace(html.UnescapeString(title))
	if title == "" || rawURL == "" {
		return Item{}, false
	}
	c, err := CanonicalURL(rawURL)
	if err != nil || c == "" {
		return Item{}, false
	}
	return Item{
		SourceID: src, URLCanonical: c, Title: title,
		Snippet: CleanSnippet(snippet, 600), Lang: lang,
		PublishedAt: pub, TitleHash: TitleHash(title),
	}, true
}

// Fresh reports whether the item is within maxAge of now. Unknown dates count as fresh.
func Fresh(it Item, now time.Time, maxAge time.Duration) bool {
	return it.PublishedAt.IsZero() || now.Sub(it.PublishedAt) <= maxAge
}

// Dedupe removes entries with duplicate canonical URL or title hash, keeping the first.
func Dedupe(items []Item) []Item {
	urls, hashes := map[string]bool{}, map[string]bool{}
	out := items[:0:0]
	for _, it := range items {
		if urls[it.URLCanonical] || hashes[it.TitleHash] {
			continue
		}
		urls[it.URLCanonical], hashes[it.TitleHash] = true, true
		out = append(out, it)
	}
	return out
}
