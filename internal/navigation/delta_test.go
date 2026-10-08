package navigation_test

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/navigation"
)

func deltaText(s string) *string { return &s }

func deltaTree(t *testing.T) navigation.Tree {
	t.Helper()
	menu := &metadata.Menu{Sections: []metadata.MenuSection{
		{ID: "study", Title: "Study", Titles: map[string]string{"en": "Education"}, Items: []metadata.MenuItem{{ID: "years", Target: "catalog:Years"}},
			Groups: []metadata.MenuGroup{{ID: "work", Title: "Work", Items: []metadata.MenuItem{{ID: "order", Target: "document:Order"}, {ID: "periods", Target: "catalog:Periods"}}}}},
		{ID: "daily", Title: "Daily", Items: []metadata.MenuItem{{ID: "journal", Target: "processor:ClassJournal"}}},
	}}
	tree, diags := navigation.Normalize("Education", menu, navigation.NewScope(schoolObjects(), schoolContents(), false))
	if len(diags) != 0 {
		t.Fatal(diags)
	}
	return tree
}

func TestDeltaDiffExplicitRenameToBaseTitleClearsTranslations(t *testing.T) {
	base := deltaTree(t)
	desired := deltaClone(t, base)
	desired.Sections[0].Titles = nil
	desired.Sections[0].TitleExplicit = true
	delta, err := navigation.Diff(base, desired, navigation.AdminLayer)
	if err != nil {
		t.Fatal(err)
	}
	if len(delta.Ops) != 1 || delta.Ops[0].Op != "rename" || delta.Ops[0].Title == nil || *delta.Ops[0].Title != "Study" {
		t.Fatalf("explicit rename lost: %+v", delta.Ops)
	}
	got, diagnostics, err := navigation.ApplyDelta(base, delta, navigation.AdminLayer)
	if err != nil || len(diagnostics) != 0 || !reflect.DeepEqual(got, desired) {
		t.Fatalf("round trip: %v %v %+v", err, diagnostics, got)
	}
	desired = deltaClone(t, base)
	desired.Sections[0].Titles["en"] = "Forged translation"
	if _, err := navigation.Diff(base, desired, navigation.AdminLayer); err == nil {
		t.Fatal("localized translations became editable")
	}
}

func deltaFor(t *testing.T, base navigation.Tree, ops ...navigation.Operation) navigation.Delta {
	t.Helper()
	hash, err := base.Hash()
	if err != nil {
		t.Fatal(err)
	}
	return navigation.Delta{Version: 1, BaseHash: hash, Ops: ops}
}

