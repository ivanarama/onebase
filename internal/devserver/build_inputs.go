package devserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/fsnotify/fsnotify"
)

// goListModule is the subset of `go list -json` module metadata needed to
// distinguish editable local/workspace modules from immutable module-cache
// dependencies.
type goListModule struct {
	Dir     string
	GoMod   string
	Main    bool
	Version string
	Replace *goListModule
}

// goListPackage contains every source category consumed by `go build`.
// EmbedFiles is essential here: ui.js, CSS, locales, PWA assets and fonts are
// inputs to the binary even though none of them has a Go-specific extension.
type goListPackage struct {
	Dir          string
	Standard     bool
	Module       *goListModule
	GoFiles      []string
	CgoFiles     []string
	CFiles       []string
	CXXFiles     []string
	MFiles       []string
	HFiles       []string
	FFiles       []string
	SFiles       []string
	SwigFiles    []string
	SwigCXXFiles []string
	SysoFiles    []string
	EmbedFiles   []string
}

type goBuildInputSnapshot struct {
	files     map[string]struct{}
	watchDirs []string
}

type goBuildInputTracker struct {
	ctx        context.Context
	root       string
	snapshot   goBuildInputSnapshot
	discovered bool
	force      bool
	probe      bool
}

func newGoBuildInputTracker(ctx context.Context, root string) *goBuildInputTracker {
	if ctx == nil {
		ctx = context.Background()
	}
	root = cleanBuildPath(root)
	t := &goBuildInputTracker{ctx: ctx, root: root}
	snapshot, err := discoverGoBuildInputs(ctx, root)
	if err != nil {
		// A temporarily broken module must remain watchable: the next source edit
		// is precisely what may repair it. Until discovery succeeds, accept a
		// conservative set of file changes (see accept).
		watcherLog().Warn("discover Go build inputs failed; using extension fallback", "dir", root, "err", err)
		return t
	}
	t.snapshot = snapshot
	t.discovered = true
	return t
}

func (t *goBuildInputTracker) watchDirs() []string {
	return append([]string(nil), t.snapshot.watchDirs...)
}

