package reviewrouting

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func independentBase(t *testing.T, n int) []Record {
	t.Helper()
	records := fixtureRecords(t, fixture(t, "review-routing-wire-v1.json"))
	return records[:n:n]
}

func TestIndependentHistoricalLeaseBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, leaseTime, owner, receiptOwner string
		want                                 bool
	}{
		{"valid-renewal", "2026-10-29T00:26:00Z", "00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000001", true},
		{"renewal-too-early", "2026-10-29T00:25:00Z", "00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000001", false},
		{"valid-takeover", "2026-10-29T00:30:00Z", "00000000-0000-4000-8000-000000000002", "00000000-0000-4000-8000-000000000002", true},
		{"premature-takeover", "2026-10-29T00:29:00Z", "00000000-0000-4000-8000-000000000002", "00000000-0000-4000-8000-000000000002", false},
		{"expired-owner-after-takeover", "2026-10-29T00:30:00Z", "00000000-0000-4000-8000-000000000002", "00000000-0000-4000-8000-000000000001", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			records := independentBase(t, 3)
			ctx, err := Context(records[0].Payload)
			if err != nil {
				t.Fatal(err)
			}
			lease := Record{"1004", tc.leaseTime, map[string]any{"protocol": Version, "kind": "lease", "pr": records[0].Payload["pr"], "owner": tc.owner, "context_sha256": ctx, "purpose": "review-publication", "root": "1000", "previous": "1000"}}
			records[1].ID = "1005"
			records[1].CreatedAt = "2026-10-29T00:31:00Z"
			records[1].Payload["owner"] = tc.receiptOwner
			records[1].Payload["lease_id"] = "1004"
			records[2].ID = "1006"
			records[2].CreatedAt = "2026-10-29T00:31:01Z"
			records[2].Payload["owner"] = tc.receiptOwner
			records[2].Payload["lease_id"] = "1004"
			records[2].Payload["claim_id"] = "1005"
			records = append(records, lease)
			_, err = Reduce(records, nil, 2)
			if (err == nil) != tc.want {
				t.Fatalf("accepted=%v want=%v error=%v", err == nil, tc.want, err)
			}
		})
	}
}

func TestIndependentDuplicateSettlementExpiredLease(t *testing.T) {
	records := independentBase(t, 6)
	extra := fixtureRecords(t, fixture(t, "review-routing-settlement-v1.json"))
	records = append(records, extra[0])
	dup := Record{"1090", "2026-10-29T01:00:00Z", clone(t, extra[0].Payload)}
	records = append(records, dup)
	if _, err := Reduce(records, nil, 2); err == nil {
		t.Fatal("accepted a new settlement receipt one hour after its referenced owner lease expired")
	}
}

func TestIndependentAuthorStartedAfterFixDone(t *testing.T) {
	records := independentBase(t, 8)
	records[6].CreatedAt = "2026-10-29T00:00:08Z"
	// Author START is optional, but a START after terminal fix-done cannot
	// describe the already completed old execution.
	if _, err := Reduce(records, nil, 2); err == nil {
		t.Fatal("accepted START after the same attempt's terminal fix-done")
	}
}

func TestIndependentDecodeAmbiguity(t *testing.T) {
	inputs := []string{`{"x":1,"\u0078":2}`, `{"x":1e1}`, `{"x":1.0}`, `"\ud800"`, `"\udc00"`, `"\ud800\ud800"`, string([]byte{'"', 0xff, '"'}), strings.Repeat("[", 66) + "0" + strings.Repeat("]", 66)}
	for i, input := range inputs {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			if _, err := Decode([]byte(input), MaxWireBytes); err == nil {
				t.Fatal("ambiguous input accepted")
			}
		})
	}
}

func TestIndependentConsumerBounds(t *testing.T) {
	for _, fault := range []string{"large-entry", "large-reference", "total-bytes", "file-count", "directory-entry", "bad-role"} {
		t.Run(fault, func(t *testing.T) {
			root, _ := consumerFixture(t)
			dir := filepath.Join(root, "review-queue")
			role := "review"
			write := func(name string, data []byte) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			switch fault {
			case "large-entry":
				write("SKILL.md", []byte(strings.Repeat("x", 1048577)))
			case "large-reference":
				write("oversized", []byte(strings.Repeat("x", 1048577)))
			case "total-bytes":
				for i := 0; i < 5; i++ {
					write("large-"+strconv.Itoa(i), []byte(strings.Repeat("x", 1048576)))
				}
			case "file-count":
				for i := 0; i < 257; i++ {
					write("file-"+strconv.Itoa(i), []byte("x"))
				}
			case "directory-entry":
				if err := os.Remove(filepath.Join(dir, "SKILL.md")); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filepath.Join(dir, "SKILL.md"), 0700); err != nil {
					t.Fatal(err)
				}
			case "bad-role":
				role = "../review-queue"
			}
			if _, err := ConsumerTree(root, role); err == nil {
				t.Fatal("consumer bounds/type constraint accepted")
			}
		})
	}
}

