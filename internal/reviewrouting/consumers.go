package reviewrouting

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

var consumerRoles = map[string]string{"review": "review-queue", "fix": "fix-approved", "merge": "merge-shepherd", "tail": "tail-issues"}

func readRegular(path string, limit int64) (data []byte, err error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() > limit {
		return nil, fmt.Errorf("routing consumer must be a bounded regular file")
	}
	file, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
	}()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return nil, fmt.Errorf("routing consumer changed while opening")
	}
	data, err = io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("routing consumer file exceeds size limit")
	}
	return data, nil
}

// ConsumerTree matches PromptPilot's complete recursive tree hash. A marker
// declares parser version only; this function grants no operation authority.
func ConsumerTree(root, role string) (string, error) {
	name, ok := consumerRoles[role]
	if !ok {
		return "", fmt.Errorf("unsupported routing consumer role")
	}
	directory := filepath.Join(root, name)
	info, err := os.Lstat(directory)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("routing consumer directory is missing or linked")
	}
	entry := filepath.Join(directory, "SKILL.md")
	entryBytes, err := readRegular(entry, 1048576)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(entryBytes) {
		return "", fmt.Errorf("routing consumer entry is not UTF-8")
	}
	marker := "<!-- pp:review-routing-v1-consumer stage=" + role + " -->"
	lines := strings.FieldsFunc(string(entryBytes), func(r rune) bool {
		return r == '\n' || r == '\r' || r == '\v' || r == '\f' || r >= 0x1c && r <= 0x1e || r == 0x85 || r == 0x2028 || r == 0x2029
	})
	count := 0
	for _, line := range lines {
		if line == marker {
			count++
		}
	}
	if count != 1 {
		return "", fmt.Errorf("routing consumer version marker is missing or ambiguous")
	}
	var names []string
	err = filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == directory {
			return nil
		}
		if entry.Name() == "__pycache__" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(entry.Name()) == ".pyc" || filepath.Ext(entry.Name()) == ".pyo" {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("routing consumer tree contains a symbolic link")
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(directory, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if !utf8.ValidString(relative) {
			return fmt.Errorf("routing consumer path is not UTF-8")
		}
		for _, r := range relative {
			if r < 32 || r == 127 {
				return fmt.Errorf("routing consumer path contains a control character")
			}
		}
		names = append(names, relative)
		if len(names) > 256 {
			return fmt.Errorf("routing consumer tree exceeds file count")
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(names) == 0 {
		return "", fmt.Errorf("routing consumer file set is empty")
	}
	sort.Strings(names)
	digest := sha256.New()
	digest.Write([]byte("pp-review-routing-consumer-tree-v1\n"))
	total := 0
	for _, name := range names {
		var data []byte
		if name == "SKILL.md" {
			data = entryBytes
		} else {
			data, err = readRegular(filepath.Join(directory, filepath.FromSlash(name)), 1048576)
			if err != nil {
				return "", err
			}
		}
		total += len(data)
		if total > 4194304 {
			return "", fmt.Errorf("routing consumer tree exceeds total size")
		}
		fileHash := sha256.Sum256(data)
		digest.Write([]byte(name))
		digest.Write([]byte{0})
		digest.Write([]byte(hex.EncodeToString(fileHash[:])))
		digest.Write([]byte{'\n'})
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func ConsumerTrees(root string) (map[string]string, error) {
	result := map[string]string{}
	for _, role := range []string{"review", "fix", "merge", "tail"} {
		digest, err := ConsumerTree(root, role)
		if err != nil {
			return nil, err
		}
		result[role] = digest
	}
	return result, nil
}

func equalValue(left, right any) bool {
	a, err := Canonical(left)
	if err != nil {
		return false
	}
	b, err := Canonical(right)
	return err == nil && bytes.Equal(a, b)
}
