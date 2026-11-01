package reviewrouting

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestOriginalGoldenBytesMatchPinnedUpstreamGitBlobs(t *testing.T) {
	origin := fixture(t, "origin.json")
	files := origin["files"].(map[string]any)
	names := []string{"review-routing-v1.json", "review-routing-wire-v1.json", "review-routing-settlement-v1.json", "pr-identity-v1.json"}
	if len(files) != len(names) {
		t.Fatal("original source manifest must pin all four exact upstream artifacts")
	}
	for _, name := range names {
		raw, err := os.ReadFile("testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != files[name] {
			t.Fatalf("%s bytes differ from the pinned upstream Git blob; check EOL attributes", name)
		}
	}
}

func fixture(t *testing.T, name string) map[string]any {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	value, err := Decode(data, maxJSONBytes)
	if err != nil {
		t.Fatal(err)
	}
	return value.(map[string]any)
}

func TestSharedReportGoldens(t *testing.T) {
	for _, raw := range fixture(t, "review-routing-v1.json")["cases"].([]any) {
		item := raw.(map[string]any)
		t.Run(item["name"].(string), func(t *testing.T) {
			report := item["report"].(map[string]any)
			got, err := Inspect(report, report["head"].(string))
			if err != nil {
				t.Fatal(err)
			}
			if got.Classification != item["classification"] || got.CanonicalRecord != item["canonical_record"] || got.ReportSHA256 != item["sha256"] {
				t.Fatalf("report differs from Python golden: %+v", got)
			}
		})
	}
}

func TestSharedWireGoldens(t *testing.T) {
	for _, name := range []string{"review-routing-wire-v1.json", "review-routing-settlement-v1.json"} {
		for _, raw := range fixture(t, name)["records"].([]any) {
			item := raw.(map[string]any)
			t.Run(name+"/"+item["id"].(string), func(t *testing.T) {
				body := item["body"].(string)
				got, err := Render(item["payload"])
				if err != nil {
					t.Fatal(err)
				}
				if got != body {
					t.Fatalf("wire differs from Python golden:\n%s", got)
				}
				parsed, err := Parse(body)
				if err != nil {
					t.Fatal(err)
				}
				if context, ok := item["context_sha256"].(string); ok {
					gotContext, err := Context(parsed)
					if err != nil || gotContext != context {
						t.Fatalf("context = %q, err = %v; want %q", gotContext, err, context)
					}
				}
			})
		}
	}
}

func TestCanonicalStrictUnicodeAndLargeIntegers(t *testing.T) {
	input := "{\"z\":9007199254740993123456789,\"b\":\"<>&\u2028\u2029/я😀\",\"a\":\"\\b\\f\\n\\r\\t\\u0000\\\"\\\\\"}"
	value, err := Decode([]byte(input), MaxWireBytes)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Canonical(value)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"a\":\"\\b\\f\\n\\r\\t\\u0000\\\"\\\\\",\"b\":\"<>&\u2028\u2029/я😀\",\"z\":9007199254740993123456789}\n"
	if string(got) != want {
		t.Fatalf("canonical bytes differ: %q", got)
	}
	for _, input := range []string{`"\ud83d\ude00"`, `"\\ud800"`} {
		if _, err := Decode([]byte(input), MaxWireBytes); err != nil {
			t.Fatalf("valid escaped input rejected: %s: %v", input, err)
		}
	}
}

func TestDecodeRefusesAmbiguousOrUnboundedInput(t *testing.T) {
	for _, input := range []string{
		`{"a":1,"a":2}`, `{"a":1,"\u0061":2}`, `{"id":1.0}`, `{"id":1e3}`,
		`{"x":NaN}`, `{"x":Infinity}`, `{} true`, `"\ud800"`, `"\udc00"`,
		`"\ud800\u0061"`, `"\ud800\ud800"`, string([]byte{'"', 0xff, '"'}),
		strings.Repeat("[", maxJSONDepth+2) + "0" + strings.Repeat("]", maxJSONDepth+2),
	} {
		t.Run(input, func(t *testing.T) {
			if _, err := Decode([]byte(input), MaxWireBytes); err == nil {
				t.Fatal("ambiguous input accepted")
			}
		})
	}
	if _, err := Decode([]byte(`{"valid":true}`), 1); err == nil {
		t.Fatal("size bound ignored")
	}
}

