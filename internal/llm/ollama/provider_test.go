package ollama_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/PatrickFanella/get-rich-quick/internal/llm"
	ollamaprovider "github.com/PatrickFanella/get-rich-quick/internal/llm/ollama"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestCompleteUsesConfiguredModelAndTracksUsage(t *testing.T) {
	t.Parallel()

	requestBodyChannel := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("request method = %s, want %s", r.Method, http.MethodPost)
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("request path = %s, want /v1/chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization header = %q, want %q", got, "Bearer test-key")
		}

		var requestBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		requestBodyChannel <- requestBody

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-123",
			"object":"chat.completion",
			"created":1730000000,
			"model":"llama3.2",
			"choices":[
				{
					"index":0,
					"finish_reason":"stop",
					"logprobs":null,
					"message":{"role":"assistant","content":"Hello back","refusal":""}
				}
			],
			"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}
		}`))
	}))
	defer server.Close()

	provider, err := ollamaprovider.NewProvider(ollamaprovider.Config{
		BaseURL: server.URL + "/v1",
		APIKey:  "test-key",
		Model:   ollamaprovider.ModelLlama3,
	})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}

	response, err := provider.Complete(context.Background(), llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: "system", Content: "You are concise."},
			{Role: "user", Content: "Say hello"},
		},
		Temperature: 0.4,
		MaxTokens:   32,
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if response.Content != "Hello back" {
		t.Fatalf("response.Content = %q, want %q", response.Content, "Hello back")
	}
	if response.Model != "llama3.2" {
		t.Fatalf("response.Model = %q, want %q", response.Model, "llama3.2")
	}
	if response.Usage.PromptTokens != 11 {
		t.Fatalf("response.Usage.PromptTokens = %d, want %d", response.Usage.PromptTokens, 11)
	}
	if response.Usage.CompletionTokens != 7 {
		t.Fatalf("response.Usage.CompletionTokens = %d, want %d", response.Usage.CompletionTokens, 7)
	}
	if response.LatencyMS < 0 {
		t.Fatalf("response.LatencyMS = %d, want >= 0", response.LatencyMS)
	}

	requestBody := <-requestBodyChannel
	if got := requestBody["model"]; got != ollamaprovider.ModelLlama3 {
		t.Fatalf("request model = %v, want %q", got, ollamaprovider.ModelLlama3)
	}
	if got := requestBody["temperature"]; got != 0.4 {
		t.Fatalf("request temperature = %v, want %v", got, 0.4)
	}
	if got := requestBody["max_completion_tokens"]; got != float64(32) {
		t.Fatalf("request max_completion_tokens = %v, want %d", got, 32)
	}

	messages, ok := requestBody["messages"].([]any)
	if !ok {
		t.Fatalf("request messages type = %T, want []any", requestBody["messages"])
	}
	if len(messages) != 2 {
		t.Fatalf("request message count = %d, want %d", len(messages), 2)
	}

	firstMessage, ok := messages[0].(map[string]any)
	if !ok {
		t.Fatalf("first message type = %T, want map[string]any", messages[0])
	}
	if firstMessage["role"] != "system" || firstMessage["content"] != "You are concise." {
		t.Fatalf("first message = %#v, want system prompt", firstMessage)
	}
}

func TestCompleteUsesDefaultBaseURL(t *testing.T) {
	t.Parallel()

	// Verify that DefaultBaseURL includes the /v1 path required by Ollama's
	// OpenAI-compatible endpoint, and that a provider configured with it routes
	// requests to /v1/chat/completions.
	pathChannel := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization header = %q, want %q", got, "Bearer test-key")
		}
		pathChannel <- r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-def",
			"object":"chat.completion",
			"created":1730000003,
			"model":"llama3.2",
			"choices":[
				{
					"index":0,
					"finish_reason":"stop",
					"logprobs":null,
					"message":{"role":"assistant","content":"ok","refusal":""}
				}
			],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`))
	}))
	defer server.Close()

	// Substitute the test server host while preserving the /v1 path from
	// DefaultBaseURL to confirm the constant carries the correct path prefix.
	if !strings.HasSuffix(ollamaprovider.DefaultBaseURL, "/v1") {
		t.Fatalf("DefaultBaseURL = %q, must end with /v1 for Ollama's OpenAI-compatible endpoint", ollamaprovider.DefaultBaseURL)
	}
	baseURL := server.URL + "/v1"

	provider, err := ollamaprovider.NewProvider(ollamaprovider.Config{
		BaseURL: baseURL,
		APIKey:  "test-key",
		Model:   ollamaprovider.ModelLlama3,
	})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}

	_, err = provider.Complete(context.Background(), llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: "user", Content: "hello"},
		},
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if path := <-pathChannel; path != "/v1/chat/completions" {
		t.Fatalf("request path = %s, want /v1/chat/completions", path)
	}
}

