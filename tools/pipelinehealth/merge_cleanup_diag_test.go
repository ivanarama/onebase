package main

// Диагностика merge-cleanup (#1524): поломка гарантий merge-cleanup обязана
// называться отдельно (unsafe_merge_cleanup_contract), а не маскироваться под
// unsafe_base_sync_contract — иначе cleanup-only отказ сообщает не ту причину.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureSkillsRoot собирает временный skills-root со всеми шестью contracts
// из активного репозитория (SKILL.md + references/legacy-protocol.md).
func fixtureSkillsRoot(t *testing.T) string {
	t.Helper()
	skillsRoot := filepath.Join(t.TempDir(), "skills")
	names := []string{"triage-issues", "plan-approved", "fix-approved", "review-queue", "merge-shepherd", "tail-issues"}
	for _, name := range names {
		dir := filepath.Join(skillsRoot, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join("..", "..", ".claude", "skills", name, "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), data, 0o600); err != nil { //nolint:gosec // G703: test-owned path below t.TempDir
			t.Fatal(err)
		}
		legacy, err := os.ReadFile(filepath.Join("..", "..", ".claude", "skills", name, "references", "legacy-protocol.md"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		refDir := filepath.Join(dir, "references")
		if err := os.MkdirAll(refDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(refDir, "legacy-protocol.md"), legacy, 0o600); err != nil { //nolint:gosec // G703: test-owned path below t.TempDir
			t.Fatal(err)
		}
	}
	return skillsRoot
}

// dropFromMergeShepherd изымает фрагмент из merge-shepherd SKILL.md и
// references/legacy-protocol.md (куда бы readContract его ни включил).
func dropFromMergeShepherd(t *testing.T, skillsRoot, dropFragment string) {
	t.Helper()
	for _, rel := range []string{"SKILL.md", filepath.Join("references", "legacy-protocol.md")} {
		path := filepath.Join(skillsRoot, "merge-shepherd", rel)
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), dropFragment) {
			continue
		}
		clean := strings.ReplaceAll(string(data), dropFragment, "фрагмент-убран-тестом")
		if err := os.WriteFile(path, []byte(clean), 0o600); err != nil { //nolint:gosec // G703: test-owned path below t.TempDir
			t.Fatal(err)
		}
	}
}

func TestMergeCleanupOnlyBroken_GetsOwnDiagnostic(t *testing.T) {
	skillsRoot := fixtureSkillsRoot(t)
	dropFromMergeShepherd(t, skillsRoot, "complete merge-cleanup")
	got := report{State: "green"}
	checkContract(&got, filepath.Join(skillsRoot, "review-queue", "SKILL.md"))
	if !hasFinding(got, "unsafe_merge_cleanup_contract") {
		t.Fatalf("поломка merge-cleanup не названа отдельно: %+v", got.Findings)
	}
	if hasFinding(got, "unsafe_base_sync_contract") {
		t.Fatalf("cleanup-only поломка ошибочно названа base-sync: %+v", got.Findings)
	}
}

func TestBaseSyncOnlyBroken_KeepsBaseSyncDiagnostic(t *testing.T) {
	skillsRoot := fixtureSkillsRoot(t)
	dropFromMergeShepherd(t, skillsRoot, "pp:base-sync-intent")
	got := report{State: "green"}
	checkContract(&got, filepath.Join(skillsRoot, "review-queue", "SKILL.md"))
	if !hasFinding(got, "unsafe_base_sync_contract") {
		t.Fatalf("поломка base-sync не названа: %+v", got.Findings)
	}
	if hasFinding(got, "unsafe_merge_cleanup_contract") {
		t.Fatalf("base-sync поломка ошибочно названа merge-cleanup: %+v", got.Findings)
	}
}