// accept records why a debounced refresh was requested. Known build inputs and
// potential compiler inputs force a rebuild. Other create/remove/rename events
// merely probe the go-list manifest: this catches a newly matched go:embed file
// without rebuilding for an unrelated README.
func (t *goBuildInputTracker) accept(path string, isDir bool, op fsnotify.Op) bool {
	if t == nil || t.ctx.Err() != nil {
		return false
	}
	path = cleanBuildPath(path)
	structural := op&(fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0
	if isDir {
		if structural {
			t.probe = true
		}
		return structural
	}

	_, known := t.snapshot.files[pathKey(path)]
	if known {
		t.force = true
		// Creation/removal, module metadata and Go files can change the package
		// manifest, including its embed patterns and local replace modules.
		if structural || isGoBuildMetadata(path) || strings.EqualFold(filepath.Ext(path), ".go") {
			t.probe = true
		}
		return true
	}
	if isPotentialGoBuildInput(path) {
		// When discovery succeeded, let the refreshed manifest decide whether an
		// inactive/foreign compiler-looking file actually affects this build. In
		// fallback mode there is no trustworthy manifest, so remain conservative.
		t.probe = true
		t.force = !t.discovered
		return true
	}
	if structural {
		t.probe = true
		return true
	}
	return false
}

func (t *goBuildInputTracker) refresh() (bool, []string) {
	if t == nil || t.ctx.Err() != nil {
		return false, nil
	}
	force, probe := t.force, t.probe
	t.force, t.probe = false, false
	if !probe {
		return force, nil
	}

	next, err := discoverGoBuildInputs(t.ctx, t.root)
	if err != nil {
		watcherLog().Warn("refresh Go build inputs failed", "dir", t.root, "err", err)
		// Do not swallow a source/module repair edit just because the current
		// module graph is broken. An unrelated structural event (for example a
		// README creation) still must not cause a rebuild.
		return force, nil
	}
	changed := !sameBuildInputFiles(t.snapshot.files, next.files)
	t.snapshot = next
	t.discovered = true
	return force || changed, next.watchDirs
}

func discoverGoBuildInputs(ctx context.Context, root string) (goBuildInputSnapshot, error) {
	tool, err := GoTool()
	if err != nil {
		return goBuildInputSnapshot{}, err
	}
	cmd := exec.CommandContext(ctx, tool, "list", "-e", "-deps", "-json", "./...") //nolint:gosec // tool is resolved by GoTool; fixed go-list arguments
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return goBuildInputSnapshot{}, fmt.Errorf("go list: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	snapshot := goBuildInputSnapshot{files: make(map[string]struct{})}
	watchDirs := make(map[string]struct{})
	dec := json.NewDecoder(bytes.NewReader(out))
	localPackages := 0
	for {
		var pkg goListPackage
		if err := dec.Decode(&pkg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return goBuildInputSnapshot{}, fmt.Errorf("decode go list output: %w", err)
		}
		if !isLocalBuildPackage(root, pkg) {
			continue
		}
		localPackages++
		for _, group := range [][]string{
			pkg.GoFiles, pkg.CgoFiles, pkg.CFiles, pkg.CXXFiles, pkg.MFiles,
			pkg.HFiles, pkg.FFiles, pkg.SFiles, pkg.SwigFiles,
			pkg.SwigCXXFiles, pkg.SysoFiles, pkg.EmbedFiles,
		} {
			for _, name := range group {
				addBuildInput(&snapshot, watchDirs, root, pkg.Dir, name)
			}
		}
		addModuleInputs(&snapshot, watchDirs, root, pkg.Module)
	}
	if localPackages == 0 {
		return goBuildInputSnapshot{}, fmt.Errorf("go list returned no local packages below %s", root)
	}

	// These files affect module/workspace selection even before they exist, so
	// their creation must be recognized without relying on a previous manifest.
	for _, name := range []string{"go.mod", "go.sum", "go.work", "go.work.sum", filepath.Join("vendor", "modules.txt")} {
		addBuildInput(&snapshot, watchDirs, root, root, name)
	}
	if work := activeGoWork(ctx, tool, root); work != "" {
		addBuildInput(&snapshot, watchDirs, root, "", work)
		addBuildInput(&snapshot, watchDirs, root, "", work+".sum")
	}

	snapshot.watchDirs = make([]string, 0, len(watchDirs))
	for dir := range watchDirs {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			snapshot.watchDirs = append(snapshot.watchDirs, dir)
		}
	}
	sort.Strings(snapshot.watchDirs)
	return snapshot, nil
}

func isLocalBuildPackage(root string, pkg goListPackage) bool {
	if pkg.Standard || pkg.Dir == "" {
		return false
	}
	if pathWithin(root, pkg.Dir) {
		return true
	}
	if pkg.Module == nil {
		return false
	}
	if pkg.Module.Main {
		return true // another main module selected by go.work
	}
	return pkg.Module.Replace != nil && pkg.Module.Replace.Dir != "" && pkg.Module.Replace.Version == ""
}

func addModuleInputs(snapshot *goBuildInputSnapshot, watchDirs map[string]struct{}, root string, module *goListModule) {
	if module == nil {
		return
	}
	effective := module
	if module.Replace != nil && module.Replace.Dir != "" && module.Replace.Version == "" {
		effective = module.Replace
	}
	goMod := effective.GoMod
	if goMod == "" && effective.Dir != "" {
		goMod = filepath.Join(effective.Dir, "go.mod")
	}
	if goMod == "" {
		return
	}
	addBuildInput(snapshot, watchDirs, root, "", goMod)
	addBuildInput(snapshot, watchDirs, root, "", filepath.Join(filepath.Dir(goMod), "go.sum"))
}

func addBuildInput(snapshot *goBuildInputSnapshot, watchDirs map[string]struct{}, root, dir, name string) {
	if name == "" {
		return
	}
	path := name
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	path = cleanBuildPath(path)
	snapshot.files[pathKey(path)] = struct{}{}
	// addTree already covers ordinary directories below root, and addDir makes
	// duplicates free. Listing every direct parent also covers real build inputs
	// below an otherwise skipped directory and local/workspace modules outside root.
	watchDirs[filepath.Dir(path)] = struct{}{}
}

func activeGoWork(ctx context.Context, tool, root string) string {
	cmd := exec.CommandContext(ctx, tool, "env", "GOWORK") //nolint:gosec // tool is resolved by GoTool; fixed go-env argument
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	work := strings.TrimSpace(string(out))
	if work == "" || strings.EqualFold(work, "off") {
		return ""
	}
	return cleanBuildPath(work)
}

func isGoBuildMetadata(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	if base == "go.mod" || base == "go.sum" || base == "go.work" || base == "go.work.sum" {
		return true
	}
	return base == "modules.txt" && strings.EqualFold(filepath.Base(filepath.Dir(path)), "vendor")
}

func isPotentialGoBuildInput(path string) bool {
	if isGoBuildMetadata(path) {
		return true
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".c", ".cc", ".cpp", ".cxx", ".m", ".h", ".hh", ".hpp",
		".f", ".for", ".f90", ".s", ".swig", ".swigcxx", ".syso":
		return true
	default:
		return false
	}
}

func sameBuildInputFiles(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for path := range a {
		if _, ok := b[path]; !ok {
			return false
		}
	}
	return true
}

func cleanBuildPath(path string) string {
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return filepath.Clean(path)
}

func pathKey(path string) string {
	path = cleanBuildPath(path)
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

func pathWithin(root, path string) bool {
	root, path = cleanBuildPath(root), cleanBuildPath(path)
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
