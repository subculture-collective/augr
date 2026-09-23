package polymarket

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultAPIBaseURL     = "https://api.polymarket.us"
	defaultGatewayBaseURL = "https://gateway.polymarket.us"
	defaultTimeout        = 30 * time.Second
)

// Client is a small HTTP client for Polymarket US retail APIs.
type Client struct {
	apiBaseURL     string
	gatewayBaseURL string
	httpClient     *http.Client
	logger         *slog.Logger
	address        string
	keyID          string
	secretKey      string
	passphrase     string
	now            func() time.Time
	// signatureType mirrors POLYMARKET_SIGNATURE_TYPE (0 = EOA, 1 = Magic/email
	// proxy, 2 = Gnosis Safe proxy). It describes how the funder wallet signs
	// CLOB orders. The api.polymarket.us retail order payload has no
	// signatureType field, so the value is stored for diagnostics and future
	// CLOB order signing only.
	signatureType int
}

// ErrorResponse captures Polymarket's standard error response shape.
type ErrorResponse struct {
	Message string `json:"error"`

	statusCode int
}

// SetL2Auth configures Polymarket CLOB L2 API credentials. The values are
// generated from L1 wallet authentication and are used only to authenticate API
// requests; order creation still requires a separately signed order payload.
func (c *Client) SetL2Auth(address, apiKey, secret, passphrase string) {
	if c == nil {
		return
	}
	c.address = strings.TrimSpace(address)
	c.keyID = strings.TrimSpace(apiKey)
	c.secretKey = strings.TrimSpace(secret)
	c.passphrase = strings.TrimSpace(passphrase)
}

// SetSignatureType records the wallet signature type (0 EOA, 1 POLY_PROXY,
// 2 GNOSIS_SAFE). Values outside that range are rejected.
func (c *Client) SetSignatureType(signatureType int) error {
	if c == nil {
		return errors.New("polymarket: client is nil")
	}
	if signatureType < 0 || signatureType > 2 {
		return fmt.Errorf("polymarket: unsupported signature type %d (expected 0, 1 or 2)", signatureType)
	}
	c.signatureType = signatureType
	return nil
}

// SignatureType returns the configured wallet signature type.
func (c *Client) SignatureType() int {
	if c == nil {
		return 0
	}
	return c.signatureType
}

// NewClient constructs a Polymarket US retail HTTP client.
func NewClient(keyID, secretKey string, logger *slog.Logger) *Client {
	if logger == nil {
		logger = slog.Default()
	}

	return &Client{
		keyID:          strings.TrimSpace(keyID),
		secretKey:      strings.TrimSpace(secretKey),
		apiBaseURL:     defaultAPIBaseURL,
		gatewayBaseURL: defaultGatewayBaseURL,
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
		logger: logger,
		now:    time.Now,
	}
}

// StatusCode returns the HTTP status code associated with the error.
func (e *ErrorResponse) StatusCode() int {
	if e == nil {
		return 0
	}

	return e.statusCode
}

func (e *ErrorResponse) Error() string {
	if e == nil {
		return "polymarket: request failed"
	}

	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = http.StatusText(e.statusCode)
	}
	if message == "" {
		message = "request failed"
	}

	return fmt.Sprintf("polymarket: %s (status=%d)", message, e.statusCode)
}

// SetAPIBaseURL overrides the configured authenticated API base URL.
func (c *Client) SetAPIBaseURL(baseURL string) {
	if c == nil {
		return
	}

	c.apiBaseURL = strings.TrimSpace(baseURL)
}

// SetGatewayBaseURL overrides the configured public gateway base URL.
func (c *Client) SetGatewayBaseURL(baseURL string) {
	if c == nil {
		return
	}

	c.gatewayBaseURL = strings.TrimSpace(baseURL)
}

// SetHTTPClient replaces the underlying HTTP client. This is primarily useful for testing.
func (c *Client) SetHTTPClient(httpClient *http.Client) {
	if c == nil || httpClient == nil {
		return
	}

	c.httpClient = httpClient
}

// SetTimeout updates the timeout used by the underlying HTTP client.
func (c *Client) SetTimeout(timeout time.Duration) {
	if c == nil {
		return
	}
	logger := c.getLogger()
	if timeout <= 0 {
		logger.Warn("polymarket: ignoring invalid timeout", slog.String("timeout", timeout.String()))
		return
	}
	if c.httpClient == nil {
		c.httpClient = &http.Client{Timeout: defaultTimeout}
	}

	c.httpClient.Timeout = timeout
}

