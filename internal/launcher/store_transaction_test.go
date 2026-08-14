package launcher

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestStoreRWMutexKeepsSnapshotOutOfMutation(t *testing.T) {
	st := newTestStore(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	mutationDone := make(chan error, 1)
	go func() {
		mutationDone <- st.mutateDocument(func(doc *yaml.Node) (bool, error) {
			close(entered)
			<-release
			return true, setStoreBases(doc, []*Base{{ID: "b1", Name: "База"}})
		})
	}()
	<-entered

	type snapshotResult struct {
		bases []*Base
		err   error
	}
	snapshotDone := make(chan snapshotResult, 1)
	go func() {
		bases, _, err := st.Snapshot()
		snapshotDone <- snapshotResult{bases: bases, err: err}
	}()

	early := false
	select {
	case <-snapshotDone:
		early = true
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-mutationDone; err != nil {
		t.Fatalf("mutation: %v", err)
	}
	if early {
		t.Fatal("Snapshot прошёл внутри незавершённой mutation: RWMutex не удерживается на всю транзакцию")
	}
	result := <-snapshotDone
	if result.err != nil {
		t.Fatalf("Snapshot: %v", result.err)
	}
	if len(result.bases) != 1 || result.bases[0].ID != "b1" {
		t.Fatalf("Snapshot увидел не атомарный результат: %#v", result.bases)
	}
}

func TestStoreCrossInstanceLockSerializesWholeTransaction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ibases.yaml")
	first := &Store{path: path}
	second := &Store{path: path}

	entered := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- first.mutateDocument(func(doc *yaml.Node) (bool, error) {
			close(entered)
			<-release
			return true, setStoreBases(doc, []*Base{{ID: "b1", Name: "База"}})
		})
	}()
	<-entered

	secondDone := make(chan error, 1)
	go func() { secondDone <- second.SetOnClose(OnCloseStop) }()
	early := false
	select {
	case <-secondDone:
		early = true
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first mutation: %v", err)
	}
	if early {
		t.Fatal("второй Store вошёл в mutation до освобождения межпроцессного lock")
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second mutation: %v", err)
	}

	bases, settings, err := first.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(bases) != 1 || bases[0].ID != "b1" || settings.OnClose != OnCloseStop {
		t.Fatalf("lost update после двух Store: bases=%#v settings=%#v", bases, settings)
	}
}

func TestStoreConcurrentCrossInstanceAddsLoseNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ibases.yaml")
	stores := []*Store{{path: path}, {path: path}}
	const count = 24
	start := make(chan struct{})
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs <- stores[i%len(stores)].Add(&Base{ID: fmt.Sprintf("b-%02d", i), Name: "База"})
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Add: %v", err)
		}
	}

	bases, err := stores[0].List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(bases) != count {
		t.Fatalf("lost update: записано %d баз из %d", len(bases), count)
	}
	seen := make(map[string]bool, count)
	seenPorts := make(map[int]string, count)
	for _, base := range bases {
		seen[base.ID] = true
		if previous := seenPorts[base.Port]; previous != "" {
			t.Fatalf("atomic default-port allocation duplicated port %d for %s and %s", base.Port, previous, base.ID)
		}
		seenPorts[base.Port] = base.ID
	}
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("b-%02d", i)
		if !seen[id] {
			t.Errorf("потеряна база %s", id)
		}
	}
}

func TestStoreConcurrentCrossInstanceAddsRejectSameExplicitPort(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ibases.yaml")
	stores := []*Store{{path: path}, {path: path}}
	start := make(chan struct{})
	errs := make(chan error, len(stores))
	for i, st := range stores {
		go func(i int, st *Store) {
			<-start
			errs <- st.Add(&Base{ID: fmt.Sprintf("b-%d", i), Name: "База", Port: 18080})
		}(i, st)
	}
	close(start)

	var successes, conflicts int
	for range stores {
		err := <-errs
		if err == nil {
			successes++
			continue
		}
		var conflict *BasePortConflictError
		if !errors.As(err, &conflict) || conflict.Port != 18080 {
			t.Fatalf("Add returned unexpected error: %v", err)
		}
		conflicts++
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("same-port Add results: successes=%d conflicts=%d", successes, conflicts)
	}
	bases, err := stores[0].List()
	if err != nil {
		t.Fatal(err)
	}
	if len(bases) != 1 || bases[0].Port != 18080 {
		t.Fatalf("same-port transaction persisted unexpected bases: %+v", bases)
	}
}

