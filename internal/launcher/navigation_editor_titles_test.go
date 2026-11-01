package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"reflect"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"gopkg.in/yaml.v3"
)

func navigationTitlesSubprocessContext(t *testing.T, budget time.Duration) (context.Context, context.CancelFunc) {
	t.Helper()
	deadline := time.Now().Add(budget)
	if outer, ok := t.Deadline(); ok && outer.Add(-5*time.Second).Before(deadline) {
		// Leave time for reporting and cleanup before the package timeout.
		deadline = outer.Add(-5 * time.Second)
	}
	return context.WithDeadline(context.Background(), deadline)
}

func runNavigationTitlesSubprocess(ctx context.Context, pattern, scenario string, env []string) error {
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run="+pattern, "-test.v") //nolint:gosec // Test subprocess uses the current test executable and fixed test patterns.
	cmd.Env = append(os.Environ(), env...)
	started := time.Now()
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	if len(output) > 4096 {
		// A stack trace or assertion at the end is as useful as startup output.
		output = []byte(fmt.Sprintf("%s\n... %d bytes omitted ...\n%s", output[:2048], len(output)-4096, output[len(output)-2048:]))
	}
	deadline, _ := ctx.Deadline()
	return fmt.Errorf("HTTP round trip subprocess %q: process error: %w; context error: %v; deadline exceeded: %t; deadline: %s; elapsed: %s\n%s",
		scenario, err, ctx.Err(), errors.Is(ctx.Err(), context.DeadlineExceeded), deadline.Format(time.RFC3339Nano), time.Since(started), output)
}

// Exercise the actual subprocess runner: a hung child must be killed, while a
// child assertion must remain distinguishable from a wrapper deadline.
func TestNavigationEditorTitlesSubprocessFailure(t *testing.T) {
	if control := os.Getenv("ONEBASE_NAVIGATION_TITLES_FAILURE_CONTROL"); control != "" {
		fmt.Println("subprocess diagnostic beginning")
		if control != "cycle" {
			fmt.Println(strings.Repeat("x", 8192))
		}
		fmt.Println("subprocess diagnostic ending")
		if control == "cycle" {
			debug.SetMaxStack(2 << 20)
			node := &yaml.Node{Kind: yaml.MappingNode}
			node.Content = []*yaml.Node{{Kind: yaml.ScalarNode, Value: "cycle"}, node}
			if _, err := yaml.Marshal(node); err != nil {
				t.Fatal(err)
			}
			t.Fatal("cyclic encoder unexpectedly returned")
		}
		if control == "exit" {
			t.Fatal("synthetic HTTP/YAML failure")
		}
		for {
			time.Sleep(time.Hour)
		}
	}
	// Run the timeout last: it can consume the remaining outer test budget.
	for _, control := range []string{"exit", "cycle", "deadline"} {
		t.Run(control, func(t *testing.T) {
			ctx, cancel := navigationTitlesSubprocessContext(t, 5*time.Second)
			defer cancel()
			err := runNavigationTitlesSubprocess(ctx, "^TestNavigationEditorTitlesSubprocessFailure$", control,
				[]string{"ONEBASE_NAVIGATION_TITLES_FAILURE_CONTROL=" + control})
			if err == nil {
				t.Fatal("failing subprocess reported success")
			}
			for _, want := range []string{"subprocess \"" + control + "\"", "process error:", "context error:",
				"deadline exceeded: " + fmt.Sprint(control == "deadline"), "bytes omitted",
				"subprocess diagnostic beginning", "subprocess diagnostic ending"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("missing diagnostic %q: %v", want, err)
				}
			}
			if control == "deadline" && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
				t.Fatalf("hung child was not stopped by deadline: %v", err)
			}
			if control == "exit" && (!strings.Contains(err.Error(), "synthetic HTTP/YAML failure") || ctx.Err() != nil) {
				t.Fatalf("child assertion confused with wrapper timeout: %v", err)
			}
			if control == "cycle" && (!strings.Contains(err.Error(), "fatal error: stack overflow") || ctx.Err() != nil) {
				t.Fatalf("encoder cycle did not report a child stack overflow: %v", err)
			}
		})
	}
}

