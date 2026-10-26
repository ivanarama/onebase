package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Exercise the actual command against a real executable. A module present only
// in go.mod must not be represented as a linked dependency in the inventory.
func TestBundleCommand(t *testing.T) {
	root := t.TempDir()
	fixture, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := fixture.Close(); err != nil {
			t.Error(err)
		}
	}()
	put := func(path, body string) {
		t.Helper()
		name := filepath.FromSlash(path)
		if err := fixture.MkdirAll(filepath.Dir(name), 0755); err != nil {
			t.Fatal(err)
		}
		if err := fixture.WriteFile(name, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	put("go.mod", "module "+modulePath+"\n\ngo 1.26.0\nrequire (\nexample.org/linked v1.0.0\nexample.org/unused v1.0.0\n)\nreplace example.org/linked => ./linked\nreplace example.org/unused => ./unused\n")
	put("cmd/onebase/main.go", "package main\nimport (\"fmt\";\"example.org/linked\")\nfunc main(){fmt.Println(linked.Value())}\n")
	put("linked/go.mod", "module example.org/linked\ngo 1.26.0\n")
	put("linked/lib.go", "package linked\nfunc Value() string{return \"fixture\"}\n")
	const legal = "Fixture copyright\r\nComplete permission text\r\nComplete disclaimer\r\n"
	put("linked/LICENSE", legal)
	put("unused/go.mod", "module example.org/unused\ngo 1.26.0\n")
	put("LICENSE", "Fixture platform license\n")
	put("docs/commercial-use.md", "Fixture bilingual guide\n")
	put("assets/bundle.js", "fixture JavaScript\n")
	put("assets/LICENSE", "Fixture asset terms\n")
	goRoot, err := goOutput(root, "env", "GOROOT")
	if err != nil {
		t.Fatal(err)
	}
	goTerms, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(goRoot)), "LICENSE"))
	if err != nil {
		t.Fatal(err)
	}
	c := catalog{Schema: 1, Modules: map[string]terms{
		"example.org/linked": {License: "fixture", Files: []pin{{Path: "LICENSE", SHA256: hash(canonical([]byte(legal)))}}},
	}, Go: terms{License: "BSD-3-Clause", Files: []pin{{Path: "LICENSE", SHA256: hash(canonical(goTerms))}}},
		ManagedRoots: []string{"assets"}, Resources: []resource{{ID: "browser/fixture", Version: "1", Source: "fixture", Assets: []pin{{Path: "assets/bundle.js", SHA256: hash([]byte("fixture JavaScript\n"))}}, Terms: terms{License: "fixture", Files: []pin{{Path: "assets/LICENSE", SHA256: hash([]byte("Fixture asset terms\n"))}}}}}}
	writeCatalog := func() {
		t.Helper()
		b, e := json.Marshal(c)
		if e != nil {
			t.Fatal(e)
		}
		put("third_party/licenses/catalog.json", string(b))
	}
	writeCatalog()
	git := func(args ...string) {
		t.Helper()
		//nolint:gosec // G204: fixed executable; fixture-only Git arguments, no shell.
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if b, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("git %v: %v\n%s", args, e, b)
		}
	}
	git("init", "-q")
	git("add", ".")
	git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "-qm", "Fixture source")
	exe := filepath.Join(t.TempDir(), "onebase")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	//nolint:gosec // G204: fixed build command; the only variable argument is an output in t.TempDir.
	cmd := exec.Command("go", "build", "-mod=readonly", "-o", exe, "./cmd/onebase")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0")
	if b, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("fixture build: %v\n%s", e, b)
	}
	invoke := func(out string, extra ...string) error {
		args := []string{"-root", root, "-out", out, "-version", "fixture"}
		args = append(args, extra...)
		args = append(args, exe)
		return run(args, new(bytes.Buffer))
	}
	out := filepath.Join(t.TempDir(), "bundle")
	if err := invoke(out, "-release"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(out, "components.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Components) != 3 || bytes.Contains(data, []byte("example.org/unused")) {
		t.Fatalf("wrong linked inventory: %s", data)
	}
	binaryBytes, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Binaries) != 1 || m.Binaries[0].SHA256 != hash(binaryBytes) {
		t.Fatal("wrong binary identity")
	}
	for _, item := range m.Components {
		for _, file := range item.Files {
			body, e := os.ReadFile(filepath.Join(out, filepath.FromSlash(file.Path)))
			if e != nil {
				t.Fatal(e)
			}
			if hash(body) != file.SHA256 {
				t.Fatalf("wrong document checksum: %s", file.Path)
			}
			if item.ID == "go-modules/example.org/linked" && string(body) != legal {
				t.Fatal("original legal text was changed or truncated")
			}
		}
	}
	for _, file := range []string{"LICENSE", "COMMERCIAL_USE.md", "THIRD_PARTY_NOTICES.md"} {
		if _, err := os.Stat(filepath.Join(out, file)); err != nil {
			t.Fatal(err)
		}
	}
	if err := invoke(out); err == nil {
		t.Fatal("existing destination was accepted")
	}
	if body, _ := os.ReadFile(filepath.Join(out, "components.json")); !bytes.Equal(body, data) {
		t.Fatal("existing output changed")
	}
	tests := []struct {
		name, match     string
		change, restore func()
	}{
		{"changed terms", "licens", func() { put("linked/LICENSE", legal+"changed") }, func() { put("linked/LICENSE", legal) }},
		{"new notice", "NOTICE", func() { put("linked/NOTICE", "new notice") }, func() {
			if e := os.Remove(filepath.Join(root, "linked/NOTICE")); e != nil {
				t.Fatal(e)
			}
		}},
		{"unknown module", "unreviewed linked module", func() { delete(c.Modules, "example.org/linked"); writeCatalog() }, func() {
			c.Modules["example.org/linked"] = terms{License: "fixture", Files: []pin{{Path: "LICENSE", SHA256: hash(canonical([]byte(legal)))}}}
			writeCatalog()
		}},
		{"changed asset", "assets/bundle.js", func() { put("assets/bundle.js", "changed") }, func() { put("assets/bundle.js", "fixture JavaScript\n") }},
		{"new asset", "unreviewed vendored asset", func() { put("assets/unknown.js", "unknown") }, func() {
			if e := os.Remove(filepath.Join(root, "assets/unknown.js")); e != nil {
				t.Fatal(e)
			}
		}},
		{"escaping path", "non-local", func() { c.Go.Files[0].Path = "../LICENSE"; writeCatalog() }, func() { c.Go.Files[0].Path = "LICENSE"; writeCatalog() }},
		{"duplicate component", "duplicate component", func() { c.Resources[0].ID = "go"; writeCatalog() }, func() { c.Resources[0].ID = "browser/fixture"; writeCatalog() }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.change()
			defer tt.restore()
			dest := filepath.Join(t.TempDir(), "bundle")
			err := invoke(dest)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tt.match)) {
				t.Fatalf("want %q error, got %v", tt.match, err)
			}
			if _, e := os.Stat(dest); !os.IsNotExist(e) {
				t.Fatal("failed generation left published output")
			}
		})
	}
	t.Run("dirty release source", func(t *testing.T) {
		put("LICENSE", "changed")
		defer put("LICENSE", "Fixture platform license\n")
		if err := invoke(filepath.Join(t.TempDir(), "bundle"), "-release"); err == nil || !strings.Contains(err.Error(), "clean tracked source") {
			t.Fatalf("dirty source accepted: %v", err)
		}
	})
	t.Run("trailing catalog", func(t *testing.T) {
		b, e := json.Marshal(c)
		if e != nil {
			t.Fatal(e)
		}
		put("third_party/licenses/catalog.json", string(b)+" {}")
		defer writeCatalog()
		if err := invoke(filepath.Join(t.TempDir(), "bundle")); err == nil || !strings.Contains(err.Error(), "trailing JSON") {
			t.Fatalf("extra catalog data accepted: %v", err)
		}
	})

	t.Run("bundled browser dependency licenses", func(t *testing.T) {
		repo, err := filepath.Abs(filepath.Join("..", ".."))
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(repo, "third_party/licenses/catalog.json"))
		if err != nil {
			t.Fatal(err)
		}
		var reviewed catalog
		if err := json.Unmarshal(data, &reviewed); err != nil {
			t.Fatal(err)
		}
		original := c.Resources
		defer func() { c.Resources = original; writeCatalog() }()
		for _, resource := range reviewed.Resources {
			if resource.ID != "assets/quill" && resource.ID != "assets/rapidoc" {
				continue
			}
			c.Resources = append(c.Resources, resource)
			for _, file := range append(append([]pin{}, resource.Assets...), resource.Terms.Files...) {
				body, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(file.Path)))
				if err != nil {
					t.Fatal(err)
				}
				put(file.Path, string(body))
			}
		}
		writeCatalog()
		dest := filepath.Join(t.TempDir(), "browser-bundle")
		if err := invoke(dest); err != nil {
			t.Fatal(err)
		}
		data, err = os.ReadFile(filepath.Join(dest, "components.json"))
		if err != nil {
			t.Fatal(err)
		}
		var result manifest
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"assets/quill", "assets/rapidoc"} {
			t.Run(id, func(t *testing.T) {
				prefix := "third_party/licenses/" + strings.TrimPrefix(id, "assets/") + "/"
				body, err := os.ReadFile(filepath.Join(dest, "licenses", filepath.FromSlash(id+"/"+prefix+"UPSTREAM_DEPENDENCIES.json")))
				if err != nil {
					t.Fatal(err)
				}
				var upstream struct {
					Packages []struct {
						Name, Version string
						Files         []string
					}
				}
				if err := json.Unmarshal(body, &upstream); err != nil {
					t.Fatal(err)
				}
				listed := map[string]bool{}
				for _, component := range result.Components {
					if component.ID == id {
						for _, file := range component.Files {
							listed[file.Path] = true
						}
					}
				}
				quill := map[string]string{"parchment": "3.0.0", "quill-delta": "5.1.0", "eventemitter3": "5.0.1", "fast-diff": "1.3.0", "lodash.clonedeep": "4.5.0", "lodash.isequal": "4.5.0", "lodash-es": "4.17.21"}
				count := 0
				for _, pkg := range upstream.Packages {
					var required []string
					base := prefix + "dependencies/" + pkg.Name + "/" + pkg.Version + "/"
					if id == "assets/quill" {
						if quill[pkg.Name] != pkg.Version {
							t.Fatalf("unreviewed Quill dependency: %s@%s", pkg.Name, pkg.Version)
						}
						delete(quill, pkg.Name)
						required = []string{base + "LICENSE"}
						if pkg.Name == "fast-diff" {
							required = append(required, base+"diff.js.HEADER.txt")
						}
					} else if strings.HasPrefix(pkg.Name, "@swagger-api/apidom-") {
						count++
						required = []string{base + "NOTICE", base + "LICENSES/Apache-2.0.txt", base + "LICENSES/MIT.txt"}
					}
					for _, path := range required {
						found := false
						for _, file := range pkg.Files {
							found = found || file == path
						}
						output := "licenses/" + id + "/" + path
						if !found || !listed[output] {
							t.Fatalf("missing upstream licensing document: %s", path)
						}
						expected, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(path)))
						if err != nil {
							t.Fatal(err)
						}
						actual, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(output)))
						if err != nil || !bytes.Equal(expected, actual) {
							t.Fatalf("original document not preserved: %s: %v", path, err)
						}
					}
				}
				if id == "assets/quill" && len(quill) != 0 {
					t.Fatalf("missing Quill dependencies: %v", quill)
				}
				if id == "assets/rapidoc" && count != 28 {
					t.Fatalf("want 28 reviewed ApiDOM packages, got %d", count)
				}
			})
		}
	})
}

func TestSourceFileSymlinkConfinement(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "LICENSE"), []byte("fixture terms"), 0644); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "source")
	if err := os.Symlink(root, alias); err != nil {
		t.Skipf("symbolic link privilege unavailable (Windows junctions have a separate test): %v", err)
	}
	if data, err := sourceFile(alias, "LICENSE"); err != nil || string(data) != "fixture terms" {
		t.Fatalf("declared symlink root: %q, %v", data, err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "PRIVATE"), []byte("outside source"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := sourceFile(root, "escape/PRIVATE"); err == nil {
		t.Fatal("source root allowed symlink traversal")
	}
}
