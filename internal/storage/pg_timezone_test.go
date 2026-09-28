package storage

import (
	"archive/zip"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestApplicationTimeZoneNameKeepsUnnamedLocalDSTRules(t *testing.T) {
	loc := loadTestLocationNamed(t, "Local", "America/New_York")
	got := applicationTimeZoneNameFor(loc, "", time.Date(2026, time.July, 1, 0, 0, 0, 0, loc))
	const want = "<STD>5<DST>4,M3.2.0/2,M11.1.0/2"
	if got != want {
		t.Fatalf("applicationTimeZoneNameFor() = %q, want %q", got, want)
	}
}

func loadTestLocationNamed(t *testing.T, name, zoneName string) *time.Location {
	t.Helper()
	zones, err := zip.OpenReader(filepath.Join(testZoneinfoRoot(t), "lib", "time", "zoneinfo.zip"))
	if err != nil {
		t.Fatalf("open Go zoneinfo.zip: %v", err)
	}
	defer func() {
		if err := zones.Close(); err != nil {
			t.Errorf("close Go zoneinfo.zip: %v", err)
		}
	}()
	for _, file := range zones.File {
		if file.Name != zoneName {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			t.Fatalf("open zone %s: %v", zoneName, err)
		}
		data, readErr := io.ReadAll(reader)
		closeErr := reader.Close()
		if readErr != nil {
			t.Fatalf("read zone %s: %v", zoneName, readErr)
		}
		if closeErr != nil {
			t.Fatalf("close zone %s: %v", zoneName, closeErr)
		}
		loc, err := time.LoadLocationFromTZData(name, data)
		if err != nil {
			t.Fatalf("load zone %s as %s: %v", zoneName, name, err)
		}
		return loc
	}
	t.Fatalf("zone %s not found in Go zoneinfo.zip", zoneName)
	return nil
}

func testZoneinfoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatalf("determine GOROOT: %v", err)
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		t.Fatal("go env GOROOT returned an empty path")
	}
	return root
}
