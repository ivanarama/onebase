package metadata

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFile_RequiredHeaderAndTablePartFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "required.yaml")
	body := `name: RequiredLoaded
fields:
  - name: HeaderRequired
    type: string
    required: true
  - name: HeaderOptional
    type: string
tableparts:
  - name: Lines
    fields:
      - name: RowRequired
        type: string
        required: true
      - name: RowOptional
        type: number
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	entity, err := LoadFile(path, KindDocument)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if len(entity.Fields) != 2 || !entity.Fields[0].Required || entity.Fields[1].Required {
		t.Fatalf("header required flags were not preserved: %#v", entity.Fields)
	}
	if len(entity.TableParts) != 1 || len(entity.TableParts[0].Fields) != 2 ||
		!entity.TableParts[0].Fields[0].Required || entity.TableParts[0].Fields[1].Required {
		t.Fatalf("table-part required flags were not preserved: %#v", entity.TableParts)
	}
}