func TestStoreConcurrentCrossInstanceUpdatesRejectSameTargetPort(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ibases.yaml")
	first, second := &Store{path: path}, &Store{path: path}
	for _, base := range []*Base{
		{ID: "a", Name: "A", Port: 18081},
		{ID: "b", Name: "B", Port: 18082},
	} {
		if err := first.Add(base); err != nil {
			t.Fatal(err)
		}
	}
	a, err := first.Get("a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := second.Get("b")
	if err != nil {
		t.Fatal(err)
	}
	a.Port, b.Port = 18083, 18083
	start := make(chan struct{})
	errs := make(chan error, 2)
	go func() { <-start; errs <- first.Update(a) }()
	go func() { <-start; errs <- second.Update(b) }()
	close(start)

	var successes, conflicts int
	for range 2 {
		err := <-errs
		if err == nil {
			successes++
			continue
		}
		var conflict *BasePortConflictError
		if !errors.As(err, &conflict) || conflict.Port != 18083 {
			t.Fatalf("Update returned unexpected error: %v", err)
		}
		conflicts++
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("same-target Update results: successes=%d conflicts=%d", successes, conflicts)
	}
	bases, err := first.List()
	if err != nil {
		t.Fatal(err)
	}
	owners := 0
	for _, base := range bases {
		if base.Port == 18083 {
			owners++
		}
	}
	if owners != 1 {
		t.Fatalf("target port owners=%d, bases=%+v", owners, bases)
	}
}

func TestSetOnClosePreservesUnknownYAMLAndComments(t *testing.T) {
	st := newTestStore(t)
	raw := `# registry-comment
future_top:
  enabled: true # future-top-comment
settings:
  # theme-comment
  theme: dark
  on_close: ask # close-comment
bases:
  - id: b1
    name: База
    db: ""
    port: 8080
    created: 2026-08-11T00:00:00Z
    future_base: keep-me # future-base-comment
`
	if err := os.WriteFile(st.path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.SetOnClose(OnCloseStop); err != nil {
		t.Fatalf("SetOnClose: %v", err)
	}
	data, err := os.ReadFile(st.path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{
		"registry-comment", "future_top:", "future-top-comment", "theme: dark",
		"theme-comment", "on_close: stop", "close-comment", "future_base: keep-me", "future-base-comment",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("после SetOnClose потеряно %q:\n%s", want, got)
		}
	}
	bases, settings, err := st.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(bases) != 1 || settings.OnClose != OnCloseStop {
		t.Fatalf("неверный snapshot: bases=%#v settings=%#v", bases, settings)
	}
}

func TestBaseWritePreservesUnknownTopLevelAndSettings(t *testing.T) {
	st := newTestStore(t)
	raw := `future_top:
  enabled: true
settings:
  on_close: background
  theme: dark
bases: []
`
	if err := os.WriteFile(st.path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.Add(&Base{ID: "b1", Name: "База"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	data, err := os.ReadFile(st.path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{"future_top:", "enabled: true", "theme: dark", "on_close: background"} {
		if !strings.Contains(got, want) {
			t.Errorf("base write потерял %q:\n%s", want, got)
		}
	}
}

func TestEnsureControlTokenIsPersistentAndPreservesBaseYAML(t *testing.T) {
	st := newTestStore(t)
	raw := `bases:
  - id: b1
    name: База
    db: ""
    port: 8080
    created: 2026-08-11T00:00:00Z
    future_base: keep-me # base-comment
settings:
  future_setting: keep-too
`
	if err := os.WriteFile(st.path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	token, err := st.EnsureControlToken("b1")
	if err != nil {
		t.Fatalf("EnsureControlToken: %v", err)
	}
	decoded, err := hex.DecodeString(token)
	if err != nil || len(decoded) != 32 {
		t.Fatalf("control token не 256-bit hex: %q / %v", token, err)
	}
	again, err := st.EnsureControlToken("b1")
	if err != nil || again != token {
		t.Fatalf("token не persistent: first=%q second=%q err=%v", token, again, err)
	}
	data, err := os.ReadFile(st.path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{"control_token: " + token, "future_base: keep-me", "base-comment", "future_setting: keep-too"} {
		if !strings.Contains(got, want) {
			t.Errorf("EnsureControlToken потерял %q:\n%s", want, got)
		}
	}
}

func TestEnsureControlTokenCrossInstanceReturnsSameSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ibases.yaml")
	first := &Store{path: path}
	if err := first.Add(&Base{ID: "b1", Name: "База"}); err != nil {
		t.Fatal(err)
	}
	second := &Store{path: path}
	start := make(chan struct{})
	tokens := make(chan string, 2)
	errs := make(chan error, 2)
	for _, st := range []*Store{first, second} {
		go func(st *Store) {
			<-start
			token, err := st.EnsureControlToken("b1")
			tokens <- token
			errs <- err
		}(st)
	}
	close(start)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("EnsureControlToken: %v", err)
		}
	}
	a, b := <-tokens, <-tokens
	if a == "" || a != b {
		t.Fatalf("два Store выпустили разные токены: %q / %q", a, b)
	}
}

func TestStoreUpdateDoesNotEraseConcurrentControlToken(t *testing.T) {
	st := newTestStore(t)
	if err := st.Add(&Base{ID: "b1", Name: "До"}); err != nil {
		t.Fatal(err)
	}
	stale, err := st.Get("b1")
	if err != nil {
		t.Fatal(err)
	}
	token, err := st.EnsureControlToken("b1")
	if err != nil {
		t.Fatal(err)
	}
	stale.Name = "После"
	if err := st.Update(stale); err != nil {
		t.Fatal(err)
	}
	got, err := st.Get("b1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "После" || got.ControlToken != token {
		t.Fatalf("stale Update затёр control token: %#v", got)
	}
}

func TestBaseListMutationsPreserveUnknownBaseFieldsAndComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ibases.yaml")
	raw := `bases:
  - id: b1
    name: Before
    config_source: file
    db: ""
    port: 8080
    created: 2026-01-01T00:00:00Z
    future_base: keep-me # future-base-comment
  - id: b2
    name: Second
    config_source: file
    db: ""
    port: 8081
    created: 2026-01-01T00:00:00Z
    future_second: keep-too
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	st := &Store{path: path}
	b, err := st.Get("b1")
	if err != nil {
		t.Fatal(err)
	}
	b.Name = "After"
	if err := st.Update(b); err != nil {
		t.Fatal(err)
	}
	if err := st.Move("b2", -1); err != nil {
		t.Fatal(err)
	}
	if err := st.Add(&Base{ID: "b3", Name: "Third", ConfigSource: "file", Port: 8082}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{
		"name: After", "future_base: keep-me", "future-base-comment",
		"future_second: keep-too", "id: b3",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("base mutation lost %q:\n%s", want, got)
		}
	}
}

func TestTouchLastOpenedDoesNotRevertConcurrentEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ibases.yaml")
	st := &Store{path: path}
	if err := st.Add(&Base{ID: "b1", Name: "Before", ConfigSource: "file", Port: 8080}); err != nil {
		t.Fatal(err)
	}
	stale, err := st.Get("b1")
	if err != nil {
		t.Fatal(err)
	}
	latest, err := st.Get("b1")
	if err != nil {
		t.Fatal(err)
	}
	latest.Name = "After"
	latest.Port = 9090
	if err := st.Update(latest); err != nil {
		t.Fatal(err)
	}
	openedAt := time.Now().UTC().Round(time.Second)
	if err := st.TouchLastOpened(stale.ID, openedAt); err != nil {
		t.Fatal(err)
	}
	got, err := st.Get("b1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "After" || got.Port != 9090 || !got.LastOpened.Equal(openedAt) {
		t.Fatalf("timestamp mutation reverted concurrent edit: %+v", got)
	}
}

func TestStoreTempIsUniquePrivateAndLeavesPredictablePathUntouched(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ibases.yaml")
	legacy := path + ".tmp"
	if err := os.WriteFile(legacy, []byte("legacy"), 0o644); err != nil {
		t.Fatal(err)
	}

	first, err := stageStoreFile(path, []byte("one"))
	if err != nil {
		t.Fatalf("stage first: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(first) })
	second, err := stageStoreFile(path, []byte("two"))
	if err != nil {
		t.Fatalf("stage second: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(second) })
	if first == second || first == legacy || second == legacy {
		t.Fatalf("temp не уникален: first=%q second=%q legacy=%q", first, second, legacy)
	}
	for _, temp := range []string{first, second} {
		if filepath.Dir(temp) != dir {
			t.Errorf("temp создан не рядом с registry: %s", temp)
		}
		if runtime.GOOS != "windows" {
			info, err := os.Stat(temp)
			if err != nil {
				t.Fatal(err)
			}
			if mode := info.Mode().Perm(); mode != 0o600 {
				t.Errorf("mode temp %s = %04o, ожидался 0600", temp, mode)
			}
		}
	}

	st := &Store{path: path}
	if err := st.SetOnClose(OnCloseAsk); err != nil {
		t.Fatalf("SetOnClose: %v", err)
	}
	legacyData, err := os.ReadFile(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if string(legacyData) != "legacy" {
		t.Fatalf("предсказуемый старый temp был переиспользован: %q", legacyData)
	}
	leftovers, err := filepath.Glob(filepath.Join(dir, ".ibases.yaml.*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	// Два staged temp ещё существуют намеренно; mutation не должна добавить третий.
	if len(leftovers) != 2 {
		t.Fatalf("atomic write оставил неожиданные temp: %v", leftovers)
	}
}

func TestStoreAddRejectsDuplicatePersistentIdentity(t *testing.T) {
	st := newTestStore(t)
	if err := st.Add(&Base{ID: "same", Name: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Add(&Base{ID: "same", Name: "replacement"}); err == nil {
		t.Fatal("duplicate base ID was accepted")
	}
	bases, err := st.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(bases) != 1 || bases[0].Name != "first" {
		t.Fatalf("failed duplicate Add changed the registry: %+v", bases)
	}
}

func TestStoreSnapshotRejectsAmbiguousOrNullBases(t *testing.T) {
	for name, body := range map[string]string{
		"duplicate": "bases:\n  - {id: same, name: first}\n  - {id: same, name: second}\n",
		"null":      "bases:\n  - null\n",
		"empty-id":  "bases:\n  - {name: unnamed}\n",
	} {
		t.Run(name, func(t *testing.T) {
			st := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
			if err := os.WriteFile(st.path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := st.Snapshot(); err == nil {
				t.Fatal("ambiguous registry was accepted")
			}
		})
	}
}
