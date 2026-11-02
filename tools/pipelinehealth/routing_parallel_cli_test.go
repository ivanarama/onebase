package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The public command must overlap independent PR reads, while retaining both
// complete passes and refusing a changed history anywhere in the census.
func TestRoutingParallelCensusThroughCLI(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "pipelinehealth")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "./tools/pipelinehealth") //nolint:gosec // Test-owned binary path.
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, output)
	}
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, drift := range []bool{false, true} {
		t.Run(fmt.Sprintf("drift=%v", drift), func(t *testing.T) {
			pulls := []any{}
			snapshots := map[int]map[string]any{}
			for number := 42; number < 49; number++ {
				snapshot := routingClone(t, routingGolden(t, "third-typed-round"))
				identity := routingObject(snapshot["identity"])
				identity["pr_number"] = json.Number(fmt.Sprint(number))
				identity["pr_id"] = fmt.Sprintf("PR_%d", number)
				snapshot["edges"] = []any{}
				snapshot["labels"] = []any{}
				snapshots[number] = snapshot
				pulls = append(pulls, gqlTestPullNode(fmt.Sprintf("PR_%d", number), number, snapshot["headRefOid"].(string), nil, nil))
			}
			var mu sync.Mutex
			calls := map[int]int{}
			active, peak := 0, 0
			ready := make(chan struct{})
			var release sync.Once
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Query     string
					Variables map[string]any
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				var data any
				switch {
				case strings.Contains(request.Query, "query PipelineHealthSnapshot"):
					data = snapshotResult(len(pulls), pulls, false, nil, 0, nil, false, nil)
				case strings.Contains(request.Query, "query PipelineRoutingRepository"):
					data = map[string]any{"repository": map[string]any{"id": "R_BASE", "nameWithOwner": "owner/repo"}}
				case strings.Contains(request.Query, "query PipelineRoutingTimeline"):
					number := int(request.Variables["number"].(float64))
					mu.Lock()
					calls[number]++
					pass := calls[number]
					active++
					peak = max(peak, active)
					if active == 6 {
						release.Do(func() { close(ready) })
					}
					mu.Unlock()
					select {
					case <-ready:
					case <-r.Context().Done():
						return
					case <-time.After(10 * time.Second):
						t.Error("public census serialized independent PR reads")
						release.Do(func() { close(ready) })
					}
					// Clone while constructing each response: no fixture is mutated
					// while another concurrent request serializes the same map.
					encoded, _ := json.Marshal(snapshots[number])
					var snapshot map[string]any
					_ = json.Unmarshal(encoded, &snapshot)
					if drift && number == 42 && pass == 2 {
						snapshot["baseRefOid"] = strings.Repeat("d", 40)
					}
					data = routingPageData(t, snapshot, []any{}, false, "")
					mu.Lock()
					active--
					mu.Unlock()
				default:
					t.Errorf("unexpected query: %s", request.Query)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, "-transport", "graphql", "-repo", "owner/repo", "-owner", "owner", "-json", "-technical-handoff", "-routing-max-attempts", "2", "-contract", filepath.Join(root, ".claude", "skills", "review-queue", "SKILL.md")) //nolint:gosec // Fixed CLI and test-owned paths.
			command.Dir = root
			command.Env = append(os.Environ(), "GH_EXE="+helper, "PIPELINEHEALTH_TEST_GH_HELPER=1", "PIPELINEHEALTH_TEST_GH_ROUTING_SERVER="+server.URL)
			output, err := command.Output()
			if err != nil {
				t.Fatalf("CLI: %v %s", err, output)
			}
			var got report
			if err := json.Unmarshal(output, &got); err != nil {
				t.Fatal(err)
			}
			if (got.ReviewRoutingContract != nil) != !drift {
				t.Fatalf("complete contract after drift=%v: %+v", drift, got.ReviewRoutingContract)
			}
			mu.Lock()
			defer mu.Unlock()
			if peak != 6 || active != 0 || len(calls) != 7 {
				t.Fatalf("peak=%d active=%d calls=%v", peak, active, calls)
			}
			for number, count := range calls {
				if count != 2 {
					t.Errorf("PR%d read %d passes, want2", number, count)
				}
			}
		})
	}
}

func TestConcurrentGraphQLQueriesKeepOnePhysicalBudget(t *testing.T) {
	var calls atomic.Int32
	client := &limitedPipelineGraphQLClient{used: maxPipelineGraphQLQueries - 3, delegate: &ghPipelineGraphQLClient{run: func(_ context.Context, _ string, _ []byte, stdout, _ io.Writer) error {
		calls.Add(1)
		_, err := io.WriteString(stdout, `{"data":{"value":1}}`)
		return err
	}}}
	var workers sync.WaitGroup
	var succeeded atomic.Int32
	for range 32 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			var result map[string]any
			if err := client.Query("query Budget { value }", nil, &result); err == nil {
				succeeded.Add(1)
			}
		}()
	}
	workers.Wait()
	if calls.Load() != 3 || succeeded.Load() != 3 || client.used != maxPipelineGraphQLQueries {
		t.Fatalf("physical requests=%d succeeded=%d budget=%d", calls.Load(), succeeded.Load(), client.used)
	}
}