func TestIndependentClosedSchemas(t *testing.T) {
	inputs := fixtureRecords(t, fixture(t, "review-routing-wire-v1.json"))
	inputs = append(inputs, fixtureRecords(t, fixture(t, "review-routing-settlement-v1.json"))...)
	roots := map[string]map[string]any{}
	for _, r := range inputs {
		roots[r.Payload["kind"].(string)] = r.Payload
	}
	pub := inputs[0]
	ctx, err := Context(pub.Payload)
	if err != nil {
		t.Fatal(err)
	}
	roots["lease"] = map[string]any{"protocol": Version, "kind": "lease", "pr": pub.Payload["pr"], "owner": pub.Payload["owner"], "context_sha256": ctx, "purpose": "review-publication", "root": pub.ID, "previous": pub.ID}
	for _, kind := range []string{"publication", "claim", "completion", "lease", "attempt", "phase", "settlement", "resume"} {
		t.Run(kind, func(t *testing.T) {
			p := clone(t, roots[kind])
			if err := Validate(p); err != nil {
				t.Fatal(err)
			}
			p["unexpected"] = true
			if err := Validate(p); err == nil {
				t.Fatal("unknown field accepted")
			}
			delete(p, "unexpected")
			delete(p, "pr")
			if err := Validate(p); err == nil {
				t.Fatal("missing lifetime PR field accepted")
			}
		})
	}
}

func independentBudgetOmissions(t *testing.T, lo, hi int) {
	t.Helper()
	all := fixtureRecords(t, fixture(t, "review-routing-wire-v1.json"))
	for index := lo; index < hi; index++ {
		t.Run(all[index].ID, func(t *testing.T) {
			records := append([]Record{}, all[:index]...)
			records = append(records, all[index+1:]...)
			ledger, err := Reduce(records, nil, 2)
			optional := index == 6 || index == 14 || index == 18
			if (err == nil) != optional {
				t.Fatalf("missing %s accepted=%v err=%v", all[index].ID, err == nil, err)
			}
			if err == nil && len(ledger.Attempts) != 2 {
				t.Fatal("optional receipt removal refunded lifetime budget")
			}
		})
	}
}
func TestIndependentBudgetOmissionsA(t *testing.T) { independentBudgetOmissions(t, 0, 10) }
func TestIndependentBudgetOmissionsB(t *testing.T) { independentBudgetOmissions(t, 10, 19) }

func TestIndependentDeletedAndReducedBudget(t *testing.T) {
	if _, err := Reduce(nil, []string{"unknown-deleted-history"}, 2); err == nil {
		t.Fatal("deleted history manufactured fresh empty budget")
	}
	all := fixtureRecords(t, fixture(t, "review-routing-wire-v1.json"))
	if _, err := Reduce(all, nil, 1); err == nil {
		t.Fatal("lower limit admitted two spent attempts")
	}
}

func TestIndependentResumeChronology(t *testing.T) {
	for _, fault := range []string{"same-source-head", "resume-before-settlement", "settlement-after-review"} {
		t.Run(fault, func(t *testing.T) {
			base := independentBase(t, 6)
			extras := fixtureRecords(t, fixture(t, "review-routing-settlement-v1.json"))
			switch fault {
			case "same-source-head":
				extras[1].Payload["head"] = base[0].Payload["identity"].(map[string]any)["head_sha"]
			case "resume-before-settlement":
				extras[1].CreatedAt = "2026-10-29T00:00:40Z"
			case "settlement-after-review":
				extras[0].CreatedAt = "2026-10-29T00:03:00Z"
			}
			if _, err := Reduce(append(base, extras...), nil, 2); err == nil {
				t.Fatal("contradictory settlement/resume order accepted")
			}
		})
	}
}
