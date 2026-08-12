package backup

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/storage"
)

var universalExternalObjectTables = []string{"_attachments", "_blobs"}

// externalObjectFile is one file which must be present in the attachments
// snapshot. rel always uses portable forward slashes and is relative to the
// files directory (for example, "Orders/<uuid>" or "_blobs/<uuid>").
type externalObjectFile struct {
	rel    string
	size   int64
	source string
}

type externalObjectSet map[string]externalObjectFile // lower-case rel -> file

func (s externalObjectSet) add(rel string, size int64, source string) error {
	if size < 0 {
		return fmt.Errorf("%s has negative size %d", source, size)
	}
	rel = filepath.ToSlash(rel)
	if _, err := portableArchivePath("attachments/"+rel, false); err != nil {
		return fmt.Errorf("%s has unsafe external-object path %q: %w", source, rel, err)
	}
	key := strings.ToLower(rel)
	if previous, ok := s[key]; ok {
		return fmt.Errorf("external-object path %q is referenced more than once (%s and %s)", rel, previous.source, source)
	}
	s[key] = externalObjectFile{rel: rel, size: size, source: source}
	return nil
}

// validateUniversalExportExternalObjects builds the database-derived allowlist
// and proves that the on-disk snapshot is an exact, size-consistent match. A
// successful universal backup must never preserve metadata while silently
// omitting the bytes to which that metadata points.
func validateUniversalExportExternalObjects(ctx context.Context, db *storage.DB, filesDir string) (externalObjectSet, error) {
	expected, err := collectUniversalExportExternalObjects(ctx, db)
	if err != nil {
		return nil, err
	}
	if err := validateExternalObjectTree(filesDir, expected, true); err != nil {
		return nil, fmt.Errorf("export external objects: %w", err)
	}
	return expected, nil
}

// rejectUniversalExportS3References is retained as a narrow preflight helper
// for callers/tests which only need to inspect database-backed locations.
func rejectUniversalExportS3References(ctx context.Context, db *storage.DB) error {
	for _, tableName := range universalExternalObjectTables {
		exists, err := tableExistsChecked(ctx, db, tableName)
		if err != nil {
			return fmt.Errorf("inspect %s for external objects: %w", tableName, err)
		}
		if !exists {
			continue
		}
		hasLoc, err := db.Dialect().ColumnExists(ctx, db, tableName, "loc")
		if err != nil {
			return fmt.Errorf("inspect %s.loc for external objects: %w", tableName, err)
		}
		if !hasLoc {
			continue
		}
		var count int64
		query := "SELECT COUNT(*) FROM " + quotedIdent(db, tableName) +
			" WHERE LOWER(TRIM(COALESCE(loc, ''))) = 's3'"
		if err := db.QueryRow(ctx, query).Scan(&count); err != nil {
			return fmt.Errorf("inspect %s S3 objects: %w", tableName, err)
		}
		if count != 0 {
			return fmt.Errorf("full export does not support S3 content: table %s contains %d external objects", tableName, count)
		}
	}
	return nil
}

func collectUniversalExportExternalObjects(ctx context.Context, db *storage.DB) (externalObjectSet, error) {
	expected := make(externalObjectSet)
	if err := collectExportAttachmentObjects(ctx, db, expected); err != nil {
		return nil, err
	}
	if err := collectExportBlobObjects(ctx, db, expected); err != nil {
		return nil, err
	}
	return expected, nil
}

func collectExportAttachmentObjects(ctx context.Context, db *storage.DB, expected externalObjectSet) error {
	exists, err := tableExistsChecked(ctx, db, "_attachments")
	if err != nil {
		return fmt.Errorf("inspect _attachments for external objects: %w", err)
	}
	if !exists {
		return nil
	}
	cols, err := getTableCols(ctx, db, "_attachments")
	if err != nil {
		return fmt.Errorf("inspect _attachments columns: %w", err)
	}
	for _, required := range []string{"id", "owner_name", "size_bytes"} {
		if !cols[required] {
			return fmt.Errorf("inspect _attachments: required column %q is missing", required)
		}
	}
	locExpr := "''"
	if cols["loc"] {
		locExpr = "COALESCE(loc, '')"
	}
	rows, err := db.Query(ctx, "SELECT id, owner_name, size_bytes, "+locExpr+" FROM "+quotedIdent(db, "_attachments"))
	if err != nil {
		return fmt.Errorf("inspect _attachments objects: %w", err)
	}
	defer rows.Close()
	rowNumber := 0
	for rows.Next() {
		rowNumber++
		var id, owner, loc string
		var size int64
		if err := rows.Scan(&id, &owner, &size, &loc); err != nil {
			return fmt.Errorf("inspect _attachments row %d: %w", rowNumber, err)
		}
		source := fmt.Sprintf("_attachments row %d (%s)", rowNumber, id)
		loc = strings.ToLower(strings.TrimSpace(loc))
		switch loc {
		case storage.FileStorageS3:
			return fmt.Errorf("full export does not support S3 content: %s", source)
		case "", storage.FileStorageDisk:
		default:
			return fmt.Errorf("%s has unsupported loc %q", source, loc)
		}
		rel, err := attachmentExternalPath(owner, id)
		if err != nil {
			return fmt.Errorf("%s: %w", source, err)
		}
		if err := expected.add(rel, size, source); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate _attachments objects: %w", err)
	}
	return nil
}

