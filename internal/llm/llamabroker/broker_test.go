package llamabroker_test

import (
	"io"
	"strings"
	"testing"

	"github.com/PatrickFanella/get-rich-quick/internal/llm/llamabroker"
)

func TestStripSSEPreamble_NoSSE(t *testing.T) {
	t.Parallel()

	body := `{"ok":true}`
	got, err := io.ReadAll(llamabroker.StripSSEPreamble(strings.NewReader(body)))
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(got) != body {
		t.Fatalf("body = %q, want %q", string(got), body)
	}
}

func TestStripSSEPreamble_WithPreamble(t *testing.T) {
	t.Parallel()

	// New llama-line format: final response is also wrapped in data: line.
	input := "data: {\"request_id\":\"abc\",\"wait_seconds\":2,\"status\":\"queued\"}\n\ndata: {\"answer\":\"yes\"}\n\n"
	got, err := io.ReadAll(llamabroker.StripSSEPreamble(strings.NewReader(input)))
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(got) != "{\"answer\":\"yes\"}\n\n" {
		t.Fatalf("body = %q, want %q", string(got), "{\"answer\":\"yes\"}\n\n")
	}
}

func TestStripSSEPreamble_OllamaUnavailable(t *testing.T) {
	t.Parallel()

	input := "data: {\"request_id\":\"abc\",\"status\":\"queued\"}\n\ndata: {\"request_id\":\"abc\",\"status\":\"ollama_unavailable\",\"message\":\"connection refused\"}\n\n"
	got, err := io.ReadAll(llamabroker.StripSSEPreamble(strings.NewReader(input)))
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if !strings.Contains(string(got), "broker_error") {
		t.Fatalf("body = %q, want broker_error JSON", string(got))
	}
}

func TestStripBrokerHeartbeats_PassesSSEThrough(t *testing.T) {
	t.Parallel()

	// Broker heartbeats followed by real ollama SSE chunks.
	input := strings.Join([]string{
		`data: {"request_id":"abc","wait_seconds":2,"status":"queued"}`,
		"",
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"delta":{"content":"Hi"}}]}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	got, err := io.ReadAll(llamabroker.StripBrokerHeartbeats(strings.NewReader(input)))
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	s := string(got)
	if strings.Contains(s, `"status":"queued"`) {
		t.Errorf("heartbeat not stripped: %q", s)
	}
	if !strings.Contains(s, `"chat.completion.chunk"`) {
		t.Errorf("chunk missing: %q", s)
	}
	if !strings.Contains(s, "[DONE]") {
		t.Errorf("[DONE] missing: %q", s)
	}
}

func TestStripBrokerHeartbeats_ErrorEvent(t *testing.T) {
	t.Parallel()

	input := strings.Join([]string{
		`data: {"request_id":"abc","status":"queued"}`,
		"",
		`data: {"request_id":"abc","status":"ollama_unavailable","message":"timeout"}`,
		"",
	}, "\n")
	got, err := io.ReadAll(llamabroker.StripBrokerHeartbeats(strings.NewReader(input)))
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	s := string(got)
	if !strings.Contains(s, "broker_error") {
		t.Errorf("error event not synthesised: %q", s)
	}
}

func TestStripSSEPreamble_MultipleEvents(t *testing.T) {
	t.Parallel()

	// Multiple broker heartbeats then final response wrapped in data: line.
	input := strings.Join([]string{
		`data: {"request_id":"abc","wait_seconds":3,"status":"queued"}`,
		"",
		`data: {"request_id":"abc","wait_seconds":8,"status":"queued"}`,
		"",
		`data: {"answer":"ok"}`,
		"",
	}, "\n")
	got, err := io.ReadAll(llamabroker.StripSSEPreamble(strings.NewReader(input)))
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(got) != "{\"answer\":\"ok\"}\n" {
		t.Fatalf("body = %q, want %q", string(got), "{\"answer\":\"ok\"}\n")
	}
}

func TestStripSSEPreambleReadCloser_ClosePropagates(t *testing.T) {
	t.Parallel()

	closer := &trackingReadCloser{Reader: strings.NewReader(`{"ok":true}`)}
	rc := llamabroker.StripSSEPreambleReadCloser(closer)
	if rc == nil {
		t.Fatal("StripSSEPreambleReadCloser() = nil, want non-nil")
	}
	if err := rc.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !closer.closed {
		t.Fatal("Close() did not propagate to underlying ReadCloser")
	}
}

type trackingReadCloser struct {
	io.Reader
	closed bool
}

func (t *trackingReadCloser) Read(p []byte) (int, error) {
	return t.Reader.Read(p)
}

func (t *trackingReadCloser) Close() error {
	t.closed = true
	return nil
}
