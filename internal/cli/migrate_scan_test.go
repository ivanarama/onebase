package cli

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/ivantit66/onebase/internal/onec_forms"
	"github.com/spf13/cobra"
	"golang.org/x/text/encoding/charmap"
)

// Exercise Cobra argument/flag parsing and the same command factory registered
// in the production root, rather than private aggregation helpers.
func executeScan(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()
	root := &cobra.Command{Use: "onebase", SilenceErrors: true, SilenceUsage: true}
	root.AddCommand(newMigrateScanCmd())
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"migrate-scan"}, args...))
	err := root.Execute()
	return out.Bytes(), err
}

func scanJSON(t *testing.T, dir string, args ...string) ([]byte, onec_forms.ExportScan) {
	t.Helper()
	out, err := executeScan(t, append([]string{dir, "--json"}, args...)...)
	if err != nil {
		t.Fatal(err)
	}
	var report onec_forms.ExportScan
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatal(err)
	}
	return out, report
}

func TestMigrateScan_PublicCommand(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"migrate-scan"})
	if err != nil || cmd.Name() != "migrate-scan" {
		t.Fatalf("command not registered: %v", err)
	}
	dir := filepath.Join("testdata", "migrate-scan")
	out, r := scanJSON(t, dir)
	if r.Total != (onec_forms.ScanVolume{Modules: 4, Lines: 20}) || r.BSP != (onec_forms.ScanVolume{Modules: 3, Lines: 9}) || r.Application != (onec_forms.ScanVolume{Modules: 1, Lines: 11}) {
		t.Fatalf("volume: %+v / %+v / %+v", r.Total, r.BSP, r.Application)
	}
	if r.BSPCalls != 5 || r.BSPSubsystems != 2 || r.BSPCommonModules != 3 || r.UsedBSPCommonModules != 2 || r.UnusedBSPCommonModules != 1 || r.RareMethods != 1 {
		t.Fatalf("bad BSP aggregates: %+v", r)
	}
	wantMethods := []onec_forms.ScanFrequency{{Name: "Утилиты.Помощь", Count: 4, Files: 1}, {Name: "Редкий.Вход", Count: 1, Files: 1}}
	if !reflect.DeepEqual(r.Methods, wantMethods) {
		t.Fatalf("lexical calls: %+v", r.Methods)
	}
	for _, row := range r.Concentration {
		if row.Calls != 5 || row.Percent != 100 {
			t.Fatalf("concentration: %+v", row)
		}
	}
	for _, s := range r.Subsystems {
		if s.Calls != 5 || len(s.Methods) != 2 || s.Modules != 3 {
			t.Fatalf("subsystem: %+v", s)
		}
	}
	for _, name := range []string{"НСтр(", "Новый Массив"} {
		want := 3
		if name == "Новый Массив" {
			want = 2
		}
		found := false
		for _, row := range r.RawW040 {
			if row.Name == name {
				found = true
				if row.Count != want || row.Files != 1 {
					t.Fatalf("raw occurrence count: %+v", row)
				}
			}
		}
		if !found {
			t.Fatalf("missing W040 fragment %s", name)
		}
	}
	if r.CompatibilityStatus != "not_evaluated" || len(r.ConfirmedIncompatibilities) != 0 {
		t.Fatal("raw W040 became a compatibility verdict")
	}
	if r.RawW040Profile != "plan179-w040-v1" || len(r.RawW040) != 30 || r.RawW040Occurrences != 5 {
		t.Fatalf("historical measurement vocabulary changed: %+v", r.RawW040)
	}
	if bytes.Contains(out, []byte("Процедура")) || bytes.Contains(out, []byte(dir)) {
		t.Fatal("report leaked source or input path")
	}
	text, err := executeScan(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, phrase := range []string{"Объём BSL", "Вызовы по модулям", "Вызовы по методам", "Концентрация", "Точки входа", "Сырые W040", "не оценивались", "автоматически не подтверждаются"} {
		if !bytes.Contains(text, []byte(phrase)) {
			t.Fatalf("missing text section %q", phrase)
		}
	}
	text2, err := executeScan(t, dir)
	if err != nil || !bytes.Equal(text, text2) {
		t.Fatal("text report is not deterministic")
	}
}