func collectExportBlobObjects(ctx context.Context, db *storage.DB, expected externalObjectSet) error {
	exists, err := tableExistsChecked(ctx, db, "_blobs")
	if err != nil {
		return fmt.Errorf("inspect _blobs for external objects: %w", err)
	}
	if !exists {
		return nil
	}
	cols, err := getTableCols(ctx, db, "_blobs")
	if err != nil {
		return fmt.Errorf("inspect _blobs columns: %w", err)
	}
	for _, required := range []string{"id", "size", "data"} {
		if !cols[required] {
			return fmt.Errorf("inspect _blobs: required column %q is missing", required)
		}
	}
	locExpr := "''"
	if cols["loc"] {
		locExpr = "COALESCE(loc, '')"
	}
	query := "SELECT id, size, " + locExpr + ", " +
		"CASE WHEN data IS NULL THEN 0 ELSE 1 END, COALESCE(LENGTH(data), 0) FROM " + quotedIdent(db, "_blobs")
	rows, err := db.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("inspect _blobs objects: %w", err)
	}
	defer rows.Close()
	rowNumber := 0
	for rows.Next() {
		rowNumber++
		var id, loc string
		var size, dataPresent, dataSize int64
		if err := rows.Scan(&id, &size, &loc, &dataPresent, &dataSize); err != nil {
			return fmt.Errorf("inspect _blobs row %d: %w", rowNumber, err)
		}
		source := fmt.Sprintf("_blobs row %d (%s)", rowNumber, id)
		if size < 0 || dataSize < 0 {
			return fmt.Errorf("%s has invalid size metadata", source)
		}
		loc = strings.ToLower(strings.TrimSpace(loc))
		switch loc {
		case storage.FileStorageS3:
			return fmt.Errorf("full export does not support S3 content: %s", source)
		case storage.FileStorageDB:
			if dataPresent == 0 {
				return fmt.Errorf("%s has loc=db but no inline data", source)
			}
			if dataSize != size {
				return fmt.Errorf("%s inline size is %d, metadata requires %d", source, dataSize, size)
			}
			continue
		case storage.FileStorageDisk:
			if dataPresent != 0 {
				return fmt.Errorf("%s has loc=disk but also contains inline data", source)
			}
		case "":
			// Legacy rows predate loc. SQL NULL means disk; any non-NULL
			// value, including an empty BLOB, is the inline representation.
			if dataPresent != 0 {
				if dataSize != size {
					return fmt.Errorf("%s inline size is %d, metadata requires %d", source, dataSize, size)
				}
				continue
			}
		default:
			return fmt.Errorf("%s has unsupported loc %q", source, loc)
		}
		rel, err := blobExternalPath(id)
		if err != nil {
			return fmt.Errorf("%s: %w", source, err)
		}
		if err := expected.add(rel, size, source); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate _blobs objects: %w", err)
	}
	return nil
}

// validateUniversalArchiveExternalObjects performs the same semantic check on
// the extracted archive. It runs before directory staging or a database
// transaction, so malformed metadata cannot replace a healthy files tree.
func validateUniversalArchiveExternalObjects(tmpDir, filesDest string) (externalObjectSet, error) {
	expected, err := collectUniversalArchiveExternalObjects(tmpDir)
	if err != nil {
		return nil, err
	}
	if len(expected) != 0 && strings.TrimSpace(filesDest) == "" {
		return nil, fmt.Errorf("import: archive contains %d disk-backed external objects but no files destination was provided", len(expected))
	}
	if err := validateExternalObjectTree(filepath.Join(tmpDir, "attachments"), expected, false); err != nil {
		return nil, fmt.Errorf("import external objects: %w", err)
	}
	return expected, nil
}

// rejectUniversalArchiveS3References remains available for focused callers and
// old tests; the full importer uses validateUniversalArchiveExternalObjects.
func rejectUniversalArchiveS3References(tmpDir string) error {
	_, err := collectUniversalArchiveExternalObjects(tmpDir)
	return err
}

