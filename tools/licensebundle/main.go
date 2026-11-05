// licensebundle packages reviewed licensing material for the dependencies of
// actual release binaries. Unknown components or changed terms require review.
package main

import (
	"bytes"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"unicode/utf8"
)

const modulePath = "github.com/ivantit66/onebase"

type pin struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type terms struct {
	License string `json:"license"`
	Files   []pin  `json:"files"`
}

type resource struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Source  string `json:"source"`
	Module  string `json:"module,omitempty"`
	Assets  []pin  `json:"assets"`
	Terms   terms  `json:"terms"`
}

type catalog struct {
	Schema       int              `json:"schema"`
	Modules      map[string]terms `json:"modules"`
	Go           terms            `json:"go"`
	Resources    []resource       `json:"resources"`
	ManagedRoots []string         `json:"managed_roots"`
}

type goModule struct {
	Path, Version, Dir, Sum string
	Replace                 *goModule
}

type document struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type component struct {
	ID          string     `json:"id"`
	Version     string     `json:"version"`
	License     string     `json:"license"`
	Source      string     `json:"source"`
	ModuleSum   string     `json:"module_sum,omitempty"`
	Replacement string     `json:"replacement,omitempty"`
	Files       []document `json:"files"`
	Assets      []pin      `json:"assets,omitempty"`
}

type binary struct {
	Name      string            `json:"name"`
	SHA256    string            `json:"sha256"`
	GoVersion string            `json:"go_version"`
	Settings  map[string]string `json:"settings"`
}

