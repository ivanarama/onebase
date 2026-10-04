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

// Зона без перехода на летнее время — вся Россия, Индия, Китай — на Windows и
// на Linux без TZ приходит неименованным time.Local, и её описывает текущее
// смещение. PostgreSQL читает строку TimeZone по правилам POSIX, где
// положительное смещение означает запад: «+03:00» было бы UTC−3. Смещение
// обязано уходить с аббревиатурой в угловых скобках и перевёрнутым знаком.
func TestApplicationTimeZoneNameWritesFixedOffsetsInPOSIX(t *testing.T) {
	at := time.Date(2026, time.September, 28, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		loc  *time.Location
		want string
	}{
		{"Local Europe/Moscow", loadTestLocationNamed(t, "Local", "Europe/Moscow"), "<+03>-3"},
		{"Local Asia/Kolkata", loadTestLocationNamed(t, "Local", "Asia/Kolkata"), "<+0530>-5:30"},
		{"Local Asia/Novosibirsk", loadTestLocationNamed(t, "Local", "Asia/Novosibirsk"), "<+07>-7"},
		{"FixedZone восточнее UTC", time.FixedZone("UTC+3", 3*3600), "<+03>-3"},
		{"FixedZone западнее UTC", time.FixedZone("UTC-5", -5*3600), "<-05>5"},
		{"FixedZone UTC", time.FixedZone("Z0", 0), "<+00>0"},
	} {
		if got := applicationTimeZoneNameFor(tc.loc, "", at); got != tc.want {
			t.Errorf("%s: applicationTimeZoneNameFor() = %q, want %q", tc.name, got, tc.want)
		}
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
