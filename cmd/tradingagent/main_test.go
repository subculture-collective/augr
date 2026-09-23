package main

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/cli"
)

func TestRun_ReturnsStartupErrorWithoutBlocking(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer func() {
		_ = listener.Close()
	}()

	server := &http.Server{
		Addr:    listener.Addr().String(),
		Handler: http.NewServeMux(),
	}

	done := make(chan error, 1)
	go func() {
		done <- cli.RunServerLifecycle(context.Background(), server.ListenAndServe, server.Shutdown)
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("run() error = nil, want startup error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run() did not return after startup failure")
	}
}
