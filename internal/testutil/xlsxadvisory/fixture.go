// Package xlsxadvisory provides small, synthetic regression workbooks for the
// publicly disclosed Excelize advisories. Never open these in the parent test
// process: Run confines crashes and excessive iteration to a timed subprocess.
package xlsxadvisory

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime/debug"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/xuri/excelize/v2"
)

var Cases = []string{"short-encryption-info", "oversized-cfb-stream", "excessive-row", "cyclic-array"}

func Run(t *testing.T, body func()) {
	t.Helper()
	if os.Getenv("ONEBASE_XLSX_ADVISORY_CHILD") == "1" {
		debug.SetMaxStack(8 << 20)
		body()
		return
	}
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("locate current test executable: %v", err)
	}
	// #nosec G204 -- os.Executable above supplies the current test binary; arguments are separate, with no shell or PATH lookup.
	cmd := exec.CommandContext(ctx, executable, "-test.run=^"+regexp.QuoteMeta(t.Name())+"$", "-test.v")
	cmd.Env = append(os.Environ(), "ONEBASE_XLSX_ADVISORY_CHILD=1", "GOMEMLIMIT=256MiB", "TMPDIR="+dir, "TMP="+dir, "TEMP="+dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("isolated XLSX path failed (deadline=%v): %v\n%s", ctx.Err(), err, out)
	}
	if !bytes.Contains(out, []byte("--- PASS: "+t.Name()+" (")) {
		t.Fatalf("isolated XLSX test did not run: %s", out)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary files survived: %v (%v)", entries, err)
	}
	t.Logf("%s", out)
}

func Book(t *testing.T, scenario string) []byte {
	t.Helper()
	f := excelize.NewFile()
	if err := f.SetCellStr("Sheet1", "A1", "007"); err != nil {
		t.Fatal(err)
	}
	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	source := buf.Bytes()
	switch scenario {
	case "short-encryption-info", "oversized-cfb-stream":
		data, err := excelize.Encrypt(source, &excelize.Options{Password: "fixture"})
		if err != nil {
			t.Fatal(err)
		}
		var name []byte
		for _, u := range utf16.Encode([]rune("EncryptionInfo")) {
			name = binary.LittleEndian.AppendUint16(name, u)
		}
		off := bytes.Index(data, name)
		if off < 512 || off%128 != 0 || off+128 > len(data) || data[off+66] != 2 {
			t.Fatal("missing CFB directory entry")
		}
		size := uint64(4)
		// Larger than the entire file, small enough that a dependency regression
		// cannot allocate gigabytes even inside the isolated worker.
		if scenario == "oversized-cfb-stream" {
			size = uint64(len(data) + 4096)
		}
		binary.LittleEndian.PutUint64(data[off+120:off+128], size)
		return data
	case "excessive-row", "cyclic-array":
		sheet := `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`
		if scenario == "excessive-row" {
			sheet += `<row r="1"><c r="A1" t="inlineStr"><is><t>007</t></is></c></row><row r="2147483647"><c r="A2147483647"><v>1</v></c></row>`
		} else {
			sheet += `<row r="1"><c r="A1"><f t="array" ref="B1">B1</f><v>7</v></c><c r="B1"><f t="array" ref="A1">A1</f><v>12</v></c></row>`
		}
		sheet += `</sheetData></worksheet>`
		data, err := rewriteWorksheet(source, sheet)
		if err != nil {
			t.Fatal(err)
		}
		return data
	default:
		t.Fatalf("unknown scenario %s", scenario)
		return nil
	}
}

// A generated single-cell workbook needs only a few KiB per entry. These
// generous limits bound actual decompression, independently of ZIP headers.
const (
	maxFixtureEntryBytes   = 1 << 20
	maxFixtureArchiveBytes = 4 << 20
)

func rewriteWorksheet(source []byte, sheet string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(source), int64(len(source)))
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	var total int64
	for _, entry := range zr.File {
		w, err := zw.Create(entry.Name)
		if err != nil {
			return nil, err
		}
		var r io.ReadCloser
		if entry.Name == "xl/worksheets/sheet1.xml" {
			r = io.NopCloser(strings.NewReader(sheet))
		} else {
			r, err = entry.Open()
			if err != nil {
				return nil, err
			}
		}
		remaining := min(int64(maxFixtureEntryBytes), int64(maxFixtureArchiveBytes)-total)
		n, copyErr := io.Copy(w, io.LimitReader(r, remaining+1))
		closeErr := r.Close()
		if copyErr != nil {
			return nil, fmt.Errorf("fixture entry %q: %w", entry.Name, copyErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close fixture entry %q: %w", entry.Name, closeErr)
		}
		if n > remaining {
			return nil, fmt.Errorf("fixture entry %q exceeds entry or archive byte limit", entry.Name)
		}
		total += n
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func NoRecoveredPanic(t *testing.T, err error) {
	t.Helper()
	if err != nil && (strings.Contains(err.Error(), "panic") || strings.Contains(err.Error(), "runtime error")) {
		t.Fatalf("dependency panicked: %v", err)
	}
}
