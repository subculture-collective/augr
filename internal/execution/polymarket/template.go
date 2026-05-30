package polymarket

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	neturl "net/url"
	"strings"
)

// OrderTemplate caches the immutable pieces of an authenticated order send.
type OrderTemplate struct {
	body       []byte
	method     string
	url        string
	secretKey  []byte
	path       string
	StrategyID string
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
		body:      append([]byte(nil), body...),
		method:    strings.ToUpper(strings.TrimSpace(method)),
		url:       strings.TrimSpace(rawURL),
		secretKey: append([]byte(nil), secret...),
		path:      signingPath,
	}
	return tmpl, nil
}

func (t *OrderTemplate) Clone() *OrderTemplate {
	if t == nil {
		return nil
	}
	clone := &OrderTemplate{
		body:       append([]byte(nil), t.body...),
		method:     t.method,
		url:        t.url,
		secretKey:  append([]byte(nil), t.secretKey...),
		path:       t.path,
		StrategyID: t.StrategyID,
	}
	return clone
}

func (t *OrderTemplate) SignAt(ts int64) string {
	if t == nil {
		return ""
	}
	secret := append([]byte(nil), t.secretKey...)
	if len(secret) == 64 {
		secret = secret[:32]
	}
	if len(secret) != ed25519.SeedSize {
		return ""
	}
	privateKey := ed25519.NewKeyFromSeed(secret)
	message := canonicalSigningMessage(fmt.Sprintf("%d", ts), t.method, t.path)
	sig := ed25519.Sign(privateKey, []byte(message))
	return base64.StdEncoding.EncodeToString(sig)
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

func (t *OrderTemplate) SigningPath() string {
	if t == nil {
		return ""
	}
	return t.path
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