func TestCompleteUsesRequestModelOverride(t *testing.T) {
	t.Parallel()

	requestBodyChannel := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization header = %q, want %q", got, "Bearer test-key")
		}
		var requestBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		requestBodyChannel <- requestBody

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-456",
			"object":"chat.completion",
			"created":1730000001,
			"model":"mistral",
			"choices":[
				{
					"index":0,
					"finish_reason":"stop",
					"logprobs":null,
					"message":{"role":"assistant","content":"hi","refusal":""}
				}
			],
			"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}
		}`))
	}))
	defer server.Close()

	provider, err := ollamaprovider.NewProvider(ollamaprovider.Config{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Model:   ollamaprovider.ModelLlama3,
	})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}

	response, err := provider.Complete(context.Background(), llm.CompletionRequest{
		Model: ollamaprovider.ModelMistral,
		Messages: []llm.Message{
			{Role: "user", Content: "hi"},
		},
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if response.Model != "mistral" {
		t.Fatalf("response.Model = %q, want %q", response.Model, "mistral")
	}

	requestBody := <-requestBodyChannel
	if got := requestBody["model"]; got != ollamaprovider.ModelMistral {
		t.Fatalf("request model = %v, want %q", got, ollamaprovider.ModelMistral)
	}
}

func TestCompleteSupportsJSONMode(t *testing.T) {
	t.Parallel()

	requestBodyChannel := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization header = %q, want %q", got, "Bearer test-key")
		}
		var requestBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		requestBodyChannel <- requestBody

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-789",
			"object":"chat.completion",
			"created":1730000002,
			"model":"llama3.2",
			"choices":[
				{
					"index":0,
					"finish_reason":"stop",
					"logprobs":null,
					"message":{"role":"assistant","content":"{\"answer\":\"ok\"}","refusal":""}
				}
			],
			"usage":{"prompt_tokens":20,"completion_tokens":9,"total_tokens":29}
		}`))
	}))
	defer server.Close()

	provider, err := ollamaprovider.NewProvider(ollamaprovider.Config{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Model:   ollamaprovider.ModelLlama3,
	})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}

	response, err := provider.Complete(context.Background(), llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: "user", Content: "Return JSON"},
		},
		ResponseFormat: &llm.ResponseFormat{
			Type: llm.ResponseFormatJSONObject,
		},
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if response.Content != `{"answer":"ok"}` {
		t.Fatalf("response.Content = %q, want JSON payload", response.Content)
	}

	requestBody := <-requestBodyChannel
	responseFormat, ok := requestBody["response_format"].(map[string]any)
	if !ok {
		t.Fatalf("response_format type = %T, want map[string]any", requestBody["response_format"])
	}
	if responseFormat["type"] != "json_object" {
		t.Fatalf("response_format.type = %v, want %q", responseFormat["type"], "json_object")
	}
}

func TestCompleteWrapsSDKErrorsWithoutRetries(t *testing.T) {
	t.Parallel()

	var requestCounter atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization header = %q, want %q", got, "Bearer test-key")
		}
		requestCounter.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"backend unavailable","type":"server_error"}}`))
	}))
	defer server.Close()

	provider, err := ollamaprovider.NewProvider(ollamaprovider.Config{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Model:   ollamaprovider.ModelLlama3,
	})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}

	_, err = provider.Complete(context.Background(), llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: "user", Content: "hello"},
		},
	})
	if err == nil {
		t.Fatal("Complete() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "ollama: complete request") {
		t.Fatalf("Complete() error = %q, want wrapped context", err)
	}
	if requestCounter.Load() != 1 {
		t.Fatalf("request count = %d, want %d (retries disabled)", requestCounter.Load(), 1)
	}
}

func TestNewProvider_RequiresAPIKey(t *testing.T) {
	t.Parallel()

	_, err := ollamaprovider.NewProvider(ollamaprovider.Config{
		BaseURL: "http://example.com/v1",
		Model:   ollamaprovider.ModelLlama3,
	})
	if err == nil {
		t.Fatal("NewProvider() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "api key is required") {
		t.Fatalf("NewProvider() error = %q, want api key requirement", err)
	}
}

func TestComplete_StripsBrokerSSEPreamble(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization header = %q, want %q", got, "Bearer test-key")
		}
		// New llama-line format: text/event-stream with broker heartbeats
		// followed by the final response wrapped in a data: line.
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"request_id\":\"abc\",\"wait_seconds\":1,\"status\":\"queued\"}\n\n"))
		_, _ = w.Write([]byte("data: "))
		_, _ = w.Write([]byte(`{"id":"chatcmpl-sse","object":"chat.completion","created":1730000004,"model":"llama3.2","choices":[{"index":0,"finish_reason":"stop","logprobs":null,"message":{"role":"assistant","content":"works","refusal":""}}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`))
		_, _ = w.Write([]byte("\n\n"))
	}))
	defer server.Close()

	provider, err := ollamaprovider.NewProvider(ollamaprovider.Config{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Model:   ollamaprovider.ModelLlama3,
	})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}

	response, err := provider.Complete(context.Background(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if response.Content != "works" {
		t.Fatalf("response.Content = %q, want %q", response.Content, "works")
	}
}

func TestComplete_BrokerQueueFull(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization header = %q, want %q", got, "Bearer test-key")
		}
		w.Header().Set("X-Ollama-Broker", "true")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"queue full","message":"broker queue full"}`))
	}))
	defer server.Close()

	provider, err := ollamaprovider.NewProvider(ollamaprovider.Config{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Model:   ollamaprovider.ModelLlama3,
	})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}

	_, err = provider.Complete(context.Background(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: "user", Content: "hello"}},
	})
	if err == nil {
		t.Fatal("Complete() error = nil, want non-nil")
	}
}

