package gengen

import (
	"fmt"
	"github.com/ivantit66/onebase/internal/fsmode"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Generator creates a project by copying a domain template to the output directory.
type Generator struct {
	OutputDir string
}

// Generate copies the resolved template directory to OutputDir and patches app.yaml.
func (g *Generator) Generate(templateDir string, addons []string) error {
	if templateDir == "" {
		return fmt.Errorf("no template directory resolved for this domain")
	}

	// 1. Copy template directory
	if err := copyDir(templateDir, g.OutputDir); err != nil {
		return fmt.Errorf("copy template: %w", err)
	}

	// 2. Apply addons (each addon overlays its files on top of the base template)
	for _, addon := range addons {
		addonDir := filepath.Join(templateDir, "addons", addon)
		if !dirExists(addonDir) {
			continue
		}
		if err := copyDir(addonDir, g.OutputDir); err != nil {
			return fmt.Errorf("apply addon %s: %w", addon, err)
		}
	}

	// 3. Patch config/app.yaml if it exists
	appYAML := filepath.Join(g.OutputDir, "config", "app.yaml")
	if fileExists(appYAML) {
		if err := patchAppYAML(appYAML, addons); err != nil {
			return fmt.Errorf("patch app.yaml: %w", err)
		}
	}

	return nil
}

// copyDir recursively copies srcDir to dstDir.
// Existing files are NOT overwritten (safe for merge mode).
func copyDir(srcDir, dstDir string) error {
	return filepath.WalkDir(srcDir, func(srcPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(srcDir, srcPath)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dstDir, rel)

		if d.IsDir() {
			return os.MkdirAll(dstPath, fsmode.Dir)
		}

		// Skip if destination already exists (merge-safe)
		if fileExists(dstPath) {
			return nil
		}

		return copyFile(srcPath, dstPath)
	})
}

// copyFile copies a single file from src to dst.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), fsmode.Dir); err != nil {
		return err
	}
	return os.WriteFile(dst, data, fsmode.File) //nolint:gosec // G703: путь получен обходом каталога проекта (os.ReadDir/WalkDir), из запроса он не приходит
}

// patchAppYAML updates config/app.yaml with addon references.
// In MVP this is a no-op; full implementation would parse and merge YAML.
func patchAppYAML(path string, addons []string) error {
	if len(addons) == 0 {
		return nil
	}
	// TODO: parse YAML, append addon references, write back
	// For MVP, app.yaml is copied as-is from the template.
	return nil
}

// fileExists checks if a file exists on the filesystem.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// SanitizePrompt extracts a project name from the prompt for directory naming.
func SanitizePrompt(prompt string) string {
	// Take first 3 words, lowercase, replace spaces with dashes
	words := strings.Fields(prompt)
	if len(words) > 3 {
		words = words[:3]
	}
	name := strings.Join(words, "-")
	// Remove non-alphanumeric (keep cyrillic, latin, dashes)
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r >= 0x0400 {
			b.WriteRune(r)
		}
	}
	return b.String()
}
