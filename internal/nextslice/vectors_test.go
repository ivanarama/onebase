package nextslice

import (
	"os"
	"testing"
)

func TestSharedGoldenVectors(t *testing.T) {
	data, err := os.ReadFile("testdata/golden-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CheckVectors(data); err != nil {
		t.Fatal(err)
	}
}
func TestRealPlan159Manifest(t *testing.T) {
	path := "Plans/159-undefined-and-typed-empty-values.md"
	data, err := os.ReadFile("../../" + path)
	if err != nil {
		t.Fatal(err)
	}
	m, err := ParseManifest(path, data)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Slices) != 4 || m.Slices[0].Key != "A" || m.Slices[3].Next != "done" {
		t.Fatalf("unexpected chain: %+v", m)
	}
}