func collectUniversalArchiveExternalObjects(tmpDir string) (externalObjectSet, error) {
	expected := make(externalObjectSet)
	if err := readExternalObjectJSONL(filepath.Join(tmpDir, "system", "_attachments.jsonl"), "_attachments",
		func(rowNumber int, row map[string]json.RawMessage, _ map[string]bool) error {
			id, err := requiredArchiveString(row, "id")
			if err != nil {
				return err
			}
			owner, err := requiredArchiveString(row, "owner_name")
			if err != nil {
				return err
			}
			size, err := requiredArchiveInt64(row, "size_bytes")
			if err != nil {
				return err
			}
			loc, err := optionalArchiveString(row, "loc")
			if err != nil {
				return err
			}
			source := fmt.Sprintf("_attachments row %d (%s)", rowNumber, id)
			switch strings.ToLower(strings.TrimSpace(loc)) {
			case storage.FileStorageS3:
				return fmt.Errorf("import: archive contains unsupported S3 content in %s", source)
			case "", storage.FileStorageDisk:
			default:
				return fmt.Errorf("%s has unsupported loc %q", source, loc)
			}
			rel, err := attachmentExternalPath(owner, id)
			if err != nil {
				return err
			}
			return expected.add(rel, size, source)
		}); err != nil {
		return nil, err
	}
	if err := readExternalObjectJSONL(filepath.Join(tmpDir, "system", "_blobs.jsonl"), "_blobs",
		func(rowNumber int, row map[string]json.RawMessage, btypes map[string]bool) error {
			id, err := requiredArchiveString(row, "id")
			if err != nil {
				return err
			}
			size, err := requiredArchiveInt64(row, "size")
			if err != nil {
				return err
			}
			loc, err := optionalArchiveString(row, "loc")
			if err != nil {
				return err
			}
			dataPresent, dataSize, err := archiveBlobDataSize(row, btypes)
			if err != nil {
				return err
			}
			source := fmt.Sprintf("_blobs row %d (%s)", rowNumber, id)
			switch strings.ToLower(strings.TrimSpace(loc)) {
			case storage.FileStorageS3:
				return fmt.Errorf("import: archive contains unsupported S3 content in %s", source)
			case storage.FileStorageDB:
				if !dataPresent {
					return fmt.Errorf("%s has loc=db but no inline data", source)
				}
				if dataSize != size {
					return fmt.Errorf("%s inline size is %d, metadata requires %d", source, dataSize, size)
				}
				return nil
			case storage.FileStorageDisk:
				if dataPresent {
					return fmt.Errorf("%s has loc=disk but also contains inline data", source)
				}
			case "":
				if dataPresent {
					if dataSize != size {
						return fmt.Errorf("%s inline size is %d, metadata requires %d", source, dataSize, size)
					}
					return nil
				}
			default:
				return fmt.Errorf("%s has unsupported loc %q", source, loc)
			}
			rel, err := blobExternalPath(id)
			if err != nil {
				return err
			}
			return expected.add(rel, size, source)
		}); err != nil {
		return nil, err
	}
	return expected, nil
}

func readExternalObjectJSONL(filePath, tableName string, visit func(int, map[string]json.RawMessage, map[string]bool) error) error {
	f, err := os.Open(filePath) //nolint:gosec // fixed path below the private extracted archive directory
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer closeRead("external-object JSONL", f)

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), maxUniversalJSONLLineBytes)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return err
		}
		return fmt.Errorf("import: %s is empty", filepath.ToSlash(filePath))
	}
	var schema struct {
		Schema int      `json:"_schema"`
		Btypes []string `json:"btypes,omitempty"`
	}
	if err := decodeStrictJSONObject(scanner.Bytes(), &schema); err != nil {
		return fmt.Errorf("import: %s schema: %w", tableName, err)
	}
	btypes := make(map[string]bool, len(schema.Btypes))
	for _, column := range schema.Btypes {
		btypes[column] = true
	}
	rowNumber := 0
	for scanner.Scan() {
		rowNumber++
		var row map[string]json.RawMessage
		if err := decodeStrictJSONObject(scanner.Bytes(), &row); err != nil {
			return fmt.Errorf("import: %s row %d: %w", tableName, rowNumber, err)
		}
		if err := visit(rowNumber, row, btypes); err != nil {
			return fmt.Errorf("import: %s row %d: %w", tableName, rowNumber, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("import: scan %s: %w", tableName, err)
	}
	return nil
}

func requiredArchiveString(row map[string]json.RawMessage, name string) (string, error) {
	raw, ok := row[name]
	if !ok || string(raw) == "null" {
		return "", fmt.Errorf("required field %q is missing", name)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("field %q must be a string: %w", name, err)
	}
	return value, nil
}

func optionalArchiveString(row map[string]json.RawMessage, name string) (string, error) {
	raw, ok := row[name]
	if !ok || string(raw) == "null" {
		return "", nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("field %q must be a string: %w", name, err)
	}
	return value, nil
}

func requiredArchiveInt64(row map[string]json.RawMessage, name string) (int64, error) {
	raw, ok := row[name]
	if !ok || string(raw) == "null" {
		return 0, fmt.Errorf("required field %q is missing", name)
	}
	value, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("field %q must be a non-negative integer", name)
	}
	return value, nil
}