func TestCompleteRetriesOnceOnBrokerConnectionClosed(t *testing.T) {
	t.Parallel()

	var requestCounter atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization header = %q, want %q", got, "Bearer test-key")
		}
		attempt := requestCounter.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if attempt == 1 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":{"message":"broker connection closed before response was received","type":"broker_error","code":"broker_error"}}`))
			return
		}
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-retry-broker",
			"object":"chat.completion",
			"created":1730000005,
			"model":"llama3.2",
			"choices":[{"index":0,"finish_reason":"stop","logprobs":null,"message":{"role":"assistant","content":"recovered","refusal":""}}],
			"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}
		}`))
	}))
	defer server.Close()

	provider, err := ollamaprovider.NewProvider(ollamaprovider.Config{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Model:   ollamaprovider.ModelLlama3,
	})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}

	response, err := provider.Complete(context.Background(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if response.Content != "recovered" {
		t.Fatalf("response.Content = %q, want %q", response.Content, "recovered")
	}
	if requestCounter.Load() != 2 {
		t.Fatalf("request count = %d, want 2", requestCounter.Load())
	}
}

func TestCompleteRetriesOnceOnMessageDeadlineExceeded(t *testing.T) {
	t.Parallel()

	var requestCounter atomic.Int32
	provider, err := ollamaprovider.NewProvider(ollamaprovider.Config{
		BaseURL: "http://ollama.test/v1",
		APIKey:  "test-key",
		Model:   ollamaprovider.ModelLlama3,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
				t.Fatalf("authorization header = %q, want %q", got, "Bearer test-key")
			}
			attempt := requestCounter.Add(1)
			if attempt == 1 {
				return nil, errors.New("context deadline exceeded")
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(strings.NewReader(`{
					"id":"chatcmpl-timeout-retry",
					"object":"chat.completion",
					"created":1730000006,
					"model":"llama3.2",
					"choices":[{"index":0,"finish_reason":"stop","logprobs":null,"message":{"role":"assistant","content":"timeout-recovered","refusal":""}}],
					"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}
				}`)),
				Request: r,
			}, nil
		})},
	})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}

	response, err := provider.Complete(context.Background(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if response.Content != "timeout-recovered" {
		t.Fatalf("response.Content = %q, want %q", response.Content, "timeout-recovered")
	}
	if requestCounter.Load() != 2 {
		t.Fatalf("request count = %d, want 2", requestCounter.Load())
	}
}

func TestCompleteRetriesOnceOnNoChoices(t *testing.T) {
	t.Parallel()

	var requestCounter atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization header = %q, want %q", got, "Bearer test-key")
		}
		attempt := requestCounter.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if attempt == 1 {
			_, _ = w.Write([]byte(`{
				"id":"chatcmpl-empty",
				"object":"chat.completion",
				"created":1730000006,
				"model":"llama3.2",
				"choices":[],
				"usage":{"prompt_tokens":3,"completion_tokens":0,"total_tokens":3}
			}`))
			return
		}
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-empty-retry",
			"object":"chat.completion",
			"created":1730000007,
			"model":"llama3.2",
			"choices":[{"index":0,"finish_reason":"stop","logprobs":null,"message":{"role":"assistant","content":"retry-success","refusal":""}}],
			"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}
		}`))
	}))
	defer server.Close()

	provider, err := ollamaprovider.NewProvider(ollamaprovider.Config{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Model:   ollamaprovider.ModelLlama3,
	})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}

	response, err := provider.Complete(context.Background(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if response.Content != "retry-success" {
		t.Fatalf("response.Content = %q, want %q", response.Content, "retry-success")
	}
	if requestCounter.Load() != 2 {
		t.Fatalf("request count = %d, want 2", requestCounter.Load())
	}
}

func TestCompleteRetriesTwiceOnConsecutiveNoChoices(t *testing.T) {
	t.Parallel()

	var requestCounter atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization header = %q, want %q", got, "Bearer test-key")
		}
		attempt := requestCounter.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch attempt {
		case 1, 2:
			_, _ = w.Write([]byte(`{
				"id":"chatcmpl-empty-repeat",
				"object":"chat.completion",
				"created":1730000010,
				"model":"llama3.2",
				"choices":[],
				"usage":{"prompt_tokens":3,"completion_tokens":0,"total_tokens":3}
			}`))
			return
		default:
			_, _ = w.Write([]byte(`{
				"id":"chatcmpl-empty-repeat-retry",
				"object":"chat.completion",
				"created":1730000011,
				"model":"llama3.2",
				"choices":[{"index":0,"finish_reason":"stop","logprobs":null,"message":{"role":"assistant","content":"second-retry-success","refusal":""}}],
				"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}
			}`))
		}
	}))
	defer server.Close()

	provider, err := ollamaprovider.NewProvider(ollamaprovider.Config{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Model:   ollamaprovider.ModelLlama3,
	})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}

	response, err := provider.Complete(context.Background(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if response.Content != "second-retry-success" {
		t.Fatalf("response.Content = %q, want %q", response.Content, "second-retry-success")
	}
	if requestCounter.Load() != 3 {
		t.Fatalf("request count = %d, want 3", requestCounter.Load())
	}
}

func TestCompleteRetriesAcrossNoChoicesThenBrokerConnectionClosed(t *testing.T) {
	t.Parallel()

	var requestCounter atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization header = %q, want %q", got, "Bearer test-key")
		}
		attempt := requestCounter.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch attempt {
		case 1:
			_, _ = w.Write([]byte(`{
				"id":"chatcmpl-empty-seq",
				"object":"chat.completion",
				"created":1730000008,
				"model":"llama3.2",
				"choices":[],
				"usage":{"prompt_tokens":3,"completion_tokens":0,"total_tokens":3}
			}`))
			return
		case 2:
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":{"message":"broker connection closed before response was received","type":"broker_error","code":"broker_error"}}`))
			return
		default:
			_, _ = w.Write([]byte(`{
				"id":"chatcmpl-retry-seq",
				"object":"chat.completion",
				"created":1730000009,
				"model":"llama3.2",
				"choices":[{"index":0,"finish_reason":"stop","logprobs":null,"message":{"role":"assistant","content":"sequence-recovered","refusal":""}}],
				"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}
			}`))
		}
	}))
	defer server.Close()

	provider, err := ollamaprovider.NewProvider(ollamaprovider.Config{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Model:   ollamaprovider.ModelLlama3,
	})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}

	response, err := provider.Complete(context.Background(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if response.Content != "sequence-recovered" {
		t.Fatalf("response.Content = %q, want %q", response.Content, "sequence-recovered")
	}
	if requestCounter.Load() != 3 {
		t.Fatalf("request count = %d, want 3", requestCounter.Load())
	}
}

func TestCompleteRejectsUnsupportedMessageRole(t *testing.T) {
	t.Parallel()

	provider, err := ollamaprovider.NewProvider(ollamaprovider.Config{
		APIKey: "test-key",
		Model:  ollamaprovider.ModelLlama3,
	})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}

	_, err = provider.Complete(context.Background(), llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: "tool", Content: "not supported"},
		},
	})
	if err == nil {
		t.Fatal("Complete() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), `ollama: unsupported message role "tool"`) {
		t.Fatalf("Complete() error = %q, want unsupported role message", err)
	}
}

func TestDefaultModelsByTier(t *testing.T) {
	t.Parallel()

	models := ollamaprovider.DefaultModelsByTier()
	if models[llm.ModelTierDeepThink] != ollamaprovider.ModelLlama3 {
		t.Fatalf("deep think model = %q, want %q", models[llm.ModelTierDeepThink], ollamaprovider.ModelLlama3)
	}
	if models[llm.ModelTierQuickThink] != ollamaprovider.ModelLlama3 {
		t.Fatalf("quick think model = %q, want %q", models[llm.ModelTierQuickThink], ollamaprovider.ModelLlama3)
	}

	models[llm.ModelTierQuickThink] = "mutated"
	if ollamaprovider.DefaultModelsByTier()[llm.ModelTierQuickThink] != ollamaprovider.ModelLlama3 {
		t.Fatal("DefaultModelsByTier() returned a shared map")
	}
}
