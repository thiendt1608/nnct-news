// Package extract pulls the readable text out of an article URL.
package extract

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/go-shiori/go-readability"
)

const (
	MaxBody  = 2 << 20 // 2 MB
	MaxChars = 24000   // ≈ 8K token
	minChars = 400     // ngắn hơn → coi như paywall/lỗi → low_context
)

type Result struct {
	Text       string
	LowContext bool
}

// NewClient returns an HTTP client that refuses to connect to private/loopback addresses
// (feed URLs are untrusted input). allowPrivate is for tests only.
func NewClient(allowPrivate bool) *http.Client {
	d := &net.Dialer{Timeout: 10 * time.Second}
	if !allowPrivate {
		d.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
				return fmt.Errorf("địa chỉ bị chặn: %s", host)
			}
			return nil
		}
	}
	return &http.Client{
		Timeout:   20 * time.Second,
		Transport: &http.Transport{DialContext: d.DialContext, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 15 * time.Second},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("quá nhiều redirect")
			}
			return nil
		},
	}
}

// Fetch downloads rawURL and extracts the main text. Any failure (or too little text)
// yields LowContext=true with the error, so callers fall back to the feed snippet.
func Fetch(ctx context.Context, c *http.Client, rawURL string) (Result, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return Result{LowContext: true}, fmt.Errorf("URL không hợp lệ")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Result{LowContext: true}, err
	}
	req.Header.Set("User-Agent", "nnct-news/0.1 (personal feed reader)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := c.Do(req)
	if err != nil {
		return Result{LowContext: true}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Result{LowContext: true}, fmt.Errorf("http %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" && !strings.Contains(ct, "html") {
		return Result{LowContext: true}, fmt.Errorf("content-type %q", ct)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody))
	if err != nil {
		return Result{LowContext: true}, err
	}
	return FromHTML(body, u)
}

// FromHTML extracts text from an HTML document.
func FromHTML(body []byte, u *url.URL) (Result, error) {
	art, err := readability.FromReader(bytes.NewReader(body), u)
	if err != nil {
		return Result{LowContext: true}, err
	}
	text := strings.TrimSpace(art.TextContent)
	text = strings.Join(strings.Fields(strings.ReplaceAll(text, "\n", " \n ")), " ")
	r := []rune(text)
	if len(r) > MaxChars {
		r = r[:MaxChars]
	}
	return Result{Text: string(r), LowContext: len(r) < minChars}, nil
}
