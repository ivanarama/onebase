package pipelinecontract

import (
	"strings"
	"testing"
)

func TestNextSliceBootstrapIsReadOnlyAndPlanOwned(t *testing.T) {
	entry := repositoryFile(t, ".claude", "skills", "plan-approved", "SKILL.md")
	requireAll(t, entry, "pp:plan-slice", "internal/nextslice", "next-slice mutations запрещены", "hold", "Односрезовый PLAN/FIX не меняется")
	docs := repositoryFile(t, "docs", "next-slice-v1.md")
	requireAll(t, docs, "authoritative=false", "executable=false", "mutations_enabled=false", "decision-sha256=", "legacy-bootstrap-required", "срезом C")
	plan := repositoryFile(t, "Plans", "159-undefined-and-typed-empty-values.md")
	for _, marker := range []string{"key=A next=B", "key=B next=C", "key=C next=D", "key=D next=done"} {
		if strings.Count(plan, "<!-- pp:plan-slice "+marker+" -->") != 1 {
			t.Fatalf("missing or duplicate marker %s", marker)
		}
	}
}