func archiveBlobDataSize(row map[string]json.RawMessage, btypes map[string]bool) (bool, int64, error) {
	raw, ok := row["data"]
	if !ok || string(raw) == "null" {
		return false, 0, nil
	}
	if !btypes["data"] {
		return false, 0, fmt.Errorf("inline blob data is not declared as binary in the schema")
	}
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return false, 0, fmt.Errorf("inline blob data must be a base64 string: %w", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return false, 0, fmt.Errorf("inline blob data is not valid base64: %w", err)
	}
	return true, int64(len(decoded)), nil
}

func canonicalExternalUUID(raw string) (string, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid object id %q: %w", raw, err)
	}
	canonical := id.String()
	if raw != canonical {
		return "", fmt.Errorf("object id %q is not in canonical UUID form %q", raw, canonical)
	}
	return canonical, nil
}

func attachmentExternalPath(owner, rawID string) (string, error) {
	if owner == "" || strings.EqualFold(owner, "_attach_tmp") || strings.EqualFold(owner, "_blobs") {
		return "", fmt.Errorf("unsafe attachment owner %q", owner)
	}
	id, err := canonicalExternalUUID(rawID)
	if err != nil {
		return "", err
	}
	rel := owner + "/" + id
	if _, err := portableArchivePath("attachments/"+rel, false); err != nil {
		return "", fmt.Errorf("unsafe attachment owner %q: %w", owner, err)
	}
	return rel, nil
}

func blobExternalPath(rawID string) (string, error) {
	id, err := canonicalExternalUUID(rawID)
	if err != nil {
		return "", err
	}
	return "_blobs/" + id, nil
}

// validateExternalObjectTree checks an exact allowlist rather than merely a
// total file count. Case-folded keys make an archive portable to Windows while
// exact rel comparison prevents owner-directory casing from changing meaning
// when restored on a case-sensitive filesystem.
func validateExternalObjectTree(root string, expected externalObjectSet, allowAttachmentTemp bool) error {
	if strings.TrimSpace(root) == "" {
		if len(expected) == 0 {
			return nil
		}
		return fmt.Errorf("files directory is empty but %d disk-backed objects are referenced", len(expected))
	}
	info, err := os.Lstat(root)
	if errors.Is(err, fs.ErrNotExist) {
		if len(expected) == 0 {
			return nil
		}
		return fmt.Errorf("files directory %s is missing but %d disk-backed objects are referenced", root, len(expected))
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("files root is not a real directory: %s", root)
	}

	seen := make(map[string]struct{}, len(expected))
	err = filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, filePath)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if allowAttachmentTemp && entry.IsDir() && strings.EqualFold(rel, "_attach_tmp") {
			return fs.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		entryInfo, err := entry.Info()
		if err != nil {
			return err
		}
		if entryInfo.Mode()&os.ModeSymlink != 0 || !entryInfo.Mode().IsRegular() {
			return fmt.Errorf("unsupported external-object file type: %s", rel)
		}
		if _, err := portableArchivePath("attachments/"+rel, false); err != nil {
			return fmt.Errorf("non-portable external-object path %q: %w", rel, err)
		}
		key := strings.ToLower(rel)
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("case-insensitive duplicate external-object path %q", rel)
		}
		seen[key] = struct{}{}
		wanted, ok := expected[key]
		if !ok {
			return fmt.Errorf("unreferenced external-object file %q", rel)
		}
		if wanted.rel != rel {
			return fmt.Errorf("external-object path casing mismatch: metadata requires %q, found %q", wanted.rel, rel)
		}
		if entryInfo.Size() != wanted.size {
			return fmt.Errorf("external-object file %q has size %d, metadata requires %d", rel, entryInfo.Size(), wanted.size)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for key, wanted := range expected {
		if _, ok := seen[key]; !ok {
			return fmt.Errorf("referenced external-object file %q is missing", wanted.rel)
		}
	}
	return nil
}
