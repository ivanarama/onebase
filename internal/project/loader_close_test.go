package project

import (
	"sync/atomic"
	"testing"
)

func TestProjectCloseIsNilSafeAndIdempotent(t *testing.T) {
	var calls atomic.Int32
	proj := &Project{cleanup: func() { calls.Add(1) }}

	proj.Close()
	proj.Close()
	var nilProject *Project
	nilProject.Close()

	if got := calls.Load(); got != 1 {
		t.Fatalf("cleanup calls = %d, want 1", got)
	}
}
