package ui

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Exercise the same owned-process helper with a real browser whose launcher
// deliberately takes longer than the old budget. The two expected failures run
// in subprocesses so Fatal and the helper's actual Cleanup both execute.
func TestMobileLayoutBrowserStartupBudget(t *testing.T) {
	browser := managedLayoutTestBrowser(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Run("delayed-ready", func(t *testing.T) {
		t.Setenv("OB_LAYOUT_STARTUP_PROCESS", "delay")
		t.Setenv("OB_LAYOUT_STARTUP_BROWSER", browser)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprint(w, `<html><body><pre id="list-mobile-measure">{"ready":true}</pre></body></html>`)
		}))
		t.Cleanup(server.Close)
		start := time.Now()
		b := newMobileLayoutBrowser(t, executable, "-test.run=^TestMobileLayoutStartupProcess$", "--")
		if elapsed := time.Since(start); elapsed < 31*time.Second {
			t.Fatalf("controlled browser startup was not delayed beyond 30 seconds: %s", elapsed)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		data, err := b.measure(ctx, server.URL)
		if err != nil || string(data) != `{"ready":true}` {
			t.Fatalf("real delayed browser measurement: %s, %v", data, err)
		}
	})
	for _, mode := range []string{"exit", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 135*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestMobileLayoutStartupFailure$") //nolint:gosec // exact current test executable and fixed test name
			cmd.Env = append(os.Environ(), "OB_LAYOUT_STARTUP_FAILURE="+mode)
			var output bytes.Buffer
			cmd.Stdout, cmd.Stderr = &output, &output
			start := time.Now()
			stopTree, err := startMobileBrowserProcess(cmd)
			if err != nil {
				t.Fatal(err)
			}
			// The fixture owns its browser tree; this outer owner also bounds the
			// whole fixture if a regression prevents its cleanup from returning.
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case err = <-done:
				if stopErr := stopTree(); stopErr != nil {
					t.Errorf("stop startup fixture: %v", stopErr)
				}
			case <-ctx.Done():
				_ = stopTree()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("startup fixture process tree did not exit")
				}
				t.Fatalf("startup fixture exceeded its outer deadline: %v", ctx.Err())
			}
			elapsed, text := time.Since(start), output.String()
			if err == nil {
				t.Fatalf("unready browser unexpectedly succeeded:\n%s", text)
			}
			want := "browser stopped before ready:"
			if mode == "exit" {
				if elapsed > 30*time.Second || !strings.Contains(text, "controlled startup exit") {
					t.Fatalf("process exit was not diagnosed promptly (%s):\n%s", elapsed, text)
				}
			} else {
				want = "browser readiness: context deadline exceeded"
				if elapsed < 119*time.Second {
					t.Fatalf("startup deadline expired before the new budget (%s):\n%s", elapsed, text)
				}
			}
			if !strings.Contains(text, want) || !strings.Contains(text, "owned startup fixture cleanup completed") {
				t.Fatalf("missing startup failure or cleanup evidence:\n%s", text)
			}
			for _, unexpected := range []string{"stop owned browser process tree:", "browser process tree did not exit", "unready browser was accepted"} {
				if strings.Contains(text, unexpected) {
					t.Fatalf("startup failure did not preserve owned cleanup:\n%s", text)
				}
			}
		})
	}
}

func TestMobileLayoutStartupFailure(t *testing.T) {
	mode := os.Getenv("OB_LAYOUT_STARTUP_FAILURE")
	if mode == "" {
		t.Skip("startup failure subprocess only")
	}
	if mode != "exit" && mode != "deadline" {
		t.Fatal("unexpected startup fixture mode")
	}
	t.Setenv("OB_LAYOUT_STARTUP_PROCESS", mode)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fmt.Println("owned startup fixture cleanup completed") })
	newMobileLayoutBrowser(t, executable, "-test.run=^TestMobileLayoutStartupProcess$", "--")
	t.Fatal("unready browser was accepted")
}

func TestMobileLayoutStartupProcess(t *testing.T) {
	mode := os.Getenv("OB_LAYOUT_STARTUP_PROCESS")
	if mode == "" {
		t.Skip("controlled browser launcher subprocess only")
	}
	switch mode {
	case "exit":
		fmt.Fprintln(os.Stderr, "controlled startup exit")
		os.Exit(23)
	case "deadline":
		fmt.Fprintln(os.Stderr, "controlled startup remains unready")
		time.Sleep(3 * time.Minute)
		os.Exit(24)
	case "delay":
		time.Sleep(31 * time.Second)
	default:
		t.Fatal("unexpected controlled browser mode")
	}
	var browserArgs []string
	for i, arg := range os.Args {
		if arg == "--" {
			browserArgs = os.Args[i+1:]
			break
		}
	}
	if len(browserArgs) == 0 || !strings.HasPrefix(browserArgs[0], "--") {
		t.Fatal("missing controlled browser arguments")
	}
	// Resolve the same closed browser allow-list in the child. The parent's
	// identity check proves this is the browser used for the capability probe.
	browser := managedLayoutTestBrowser(t)
	if browser != os.Getenv("OB_LAYOUT_STARTUP_BROWSER") {
		t.Fatal("controlled browser identity changed")
	}
	cmd := exec.Command(browser, browserArgs...) //nolint:gosec // resolved closed browser allow-list and inherited fixed helper arguments
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
}