func copyScanFixture(t *testing.T, reverse bool) string {
	t.Helper()
	src := filepath.Join("testdata", "migrate-scan")
	dst := t.TempDir()
	var paths []string
	if err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			paths = append(paths, path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	if reverse {
		sort.Sort(sort.Reverse(sort.StringSlice(paths)))
	}
	for _, path := range paths {
		rel, err := filepath.Rel(src, path)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeScanFixture(dst, rel, data); err != nil {
			t.Fatal(err)
		}
	}
	return dst
}

// Root keeps fixture copies and encoding rewrites inside their temporary
// directory even if a future fixture contains a path outside that directory.
func writeScanFixture(dir, rel string, data []byte) (err error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := root.Close(); err == nil {
			err = closeErr
		}
	}()
	if err := root.MkdirAll(filepath.Dir(rel), 0755); err != nil {
		return err
	}
	return root.WriteFile(rel, data, 0644)
}

func TestMigrateScan_OrderAndNoBSP(t *testing.T) {
	a := copyScanFixture(t, false)
	b := copyScanFixture(t, true)
	out, _ := scanJSON(t, a)
	other, _ := scanJSON(t, b)
	if !bytes.Equal(out, other) {
		t.Fatal("creation order or absolute directory affects JSON")
	}
	if err := os.RemoveAll(filepath.Join(a, "Subsystems")); err != nil {
		t.Fatal(err)
	}
	_, r := scanJSON(t, a)
	if r.BSP.Modules != 0 || r.BSP.Lines != 0 || r.BSPCalls != 0 || r.BSPCommonModules != 0 || r.BSPSubsystems != 0 || r.Application != r.Total {
		t.Fatalf("module names misidentified BSP: %+v", r)
	}
}

