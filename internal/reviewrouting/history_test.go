package reviewrouting

import (
	"encoding/json"
	"strings"
	"testing"
)

func fixtureRecords(t *testing.T, fixture map[string]any) []Record {
	t.Helper()
	result := []Record{}
	for _, raw := range fixture["records"].([]any) {
		item := raw.(map[string]any)
		payload, err := Parse(item["body"].(string))
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, Record{item["id"].(string), item["created_at"].(string), payload})
	}
	return result
}

func TestSharedLifetimeLedgerGoldens(t *testing.T) {
	base := fixture(t, "review-routing-wire-v1.json")
	for _, name := range []string{"review-routing-wire-v1.json", "review-routing-settlement-v1.json"} {
		t.Run(name, func(t *testing.T) {
			value := fixture(t, name)
			records := fixtureRecords(t, value)
			if count, ok := value["base_record_count"]; ok {
				n, err := integer(count, 0, 4096)
				if err != nil {
					t.Fatal(err)
				}
				records = append(fixtureRecords(t, base)[:n:n], records...)
			}
			ledger, err := Reduce(records, nil, 16)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Canonical(ledger.Values())
			if err != nil {
				t.Fatal(err)
			}
			want, err := Canonical(value["expected_ledger"])
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Fatalf("Go lifetime ledger differs from Python:\ngot %s\nwant %s", got, want)
			}
		})
	}
}

func TestSharedIdentityGoldens(t *testing.T) {
	for _, raw := range fixture(t, "pr-identity-v1.json")["cases"].([]any) {
		item := raw.(map[string]any)
		pr := item["graphql"].(map[string]any)
		t.Run(item["name"].(string), func(t *testing.T) {
			base, head := pr["baseRepository"].(map[string]any), pr["headRepository"].(map[string]any)
			identity := map[string]any{"base_repository_id": base["id"], "base_repository": base["nameWithOwner"],
				"base_ref": pr["baseRefName"], "pr_id": pr["id"], "pr_number": pr["number"],
				"head_repository_id": head["id"], "head_repository": head["nameWithOwner"],
				"head_ref": pr["headRefName"], "head_sha": pr["headRefOid"], "cross_repository": pr["isCrossRepository"]}
			digest, err := IdentityDigest(identity)
			if err != nil || digest != item["identity_sha256"] {
				t.Fatalf("identity hash %q, err %v differs from Python %q", digest, err, item["identity_sha256"])
			}
		})
	}
}

func TestSharedVerifiedHistoricalDepthGoldens(t *testing.T) {
	for _, filename := range []string{"review-routing-history-v1.json", "review-routing-operational-history-v1.json"} {
		for _, raw := range fixture(t, filename)["cases"].([]any) {
			item := raw.(map[string]any)
			t.Run(filename+"/"+item["name"].(string), func(t *testing.T) {
				state := item["snapshot"].(map[string]any)
				got, err := VerifiedDepth(state, item["trusted"].(string))
				if (err == nil) != item["accepted"].(bool) {
					t.Fatalf("historical proof differs from Python oracle: %v; expected accepted=%v (%v)", err, item["accepted"], item["reason"])
				}
				if err != nil {
					return
				}
				depth, _ := integer(item["depth"], 0, 2147483647)
				if got.Depth != int(depth) || !equalValue(got.Ledger.Values(), item["ledger"]) {
					t.Fatalf("depth/ledger differs from Python: %d", got.Depth)
				}
				audit, err := Capture(state)
				if err != nil || !equalValue(audit, item["audit"]) {
					t.Fatalf("audit baseline differs from Python: %v", err)
				}
			})
		}
	}
}

