package ui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestMobileLayoutBrowserReadiness(t *testing.T) {
	// Both paths use the same stalled-first-transport fixture and outer budget.
	// The old single Dial consumes it; bounded startup handshakes can recover.
	for _, singleDial := range []bool{true, false} {
		name := "bounded-handshakes"
		if singleDial {
			name = "old-single-dial-negative-control"
		}
		t.Run(name, func(t *testing.T) {
			var attempts atomic.Int32
			firstCancelled := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if attempts.Add(1) == 1 {
					<-r.Context().Done()
					close(firstCancelled)
					return
				}
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					t.Errorf("accept readiness connection: %v", err)
					return
				}
				defer func() { _ = conn.CloseNow() }()
				ctx, cancel := context.WithTimeout(r.Context(), time.Second)
				defer cancel()
				kind, data, err := conn.Read(ctx)
				if err != nil {
					t.Errorf("read real readiness request: %v", err)
					return
				}
				if err := conn.Write(ctx, kind, data); err != nil {
					t.Errorf("echo real readiness response: %v", err)
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 2500*time.Millisecond)
			defer cancel()
			endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
			if singleDial {
				conn, response, err := websocket.Dial(ctx, endpoint, nil)
				if response != nil && response.Body != nil {
					_ = response.Body.Close()
				}
				if conn != nil {
					_ = conn.CloseNow()
				}
				if conn != nil || !errors.Is(err, context.DeadlineExceeded) || ctx.Err() == nil || attempts.Load() != 1 {
					t.Fatalf("single Dial must exhaust parent: conn=%v err=%v parent=%v attempts=%d", conn, err, ctx.Err(), attempts.Load())
				}
			} else {
				trace := newMobileLayoutReadinessTrace()
				conn, err := dialMobileLayoutBrowser(ctx, endpoint, make(chan struct{}), trace)
				journal := trace.String()
				if !strings.Contains(journal, "phase=handshake-end attempt=1") || !strings.Contains(journal, "phase=ready attempt=2") || !strings.Contains(journal, "http_status=101") {
					t.Fatalf("missing bounded handshake observations: %s", journal)
				}
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = conn.CloseNow() }()
				if ctx.Err() != nil || attempts.Load() != 2 {
					t.Fatalf("readiness must use exactly two attempts within parent: %v, %d", ctx.Err(), attempts.Load())
				}
				request := map[string]any{"id": float64(7), "method": "Browser.getVersion"}
				data, err := json.Marshal(request)
				if err != nil {
					t.Fatal(err)
				}
				if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
					t.Fatal(err)
				}
				_, reply, err := conn.Read(ctx)
				if err != nil {
					t.Fatal(err)
				}
				var got map[string]any
				if err := json.Unmarshal(reply, &got); err != nil || got["id"] != request["id"] || got["method"] != request["method"] {
					t.Fatalf("real JSON round-trip: %s, %v", reply, err)
				}
			}
			select {
			case <-firstCancelled:
			case <-time.After(time.Second):
				t.Fatal("stalled handshake was not cancelled")
			}
		})
	}

	t.Run("parent-deadline", func(t *testing.T) {
		var attempts atomic.Int32
		cancelled := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts.Add(1)
			<-r.Context().Done()
			close(cancelled)
		}))
		defer server.Close()
		ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
		defer cancel()
		trace := newMobileLayoutReadinessTrace()
		conn, err := dialMobileLayoutBrowser(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), make(chan struct{}), trace)
		if conn != nil || !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "last transport error:") {
			t.Fatalf("deadline returned usable connection or lost diagnostics: %v, %v", conn, err)
		}
		if journal := trace.String(); !strings.Contains(journal, "phase=stopped attempt=1") || !strings.Contains(journal, "parent=context deadline exceeded") || !strings.Contains(journal, "duration=") || !strings.Contains(journal, "remaining=") {
			t.Fatalf("missing parent-deadline observations: %s", journal)
		}
		<-cancelled
		if attempts.Load() != 1 {
			t.Fatalf("late attempt after deadline: %d", attempts.Load())
		}
	})

	for _, code := range []int{http.StatusForbidden, http.StatusOK} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts.Add(1)
				w.WriteHeader(code)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			trace := newMobileLayoutReadinessTrace()
			conn, err := dialMobileLayoutBrowser(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), make(chan struct{}), trace)
			if journal := trace.String(); !strings.Contains(journal, "phase=terminal-response attempt=1") || !strings.Contains(journal, "http_status="+strconv.Itoa(code)) {
				t.Fatalf("missing terminal-response observations: %s", journal)
			}
			if conn != nil || err == nil || ctx.Err() != nil || attempts.Load() != 1 {
				t.Fatalf("HTTP/protocol failure must be terminal: %v, %v, parent=%v attempts=%d", conn, err, ctx.Err(), attempts.Load())
			}
		})
	}

	for _, cancelledParent := range []bool{true, false} {
		name := "already-exited"
		if cancelledParent {
			name = "already-cancelled"
		}
		t.Run(name, func(t *testing.T) {
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts.Add(1)
				w.WriteHeader(http.StatusForbidden)
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			exited := make(chan struct{})
			if cancelledParent {
				cancel()
			} else {
				close(exited)
			}
			trace := newMobileLayoutReadinessTrace()
			conn, err := dialMobileLayoutBrowser(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), exited, trace)
			journal := trace.String()
			if !strings.Contains(journal, "phase=stopped attempt=0") || strings.Contains(journal, "phase=handshake-start") || (cancelledParent && !strings.Contains(journal, "parent=context canceled")) || (!cancelledParent && !strings.Contains(journal, "process_exited=true")) {
				t.Fatalf("missing pre-dial stop observations: %s", journal)
			}
			if conn != nil || err == nil || attempts.Load() != 0 {
				t.Fatalf("stopped owner must make zero attempts: %v, %v, %d", conn, err, attempts.Load())
			}
		})
	}

	t.Run("process-exits-during-dial", func(t *testing.T) {
		started := make(chan struct{})
		cancelled := make(chan struct{})
		var attempts atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts.Add(1)
			close(started)
			<-r.Context().Done()
			close(cancelled)
		}))
		defer server.Close()
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		exited := make(chan struct{})
		go func() {
			select {
			case <-started:
				close(exited)
			case <-ctx.Done():
			}
		}()
		before := time.Now()
		trace := newMobileLayoutReadinessTrace()
		conn, err := dialMobileLayoutBrowser(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), exited, trace)
		if journal := trace.String(); !strings.Contains(journal, "phase=handshake-end attempt=1") || !strings.Contains(journal, "phase=stopped attempt=1") || !strings.Contains(journal, "process_exited=true") {
			t.Fatalf("missing process-exit observations: %s", journal)
		}
		if conn != nil || err == nil || !strings.Contains(err.Error(), "owned browser stopped") || time.Since(before) >= time.Second {
			t.Fatalf("process exit must interrupt dial without accepting readiness: %v, %v", conn, err)
		}
		<-cancelled
		if attempts.Load() != 1 {
			t.Fatalf("late attempt after process exit: %d", attempts.Load())
		}
	})
}