func deltaRaw(t *testing.T, delta navigation.Delta, layer navigation.Layer) []byte {
	t.Helper()
	raw, err := navigation.EncodeDelta(delta, layer)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func deltaClone(t *testing.T, tree navigation.Tree) navigation.Tree {
	t.Helper()
	raw, err := json.Marshal(tree)
	if err != nil {
		t.Fatal(err)
	}
	var clone navigation.Tree
	if err := json.Unmarshal(raw, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func hasDeltaItem(tree navigation.Tree, id string) bool {
	for _, s := range tree.Sections {
		for _, i := range s.Items {
			if i.ID == id {
				return true
			}
		}
		for _, g := range s.Groups {
			for _, i := range g.Items {
				if i.ID == id {
					return true
				}
			}
		}
	}
	return false
}

func TestDeltaLayerPrecedenceAndConfigurationEvolution(t *testing.T) {
	base := deltaTree(t)
	unchanged := deltaClone(t, base)
	admin := deltaFor(t, base,
		navigation.Operation{Op: "rename", Node: "cfg:study", Title: deltaText("Common")},
		navigation.Operation{Op: "hide", Node: "cfg:order"})
	adminRaw := deltaRaw(t, admin, navigation.AdminLayer)
	adminTree, _, err := navigation.ApplyDelta(base, admin, navigation.AdminLayer)
	if err != nil {
		t.Fatal(err)
	}
	user := deltaFor(t, adminTree,
		navigation.Operation{Op: "rename", Node: "cfg:study", Title: deltaText("Personal")},
		navigation.Operation{Op: "show", Node: "cfg:order"})
	userRaw := deltaRaw(t, user, navigation.UserLayer)
	got, diags := navigation.Compose(base, adminRaw, userRaw)
	if got.Sections[0].Title != "Personal" || len(got.Sections[0].Titles) != 0 || hasDeltaItem(got, "cfg:order") {
		t.Fatalf("precedence/visibility violated: %+v", got)
	}
	if len(diags) != 1 || diags[0].Code != "stale" || !reflect.DeepEqual(base, unchanged) {
		t.Fatalf("diagnostics or input mutation: %+v", diags)
	}
	changed := deltaClone(t, base)
	newItem := changed.Sections[0].Items[0]
	newItem.ID = "cfg:new-year"
	changed.Sections[0].Items = append(changed.Sections[0].Items, newItem)
	changed.Sections[0].Groups[0].Items = changed.Sections[0].Groups[0].Items[1:]
	changed.Sections[1].Title = "New configuration title"
	got, diags = navigation.Compose(changed, adminRaw, userRaw)
	if !hasDeltaItem(got, "cfg:new-year") || hasDeltaItem(got, "cfg:order") || got.Sections[1].Title != changed.Sections[1].Title || got.Sections[0].Title != "Personal" {
		t.Fatalf("configuration evolution lost: %+v", got)
	}
	stale := 0
	for _, d := range diags {
		if d.Code == "stale" {
			stale++
		}
	}
	if stale != 2 {
		t.Fatalf("removed references were not diagnosed: %+v", diags)
	}
}

func TestDeltaCorruptLayerFallsBackIndependently(t *testing.T) {
	base := deltaTree(t)
	admin := deltaFor(t, base, navigation.Operation{Op: "rename", Node: "cfg:daily", Title: deltaText("Common")})
	adminRaw := deltaRaw(t, admin, navigation.AdminLayer)
	user := deltaFor(t, base, navigation.Operation{Op: "rename", Node: "cfg:study", Title: deltaText("Personal")})
	userRaw := deltaRaw(t, user, navigation.UserLayer)
	for _, tc := range []struct {
		name         string
		admin, user  []byte
		study, daily string
	}{{"admin", []byte(`{"private-title":"secret", broken`), userRaw, "Personal", "Daily"}, {"user", adminRaw, []byte(`{"private-title":"secret", broken`), "Study", "Common"}, {"empty-admin", []byte{}, userRaw, "Personal", "Daily"}} {
		t.Run(tc.name, func(t *testing.T) {
			got, diags := navigation.Compose(base, tc.admin, tc.user)
			if got.Sections[0].Title != tc.study || got.Sections[1].Title != tc.daily {
				t.Fatalf("wrong fallback: %+v", got)
			}
			invalid := 0
			for _, d := range diags {
				if d.Code == "invalid-layer" {
					invalid++
				}
				if strings.Contains(d.Message, "secret") || strings.Contains(d.Message, "private-title") {
					t.Fatal("diagnostics leaked input")
				}
			}
			if invalid != 1 {
				t.Fatal(diags)
			}
		})
	}
}

func TestDeltaDiffMinimalSchoolEdits(t *testing.T) {
	base := deltaTree(t)
	identity, err := navigation.Diff(base, base, navigation.AdminLayer)
	if err != nil || len(identity.Ops) != 0 {
		t.Fatalf("unchanged tree has override: %+v %v", identity, err)
	}
	custom, err := navigation.NewCustomID(navigation.AdminLayer)
	if err != nil {
		t.Fatal(err)
	}
	want := deltaClone(t, base)
	want.Sections[0].Title, want.Sections[0].Titles = "Common study", nil
	want.Sections[0].TitleExplicit = true
	want.Sections[0].Groups = append(want.Sections[0].Groups, navigation.Group{ID: custom, Title: "Control", Icon: "clipboard-check", Items: []navigation.Item{want.Sections[1].Items[0]}})
	want.Sections[1].Items = nil
	want.Sections[0].Groups[0].Items = []navigation.Item{want.Sections[0].Groups[0].Items[1]}
	delta, err := navigation.Diff(base, want, navigation.AdminLayer)
	if err != nil {
		t.Fatal(err)
	}
	if len(delta.Ops) != 5 {
		t.Fatalf("expected add, move, rename, icon and hide: %+v", delta.Ops)
	}
	got, diags, err := navigation.ApplyDelta(base, delta, navigation.AdminLayer)
	if err != nil || len(diags) != 0 || !reflect.DeepEqual(got, want) {
		t.Fatalf("desired school menu not achieved: err=%v diags=%+v\ngot=%+v\nwant=%+v", err, diags, got, want)
	}
	first := deltaRaw(t, delta, navigation.AdminLayer)
	again, err := navigation.Diff(base, want, navigation.AdminLayer)
	if err != nil || string(first) != string(deltaRaw(t, again, navigation.AdminLayer)) {
		t.Fatal("diff is not deterministic", err)
	}
	// Metadata from a client cannot change the trusted resolver projection.
	want.Sections[0].Items[0].Object.Target.Name = "OutsideScope"
	delta, err = navigation.Diff(base, want, navigation.AdminLayer)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err = navigation.ApplyDelta(base, delta, navigation.AdminLayer)
	if err != nil || got.Sections[0].Items[0].Object.Target.Name != "Years" {
		t.Fatal("client metadata broadened target", err)
	}
	want.Sections[0].Items[0].Target = "catalog:OutsideScope"
	if _, err := navigation.Diff(base, want, navigation.AdminLayer); err == nil {
		t.Fatal("changed item target accepted")
	}
}

func TestDeltaSiblingMovesAreMinimal(t *testing.T) {
	base := deltaTree(t)
	base.Sections[0].Items = nil
	base.Sections[0].Items = append(base.Sections[0].Items, base.Sections[2].Items[:4]...)
	base.Sections[2].Items = base.Sections[2].Items[4:]
	var permute func([]int, int)
	permute = func(order []int, at int) {
		if at < len(order) {
			for i := at; i < len(order); i++ {
				order[at], order[i] = order[i], order[at]
				permute(order, at+1)
				order[at], order[i] = order[i], order[at]
			}
			return
		}
		want := deltaClone(t, base)
		for i, source := range order {
			want.Sections[0].Items[i] = base.Sections[0].Items[source]
		}
		delta, err := navigation.Diff(base, want, navigation.AdminLayer)
		if err != nil {
			t.Fatal(err)
		}
		// Independent exhaustive subsequence oracle for these four siblings.
		longest := 0
		for mask := 0; mask < 1<<len(order); mask++ {
			last, count, increasing := -1, 0, true
			for i, value := range order {
				if mask&(1<<i) != 0 {
					increasing = increasing && value > last
					last, count = value, count+1
				}
			}
			if increasing && count > longest {
				longest = count
			}
		}
		if len(delta.Ops) != len(order)-longest {
			t.Fatalf("nonminimal permutation %v: %+v", order, delta.Ops)
		}
		got, _, err := navigation.ApplyDelta(base, delta, navigation.AdminLayer)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("permutation %v failed: %v", order, err)
		}
	}
	permute([]int{0, 1, 2, 3}, 0)
}

func TestDeltaStrictCodecAndStructuralValidation(t *testing.T) {
	base := deltaTree(t)
	valid := string(deltaRaw(t, deltaFor(t, base, navigation.Operation{Op: "hide", Node: "cfg:order"}), navigation.AdminLayer))
	for _, raw := range []string{
		strings.Replace(valid, `"version":1`, `"version":2`, 1),
		strings.Replace(valid, `"version":1`, `"version":1,"version":1`, 1),
		strings.Replace(valid, `"version":1`, `"Version":1`, 1),
		strings.Replace(valid, `"version":1`, `"Version":1,"version":1`, 1),
		strings.Replace(valid, `"op":"hide"`, `"op":"hide","op":"show"`, 1),
		strings.Replace(valid, `"op":"hide"`, `"op":"script"`, 1),
		strings.Replace(valid, `"op":"hide"`, `"op":"hide","url":"https://outside"`, 1),
		strings.Replace(valid, `"op":"hide"`, `"op":"hide","title":null`, 1),
		strings.Replace(valid, `"node":"cfg:order"`, `"node":null`, 1),
		valid + `{}`, string([]byte{0xff}), strings.Repeat(" ", navigation.MaxDeltaBytes+1),
	} {
		if _, err := navigation.DecodeDelta([]byte(raw), navigation.AdminLayer); err == nil {
			t.Fatalf("invalid codec input accepted: %.100s", raw)
		}
	}
	for _, op := range []navigation.Operation{
		{Op: "move", Node: "cfg:study", Parent: "cfg:study"},
		{Op: "move", Node: "cfg:work", Parent: "cfg:work"},
		{Op: "move", Node: "cfg:work", Parent: "cfg:order"},
		{Op: "move", Node: "cfg:order"},
		{Op: "move", Node: "cfg:removed", Parent: "cfg:removed"},
		{Op: "set_icon", Node: "cfg:study", Icon: deltaText("<svg onload=bad>")},
	} {
		if _, _, err := navigation.ApplyDelta(base, deltaFor(t, base, op), navigation.AdminLayer); err == nil {
			t.Fatalf("invalid structure accepted: %+v", op)
		}
	}
	id, _ := navigation.NewCustomID(navigation.AdminLayer)
	add := navigation.Operation{Op: "add_group", ID: id, Parent: "cfg:study", Title: deltaText("Custom")}
	if _, _, err := navigation.ApplyDelta(base, deltaFor(t, base, add, add), navigation.AdminLayer); err == nil {
		t.Fatal("duplicate ID accepted")
	}
	if _, err := navigation.EncodeDelta(deltaFor(t, base, add), navigation.UserLayer); err == nil {
		t.Fatal("foreign custom namespace accepted")
	}
	ops := slices.Repeat([]navigation.Operation{{Op: "hide", Node: "cfg:order"}}, navigation.MaxOperations+1)
	if _, err := navigation.EncodeDelta(deltaFor(t, base, ops...), navigation.AdminLayer); err == nil {
		t.Fatal("operation limit not enforced")
	}
}