func clone(t *testing.T, value any) map[string]any {
	t.Helper()
	data, err := Canonical(value)
	if err != nil {
		t.Fatal(err)
	}
	copy, err := Decode(data, MaxReportBytes)
	if err != nil {
		t.Fatal(err)
	}
	return copy.(map[string]any)
}

func TestPublicationRejectsMismatchedEvidenceAndClosedFields(t *testing.T) {
	publication := fixture(t, "review-routing-wire-v1.json")["records"].([]any)[0].(map[string]any)["payload"]
	for _, mutate := range []func(map[string]any){
		func(m map[string]any) { m["outcome"] = "reviewed" },
		func(m map[string]any) { m["budget_used"] = true },
		func(m map[string]any) { m["depth"] = json.Number("2.0") },
		func(m map[string]any) { m["owner"] = "00000000-0000-1000-8000-000000000001" },
		func(m map[string]any) { m["unknown"] = "ignored evidence" },
		func(m map[string]any) { m["report"].(map[string]any)["change"] = "changed exact source" },
		func(m map[string]any) { m["identity"].(map[string]any)["pr_number"] = json.Number("43") },
		func(m map[string]any) { m["identity"].(map[string]any)["cross_repository"] = true },
		func(m map[string]any) { m["author"].(map[string]any)["login"] = "bad login" },
	} {
		m := clone(t, publication)
		mutate(m)
		if err := Validate(m); err == nil {
			t.Fatal("invalid publication accepted")
		}
	}
}

func TestWireRefusesEditsAndFutureMarkers(t *testing.T) {
	body := fixture(t, "review-routing-wire-v1.json")["records"].([]any)[0].(map[string]any)["body"].(string)
	for _, edit := range []string{
		strings.ReplaceAll(body, "\n", "\r\n"), body + " ",
		strings.Replace(body, `"kind":"publication"`, `"kind":"publication", "kind":"publication"`, 1),
		strings.Replace(body, "sha256=", "sha256=0", 1),
		strings.Replace(body, Version, "review-routing-v2", 1),
		strings.Replace(body, "kind=publication", "kind=claim", 1),
	} {
		if _, err := Parse(edit); err == nil {
			t.Fatal("edited wire accepted")
		}
	}
}

func TestOutcomePreservesLifetimeBudgetAndExplicitDecision(t *testing.T) {
	cases := fixture(t, "review-routing-v1.json")["cases"].([]any)
	technical := cases[0].(map[string]any)["report"]
	for _, test := range []struct {
		depth, used, limit int64
		want               string
	}{
		{0, 0, 2, "changes-requested"}, {1, 0, 2, "changes-requested"}, {2, 0, 2, "technical-handoff"},
		{3, 1, 2, "technical-handoff"}, {4, 2, 2, "engineering-handoff"}, {4, 2, 1, "engineering-handoff"},
	} {
		got, err := Outcome(technical, test.depth, test.used, test.limit)
		if err != nil || got != test.want {
			t.Fatalf("%+v: %s, %v", test, got, err)
		}
	}
	productReport := clone(t, cases[1].(map[string]any)["report"])
	productReport["blocking"].([]any)[0].(map[string]any)["summary"] = "purely technical, automate everything"
	if got, err := Outcome(productReport, 2, 0, 2); err != nil || got != "needs-decision" {
		t.Fatalf("product prose changed category: %s %v", got, err)
	}
	if got, err := Outcome(productReport, 0, 0, 2); err != nil || got != "changes-requested" {
		t.Fatalf("ordinary early round changed: %s %v", got, err)
	}
}
