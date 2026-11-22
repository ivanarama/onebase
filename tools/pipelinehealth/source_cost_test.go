package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const sourceTestNonce = "0123456789abcdef0123456789abcdef"

func sourceTestMeter(t *testing.T, allowance int64) *sourceGraphQLMeter {
	t.Helper()
	path := filepath.Join(t.TempDir(), "contract.md")
	if err := os.WriteFile(path, []byte("immutable test contract\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	meter, err := newSourceGraphQLMeter(allowance, sourceTestNonce, "ivanarama/onebase", "ivanarama", path)
	if err != nil {
		t.Fatal(err)
	}
	return meter
}

func sourceCostResponse(t *testing.T, cost any, viewer bool) string {
	t.Helper()
	data := map[string]any{sourceGraphQLRateAlias: map[string]any{"cost": cost}}
	if viewer {
		data[sourceGraphQLViewerAlias] = map[string]any{"login": "ivanarama"}
		data["repository"] = map[string]any{"id": "R_onebase", "nameWithOwner": "ivanarama/onebase"}
	}
	raw, err := json.Marshal(map[string]any{"data": data})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func sourceTestClient(meter *sourceGraphQLMeter, run pipelineGraphQLCommandRunner) *ghPipelineGraphQLClient {
	return &ghPipelineGraphQLClient{sourceCost: meter, run: run}
}

func TestSourceCostCentralQueryPreRequestEscrowAndCleanSettlement(t *testing.T) {
	meter := sourceTestMeter(t, 20)
	calls := 0
	client := sourceTestClient(meter, func(_ context.Context, _ string, input []byte, stdout, _ io.Writer) error {
		calls++
		var request struct{ Query string }
		if err := json.Unmarshal(input, &request); err != nil {
			t.Fatal(err)
		}
		if calls == 1 {
			if meter.escrow != 20 || meter.usage.ChargedUpperBound != 0 || !strings.Contains(request.Query, "dryRun: true") {
				t.Fatal("preview did not reserve the complete remaining allowance before physical send")
			}
			_, _ = io.WriteString(stdout, sourceCostResponse(t, 3, false))
		} else {
			if meter.escrow != 3 || meter.usage.ChargedUpperBound != 3 || strings.Contains(request.Query, "dryRun: true") {
				t.Fatal("data did not independently reserve the exact preview quote")
			}
			_, _ = io.WriteString(stdout, sourceCostResponse(t, 2, true))
		}
		return nil
	})
	var data map[string]any
	if err := client.Query(routingRepositoryQuery, nil, &data); err != nil {
		t.Fatal(err)
	}
	usage := meter.receipt(true)
	if !usage.Complete || usage.UncertainCost || usage.ChargedUpperBound != 5 || usage.ObservedActualCost != 5 ||
		usage.PhysicalAttempts != 2 || usage.PreviewAttempts != 1 || usage.DataAttempts != 1 || usage.UnprovedAttempts != 0 {
		t.Fatalf("wrong complete usage: %+v", usage)
	}
	if _, leaked := data[sourceGraphQLRateAlias]; leaked || len(data) != 1 {
		t.Fatalf("cost instrumentation leaked into canonical reader data: %v", data)
	}
}

func TestSourceCostAllowancePreventsUnreservedDataSend(t *testing.T) {
	meter := sourceTestMeter(t, 3)
	calls := 0
	client := sourceTestClient(meter, func(_ context.Context, _ string, _ []byte, stdout, _ io.Writer) error {
		calls++
		_, _ = io.WriteString(stdout, sourceCostResponse(t, 2, false))
		return nil
	})
	var data any
	if err := client.Query(routingRepositoryQuery, nil, &data); err == nil {
		t.Fatal("data sent without its pre-request allowance")
	}
	usage := meter.receipt(false)
	if calls != 1 || usage.ChargedUpperBound != 2 || usage.ObservedActualCost != 2 || usage.DataAttempts != 0 || usage.UncertainCost || usage.Complete {
		t.Fatalf("allowance fence failed: calls=%d usage=%+v", calls, usage)
	}
}

func TestSourceCostRejectsUnprovedPhysicalReceiptAndNeverRetries(t *testing.T) {
	for _, stage := range []string{"preview", "data"} {
		for _, fault := range []string{"missing", "zero", "negative", "fraction", "exponent", "string", "bool", "overflow", "duplicate", "errors-null", "errors-object", "partial-errors", "gateway", "timeout", "truncated"} {
			t.Run(stage+"/"+fault, func(t *testing.T) {
				meter := sourceTestMeter(t, 10)
				calls := 0
				client := sourceTestClient(meter, func(ctx context.Context, _ string, _ []byte, stdout, stderr io.Writer) error {
					calls++
					if stage == "data" && calls == 1 {
						_, _ = io.WriteString(stdout, sourceCostResponse(t, 2, false))
						return nil
					}
					raw := sourceCostResponse(t, 1, true)
					switch fault {
					case "missing":
						raw = `{"data":{"repository":{}}}`
					case "zero":
						raw = sourceCostResponse(t, 0, true)
					case "negative":
						raw = sourceCostResponse(t, -1, true)
					case "fraction":
						raw = sourceCostResponse(t, 1.5, true)
					case "exponent":
						raw = strings.Replace(raw, `"cost":1`, `"cost":1e0`, 1)
					case "string":
						raw = sourceCostResponse(t, "1", true)
					case "bool":
						raw = sourceCostResponse(t, true, true)
					case "overflow":
						raw = sourceCostResponse(t, int64(2147483648), true)
					case "duplicate":
						raw = strings.Replace(raw, `"cost":1`, `"cost":1,"cost":1`, 1)
					case "errors-null":
						raw = strings.TrimSuffix(raw, "}") + `,"errors":null}`
					case "errors-object":
						raw = strings.TrimSuffix(raw, "}") + `,"errors":{}}`
					case "partial-errors":
						raw = strings.TrimSuffix(raw, "}") + `,"errors":[{"message":"partial"}]}`
					case "gateway":
						_, _ = io.WriteString(stderr, "gh: HTTP 504\n")
						return errors.New("exit1")
					case "timeout":
						<-ctx.Done()
						return ctx.Err()
					case "truncated":
						raw = `{"data":`
					}
					_, _ = io.WriteString(stdout, raw)
					return nil
				})
				client.timeout = 100 * time.Millisecond
				bounded := limitPipelineGraphQLClient(client)
				destination := map[string]any{"sentinel": "unchanged"}
				if err := bounded.Query(routingRepositoryQuery, nil, &destination); err == nil {
					t.Fatal("unproved physical receipt accepted")
				}
				usage := meter.receipt(true)
				wantCalls, wantHeld := 1, int64(10)
				if stage == "data" {
					wantCalls, wantHeld = 2, 4
				}
				if calls != wantCalls || usage.Complete || !usage.UncertainCost || usage.UnprovedAttempts != 1 ||
					usage.ChargedUpperBound != wantHeld || destination["sentinel"] != "unchanged" {
					t.Fatalf("uncertainty/refund/retry leak: calls=%d usage=%+v destination=%v", calls, usage, destination)
				}
				if err := bounded.Query(routingRepositoryQuery, nil, &destination); err == nil || calls != wantCalls {
					t.Fatal("poisoned invocation recovered or made another physical call")
				}
			})
		}
	}
}

func TestSourceCostOverquoteAndViewerMismatchCannotComplete(t *testing.T) {
	for _, fault := range []string{"preview-over-cap", "data-over-preview", "viewer"} {
		t.Run(fault, func(t *testing.T) {
			meter := sourceTestMeter(t, 10)
			calls := 0
			client := sourceTestClient(meter, func(_ context.Context, _ string, _ []byte, stdout, _ io.Writer) error {
				calls++
				cost := 2
				if fault == "preview-over-cap" || calls == 2 && fault == "data-over-preview" {
					cost = 11
				}
				raw := sourceCostResponse(t, cost, calls == 2)
				if fault == "viewer" && calls == 2 {
					raw = strings.Replace(raw, `"login":"ivanarama"`, `"login":"other"`, 1)
				}
				_, _ = io.WriteString(stdout, raw)
				return nil
			})
			var data any
			if err := client.Query(routingRepositoryQuery, nil, &data); err == nil || meter.receipt(true).Complete {
				t.Fatal("overquote or wrong authenticated viewer became a complete receipt")
			}
		})
	}
}

func TestSourceCostSharedMeterAcrossClientsAndConcurrentPairs(t *testing.T) {
	meter := sourceTestMeter(t, 8)
	active, calls := 0, 0
	var guard sync.Mutex
	runner := func(_ context.Context, _ string, input []byte, stdout, _ io.Writer) error {
		guard.Lock()
		active++
		if active != 1 {
			t.Error("two physical requests spent the same pending preview escrow")
		}
		calls++
		guard.Unlock()
		time.Sleep(time.Millisecond)
		_, _ = io.WriteString(stdout, sourceCostResponse(t, 1, !strings.Contains(string(input), "dryRun: true")))
		guard.Lock()
		active--
		guard.Unlock()
		return nil
	}
	var workers sync.WaitGroup
	for range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			var data any
			if err := sourceTestClient(meter, runner).Query(routingRepositoryQuery, nil, &data); err != nil {
				t.Error(err)
			}
		}()
	}
	workers.Wait()
	usage := meter.receipt(true)
	if calls != 8 || !usage.Complete || usage.ChargedUpperBound != 8 || usage.PreviewAttempts != 4 || usage.DataAttempts != 4 {
		t.Fatalf("shared meter reset across clients: calls=%d usage=%+v", calls, usage)
	}
}

func TestSourceCostContractAndFullReportRequired(t *testing.T) {
	for _, fault := range []string{"report-incomplete", "contract-changed"} {
		t.Run(fault, func(t *testing.T) {
			meter := sourceTestMeter(t, 4)
			client := sourceTestClient(meter, func(_ context.Context, _ string, input []byte, stdout, _ io.Writer) error {
				_, _ = io.WriteString(stdout, sourceCostResponse(t, 1, !strings.Contains(string(input), "dryRun: true")))
				return nil
			})
			var data any
			if err := client.Query(routingRepositoryQuery, nil, &data); err != nil {
				t.Fatal(err)
			}
			if fault == "contract-changed" {
				if err := os.WriteFile(meter.contract, []byte("changed"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if meter.receipt(fault != "report-incomplete").Complete {
				t.Fatal("incomplete or changed-input observation became complete")
			}
		})
	}
}

func TestSourceCostPreservesExisting64MiBResponseBound(t *testing.T) {
	meter := sourceTestMeter(t, 4)
	client := sourceTestClient(meter, func(_ context.Context, _ string, input []byte, stdout, _ io.Writer) error {
		if strings.Contains(string(input), "dryRun: true") {
			_, _ = io.WriteString(stdout, sourceCostResponse(t, 1, false))
			return nil
		}
		raw := sourceCostResponse(t, 1, true)
		padding := maxPipelineGraphQLResponseBytes - 1 - len(raw) - len(`,"padding":""`)
		raw = strings.Replace(raw, `"id":"R_onebase"`, `"id":"R_onebase","padding":"`+strings.Repeat("x", padding)+`"`, 1)
		if len(raw) != maxPipelineGraphQLResponseBytes-1 {
			t.Fatalf("large-string fixture size=%d", len(raw))
		}
		_, _ = io.WriteString(stdout, raw)
		return nil
	})
	var data map[string]any
	if err := client.Query(routingRepositoryQuery, nil, &data); err != nil || !meter.receipt(true).Complete {
		t.Fatalf("cost parser replaced existing64MiB transport bound with8MiB: %v", err)
	}
}

func TestSourceCostDenseNodesAndDepthFailClosed(t *testing.T) {
	for _, fault := range []string{"dense-nodes", "depth"} {
		t.Run(fault, func(t *testing.T) {
			meter := sourceTestMeter(t, 4)
			client := sourceTestClient(meter, func(_ context.Context, _ string, input []byte, stdout, _ io.Writer) error {
				if strings.Contains(string(input), "dryRun: true") {
					_, _ = io.WriteString(stdout, sourceCostResponse(t, 1, false))
					return nil
				}
				padding := "[" + strings.Repeat("0,", sourceCostMaxJSONNodes) + "0]"
				if fault == "depth" {
					padding = strings.Repeat("[", sourceCostMaxJSONDepth+1) + "0" + strings.Repeat("]", sourceCostMaxJSONDepth+1)
				}
				raw := strings.Replace(sourceCostResponse(t, 1, true), `"data":{`, `"data":{"padding":`+padding+",", 1)
				_, _ = io.WriteString(stdout, raw)
				return nil
			})
			data := map[string]any{"sentinel": "unchanged"}
			if err := client.Query(routingRepositoryQuery, nil, &data); err == nil {
				t.Fatal("unbounded node/depth response accepted")
			}
			usage := meter.receipt(true)
			if usage.Complete || !usage.UncertainCost || usage.UnprovedAttempts != 1 || data["sentinel"] != "unchanged" {
				t.Fatalf("resource-bound refusal leaked data or proof: %+v", usage)
			}
		})
	}
}

func TestSourceCostPhysicalBoundIncludesPreview(t *testing.T) {
	meter := sourceTestMeter(t, 10)
	meter.usage.PhysicalAttempts = maxPipelineGraphQLQueries - 1
	calls := 0
	client := sourceTestClient(meter, func(_ context.Context, _ string, _ []byte, stdout, _ io.Writer) error {
		calls++
		_, _ = io.WriteString(stdout, sourceCostResponse(t, 1, false))
		return nil
	})
	var data any
	if err := client.Query(routingRepositoryQuery, nil, &data); err == nil || calls != 1 || meter.usage.PhysicalAttempts != maxPipelineGraphQLQueries {
		t.Fatalf("preview escaped existing physical bound: calls=%d err=%v", calls, err)
	}
}

func TestSourceCostContractDriftCannotBeMaskedAsAllowanceWait(t *testing.T) {
	meter := sourceTestMeter(t, 1)
	client := sourceTestClient(meter, func(_ context.Context, _ string, _ []byte, stdout, _ io.Writer) error {
		_, _ = io.WriteString(stdout, sourceCostResponse(t, 1, false))
		return nil
	})
	var data any
	if err := client.Query(routingRepositoryQuery, nil, &data); err == nil {
		t.Fatal("expected finite allowance refusal")
	}
	if err := os.WriteFile(meter.contract, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if usage := meter.receipt(false); usage.Reason != "contract_changed" || usage.Complete {
		t.Fatalf("provenance failure was masked by allowance: %+v", usage)
	}
}