func TestAuditAcceptsOnlyExactOwnPublicationSuffix(t *testing.T) {
	var snapshot map[string]any
	for _, raw := range fixture(t, "review-routing-history-v1.json")["cases"].([]any) {
		item := raw.(map[string]any)
		if item["name"] == "third-typed-round" {
			snapshot = item["snapshot"].(map[string]any)
			break
		}
	}
	if snapshot == nil {
		t.Fatal("missing shared typed history")
	}
	records, _, err := ReadRecords(snapshot, "owner")
	if err != nil {
		t.Fatal(err)
	}
	root := records[0].Payload
	binding := root["audit"].(map[string]any)
	context, err := Context(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyAudit(snapshot, binding, context, "owner"); err != nil {
		t.Fatal(err)
	}
	advanced := clone(t, snapshot)
	advanced["baseRefOid"] = strings.Repeat("c", 40)
	advanced["updatedAt"] = "2026-10-29T00:20:00Z"
	if err := VerifyAudit(advanced, binding, context, "owner"); err != nil {
		t.Fatalf("normal base advance invalidated audit: %v", err)
	}
	for _, mutate := range []func(map[string]any){
		func(m map[string]any) { m["labels"] = []any{"hold"} },
		func(m map[string]any) { m["author"] = map[string]any{"id": "OTHER", "login": "other"} },
		func(m map[string]any) { m["edges"].([]any)[0].(map[string]any)["cursor"] = "REPLACED" },
		func(m map[string]any) {
			m["edges"] = append(m["edges"].([]any), map[string]any{"cursor": "INDEPENDENT", "node": map[string]any{"id": "INDEPENDENT", "__typename": "LabeledEvent", "label": map[string]any{"name": "ship"}}})
		},
		func(m map[string]any) {
			values := m["edges"].([]any)
			values[len(values)-1].(map[string]any)["node"].(map[string]any)["author"] = map[string]any{"login": "contributor"}
		},
	} {
		state := clone(t, snapshot)
		mutate(state)
		if err := VerifyAudit(state, binding, context, "owner"); err == nil {
			t.Fatal("changed prefix or independent suffix was erased")
		}
	}
}

func TestLifetimeBudgetNeverResetsFromDeletionOrLostProof(t *testing.T) {
	records := fixtureRecords(t, fixture(t, "review-routing-wire-v1.json"))
	for _, deletion := range [][]string{{"2026-10-29T00:00:00Z"}, {"unparseable deletion still means unknown"}} {
		if _, err := Reduce(nil, deletion, 16); err == nil {
			t.Fatal("deletion reset empty lifetime budget")
		}
	}
	// Independent Python oracle: author START is optional; the final REVIEW
	// may be unfinished. These omissions retain both spent slots. Detecting
	// a removed server comment additionally requires the complete deletion fence.
	for remove := range records {
		copy := append([]Record{}, records[:remove]...)
		copy = append(copy, records[remove+1:]...)
		ledger, err := Reduce(copy, nil, 16)
		optional := remove == 6 || remove == 14 || remove == 18
		if (err == nil) != optional {
			t.Fatalf("record %s omission differs from Python oracle: %v", records[remove].ID, err)
		}
		if err == nil && len(ledger.Attempts) != 2 {
			t.Fatal("optional receipt omission reset spent lifetime budget")
		}
	}
	duplicate := append(append([]Record{}, records...), records[0])
	if _, err := Reduce(duplicate, nil, 16); err == nil {
		t.Fatal("duplicate server ID accepted")
	}
	if _, err := Reduce(records, nil, 1); err == nil {
		t.Fatal("second lifetime attempt accepted against budget1")
	}
}

func TestLifetimeRejectsMissingOrChangedSourceEvidence(t *testing.T) {
	base := fixtureRecords(t, fixture(t, "review-routing-wire-v1.json"))
	for _, mutate := range []func([]Record){
		func(records []Record) {
			records[3].Payload["source"].(map[string]any)["completion_id"] = "9007199254740993"
		},
		func(records []Record) { records[3].Payload["executor"].(map[string]any)["login"] = "another-author" },
		func(records []Record) { records[3].Payload["attempt"] = json.Number("2") },
		func(records []Record) { records[3].Payload["series"] = "99999" },
		func(records []Record) { records[2].Payload["claim_id"] = "99999" },
		func(records []Record) { records[1].CreatedAt = "2026-10-29T01:00:00Z" },
		func(records []Record) { records[3].Payload["previous"] = "1003" },
	} {
		records := make([]Record, len(base))
		for i, item := range base {
			records[i] = Record{item.ID, item.CreatedAt, clone(t, item.Payload)}
		}
		mutate(records)
		if _, err := Reduce(records, nil, 16); err == nil {
			t.Fatal("changed source/claim/owner/continuity accepted")
		}
	}
}