func TestMigrateScan_SubsystemReferenceAndCommand(t *testing.T) {
	dir := copyScanFixture(t, false)
	root := filepath.Join(dir, "Subsystems", "СтандартныеПодсистемы.xml")
	data, err := os.ReadFile(root)
	if err != nil {
		t.Fatal(err)
	}
	// This subsystem is referenced by Content rather than ChildObjects.
	data = bytes.Replace(data, []byte("<Content>"), []byte("<Content><xr:Item>Subsystem.Сервис</xr:Item>"), 1)
	if err := writeScanFixture(dir, filepath.Join("Subsystems", "СтандартныеПодсистемы.xml"), data); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Subsystems", "Сервис.xml"), []byte(`<MetaDataObject><Subsystem><Properties><Content><Item>CommonCommand.Обновить</Item></Content></Properties></Subsystem></MetaDataObject>`), 0644); err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(dir, "CommonCommands", "Обновить", "Ext", "CommandModule.bsl")
	if err := os.MkdirAll(filepath.Dir(command), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(command, []byte("Утилиты.Помощь();\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, r := scanJSON(t, dir)
	if r.BSPSubsystems != 3 || r.BSP.Modules != 4 || r.BSP.Lines != 10 || r.BSPCalls != 5 {
		t.Fatalf("referenced subsystem or command escaped BSP: %+v", r)
	}
}

func TestMigrateScan_NestedSubsystemReference(t *testing.T) {
	for _, route := range []string{"content-only", "content-and-child"} {
		t.Run(route, func(t *testing.T) {
			dir := copyScanFixture(t, false)
			_, expected := scanJSON(t, dir)
			rel := filepath.Join("Subsystems", "СтандартныеПодсистемы.xml")
			data, err := os.ReadFile(filepath.Join(dir, rel))
			if err != nil {
				t.Fatal(err)
			}
			// Designer repeats the metadata type at every nesting level.
			data = bytes.Replace(data, []byte("<Content>"), []byte("<Content><xr:Item>Subsystem.СтандартныеПодсистемы.Subsystem.Общие</xr:Item>"), 1)
			if route == "content-only" {
				data = bytes.Replace(data, []byte("<ChildObjects><Subsystem>Общие</Subsystem></ChildObjects>"), nil, 1)
			}
			if err := writeScanFixture(dir, rel, data); err != nil {
				t.Fatal(err)
			}
			_, report := scanJSON(t, dir)
			if !reflect.DeepEqual(report, expected) {
				t.Fatalf("nested Content reference changed report: %+v", report)
			}
			found := false
			for _, subsystem := range report.Subsystems {
				if subsystem.Name == "Subsystem.СтандартныеПодсистемы.Subsystem.Общие" {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing canonical nested subsystem name: %+v", report.Subsystems)
			}
		})
	}
}

// Subsystems is both a Designer directory name and a valid object name.
func TestMigrateScan_SubsystemNamedSubsystems(t *testing.T) {
	const bsp = "Subsystem.СтандартныеПодсистемы"
	for _, tc := range []struct {
		name, parentFile, key, file string
		contentOnly                 bool
	}{
		{"top", "Subsystems/СтандартныеПодсистемы.xml", "Subsystem.Subsystems", "Subsystems/Subsystems.xml", true},
		{"child", "Subsystems/СтандартныеПодсистемы.xml", bsp + ".Subsystem.Subsystems", "Subsystems/СтандартныеПодсистемы/Subsystems/Subsystems.xml", false},
		{"deep", "Subsystems/СтандартныеПодсистемы/Subsystems/Общие.xml", bsp + ".Subsystem.Общие.Subsystem.Subsystems", "Subsystems/СтандартныеПодсистемы/Subsystems/Общие/Subsystems/Subsystems.xml", false},
	} {
		for _, route := range []string{"content", "child"} {
			if tc.contentOnly && route == "child" {
				continue // A top-level sibling is referenced through Content.
			}
			t.Run(tc.name+"/"+route, func(t *testing.T) {
				_, expected := scanJSON(t, filepath.Join("testdata", "migrate-scan"))
				expected.BSPSubsystems++
				expected.Subsystems = append(expected.Subsystems, onec_forms.ScanSubsystem{
					Name: tc.key, Modules: 1, Calls: 4,
					Methods: []onec_forms.ScanFrequency{{Name: "Утилиты.Помощь", Count: 4, Files: 1}},
				})
				sort.Slice(expected.Subsystems, func(i, j int) bool { return expected.Subsystems[i].Name < expected.Subsystems[j].Name })
				var firstJSON, firstText []byte
				for _, reverse := range []bool{false, true} {
					dir := copyScanFixture(t, reverse)
					root, err := os.OpenRoot(dir)
					if err != nil {
						t.Fatal(err)
					}
					data, readErr := root.ReadFile(filepath.FromSlash(tc.parentFile))
					closeErr := root.Close()
					if readErr != nil || closeErr != nil {
						t.Fatalf("read parent: %v; close: %v", readErr, closeErr)
					}
					if route == "content" {
						data = bytes.Replace(data, []byte("<Content>"), []byte("<Content><xr:Item>"+tc.key+"</xr:Item>"), 1)
					} else {
						childRef := []byte("<Subsystem>Subsystems</Subsystem>")
						if bytes.Contains(data, []byte("</ChildObjects>")) {
							data = bytes.Replace(data, []byte("</ChildObjects>"), append(childRef, []byte("</ChildObjects>")...), 1)
						} else {
							end := bytes.LastIndex(data, []byte("</Subsystem>"))
							data = append(data[:end], append([]byte("<ChildObjects><Subsystem>Subsystems</Subsystem></ChildObjects>"), data[end:]...)...)
						}
					}
					// Both the new child and its parent include Утилиты; no module
					// or call may be counted twice through transitive membership.
					child := []byte(`<MetaDataObject><Subsystem><Properties><Name>Subsystems</Name><Content><Item>CommonModule.Утилиты</Item></Content></Properties></Subsystem></MetaDataObject>`)
					writes := []struct {
						rel  string
						body []byte
					}{{tc.parentFile, data}, {tc.file, child}}
					if reverse {
						writes[0], writes[1] = writes[1], writes[0]
					}
					for _, w := range writes {
						if err := writeScanFixture(dir, filepath.FromSlash(w.rel), w.body); err != nil {
							t.Fatal(err)
						}
					}
					for range 2 {
						out, report := scanJSON(t, dir)
						if !reflect.DeepEqual(report, expected) {
							t.Fatalf("object name lost or aggregates changed:\ngot: %+v\nwant: %+v", report, expected)
						}
						text, err := executeScan(t, dir)
						if err != nil || !bytes.Contains(text, []byte(tc.key)) {
							t.Fatalf("text report missing full key %q: err=%v output=%s", tc.key, err, text)
						}
						if firstJSON == nil {
							firstJSON, firstText = out, text
						} else if !bytes.Equal(out, firstJSON) || !bytes.Equal(text, firstText) {
							t.Fatal("report depends on repeated run or file creation order")
						}
					}
				}
			})
		}
	}
}

func TestMigrateScan_MultilineStringsAndEscapedQuotes(t *testing.T) {
	dir := copyScanFixture(t, false)
	app := filepath.Join(dir, "Documents", "Заказ", "Ext", "ObjectModule.bsl")
	code := `Текст = "начало ""Утилиты.Помощь()""
|Редкий.Вход() // inside a string";
Утилиты.Помощь(); // Редкий.Вход()
Редкий.Вход();
`
	if err := os.WriteFile(app, []byte(code), 0644); err != nil {
		t.Fatal(err)
	}
	_, r := scanJSON(t, dir)
	if r.BSPCalls != 2 || len(r.Methods) != 2 {
		t.Fatalf("string or comment was counted as a call: %+v", r.Methods)
	}
}

func TestMigrateScan_Encodings(t *testing.T) {
	for _, enc := range []string{"utf-8-bom", "windows-1251", "utf-16le", "utf-16be", "utf-16le-no-bom"} {
		t.Run(enc, func(t *testing.T) {
			for _, checkout := range []string{"LF", "CRLF"} {
				t.Run(checkout, func(t *testing.T) {
					dir := copyScanFixture(t, false)
					fixtureRoot, err := os.OpenRoot(dir)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						if err := fixtureRoot.Close(); err != nil {
							t.Error(err)
						}
					})
					// Exercise both checkout styles on every host, before conversion.
					if err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
						if err != nil || d.IsDir() {
							return err
						}
						rel, err := filepath.Rel(dir, path)
						if err != nil {
							return err
						}
						data, err := fixtureRoot.ReadFile(rel)
						if err != nil {
							return err
						}
						text := strings.ReplaceAll(string(data), "\r\n", "\n")
						if checkout == "CRLF" {
							text = strings.ReplaceAll(text, "\n", "\r\n")
						}
						return writeScanFixture(dir, rel, []byte(text))
					}); err != nil {
						t.Fatal(err)
					}
					_, expected := scanJSON(t, dir)
					var paths []string
					if err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
						if err != nil {
							return err
						}
						if !d.IsDir() && (strings.HasSuffix(path, ".bsl") || strings.HasSuffix(path, ".xml")) {
							paths = append(paths, path)
						}
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					for _, path := range paths {
						data, err := os.ReadFile(path)
						if err != nil {
							t.Fatal(err)
						}
						// Normalize checkout CRLF before producing encoded CRLF fixtures.
						text := strings.ReplaceAll(string(data), "\r\n", "\n")
						text = strings.ReplaceAll(text, "\n", "\r\n")
						var encoded []byte
						switch enc {
						case "utf-8-bom":
							encoded = append([]byte{0xef, 0xbb, 0xbf}, []byte(text)...)
						case "windows-1251":
							encoded, err = charmap.Windows1251.NewEncoder().Bytes([]byte(text))
						default:
							var order binary.ByteOrder = binary.LittleEndian
							encoded = []byte{0xff, 0xfe}
							if enc == "utf-16be" {
								order = binary.BigEndian
								encoded = []byte{0xfe, 0xff}
							}
							if enc == "utf-16le-no-bom" {
								encoded = nil
							}
							for _, unit := range utf16.Encode([]rune(text)) {
								var b [2]byte
								order.PutUint16(b[:], unit)
								encoded = append(encoded, b[:]...)
							}
						}
						if err != nil {
							t.Fatal(err)
						}
						rel, err := filepath.Rel(dir, path)
						if err != nil {
							t.Fatal(err)
						}
						if err := writeScanFixture(dir, rel, encoded); err != nil {
							t.Fatal(err)
						}
					}
					var args []string
					if enc == "utf-16le-no-bom" {
						args = []string{"--encoding", "utf-16le"}
					}
					_, r := scanJSON(t, dir, args...)
					for i := range expected.Sources {
						expected.Sources[i].Encoding = r.Sources[i].Encoding
					}
					if !reflect.DeepEqual(r, expected) {
						t.Fatalf("encoding changed report: %+v", r)
					}
				})
			}
		})
	}
}