func (c *Client) setNowFunc(now func() time.Time) {
	if c == nil || now == nil {
		return
	}

	c.now = now
}

// GetPublic issues a public GET request against the gateway API.
func (c *Client) GetPublic(ctx context.Context, requestPath string, params url.Values) ([]byte, error) {
	return c.do(ctx, http.MethodGet, requestPath, params, nil, false)
}

// Get issues an authenticated GET request.
func (c *Client) Get(ctx context.Context, requestPath string, params url.Values) ([]byte, error) {
	return c.do(ctx, http.MethodGet, requestPath, params, nil, true)
}

// Post issues an authenticated POST request with a JSON body and returns the raw response body.
func (c *Client) Post(ctx context.Context, requestPath string, body any) ([]byte, error) {
	return c.do(ctx, http.MethodPost, requestPath, nil, body, true)
}

func (c *Client) PostPublic(ctx context.Context, requestPath string, body any) ([]byte, error) {
	return c.do(ctx, http.MethodPost, requestPath, nil, body, false)
}

func (c *Client) Delete(ctx context.Context, requestPath string) ([]byte, error) {
	return c.do(ctx, http.MethodDelete, requestPath, nil, nil, true)
}

func (c *Client) do(ctx context.Context, method, requestPath string, params url.Values, requestBody any, authenticated bool) ([]byte, error) {
	if c == nil {
		return nil, errors.New("polymarket: client is nil")
	}
	if authenticated {
		if c.address == "" {
			return nil, errors.New("polymarket: address is required")
		}
		if c.keyID == "" {
			return nil, errors.New("polymarket: key id is required")
		}
		if c.secretKey == "" {
			return nil, errors.New("polymarket: secret key is required")
		}
		if c.passphrase == "" {
			return nil, errors.New("polymarket: passphrase is required")
		}
	}

	logger := c.getLogger()
	httpClient := c.getHTTPClient()

	requestURL, signingPath, err := c.buildURL(requestPath, params, authenticated)
	if err != nil {
		return nil, err
	}

	bodyBytes, err := marshalRequestBodyBytes(requestBody)
	if err != nil {
		return nil, fmt.Errorf("polymarket: marshal request body: %w", err)
	}
	bodyReader := bytes.NewReader(bodyBytes)

	req, err := http.NewRequestWithContext(ctx, method, requestURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("polymarket: create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if authenticated {
		headers, err := c.authHeaders(method, signingPath, bodyBytes)
		if err != nil {
			return nil, err
		}
		for key, value := range headers {
			req.Header.Set(key, value)
		}
	}

	startedAt := time.Now()
	logger.Debug("polymarket: sending request",
		slog.String("method", req.Method),
		slog.String("path", req.URL.Path),
		slog.Bool("authenticated", authenticated),
	)

	resp, err := httpClient.Do(req)
	if err != nil {
		logger.Warn("polymarket: request failed",
			slog.String("method", req.Method),
			slog.String("path", req.URL.Path),
			slog.Any("error", err),
			slog.Int64("duration_ms", time.Since(startedAt).Milliseconds()),
		)
		return nil, fmt.Errorf("polymarket: do request: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			logger.Warn("polymarket: failed to close response body", slog.Any("error", closeErr))
		}
	}()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("polymarket: read response body: %w", err)
	}

	logger.Debug("polymarket: received response",
		slog.String("method", req.Method),
		slog.String("path", req.URL.Path),
		slog.Int("status", resp.StatusCode),
		slog.Int64("duration_ms", time.Since(startedAt).Milliseconds()),
	)

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, parseErrorResponse(resp.StatusCode, responseBody)
	}

	return responseBody, nil
}

func marshalRequestBodyBytes(body any) ([]byte, error) {
	if body == nil {
		return nil, nil
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	return payload, nil
}

func parseErrorResponse(statusCode int, body []byte) *ErrorResponse {
	errResp := &ErrorResponse{statusCode: statusCode}
	if len(body) == 0 {
		return errResp
	}

	var parsed struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		errResp.Message = strings.TrimSpace(string(body))
		return errResp
	}
	errResp.Message = strings.TrimSpace(parsed.Error)
	if errResp.Message == "" {
		errResp.Message = strings.TrimSpace(parsed.Message)
	}
	if errResp.Message == "" {
		errResp.Message = strings.TrimSpace(string(body))
	}

	return errResp
}

