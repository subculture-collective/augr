package polymarket

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"hash"
	"net/http"
	neturl "net/url"
	"strconv"
	"strings"
	"sync"
)

// OrderTemplate caches the immutable pieces of an authenticated order send.
type OrderTemplate struct {
	mu      sync.Mutex
	body    []byte
	prefix  []byte
	method  string
	url     string
	mac     hash.Hash
	secret  []byte
	scratch []byte
}

// NewOrderTemplate constructs a reusable signing template.
func NewOrderTemplate(secret []byte, method, rawURL string, body []byte) (*OrderTemplate, error) {
	if len(secret) == 0 {
		return nil, errors.New("polymarket: signing secret is required")
	}
	if strings.TrimSpace(method) == "" {
		return nil, errors.New("polymarket: method is required")
	}
	if strings.TrimSpace(rawURL) == "" {
		return nil, errors.New("polymarket: url is required")
	}
	if body == nil {
		body = []byte("{}")
	}
	parsedURL, err := neturl.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("polymarket: parse url: %w", err)
	}
	signingPath := parsedURL.EscapedPath()
	if signingPath == "" {
		signingPath = "/"
	}
	if parsedURL.RawQuery != "" {
		signingPath += "?" + parsedURL.RawQuery
	}

	tmpl := &OrderTemplate{
		body:    append([]byte(nil), body...),
		prefix:  []byte(strings.ToUpper(strings.TrimSpace(method)) + signingPath + string(body)),
		method:  strings.ToUpper(strings.TrimSpace(method)),
		url:     strings.TrimSpace(rawURL),
		secret:  append([]byte(nil), secret...),
		scratch: make([]byte, 0, 32),
	}
	tmpl.mac = hmac.New(sha256.New, tmpl.secret)
	return tmpl, nil
}

func (t *OrderTemplate) Clone() *OrderTemplate {
	if t == nil {
		return nil
	}
	clone := &OrderTemplate{
		body:    append([]byte(nil), t.body...),
		prefix:  append([]byte(nil), t.prefix...),
		method:  t.method,
		url:     t.url,
		secret:  append([]byte(nil), t.secret...),
		scratch: make([]byte, 0, cap(t.scratch)),
	}
	clone.mac = hmac.New(sha256.New, clone.secret)
	return clone
}

func (t *OrderTemplate) SignAt(ts int64) string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	t.mac.Reset()
	t.scratch = strconv.AppendInt(t.scratch[:0], ts, 10)
	t.mac.Write(t.scratch)
	t.mac.Write(t.prefix)
	sig := t.mac.Sum(nil)
	encoded := make([]byte, base64.StdEncoding.EncodedLen(len(sig)))
	base64.StdEncoding.Encode(encoded, sig)
	return string(encoded)
}

func (t *OrderTemplate) BodyLen() int {
	if t == nil {
		return 0
	}
	return len(t.body)
}

func (t *OrderTemplate) URL() string {
	if t == nil {
		return ""
	}
	return t.url
}

func (t *OrderTemplate) newRequest(timestamp string, signature string, keyID string) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodPost, t.url, bytes.NewReader(t.body))
	if err != nil {
		return nil, fmt.Errorf("polymarket: create templated request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PM-Access-Key", keyID)
	req.Header.Set("X-PM-Timestamp", timestamp)
	req.Header.Set("X-PM-Signature", signature)
	return req, nil
}
