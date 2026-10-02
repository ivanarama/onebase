package storage_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/navigation"
	"github.com/ivantit66/onebase/internal/storage"
)

func navSettingsBase(context string) navigation.Tree {
	return navigation.Tree{Version: 1, Context: context, Sections: []navigation.Section{{ID: "cfg:main", Title: "Main",
		Items:  []navigation.Item{{ID: "cfg:a", Target: "catalog:A", Object: navigation.Object{Target: navigation.Target{Kind: "catalog", Name: "A"}, Title: "A"}}},
		Groups: []navigation.Group{{ID: "cfg:folder", Title: "Folder"}},
	}}}
}

func navSettingsDelta(t *testing.T, base navigation.Tree, title string) navigation.Delta {
	t.Helper()
	hash, err := base.Hash()
	if err != nil {
		t.Fatal(err)
	}
	return navigation.Delta{Version: 1, BaseHash: hash, Ops: []navigation.Operation{{Op: "rename", Node: "cfg:main", Title: &title}}}
}

func TestNavigationSettingsCASMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		scope := storage.NavigationSettingsScope{Layer: navigation.AdminLayer, Context: "global"}
		base := navSettingsBase(scope.Context)
		missing, err := db.GetNavigationSettings(ctx, scope)
		if err != nil || missing.Exists || missing.Raw != "" || missing.Revision != "" {
			t.Fatalf("missing: %+v %v", missing, err)
		}
		first, err := db.SaveNavigationSettings(ctx, scope, base, navSettingsDelta(t, base, "First"), missing.Revision)
		if err != nil || !first.Exists || !strings.HasPrefix(first.Revision, "sha256:") {
			t.Fatalf("first save: %+v %v", first, err)
		}
		read, err := db.GetNavigationSettings(ctx, scope)
		if err != nil || read != first {
			t.Fatalf("get: %+v %v", read, err)
		}
		second, err := db.SaveNavigationSettings(ctx, scope, base, navSettingsDelta(t, base, "Second"), first.Revision)
		if err != nil || second.Revision == first.Revision {
			t.Fatalf("update: %+v %v", second, err)
		}
		if _, err := db.SaveNavigationSettings(ctx, scope, base, navSettingsDelta(t, base, "Stale"), first.Revision); !errors.Is(err, storage.ErrVersionConflict) {
			t.Fatalf("stale update: %v", err)
		}
		if _, err := db.DeleteNavigationSettings(ctx, scope, first.Revision); !errors.Is(err, storage.ErrVersionConflict) {
			t.Fatalf("stale reset: %v", err)
		}
		read, err = db.GetNavigationSettings(ctx, scope)
		if err != nil || read != second {
			t.Fatalf("stale tab overwrote data: %+v %v", read, err)
		}
		inherited, err := db.SaveDesiredNavigation(ctx, scope, base, base, second.Revision)
		if err != nil || inherited.Exists || inherited.Raw != "" || inherited.Revision != "" {
			t.Fatalf("empty diff must delete: %+v %v", inherited, err)
		}
		if _, err := db.SaveNavigationSettings(ctx, scope, base, navSettingsDelta(t, base, "Recreate"), second.Revision); !errors.Is(err, storage.ErrVersionConflict) {
			t.Fatalf("deleted revision accepted: %v", err)
		}
		if _, err := db.DeleteNavigationSettings(ctx, scope, ""); err != nil {
			t.Fatal("reset of inherited settings", err)
		}
	})
}

func TestNavigationSettingsCollisionMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		scopes := []storage.NavigationSettingsScope{
			{Layer: navigation.UserLayer, Login: "a.b", Context: "c"},
			{Layer: navigation.UserLayer, Login: "a", Context: "b.c"},
			{Layer: navigation.UserLayer, Login: "a:3", Context: "b.c"},
			{Layer: navigation.UserLayer, Login: "Иван.李", Context: "Школа:1"},
			{Layer: navigation.AdminLayer, Context: "b.c"},
			{Layer: navigation.AdminLayer, Context: "global"},
			{Layer: navigation.AdminLayer, Context: "subsystem:global"},
		}
		var saved []storage.NavigationSettings
		for i, scope := range scopes {
			base := navSettingsBase(scope.Context)
			value, err := db.SaveNavigationSettings(ctx, scope, base, navSettingsDelta(t, base, fmt.Sprintf("Title %d", i)), "")
			if err != nil {
				t.Fatal(err)
			}
			saved = append(saved, value)
		}
		for i, scope := range scopes {
			got, err := db.GetNavigationSettings(ctx, scope)
			if err != nil || got != saved[i] {
				t.Fatalf("scope %d collision: %+v %v", i, got, err)
			}
		}
		if _, err := db.DeleteNavigationSettings(ctx, scopes[0], saved[0].Revision); err != nil {
			t.Fatal(err)
		}
		for i, scope := range scopes[1:] {
			got, err := db.GetNavigationSettings(ctx, scope)
			if err != nil || got != saved[i+1] {
				t.Fatal("reset affected another scope", err)
			}
		}
		entries, err := db.ListSettings(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if !storage.IsNavigationSettingsKey(entry.Key) {
				t.Fatalf("generated key not recognized: %q", entry.Key)
			}
		}
	})
}

func TestNavigationSettingsParallelWritersMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		for _, exists := range []bool{false, true} {
			t.Run(fmt.Sprintf("exists-%v", exists), func(t *testing.T) {
				scope := storage.NavigationSettingsScope{Layer: navigation.AdminLayer, Context: fmt.Sprintf("parallel-%v", exists)}
				base := navSettingsBase(scope.Context)
				initial := storage.NavigationSettings{}
				if exists {
					var err error
					initial, err = db.SaveNavigationSettings(ctx, scope, base, navSettingsDelta(t, base, "Initial"), "")
					if err != nil {
						t.Fatal(err)
					}
				}
				start := make(chan struct{})
				var wait sync.WaitGroup
				values := [2]storage.NavigationSettings{}
				errs := [2]error{}
				for i := range values {
					delta := navSettingsDelta(t, base, fmt.Sprintf("Writer %d", i))
					wait.Add(1)
					go func(i int, delta navigation.Delta) {
						defer wait.Done()
						<-start
						values[i], errs[i] = db.SaveNavigationSettings(ctx, scope, base, delta, initial.Revision)
					}(i, delta)
				}
				close(start)
				wait.Wait()
				winner, conflicts := -1, 0
				for i, err := range errs {
					if err == nil {
						if winner >= 0 {
							t.Fatal("both writers succeeded")
						}
						winner = i
					} else if errors.Is(err, storage.ErrVersionConflict) {
						conflicts++
					} else {
						t.Fatal("unexpected concurrent error", err)
					}
				}
				if winner < 0 || conflicts != 1 {
					t.Fatalf("winner=%d conflicts=%d", winner, conflicts)
				}
				got, err := db.GetNavigationSettings(ctx, scope)
				if err != nil || got != values[winner] {
					t.Fatal("winner's update was lost", err)
				}
			})
		}
	})
}

func TestNavigationSettingsRejectsInvalidBeforeWriteMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		scope := storage.NavigationSettingsScope{Layer: navigation.AdminLayer, Context: "Education"}
		base := navSettingsBase(scope.Context)
		valid := navSettingsDelta(t, base, "Keep")
		saved, err := db.SaveNavigationSettings(ctx, scope, base, valid, "")
		if err != nil {
			t.Fatal(err)
		}
		id, _ := navigation.NewCustomID(navigation.AdminLayer)
		title := "New"
		add := navigation.Operation{Op: "add_group", ID: id, Parent: "cfg:main", Title: &title}
		invalid := []navigation.Delta{
			{Version: 2, BaseHash: valid.BaseHash},
			{Version: 1, BaseHash: valid.BaseHash, Ops: []navigation.Operation{{Op: "unknown", Node: "cfg:a"}}},
			{Version: 1, BaseHash: valid.BaseHash, Ops: []navigation.Operation{{Op: "move", Node: "cfg:folder", Parent: "cfg:folder"}}},
			{Version: 1, BaseHash: valid.BaseHash, Ops: []navigation.Operation{{Op: "move", Node: "cfg:a", Parent: "cfg:a"}}},
			{Version: 1, BaseHash: valid.BaseHash, Ops: []navigation.Operation{add, add}},
		}
		large := strings.Repeat("x", navigation.MaxDeltaBytes)
		invalid = append(invalid, navigation.Delta{Version: 1, BaseHash: valid.BaseHash, Ops: []navigation.Operation{{Op: "rename", Node: "cfg:main", Title: &large}}})
		tooMany := navigation.Delta{Version: 1, BaseHash: valid.BaseHash}
		for i := 0; i <= navigation.MaxOperations; i++ {
			tooMany.Ops = append(tooMany.Ops, navigation.Operation{Op: "hide", Node: "cfg:a"})
		}
		invalid = append(invalid, tooMany)
		for _, delta := range invalid {
			if _, err := db.SaveNavigationSettings(ctx, scope, base, delta, saved.Revision); err == nil {
				t.Fatal("invalid delta accepted", delta)
			}
			got, err := db.GetNavigationSettings(ctx, scope)
			if err != nil || !reflect.DeepEqual(got, saved) {
				t.Fatal("invalid delta changed the stored value", err)
			}
		}
		atLimit := navSettingsBase(scope.Context)
		for i := 1; i < navigation.MaxSections; i++ {
			atLimit.Sections = append(atLimit.Sections, navigation.Section{ID: fmt.Sprintf("cfg:section-%d", i), Title: "Section"})
		}
		limited := navSettingsDelta(t, atLimit, "Keep")
		limited.Ops = []navigation.Operation{{Op: "add_section", ID: id, Title: &title}}
		if _, err := db.SaveNavigationSettings(ctx, scope, atLimit, limited, saved.Revision); err == nil {
			t.Fatal("merged section limit exceeded without rejection")
		}
		if got, err := db.GetNavigationSettings(ctx, scope); err != nil || got != saved {
			t.Fatal("node limit failure changed settings", err)
		}
		// Corrupt raw JSON can still be read and reset with its actual revision.
		if err := db.SaveSetting(ctx, "ui.navigation.admin.9:Education", "{broken"); err != nil {
			t.Fatal(err)
		}
		corrupt, err := db.GetNavigationSettings(ctx, scope)
		if err != nil || corrupt.Raw != "{broken" || corrupt.Revision == "" {
			t.Fatal("corrupt raw value cannot be reset", err)
		}
		if _, err := db.DeleteNavigationSettings(ctx, scope, corrupt.Revision); err != nil {
			t.Fatal(err)
		}
	})
}

func TestNavigationStoredLayerBehaviorMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		base := navSettingsBase("subsystem:School")
		adminScope := storage.NavigationSettingsScope{Layer: navigation.AdminLayer, Context: base.Context}
		userScope := storage.NavigationSettingsScope{Layer: navigation.UserLayer, Login: "alice", Context: base.Context}
		adminDelta := navSettingsDelta(t, base, "Common")
		adminDelta.Ops = append(adminDelta.Ops, navigation.Operation{Op: "hide", Node: "cfg:a"})
		admin, err := db.SaveNavigationSettings(ctx, adminScope, base, adminDelta, "")
		if err != nil {
			t.Fatal(err)
		}
		adminTree, _, err := navigation.ApplyDelta(base, adminDelta, navigation.AdminLayer)
		if err != nil {
			t.Fatal(err)
		}
		userDelta := navSettingsDelta(t, adminTree, "Personal")
		userDelta.Ops = append(userDelta.Ops, navigation.Operation{Op: "show", Node: "cfg:a"})
		user, err := db.SaveNavigationSettings(ctx, userScope, adminTree, userDelta, "")
		if err != nil {
			t.Fatal(err)
		}
		got, diags := navigation.Compose(base, []byte(admin.Raw), []byte(user.Raw))
		if got.Sections[0].Title != "Personal" || len(got.Sections[0].Items) != 0 || len(diags) != 1 || diags[0].Code != "stale" {
			t.Fatalf("stored precedence/visibility: %+v %+v", got, diags)
		}
		changed := navSettingsBase(base.Context)
		changed.Sections[0].Items[0].ID = "cfg:new-item"
		got, diags = navigation.Compose(changed, []byte(admin.Raw), []byte(user.Raw))
		if len(got.Sections[0].Items) != 1 || got.Sections[0].Items[0].ID != "cfg:new-item" {
			t.Fatal("stored layers froze configuration")
		}
		stale := 0
		for _, d := range diags {
			if d.Code == "stale" {
				stale++
			}
		}
		if stale != 2 {
			t.Fatal("deleted references missing diagnostics", diags)
		}
		if _, err := db.DeleteNavigationSettings(ctx, userScope, user.Revision); err != nil {
			t.Fatal(err)
		}
		got, _ = navigation.Compose(base, []byte(admin.Raw), nil)
		if got.Sections[0].Title != "Common" {
			t.Fatal("user reset did not inherit admin")
		}
		if _, err := db.DeleteNavigationSettings(ctx, adminScope, admin.Revision); err != nil {
			t.Fatal(err)
		}
		got, _ = navigation.Compose(base, nil, nil)
		if got.Sections[0].Title != "Main" || len(got.Sections[0].Items) != 1 {
			t.Fatal("admin reset did not inherit configuration")
		}
		// Restore corrupt bytes through the same raw settings entry point that
		// backup uses, then prove exactly one layer falls back at resolution.
		if err := db.SaveSetting(ctx, "ui.navigation.admin.16:subsystem:School", "{corrupt-private-title"); err != nil {
			t.Fatal(err)
		}
		corrupt, err := db.GetNavigationSettings(ctx, adminScope)
		if err != nil {
			t.Fatal(err)
		}
		freshUser, err := db.SaveNavigationSettings(ctx, userScope, base, navSettingsDelta(t, base, "Personal"), "")
		if err != nil {
			t.Fatal(err)
		}
		got, diags = navigation.Compose(base, []byte(corrupt.Raw), []byte(freshUser.Raw))
		if got.Sections[0].Title != "Personal" || len(diags) != 1 || diags[0].Code != "invalid-layer" {
			t.Fatal("corrupt stored admin broke valid personal layer", diags)
		}
	})
}

func TestNavigationSettingsKeyFamilies(t *testing.T) {
	for _, key := range []string{"ui.navigation.admin.6:global", "ui.navigation.user.3:a.b.3:c.d", "ui.navigation.user.8:Иван.9:Education"} {
		if !storage.IsNavigationSettingsKey(key) {
			t.Fatalf("valid portable key rejected: %q", key)
		}
	}
	for _, key := range []string{"llm.config", "ui.navigation.admin-secret.6:global", "ui.navigation.admin.06:global", "ui.navigation.admin.6:global.extra", "ui.navigation.user.999999999999999999999999:a.1:b", "ui.navigation.user.1:a.2:b", "ui.navigation.user.0:.1:a"} {
		if storage.IsNavigationSettingsKey(key) {
			t.Fatalf("non-navigation key accepted: %q", key)
		}
	}
}