func TestMigrateScan_InputErrorsAreAtomic(t *testing.T) {
	for _, scenario := range []string{"missing-child", "duplicate", "symlink", "bad-xml", "cycle", "bad-utf8", "bad-surrogate", "bom-conflict", "unknown-encoding", "empty", "file"} {
		t.Run(scenario, func(t *testing.T) {
			dir := copyScanFixture(t, false)
			args := []string{dir, "--json"}
			app := filepath.Join(dir, "Documents", "Заказ", "Ext", "ObjectModule.bsl")
			var err error
			switch scenario {
			case "missing-child":
				err = os.Remove(filepath.Join(dir, "Subsystems", "СтандартныеПодсистемы", "Subsystems", "Общие.xml"))
			case "duplicate":
				// Distinct files that resolve to the same case-insensitive key.
				err = writeScanFixture(dir, "Subsystems/СтандартныеПодсистемы.Subsystem.Общие.xml", []byte(`<MetaDataObject><Subsystem/></MetaDataObject>`))
			case "symlink":
				err = os.Symlink(app, filepath.Join(dir, "linked.bsl"))
				if err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			case "bad-xml":
				err = os.WriteFile(filepath.Join(dir, "Subsystems", "СтандартныеПодсистемы.xml"), []byte("<bad>"), 0644)
			case "cycle":
				err = os.WriteFile(filepath.Join(dir, "Subsystems", "СтандартныеПодсистемы", "Subsystems", "Общие.xml"), []byte(`<MetaDataObject><Subsystem><Properties><Content><Item>Subsystem.СтандартныеПодсистемы</Item></Content></Properties></Subsystem></MetaDataObject>`), 0644)
			case "bad-utf8":
				err = os.WriteFile(app, []byte{0xff}, 0644)
				args = append(args, "--encoding", "utf-8")
			case "bad-surrogate":
				err = os.WriteFile(app, []byte{0xff, 0xfe, 0, 0xd8}, 0644)
			case "bom-conflict":
				err = os.WriteFile(app, []byte{0xff, 0xfe, 'A', 0}, 0644)
				args = append(args, "--encoding", "utf-8")
			case "unknown-encoding":
				args = append(args, "--encoding", "guess")
			case "empty":
				args[0] = t.TempDir()
			case "file":
				args[0] = app
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, format := range []string{"json", "text"} {
				if format == "text" {
					args = append(args[:1], args[2:]...) // Remove --json.
				}
				out, err := executeScan(t, args...)
				if err == nil || len(out) != 0 {
					t.Fatalf("expected error without partial %s: err=%v output=%s", format, err, out)
				}
			}
		})
	}
}