func (c *Client) authHeaders(method, signingPath string, body []byte) (map[string]string, error) {
	now := time.Now
	if c.now != nil {
		now = c.now
	}
	timestamp := fmt.Sprintf("%d", now().Unix())
	signature, err := polyL2Signature(c.secretKey, timestamp, method, signingPath, body)
	if err != nil {
		return nil, err
	}

	return map[string]string{
		"POLY_ADDRESS":    c.address,
		"POLY_API_KEY":    c.keyID,
		"POLY_PASSPHRASE": c.passphrase,
		"POLY_SIGNATURE":  signature,
		"POLY_TIMESTAMP":  timestamp,
	}, nil
}

// polyL2Signature computes the CLOB L2 HMAC: base64url(HMAC-SHA256(secret,
// timestamp + method + path + body)). The path excludes any query string; the
// query stays on the request URL only.
func polyL2Signature(secret, timestamp, method, signingPath string, body []byte) (string, error) {
	secretKeyBytes, err := decodeL2Secret(secret)
	if err != nil {
		return "", err
	}
	return polyL2SignatureBytes(secretKeyBytes, timestamp, method, signingPath, body), nil
}

func polyL2SignatureBytes(secretKeyBytes []byte, timestamp, method, signingPath string, body []byte) string {
	message := timestamp + method + signingPath
	if len(body) > 0 {
		message += string(body)
	}
	mac := hmac.New(sha256.New, secretKeyBytes)
	_, _ = mac.Write([]byte(message))
	return base64.URLEncoding.EncodeToString(mac.Sum(nil))
}

// decodeL2Secret is the single decoder for the L2 API secret used by both the
// ad-hoc request path and the cached order template. It accepts URL-safe
// base64 first (the form Polymarket issues), then standard base64, padded or
// not. The decoded bytes are used as-is: no truncation, no length check, so
// both signing paths always agree.
func decodeL2Secret(value string) ([]byte, error) {
	decoded, err := decodeBase64Flexible(value)
	if err != nil {
		return nil, fmt.Errorf("polymarket: decode api secret: %w", err)
	}
	return decoded, nil
}

func decodeBase64Flexible(value string) ([]byte, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, errors.New("empty secret")
	}
	if decoded, err := base64.URLEncoding.DecodeString(trimmed); err == nil {
		return decoded, nil
	}
	if decoded, err := base64.RawURLEncoding.DecodeString(trimmed); err == nil {
		return decoded, nil
	}
	if decoded, err := base64.StdEncoding.DecodeString(trimmed); err == nil {
		return decoded, nil
	}
	return base64.RawStdEncoding.DecodeString(trimmed)
}

func (c *Client) buildURL(requestPath string, params url.Values, authenticated bool) (string, string, error) {
	base := c.gatewayBaseURL
	if authenticated {
		base = c.apiBaseURL
	}
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", "", fmt.Errorf("polymarket: parse base url: %w", err)
	}
	parsedRequestPath, err := url.Parse(strings.TrimSpace(requestPath))
	if err != nil {
		return "", "", fmt.Errorf("polymarket: parse request path: %w", err)
	}
	path := parsedRequestPath.Path
	if path == "" {
		path = requestPath
	}

	baseURL.Path = joinPath(baseURL.Path, path)
	baseURL.RawPath = ""
	if unescapedPath, err := url.PathUnescape(baseURL.Path); err == nil && unescapedPath != baseURL.Path {
		baseURL.RawPath = baseURL.Path
		baseURL.Path = unescapedPath
	}

	query := baseURL.Query()
	for key, values := range parsedRequestPath.Query() {
		for _, value := range values {
			query.Add(key, value)
		}
	}
	for key, values := range params {
		for _, value := range values {
			query.Add(key, value)
		}
	}
	baseURL.RawQuery = query.Encode()

	// CLOB L2 signs timestamp+method+path+body; the query string is not part
	// of the signed message even though it remains on the request URL.
	signingPath := baseURL.EscapedPath()
	if signingPath == "" {
		signingPath = "/"
	}

	return baseURL.String(), signingPath, nil
}

func joinPath(basePath, requestPath string) string {
	trimmedPath := strings.TrimSpace(requestPath)
	if trimmedPath == "" {
		if basePath == "" {
			return "/"
		}
		return basePath
	}

	cleanPath := "/" + strings.TrimLeft(trimmedPath, "/")
	if basePath == "" || basePath == "/" {
		return cleanPath
	}

	return strings.TrimRight(basePath, "/") + cleanPath
}

func (c *Client) getLogger() *slog.Logger {
	if c == nil || c.logger == nil {
		return slog.Default()
	}

	return c.logger
}

func (c *Client) getHTTPClient() *http.Client {
	if c == nil || c.httpClient == nil {
		return &http.Client{Timeout: defaultTimeout}
	}

	return c.httpClient
}