// Run saves in a subprocess so an encoder cycle reports a regression instead
// of exhausting the stack of the whole package's test process.
func TestNavigationEditorTitlesIDProductionRoundTrip(t *testing.T) {
	selected := os.Getenv("ONEBASE_NAVIGATION_TITLES_CASE")
	if selected != "" {
		debug.SetMaxStack(2 << 20)
	}
	for _, mode := range []string{"file", "database"} {
		for _, scope := range []string{"subsystem", "global"} {
			for _, collision := range []string{"section-section", "section-item", "group-group", "item-item"} {
				name := mode + "/" + scope + "/" + collision
				if selected != "" && selected != name {
					continue
				}
				t.Run(name, func(t *testing.T) {
					if selected == "" {
						ctx, cancel := navigationTitlesSubprocessContext(t, 30*time.Second)
						defer cancel()
						if err := runNavigationTitlesSubprocess(ctx, "^TestNavigationEditorTitlesIDProductionRoundTrip$", name,
							[]string{"ONEBASE_NAVIGATION_TITLES_CASE=" + name}); err != nil {
							t.Fatal(err)
						}
						return
					}
					h, b := newNavigationEditorFixture(t, mode, "subsystems/school.yaml")
					original := navigationEditorFixture
					switch collision {
					case "section-section":
						original = strings.Replace(original, "en: English education", "en: English education\n        id: education # Indonesian section title", 1)
					case "section-item":
						original = strings.Replace(original, "en: English education", "en: English education\n        id: a # Indonesian section title", 1)
					case "group-group":
						original = strings.Replace(original, "title: Year", "title: Year\n          titles:\n            en: English year\n            id: year # Indonesian group title", 1)
					case "item-item":
						original = strings.Replace(original, "target: catalog:B", "target: catalog:B\n              titles:\n                en: English B\n                id: b # Indonesian item title", 1)
					}
					path, subsystem := "subsystems/school.yaml", "School"
					if scope == "global" {
						path, subsystem = "config/home_page.yaml", ""
						original = strings.Replace(original, "contents:\n", "nav:\n", 1)
					}
					if err := h.writeConfigFileRaw(context.Background(), b, path, []byte(original)); err != nil {
						t.Fatal(err)
					}
					db, err := OpenDB(context.Background(), b)
					if err != nil {
						t.Fatal(err)
					}
					repo := auth.NewRepo(db)
					if err := repo.EnsureSchema(context.Background()); err != nil {
						t.Fatal(err)
					}
					user, err := repo.Create(context.Background(), "admin", "Str0ng-Passw0rd!", "Admin", true)
					if err != nil {
						t.Fatal(err)
					}
					token, err := repo.CreateSession(context.Background(), user.ID, auth.SessionMeta{Kind: auth.SessionKindConfigurator})
					if err != nil {
						t.Fatal(err)
					}
					db.Close()
					srv, err := NewServer(h.store, h.runner)
					if err != nil {
						t.Fatal(err)
					}
					done := make(chan error, 1)
					go func() { done <- srv.ListenAndServe() }()
					t.Cleanup(func() { srv.Close(); <-done })
					client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
					call := func(method, endpoint, body string) navigationEditorData {
						t.Helper()
						req, err := http.NewRequest(method, srv.URL()+"/bases/test/configurator/navigation"+endpoint, strings.NewReader(body))
						if err != nil {
							t.Fatal(err)
						}
						req.Header.Set("Content-Type", "application/json")
						req.Header.Set("Accept", "application/json")
						req.AddCookie(&http.Cookie{Name: configuratorSessionCookieName, Value: token})
						res, err := client.Do(req)
						if err != nil {
							t.Fatal(err)
						}
						defer func() { _ = res.Body.Close() }()
						raw, err := io.ReadAll(res.Body)
						if err != nil {
							t.Fatal(err)
						}
						if res.StatusCode != http.StatusOK {
							t.Fatalf("%s %s: HTTP %d %s", method, endpoint, res.StatusCode, raw)
						}
						var data navigationEditorData
						if err := json.Unmarshal(raw, &data); err != nil {
							t.Fatal(err)
						}
						return data
					}
					data := call(http.MethodGet, "?subsystem="+subsystem, "")
					var expected struct {
						Menu *metadata.Menu `yaml:"menu"`
					}
					if err := yaml.Unmarshal([]byte(original), &expected); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(data.Menu, expected.Menu) {
						t.Fatalf("GET changed translations or IDs: %+v", data.Menu)
					}
					for save := 0; save < 3; save++ {
						if save == 2 {
							// A moved typed item must retain its own YAML node,
							// translations and comments despite titles.id.
							for _, menu := range []*metadata.Menu{data.Menu, expected.Menu} {
								section := &menu.Sections[0]
								section.Items = append(section.Items, section.Groups[0].Items[0])
								section.Groups[0].Items = nil
							}
						}
						response := call(http.MethodPost, "/save", navigationEditorBody(t, subsystem, data.Menu))
						raw, ok := h.readConfigFileRaw(context.Background(), b, path)
						if !ok {
							t.Fatal("source YAML disappeared")
						}
						var saved struct {
							Menu *metadata.Menu `yaml:"menu"`
						}
						if err := yaml.Unmarshal(raw, &saved); err != nil {
							t.Fatal(err)
						}
						if !reflect.DeepEqual(saved.Menu, expected.Menu) || !reflect.DeepEqual(response.Menu, expected.Menu) {
							t.Fatalf("save lost translations or stable IDs:\n%s", raw)
						}
						for _, comment := range []string{"# school configuration", "# unrelated permissions", "# section comment", "# item B travels with this comment", fmt.Sprintf("# Indonesian %s title", strings.Split(collision, "-")[0])} {
							if !strings.Contains(string(raw), comment) {
								t.Fatalf("save lost comment %q:\n%s", comment, raw)
							}
						}
						data = call(http.MethodGet, "?subsystem="+subsystem, "")
						if !reflect.DeepEqual(data.Menu, expected.Menu) {
							t.Fatal("reloaded editor lost translations or stable IDs")
						}
					}
				})
			}
		}
	}
}