type manifest struct {
	Schema     int         `json:"schema"`
	Release    string      `json:"release"`
	Binaries   []binary    `json:"binaries"`
	Components []component `json:"components"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "licensebundle:", err)
		os.Exit(1)
	}
}

func canonical(data []byte) []byte {
	if utf8.Valid(data) && !bytes.ContainsRune(data, 0) {
		return bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	}
	return data
}

func hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Existing files must resolve inside the declared source root, including symlinks.
func sourceFile(root, path string) ([]byte, error) {
	if !filepath.IsLocal(filepath.FromSlash(path)) || strings.Contains(path, "\\") {
		return nil, fmt.Errorf("non-local catalog path %q", path)
	}
	// A root handle follows a junction at the declared root (setup-go uses
	// cross-volume junctions on Windows), while confining all subsequent reads.
	// Resolving paths lexically first is both unnecessary and fragile there.
	base, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("open licensing source root %q: %w", root, err)
	}
	defer func() { _ = base.Close() }()
	return base.ReadFile(filepath.FromSlash(path))
}

func checked(root string, p pin) ([]byte, error) {
	data, err := sourceFile(root, p.Path)
	if err != nil {
		return nil, err
	}
	if len(p.SHA256) != 64 || hash(canonical(data)) != p.SHA256 {
		return nil, fmt.Errorf("licensing material or asset changed: %s; review the catalog", p.Path)
	}
	return data, nil
}

func goOutput(root string, args ...string) ([]byte, error) {
	//nolint:gosec // G204: fixed Go executable and argument arrays; no shell or module code execution.
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go %s: %w: %s", strings.Join(args, " "), err, &stderr)
	}
	return data, nil
}

func moduleDirs(root string) (map[string]goModule, error) {
	data, err := goOutput(root, "list", "-mod=readonly", "-m", "-json", "all")
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	result := map[string]goModule{}
	for {
		var m goModule
		if err := decoder.Decode(&m); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, err
		}
		if _, exists := result[m.Path]; exists {
			return nil, fmt.Errorf("duplicate module %s", m.Path)
		}
		result[m.Path] = m
	}
	return result, nil
}

func readCatalog(root, path string) (catalog, error) {
	data, err := sourceFile(root, path)
	if err != nil {
		return catalog{}, err
	}
	var c catalog
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil {
		return c, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return c, errors.New("catalog has trailing JSON data")
	}
	if c.Schema != 1 {
		return c, fmt.Errorf("unsupported catalog schema %d", c.Schema)
	}
	return c, nil
}

func validateAssets(root string, c catalog) error {
	known := map[string]bool{}
	ids := map[string]bool{}
	for _, r := range c.Resources {
		if ids[r.ID] || r.ID == "" || r.Version == "" || r.Source == "" {
			return fmt.Errorf("invalid or duplicate resource %q", r.ID)
		}
		ids[r.ID] = true
		for _, p := range append(append([]pin{}, r.Assets...), r.Terms.Files...) {
			if _, err := checked(root, p); err != nil {
				return err
			}
			known[p.Path] = true
		}
	}
	for _, prefix := range c.ManagedRoots {
		if !filepath.IsLocal(filepath.FromSlash(prefix)) {
			return fmt.Errorf("non-local managed root %q", prefix)
		}
		err := filepath.WalkDir(filepath.Join(root, filepath.FromSlash(prefix)), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if strings.HasSuffix(rel, ".go") || rel == "internal/webassets/README.md" {
				return nil
			}
			if !known[rel] {
				return fmt.Errorf("unreviewed vendored asset %s; add its licensing provenance", rel)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func run(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("licensebundle", flag.ContinueOnError)
	rootFlag := flags.String("root", ".", "repository source root")
	outFlag := flags.String("out", "", "new destination directory (must not exist)")
	catalogFlag := flags.String("catalog", "third_party/licenses/catalog.json", "reviewed catalog relative to root")
	version := flags.String("version", "local", "release identifier")
	release := flags.Bool("release", false, "require clean source and binaries built from the current commit")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *outFlag == "" || flags.NArg() == 0 {
		return errors.New("use -out <new directory> [-version <release>] <binary> [<GUI binary>]")
	}
	root, err := filepath.Abs(*rootFlag)
	if err != nil {
		return err
	}
	out, err := filepath.Abs(*outFlag)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(out); !errors.Is(err, os.ErrNotExist) {
		return errors.New("destination already exists or is inaccessible; preserve its contents")
	}
	c, err := readCatalog(root, *catalogFlag)
	if err != nil {
		return err
	}
	if err := validateAssets(root, c); err != nil {
		return err
	}
	dirs, err := moduleDirs(root)
	if err != nil {
		return err
	}
	settingsRaw, err := goOutput(root, "env", "-json", "GOROOT", "GOVERSION")
	if err != nil {
		return err
	}
	var toolchain struct{ GOROOT, GOVERSION string }
	if err := json.Unmarshal(settingsRaw, &toolchain); err != nil {
		return err
	}
	m := manifest{Schema: 1, Release: *version}
	var sourceRevision string
	if *release {
		git := func(args ...string) (string, error) {
			//nolint:gosec // G204: fixed Git executable, only rev-parse/status argument arrays; no shell.
			cmd := exec.Command("git", args...)
			cmd.Dir = root
			data, err := cmd.Output()
			return strings.TrimSpace(string(data)), err
		}
		if sourceRevision, err = git("rev-parse", "HEAD"); err != nil {
			return fmt.Errorf("release source revision: %w", err)
		}
		status, err := git("status", "--porcelain", "--untracked-files=no")
		if err != nil || status != "" {
			return errors.New("release licensing requires clean tracked source")
		}
	}
	deps := map[string]*debug.Module{}
	seenNames := map[string]bool{}
	var revision, goos, goarch string
	for _, path := range flags.Args() {
		info, err := buildinfo.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read release binary %s: %w", path, err)
		}
		if info.Main.Path != modulePath || info.Path != modulePath+"/cmd/onebase" {
			return fmt.Errorf("not a OneBase release binary: %s", path)
		}
		if info.GoVersion != toolchain.GOVERSION {
			return errors.New("binary and license-source Go toolchains differ")
		}
		name := filepath.Base(path)
		if seenNames[name] {
			return fmt.Errorf("duplicate binary basename %s", name)
		}
		seenNames[name] = true
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		b := binary{Name: name, SHA256: hash(data), GoVersion: info.GoVersion, Settings: map[string]string{}}
		for _, setting := range info.Settings {
			switch setting.Key {
			case "GOOS", "GOARCH", "CGO_ENABLED", "-tags", "vcs.revision", "vcs.modified":
				b.Settings[setting.Key] = setting.Value
			}
		}
		if *release && (b.Settings["vcs.revision"] != sourceRevision || b.Settings["vcs.modified"] != "false") {
			return fmt.Errorf("release binary %s is not built from the current clean source commit", name)
		}
		if len(m.Binaries) == 0 {
			revision, goos, goarch = b.Settings["vcs.revision"], b.Settings["GOOS"], b.Settings["GOARCH"]
		} else if revision != b.Settings["vcs.revision"] || goos != b.Settings["GOOS"] || goarch != b.Settings["GOARCH"] {
			return errors.New("release binaries differ in source revision or target")
		}
		m.Binaries = append(m.Binaries, b)
		for _, dep := range info.Deps {
			if old := deps[dep.Path]; old != nil && !sameDependency(old, dep) {
				return fmt.Errorf("binaries disagree on module %s", dep.Path)
			}
			deps[dep.Path] = dep
		}
	}
	// Stage under the destination parent. Never replace or recursively delete an
	// existing user-selected destination; failed validation cannot leave stale output.
	stage, err := os.MkdirTemp(filepath.Dir(out), ".licensebundle-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	write := func(path string, data []byte) error {
		if !filepath.IsLocal(filepath.FromSlash(path)) {
			return fmt.Errorf("non-local output path %q", path)
		}
		name := filepath.Join(stage, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil { //nolint:gosec // G301: public release licensing documents, readable by archive recipients.
			return err
		}
		return os.WriteFile(name, data, 0644) //nolint:gosec // G306: public licenses/notices, never credentials or private material.
	}
	componentIDs := map[string]bool{}
	add := func(id, version, source, dir string, t terms) (component, error) {
		value := component{ID: id, Version: version, Source: source, License: t.License}
		if !fs.ValidPath(id) || id == "." || strings.Contains(id, "\\") {
			return value, fmt.Errorf("invalid component id %q", id)
		}
		if componentIDs[id] {
			return value, fmt.Errorf("duplicate component id %q", id)
		}
		componentIDs[id] = true
		if len(t.Files) == 0 || t.License == "" {
			return value, fmt.Errorf("missing reviewed license for %s", id)
		}
		for _, p := range t.Files {
			data, err := checked(dir, p)
			if err != nil {
				return value, fmt.Errorf("%s: %w", id, err)
			}
			path := "licenses/" + id + "/" + p.Path
			if err := write(path, data); err != nil {
				return value, err
			}
			value.Files = append(value.Files, document{Path: path, SHA256: hash(data)})
		}
		return value, nil
	}
	goComponent, err := add("go", toolchain.GOVERSION, "https://go.dev/", toolchain.GOROOT, c.Go)
	if err != nil {
		return err
	}
	m.Components = append(m.Components, goComponent)
	for path, dep := range deps {
		t, ok := c.Modules[path]
		if !ok {
			return fmt.Errorf("unreviewed linked module %s@%s", path, dep.Version)
		}
		actual, ok := dirs[path]
		if !ok || actual.Version != dep.Version {
			return fmt.Errorf("module graph differs from binary: %s", path)
		}
		dir := actual.Dir
		if dep.Replace != nil {
			// Go build info spells a local replacement's version "(devel)";
			// go list leaves it empty. Both refer to the checked source directory.
			replacementVersion := dep.Replace.Version
			if replacementVersion == "(devel)" {
				replacementVersion = ""
			}
			if actual.Replace == nil || actual.Replace.Path != dep.Replace.Path || actual.Replace.Version != replacementVersion {
				return fmt.Errorf("module replacement differs from binary: %s", path)
			}
			dir = actual.Replace.Dir
		} else if actual.Replace != nil {
			return fmt.Errorf("unexpected module replacement: %s", path)
		} else if actual.Sum != dep.Sum {
			return fmt.Errorf("module checksum differs from binary: %s", path)
		}
		if err := newModuleNotices(dir, t); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		value, err := add("go-modules/"+path, dep.Version, path, dir, t)
		if err != nil {
			return err
		}
		value.ModuleSum = dep.Sum
		if dep.Replace != nil {
			value.Replacement = dep.Replace.Path + "@" + dep.Replace.Version
		}
		m.Components = append(m.Components, value)
	}
	for _, r := range c.Resources {
		if r.Module != "" && deps[r.Module] == nil {
			continue
		}
		value, err := add(r.ID, r.Version, r.Source, root, r.Terms)
		if err != nil {
			return err
		}
		value.Assets = r.Assets
		m.Components = append(m.Components, value)
	}
	sort.Slice(m.Components, func(i, j int) bool { return m.Components[i].ID < m.Components[j].ID })
	sort.Slice(m.Binaries, func(i, j int) bool { return m.Binaries[i].Name < m.Binaries[j].Name })
	for source, destination := range map[string]string{"LICENSE": "LICENSE", "docs/commercial-use.md": "COMMERCIAL_USE.md"} {
		data, err := sourceFile(root, source)
		if err != nil {
			return err
		}
		if err := write(destination, data); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := write("components.json", append(data, '\n')); err != nil {
		return err
	}
	var notice bytes.Buffer
	fmt.Fprintf(&notice, "# Third-party licenses / Лицензии сторонних компонентов\n\nRelease / Версия: %s\n\n", *version)
	fmt.Fprint(&notice, "Inventory is derived from the listed binaries; module versions are their build metadata.\nFull original licensing material is in licenses/. Copyright notices are retained there.\nResource snapshots without an upstream version are identified by content hashes.\nCatalog asset hashes normalize CRLF to LF for UTF-8 text; binary hashes are exact.\nLicensing documents retain their original bytes and have exact file hashes.\nThese notices do not change any component's license.\n\n")
	for _, b := range m.Binaries {
		fmt.Fprintf(&notice, "- `%s`: SHA-256 `%s`, %s, %s/%s, revision `%s`\n", b.Name, b.SHA256, b.GoVersion, b.Settings["GOOS"], b.Settings["GOARCH"], b.Settings["vcs.revision"])
	}
	for _, item := range m.Components {
		fmt.Fprintf(&notice, "\n## %s\n\nVersion / Версия: %s\n\nLicense / Лицензия: %s\n\nSource / Источник: %s\n\n", item.ID, item.Version, item.License, item.Source)
		for _, file := range item.Files {
			fmt.Fprintf(&notice, "- [%s](%s) — SHA-256 `%s`\n", file.Path, file.Path, file.SHA256)
		}
	}
	if err := write("THIRD_PARTY_NOTICES.md", notice.Bytes()); err != nil {
		return err
	}
	if _, err := os.Lstat(out); !errors.Is(err, os.ErrNotExist) {
		return errors.New("destination appeared during generation; preserve it")
	}
	if err := os.Rename(stage, out); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "License bundle: %d binaries, %d components -> %s\n", len(m.Binaries), len(m.Components), out)
	return err
}

func newModuleNotices(dir string, t terms) error {
	known := map[string]bool{}
	for _, p := range t.Files {
		known[p.Path] = true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := strings.ToLower(entry.Name())
		for _, prefix := range []string{"license", "licence", "copying", "notice", "copyright", "patents"} {
			if name == prefix || strings.HasPrefix(name, prefix+".") || strings.HasPrefix(name, prefix+"-") || strings.HasPrefix(name, prefix+"_") {
				if !entry.IsDir() && !known[entry.Name()] {
					return fmt.Errorf("new licensing document %s requires review", entry.Name())
				}
			}
		}
	}
	return nil
}

func sameDependency(a, b *debug.Module) bool {
	if a.Path != b.Path || a.Version != b.Version || a.Sum != b.Sum {
		return false
	}
	if a.Replace == nil || b.Replace == nil {
		return a.Replace == nil && b.Replace == nil
	}
	return a.Replace.Path == b.Replace.Path && a.Replace.Version == b.Replace.Version && a.Replace.Sum == b.Replace.Sum
}
