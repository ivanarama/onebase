package backup

import (
	"archive/zip"
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/text/encoding/charmap"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/fsmode"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/secrets"
	"github.com/ivantit66/onebase/internal/storage"
)

// ErrLegacyFormat is returned by ImportUniversal when the archive is not in
// the universal v2 format.
var ErrLegacyFormat = errors.New("archive is not in universal format (use legacy restore)")

// ImportReport summarises what was loaded during ImportUniversal.
type ImportReport struct {
	Tables map[string]int // table name → rows inserted
	Files  int            // attachment files restored
	// TOTPReset — учётные записи, которым при восстановлении погашен второй
	// фактор: их секрет зашифрован мастер-ключом другой установки.
	TOTPReset []string
}

// disableUnreadableTOTP гасит второй фактор у учёток, чей секрет не
// разыменовывается текущим мастер-ключом. Возвращает их логины.
func disableUnreadableTOTP(ctx context.Context, db *storage.DB) ([]string, error) {
	rows, err := db.ListTOTPSecrets(ctx)
	if err != nil {
		return nil, err
	}
	var reset []string
	for _, row := range rows {
		if _, err := secrets.Default().Resolve(row.Value); err == nil {
			continue
		}
		if err := db.DisableTOTPRaw(ctx, row.UserID); err != nil {
			return nil, err
		}
		reset = append(reset, row.Login)
	}
	return reset, nil
}

// systemTables is the ordered list of system tables included in the universal
// backup. The order matters for import (users before sessions, etc.).
var systemTables = []string{
	"_users",
	// Резервные коды второго фактора (план 84). Без них восстановленная база
	// запирала учётку с 2FA намертво: пароль принимается, шаг второго фактора
	// обязателен, а пройти его нечем — секрет под чужим мастер-ключом,
	// резервных кодов ноль.
	"_auth_backup_codes",
	"_roles",
	"_user_roles",
	"_constants",
	"_numerators",
	"_attachments",
	"_audit",
	"_scheduled_runs",
	"_report_presets",
}

// exchangeTables are exported separately so restore can distinguish a safe
// clone from disaster recovery of the same node.
var exchangeTables = []string{
	"_exchange_changes",
	"_exchange_peers",
	"_exchange_applied",
}

// safeSettingKeys are non-secret _settings values that may travel in a
// universal .obz. Do not add secret-bearing keys such as llm.config here.
var safeSettingKeys = map[string]bool{
	"ai.data_scope":      true,
	"ai.daily_token_cap": true,
}

// byteColumns lists known binary columns in system tables.
// Key: "table.column", value: true.
var byteColumns = map[string]bool{
	"_users.password_hash": true,
}

// ----------------------------------------------------------------------------
// Export
// ----------------------------------------------------------------------------

// ExportUniversal writes a v2 universal .obz archive to w.
// configSource: "database" → read config from _onebase_config;
//
//	"file"     → read config from configDir on disk.
func ExportUniversal(
	ctx context.Context,
	db *storage.DB,
	configSource, configDir string,
	attachmentsDir string,
	baseName string,
	w io.Writer,
) (err error) {
	zw := zip.NewWriter(w)
	// Close дописывает центральный каталог zip — без него архив нечитаем.
	// На успешном пути эта ошибка основная, на пути ошибки — вторичная, но
	// терять её нельзя: молча «успешный» бэкап хуже явно неудавшегося.
	defer func() {
		if cerr := zw.Close(); cerr != nil {
			err = errors.Join(err, cerr)
		}
	}()

	// --- 1. DATA tables -------------------------------------------------------
	appTables, err := listAppTables(ctx, db)
	if err != nil {
		return fmt.Errorf("export: list tables: %w", err)
	}
	manifest := make(map[string]int)
	for _, tbl := range appTables {
		entryName := "data/" + tbl + ".jsonl"
		fw, err := zw.Create(entryName)
		if err != nil {
			return err
		}
		n, err := dumpTableJSONL(ctx, db, tbl, fw)
		if err != nil {
			return fmt.Errorf("export table %s: %w", tbl, err)
		}
		manifest[entryName] = n
	}

	// --- 2. SYSTEM tables -----------------------------------------------------
	for _, tbl := range systemTables {
		entryName := "system/" + tbl + ".jsonl"
		fw, err := zw.Create(entryName)
		if err != nil {
			return err
		}
		n, err := dumpTableJSONL(ctx, db, tbl, fw)
		if err != nil {
			// System table may not exist (e.g. fresh base) — skip silently.
			_ = n
		} else {
			manifest[entryName] = n
		}
	}
	// Exchange queues/watermarks are non-secret and required for lossless DR.
	// Clone restore ignores this directory and resets its local node identity.
	hasExchangeState := false
	for _, tbl := range exchangeTables {
		entryName := "exchange/" + tbl + ".jsonl"
		fw, err := zw.Create(entryName)
		if err != nil {
			return err
		}
		n, err := dumpTableJSONL(ctx, db, tbl, fw)
		if err == nil {
			manifest[entryName] = n
			hasExchangeState = true
		}
	}

	// --- 3. SAFE SETTINGS -----------------------------------------------------
	if n, err := exportSafeSettings(ctx, db, zw); err != nil {
		return fmt.Errorf("export settings: %w", err)
	} else if n > 0 {
		manifest["settings/safe.jsonl"] = n
	}

	// --- 4. CONFIG ------------------------------------------------------------
	if err := exportConfig(ctx, db, configSource, configDir, zw); err != nil {
		return fmt.Errorf("export config: %w", err)
	}

	// --- 5. ATTACHMENTS (binary files) ----------------------------------------
	hasAttachments := false
	if attachmentsDir != "" {
		if _, err := os.Stat(attachmentsDir); err == nil {
			fileCount, ferr := exportAttachments(attachmentsDir, zw)
			if ferr == nil && fileCount > 0 {
				hasAttachments = true
				manifest["attachments/"] = fileCount
			}
		}
	}

	// --- 6. manifest.json ----------------------------------------------------
	// Манифест — не украшение: восстановление сверяет по нему число строк.
	// Раньше ошибка zw.Create игнорировалась, а возвращаемый ею nil сделал бы
	// mf.Write паникой. Перебор 858 сценариев обрыва записи такую комбинацию не
	// воспроизвёл, но полагаться на недостижимость nil-указателя незачем.
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("export: манифест: %w", err)
	}
	mf, err := zw.Create("manifest.json")
	if err != nil {
		return fmt.Errorf("export: манифест: %w", err)
	}
	if _, err := mf.Write(manifestJSON); err != nil {
		return fmt.Errorf("export: манифест: %w", err)
	}

	// --- 7. META.txt ----------------------------------------------------------
	dbType := db.Dialect().Name()
	meta := fmt.Sprintf(
		"onebase_full_export\nversion=2\nformat=universal\nsource_db_type=%s\nsource_base=%s\ndate=%s\nhas_attachments=%v\nhas_exchange_state=%v\n",
		dbType, baseName, time.Now().UTC().Format(time.RFC3339), hasAttachments, hasExchangeState,
	)
	mfw, err := zw.Create("META.txt")
	if err != nil {
		return fmt.Errorf("export: META.txt: %w", err)
	}
	if _, err := mfw.Write([]byte(meta)); err != nil {
		return fmt.Errorf("export: META.txt: %w", err)
	}

	return nil
}

// listAppTables returns all non-system table names in the database, sorted.
func listAppTables(ctx context.Context, db *storage.DB) ([]string, error) {
	var q string
	if db.IsSQLite() {
		q = `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE '\_%' ESCAPE '\' ORDER BY name`
	} else {
		q = `SELECT tablename FROM pg_tables WHERE schemaname='public' AND tablename NOT LIKE '\_%' ESCAPE '\' ORDER BY tablename`
	}
	rows, err := db.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name string
		if rows.Scan(&name) != nil {
			continue
		}
		tables = append(tables, name)
	}
	return tables, rows.Err()
}

// dumpTableJSONL streams all rows of tableName into w as JSONL.
// Line 1: schema header {"_schema":1,"btypes":["col1","col2"]}
// Lines 2+: data rows as JSON objects.
// Returns the number of data rows written.
func dumpTableJSONL(ctx context.Context, db *storage.DB, tableName string, w io.Writer) (int, error) {
	// Flush — финализация записи, а не уборка. Раньше ошибка уходила в
	// `defer bw.Flush()` и терялась здесь; наружу она всё же попадала, потому что
	// zip.Writer запоминает ошибку нижележащего писателя и отдаёт её на Close.
	// Полагаться на это не стоит: ошибка приходила без указания таблицы и на
	// другом этапе. Теперь сбой сброса виден там, где произошёл.
	bw := bufio.NewWriterSize(w, 256*1024)

	// Detect byte columns.
	byCols, err := detectByteCols(ctx, db, tableName)
	if err != nil {
		return 0, err
	}

	// Write schema line.
	schemaObj := map[string]any{"_schema": 1}
	if len(byCols) > 0 {
		list := make([]string, 0, len(byCols))
		for c := range byCols {
			list = append(list, c)
		}
		schemaObj["btypes"] = list
	}
	sl, err := json.Marshal(schemaObj)
	if err != nil {
		return 0, fmt.Errorf("dump %s: заголовок схемы: %w", tableName, err)
	}
	if err := writeJSONLine(bw, sl); err != nil {
		return 0, fmt.Errorf("dump %s: заголовок схемы: %w", tableName, err)
	}

	// Stream rows.
	rows, err := db.Query(ctx, "SELECT * FROM "+quotedIdent(db, tableName))
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	cols := rows.FieldNames()
	n := 0
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return n, err
		}

		obj := make(map[string]any, len(cols))
		for i, col := range cols {
			obj[col] = marshalValue(vals[i], byCols[col])
		}
		line, err := json.Marshal(obj)
		if err != nil {
			return n, err
		}
		if err := writeJSONLine(bw, line); err != nil {
			return n, fmt.Errorf("dump %s: строка %d: %w", tableName, n+1, err)
		}
		n++
	}
	if err := rows.Err(); err != nil {
		return n, err
	}
	// Сброс буфера — часть записи: без него хвост таблицы не попадёт в архив.
	if err := bw.Flush(); err != nil {
		return n, fmt.Errorf("dump %s: сброс буфера: %w", tableName, err)
	}
	return n, nil
}

// writeJSONLine пишет одну строку JSONL вместе с переводом строки. Ошибки
// bufio.Writer «залипают»: после первой все последующие записи возвращают её же,
// поэтому достаточно проверять результат — но проверять обязательно, иначе
// обрыв записи останется незамеченным до восстановления.
func writeJSONLine(bw *bufio.Writer, line []byte) error {
	if _, err := bw.Write(line); err != nil {
		return err
	}
	return bw.WriteByte('\n')
}

// detectByteCols returns the set of columns in tableName that store binary data.
func detectByteCols(ctx context.Context, db *storage.DB, tableName string) (map[string]bool, error) {
	// Check our hardcoded list first.
	result := make(map[string]bool)
	for key := range byteColumns {
		parts := strings.SplitN(key, ".", 2)
		if len(parts) == 2 && parts[0] == tableName {
			result[parts[1]] = true
		}
	}

	// Supplement with DB schema metadata.
	if db.IsSQLite() {
		rows, err := db.Query(ctx, "PRAGMA table_info("+sqliteQuote(tableName)+")")
		if err != nil {
			return result, nil
		}
		defer rows.Close()
		for rows.Next() {
			var cid int
			var name, ctype string
			var notnull, pk int
			var dflt any
			if rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk) != nil {
				continue
			}
			if strings.ToUpper(ctype) == "BLOB" {
				result[name] = true
			}
		}
	} else {
		rows, err := db.Query(ctx,
			`SELECT column_name FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 AND data_type='bytea'`,
			tableName)
		if err != nil {
			return result, nil
		}
		defer rows.Close()
		for rows.Next() {
			var col string
			if rows.Scan(&col) == nil {
				result[col] = true
			}
		}
	}
	return result, nil
}

// detectJSONCols returns the set of columns in tableName that have JSON/JSONB type.
func detectJSONCols(ctx context.Context, db *storage.DB, tableName string) (map[string]bool, error) {
	result := make(map[string]bool)
	if db.IsSQLite() {
		return result, nil // SQLite doesn't enforce JSON types
	}
	rows, err := db.Query(ctx,
		`SELECT column_name FROM information_schema.columns
		 WHERE table_schema='public' AND table_name=$1 AND data_type IN ('json','jsonb')`,
		tableName)
	if err != nil {
		return result, nil
	}
	defer rows.Close()
	for rows.Next() {
		var col string
		if rows.Scan(&col) == nil {
			result[col] = true
		}
	}
	return result, nil
}

// detectBoolCols returns the set of columns with boolean type (PG bool / SQLite INTEGER affinity used as bool).
func detectBoolCols(ctx context.Context, db *storage.DB, tableName string) (map[string]bool, error) {
	result := make(map[string]bool)
	if db.IsSQLite() {
		return result, nil
	}
	rows, err := db.Query(ctx,
		`SELECT column_name FROM information_schema.columns
		 WHERE table_schema='public' AND table_name=$1 AND data_type='boolean'`,
		tableName)
	if err != nil {
		return result, nil
	}
	defer rows.Close()
	for rows.Next() {
		var col string
		if rows.Scan(&col) == nil {
			result[col] = true
		}
	}
	return result, nil
}

// detectByteaCols returns the set of columns with bytea type in PostgreSQL.
// Used during import to decide whether to base64-decode btype values:
// only true bytea columns get decoded; text/jsonb columns keep the original string.
func detectByteaCols(ctx context.Context, db *storage.DB, tableName string) (map[string]bool, error) {
	result := make(map[string]bool)
	if db.IsSQLite() {
		rows, err := db.Query(ctx, "PRAGMA table_info("+sqliteQuote(tableName)+")")
		if err != nil {
			return result, nil
		}
		defer rows.Close()
		for rows.Next() {
			var cid int
			var name, ctype string
			var notnull, pk int
			var dflt any
			if rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk) != nil {
				continue
			}
			if strings.ToUpper(ctype) == "BLOB" {
				result[name] = true
			}
		}
		return result, nil
	}
	rows, err := db.Query(ctx,
		`SELECT column_name FROM information_schema.columns
		 WHERE table_schema='public' AND table_name=$1 AND data_type='bytea'`,
		tableName)
	if err != nil {
		return result, nil
	}
	defer rows.Close()
	for rows.Next() {
		var col string
		if rows.Scan(&col) == nil {
			result[col] = true
		}
	}
	return result, nil
}

// jsonColValue converts a json.RawMessage column value to a Go value suitable
// for a JSON/JSONB parameter in INSERT statements. The JSONL on disk may carry
// the value in one of two shapes:
//
//  1. Old (pre-fix) format — a JSON-escaped string. marshalValue used to
//     turn the []byte returned by a JSONB scan into a Go string, and
//     json.Marshal then wrapped it in quotes. The raw bytes look like
//     {\"x\":1} and have to be JSON-unmarshalled to recover the original
//     JSON text. Importing such a value via string(rawVal) would let
//     PostgreSQL store it as a JSON string-scalar, breaking consumers
//     (e.g. unmarshalPermissions in auth/roles.go) that expect an object.
//
//  2. New format — a nested JSON object/array/number/bool. The raw bytes
//     are already valid JSON and can be passed straight through as text.
//
// In both cases we end up with a Go value that pgx will send to PostgreSQL
// as text, where the ::jsonb cast parses it into a real jsonb value.
func jsonColValue(rawVal json.RawMessage) any {
	// Case 1: JSON-escaped string — unwrap to recover the original JSON text.
	var s string
	if err := json.Unmarshal(rawVal, &s); err == nil {
		return s
	}
	// Case 2: raw bytes are already a valid JSON value — pass through as text.
	return string(rawVal)
}

// marshalValue converts a scanned DB value to a JSON-safe Go value.
// For bytes columns, returns base64 string. For Numeric, returns exact decimal string.
func marshalValue(v any, isBytesCol bool) any {
	if v == nil {
		return nil
	}
	switch t := v.(type) {
	case []byte:
		if isBytesCol {
			return base64.StdEncoding.EncodeToString(t)
		}
		// Non-bytes []byte (JSONB from PostgreSQL, JSON BLOB in SQLite).
		// Return as json.RawMessage so json.Marshal embeds the JSON
		// directly instead of encoding it as a JSON string — that
		// string-in-string wrapping is what used to break round-trip
		// imports of jsonb columns like _roles.permissions.
		return json.RawMessage(t)
	case [16]byte:
		// UUID from pgx
		s := fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
			t[0:4], t[4:6], t[6:8], t[8:10], t[10:16])
		return s
	case pgtype.Numeric:
		if !t.Valid || t.NaN {
			return nil
		}
		return numericToString(t)
	case time.Time:
		return t.UTC().Format(time.RFC3339Nano)
	case bool:
		return t
	}
	return v
}

// numericToString converts a pgtype.Numeric to an exact decimal string.
func numericToString(n pgtype.Numeric) string {
	if n.Int == nil {
		return "0"
	}
	negative := n.Int.Sign() < 0
	abs := new(big.Int).Abs(n.Int)
	s := abs.String()

	var result string
	if n.Exp >= 0 {
		// Value = s * 10^Exp — append zeros.
		result = s + strings.Repeat("0", int(n.Exp))
	} else {
		exp := int(-n.Exp)
		for len(s) <= exp {
			s = "0" + s
		}
		intPart := s[:len(s)-exp]
		fracPart := s[len(s)-exp:]
		result = intPart + "." + strings.TrimRight(fracPart, "0")
		result = strings.TrimSuffix(result, ".")
	}
	if negative {
		result = "-" + result
	}
	return result
}

// stripMonoClock normalises timestamp strings produced by Go's time.Time.String().
// SQLite backups may contain values like
//
//	"2026-05-20 21:47:08.5675381 +0300 MSK m=+103.079963701"
//
// PostgreSQL rejects both the monotonic clock suffix ("m=+...") and the
// timezone abbreviation ("MSK"). We strip the monotonic part, then try to
// parse the Go layout and reformat as RFC 3339 which PG accepts natively.
// Non-timestamp strings are returned unchanged.
func stripMonoClock(s string) string {
	// Fast check: timestamp-like strings start with "YYYY-".
	if len(s) < 6 || s[4] != '-' {
		return s
	}

	// Step 1: strip monotonic clock suffix ("m=+N...") if present.
	cleaned := s
	if i := strings.LastIndex(s, " m="); i != -1 {
		cleaned = s[:i]
	}

	// Step 2: try Go time format with timezone abbreviation.
	for _, layout := range []string{
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05 -0700 MST",
	} {
		if t, err := time.Parse(layout, cleaned); err == nil {
			return t.Format(time.RFC3339Nano)
		}
	}

	// Not a timestamp we recognise — return with monotonic already stripped.
	return cleaned
}

// skipConfigPath reports whether a slash-separated relative path inside the
// configuration directory must be excluded from backup and restore. Version
// control metadata and the backups/ directory are not part of the
// configuration; in particular, read-only .git object files break a file-mode
// restore on Windows with "Access is denied" when restore tries to overwrite
// them. The check matches whole path segments so files like ".gitignore" are
// preserved while the ".git" directory tree is pruned.
func skipConfigPath(rel string) bool {
	for _, seg := range strings.Split(rel, "/") {
		switch seg {
		case ".git", ".svn", ".hg":
			return true
		}
	}
	return strings.HasPrefix(rel, "backups/")
}

// exportConfig writes config files into the config/ directory inside zw.
func exportConfig(ctx context.Context, db *storage.DB, configSource, configDir string, zw *zip.Writer) error {
	if configSource == "database" {
		rows, err := db.Query(ctx, `SELECT path, content FROM _onebase_config ORDER BY path`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var path string
			var content []byte
			if rows.Scan(&path, &content) != nil {
				continue
			}
			entryPath := "config/" + strings.ReplaceAll(path, `\`, "/")
			fw, err := zw.Create(entryPath)
			if err != nil {
				return err
			}
			// Ошибку записи конфигурации сообщаем с именем файла: без этого она
			// всплывала бы позже и безадресно — на Close всего архива.
			if _, err := fw.Write(content); err != nil {
				return fmt.Errorf("export config %s: %w", entryPath, err)
			}
		}
		return rows.Err()
	}

	// File source: walk configDir.
	return filepath.WalkDir(configDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(configDir, path)
		rel = strings.ReplaceAll(rel, `\`, "/")
		if skipConfigPath(rel) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		content, err := os.ReadFile(path) //nolint:gosec // G122: обход идёт по каталогу проекта или по временному каталогу, который мы сами распаковали; переход на os.Root — отдельная задача, он меняет поведение
		if err != nil {
			return nil
		}
		fw, err := zw.Create("config/" + rel)
		if err != nil {
			return err
		}
		if _, err := fw.Write(content); err != nil {
			return fmt.Errorf("export config %s: %w", rel, err)
		}
		return nil
	})
}

func exportSafeSettings(ctx context.Context, db *storage.DB, zw *zip.Writer) (int, error) {
	if !tableExists(ctx, db, "_settings") {
		return 0, nil
	}
	rows, err := db.Query(ctx, `SELECT key, value FROM _settings ORDER BY key`)
	if err != nil {
		return 0, nil
	}
	defer rows.Close()

	var selected []map[string]string
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			continue
		}
		if safeSettingKeys[key] || strings.HasPrefix(strings.ToLower(key), "exchange.this_node.") {
			selected = append(selected, map[string]string{"key": key, "value": value})
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(selected) == 0 {
		return 0, nil
	}

	fw, err := zw.Create("settings/safe.jsonl")
	if err != nil {
		return 0, err
	}
	if _, err := fw.Write([]byte(`{"_schema":1}` + "\n")); err != nil {
		return 0, err
	}
	for _, row := range selected {
		line, err := json.Marshal(row)
		if err != nil {
			return 0, err
		}
		if _, err := fw.Write(append(line, '\n')); err != nil {
			return 0, err
		}
	}
	return len(selected), nil
}

// exportAttachments copies attachment binary files into attachments/ in the ZIP.
func exportAttachments(attachmentsDir string, zw *zip.Writer) (int, error) {
	count := 0
	err := filepath.WalkDir(attachmentsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(attachmentsDir, path)
		rel = strings.ReplaceAll(rel, `\`, "/")
		fw, err := zw.Create("attachments/" + rel)
		if err != nil {
			return err
		}
		f, err := os.Open(path) //nolint:gosec // G122: обход идёт по каталогу проекта или по временному каталогу, который мы сами распаковали; переход на os.Root — отдельная задача, он меняет поведение
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(fw, f)
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		count++
		return nil
	})
	return count, err
}

// quotedIdent returns a safely quoted table name for SELECT * FROM.
func quotedIdent(db *storage.DB, name string) string {
	if db.IsSQLite() {
		return sqliteQuote(name)
	}
	safe := strings.ReplaceAll(name, `"`, `""`)
	return `"` + safe + `"`
}

// sqliteQuote returns a double-quoted SQLite identifier.
func sqliteQuote(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// ----------------------------------------------------------------------------
// Import
// ----------------------------------------------------------------------------

// ExchangeRestoreMode controls whether a universal archive restores the local
// replication identity/state or creates an isolated clone.
type ExchangeRestoreMode string

const (
	ExchangeRestoreClone            ExchangeRestoreMode = "clone"
	ExchangeRestoreDisasterRecovery ExchangeRestoreMode = "disaster_recovery"
)

type ImportOptions struct {
	ExchangeMode ExchangeRestoreMode
}

// ImportUniversal restores an isolated clone. It is the safe compatibility
// default for programmatic callers: exchange node identity, tokens and queues
// are cleared. Use ImportUniversalWithOptions for same-node disaster recovery.
func ImportUniversal(
	ctx context.Context,
	db *storage.DB,
	configDest, cfgFileDir string,
	attachmentsDir string,
	r io.ReaderAt,
	size int64,
) (*ImportReport, error) {
	return ImportUniversalWithOptions(ctx, db, configDest, cfgFileDir, attachmentsDir, r, size, ImportOptions{ExchangeMode: ExchangeRestoreClone})
}

// ImportUniversalWithOptions restores a universal v2 .obz archive into db.
// configDest: "database" → store config in _onebase_config table;
//
//	"file"     → write config YAML files to cfgFileDir on disk.
func ImportUniversalWithOptions(
	ctx context.Context,
	db *storage.DB,
	configDest, cfgFileDir string,
	attachmentsDir string,
	r io.ReaderAt,
	size int64,
	opts ImportOptions,
) (*ImportReport, error) {
	if opts.ExchangeMode == "" {
		opts.ExchangeMode = ExchangeRestoreClone
	}
	if opts.ExchangeMode != ExchangeRestoreClone && opts.ExchangeMode != ExchangeRestoreDisasterRecovery {
		return nil, fmt.Errorf("import: неизвестный режим восстановления обмена %q", opts.ExchangeMode)
	}
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("import: open zip: %w", err)
	}
	if err := validateUniversalArchive(zr); err != nil {
		return nil, err
	}

	// --- 1. Read and validate META.txt ----------------------------------------
	meta, err := readMeta(zr)
	if err != nil {
		return nil, err
	}
	if meta["format"] != "universal" {
		return nil, ErrLegacyFormat
	}
	archiveHasExchangeState := strings.EqualFold(meta["has_exchange_state"], "true")

	// --- 2. Extract all entries to a temp directory ---------------------------
	tmpDir, err := os.MkdirTemp("", "onebase-univ-import-*")
	if err != nil {
		return nil, err
	}
	defer removeTemp(tmpDir)

	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		outPath := filepath.Join(tmpDir, filepath.FromSlash(f.Name))
		// Zip-slip guard: ensure the resolved path stays within tmpDir.
		if rel, err := filepath.Rel(tmpDir, outPath); err != nil ||
			rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("недопустимый путь в архиве: %s", f.Name)
		}
		if err := os.MkdirAll(filepath.Dir(outPath), fsmode.Dir); err != nil {
			return nil, err
		}
		if err := extractFile(f, outPath); err != nil {
			return nil, err
		}
	}

	// --- 3. Import configuration ----------------------------------------------
	configDir := filepath.Join(tmpDir, "config")
	if _, err := os.Stat(configDir); err == nil {
		if err := importConfig(ctx, db, configDest, cfgFileDir, configDir); err != nil {
			return nil, fmt.Errorf("import config: %w", err)
		}
	}

	// --- 4. Run schema migration ----------------------------------------------
	if err := migrateSchema(ctx, db, configDest, cfgFileDir); err != nil {
		return nil, fmt.Errorf("import: schema migration: %w", err)
	}

	// --- 5. Import data and system tables -------------------------------------
	report := &ImportReport{Tables: make(map[string]int)}

	// Disable FK constraint enforcement for the bulk load: tables are imported
	// in alphabetical order which may not respect FK dependency order (e.g.
	// поступлениетоваров → склады where с > п alphabetically).
	fkCleanup, err := db.DisableFKForImport(ctx)
	if err != nil {
		return report, fmt.Errorf("import: disable FK: %w", err)
	}
	fkDisabled := true
	defer func() {
		if fkDisabled {
			_ = fkCleanup()
		}
	}()

	// Application/system tables, safe settings and safety switches are one
	// database transaction. A malformed row can no longer leave half of the
	// tables cleared and half restored.
	if importErr := db.WithTxIfNeeded(ctx, func(txCtx context.Context) error {
		dataDir := filepath.Join(tmpDir, "data")
		if _, err := os.Stat(dataDir); err == nil {
			if err := importDir(txCtx, db, dataDir, report, nil); err != nil {
				return fmt.Errorf("import data: %w", err)
			}
		}

		sysDir := filepath.Join(tmpDir, "system")
		if _, err := os.Stat(sysDir); err == nil {
			if err := importDir(txCtx, db, sysDir, report, nil); err != nil {
				return fmt.Errorf("import system: %w", err)
			}
		}

		if opts.ExchangeMode == ExchangeRestoreDisasterRecovery && archiveHasExchangeState {
			exchangeDir := filepath.Join(tmpDir, "exchange")
			if _, err := os.Stat(exchangeDir); err == nil {
				if err := importDir(txCtx, db, exchangeDir, report, nil); err != nil {
					return fmt.Errorf("import exchange state: %w", err)
				}
			}
		}

		settingsFile := filepath.Join(tmpDir, "settings", "safe.jsonl")
		if _, err := os.Stat(settingsFile); err == nil {
			n, err := importSafeSettings(txCtx, db, settingsFile, opts.ExchangeMode == ExchangeRestoreDisasterRecovery && archiveHasExchangeState)
			if err != nil {
				return fmt.Errorf("import settings: %w", err)
			}
			if n > 0 {
				report.Tables["_settings"] = n
			}
		}
		effectiveExchangeMode := opts.ExchangeMode
		if effectiveExchangeMode == ExchangeRestoreDisasterRecovery && !archiveHasExchangeState {
			// Old universal archives cannot safely reconstruct replication state;
			// restore them as isolated clones instead of retaining stale target
			// queues/node identity.
			effectiveExchangeMode = ExchangeRestoreClone
		}
		if err := resetExchangeSecretsAndCloneState(txCtx, db, effectiveExchangeMode); err != nil {
			return fmt.Errorf("import: сброс состояния обмена: %w", err)
		}

		// Restored copies must not silently contact production systems or run OS
		// commands. These switches commit atomically with the imported data.
		if err := db.SaveNetworkEnabled(txCtx, false); err != nil {
			return fmt.Errorf("import: сброс предохранителя сети: %w", err)
		}
		if err := db.SaveExecEnabled(txCtx, false); err != nil {
			return fmt.Errorf("import: сброс переключателя команд ОС: %w", err)
		}
		return nil
	}); importErr != nil {
		cleanupErr := fkCleanup()
		fkDisabled = false
		if cleanupErr != nil {
			return report, errors.Join(importErr, fmt.Errorf("import: restore FK constraints: %w", cleanupErr))
		}
		return report, importErr
	}
	if err := fkCleanup(); err != nil {
		return report, fmt.Errorf("import: restore FK constraints: %w", err)
	}
	fkDisabled = false

	// Полнотекстовый индекс восстановленной базы (план 82) — после коммита
	// данных, см. rebuildSearchIndex.
	if err := rebuildSearchIndex(ctx, db, configDest, cfgFileDir); err != nil {
		return report, fmt.Errorf("import: пересборка полнотекстового индекса: %w", err)
	}

	// Attachment files are copied only after the database transaction commits;
	// every individual destination is published atomically.
	attachSrc := filepath.Join(tmpDir, "attachments")
	if _, err := os.Stat(attachSrc); err == nil {
		n, err := restoreAttachments(attachSrc, attachmentsDir)
		if err != nil {
			return report, fmt.Errorf("import attachments: %w", err)
		}
		report.Files = n
	}

	return report, nil
}

const (
	maxUniversalArchiveEntries  = 100_000
	maxUniversalArchiveExpanded = uint64(64 << 30)
	maxUniversalMetaBytes       = int64(1 << 20)
)

func validateUniversalArchive(zr *zip.Reader) error {
	if len(zr.File) > maxUniversalArchiveEntries {
		return fmt.Errorf("import: слишком много записей в архиве: %d", len(zr.File))
	}
	seen := make(map[string]struct{}, len(zr.File))
	var expanded uint64
	for _, f := range zr.File {
		name := strings.ReplaceAll(f.Name, `\`, "/")
		clean := filepath.Clean(filepath.FromSlash(name))
		if name == "" || strings.ContainsRune(name, 0) || filepath.IsAbs(clean) ||
			strings.HasPrefix(name, "/") ||
			(len(name) >= 2 && ((name[0] >= 'A' && name[0] <= 'Z') || (name[0] >= 'a' && name[0] <= 'z')) && name[1] == ':') ||
			clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("недопустимый путь в архиве: %s", f.Name)
		}
		key := strings.ToLower(clean)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("повторяющаяся запись в архиве: %s", f.Name)
		}
		seen[key] = struct{}{}
		mode := f.Mode()
		if mode&os.ModeType != 0 && !mode.IsDir() {
			return fmt.Errorf("недопустимый тип записи архива: %s", f.Name)
		}
		if f.UncompressedSize64 > maxUniversalArchiveExpanded-expanded {
			return fmt.Errorf("распакованный архив превышает допустимый размер")
		}
		expanded += f.UncompressedSize64
	}
	return nil
}

func importSafeSettings(ctx context.Context, db *storage.DB, filePath string, includeExchangeNode bool) (int, error) {
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		return 0, err
	}
	f, err := os.Open(filePath)
	if err != nil {
		return 0, err
	}
	defer closeRead("файл резервной копии", f)

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	if !scanner.Scan() {
		return 0, nil
	}
	var header struct {
		Schema int `json:"_schema"`
	}
	if err := json.Unmarshal(scanner.Bytes(), &header); err != nil {
		return 0, fmt.Errorf("schema line: %w", err)
	}

	d := db.Dialect()
	q := fmt.Sprintf(
		`INSERT INTO _settings (key, value) VALUES (%s, %s)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		d.Placeholder(1), d.Placeholder(2))
	n := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var row struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if err := json.Unmarshal(line, &row); err != nil {
			return n, fmt.Errorf("parse row %d: %w", n+1, err)
		}
		isExchangeNode := strings.HasPrefix(strings.ToLower(row.Key), "exchange.this_node.")
		if !safeSettingKeys[row.Key] && (!includeExchangeNode || !isExchangeNode) {
			continue
		}
		if _, err := db.Exec(ctx, q, row.Key, row.Value); err != nil {
			return n, fmt.Errorf("upsert setting %s: %w", row.Key, err)
		}
		n++
	}
	return n, scanner.Err()
}

func resetExchangeSecretsAndCloneState(ctx context.Context, db *storage.DB, mode ExchangeRestoreMode) error {
	if tableExists(ctx, db, "_settings") {
		// Tokens are intentionally never exported and an old target token must not
		// survive either restore mode.
		if _, err := db.Exec(ctx, `DELETE FROM _settings WHERE key LIKE 'exchange.token.%'`); err != nil {
			return err
		}
		if mode == ExchangeRestoreClone {
			if _, err := db.Exec(ctx, `DELETE FROM _settings WHERE key LIKE 'exchange.this_node.%'`); err != nil {
				return err
			}
		}
	}
	if mode == ExchangeRestoreClone {
		for _, table := range exchangeTables {
			if !tableExists(ctx, db, table) {
				continue
			}
			if _, err := db.Exec(ctx, "DELETE FROM "+quotedIdent(db, table)); err != nil {
				return err
			}
		}
	}
	return nil
}

// readMeta parses META.txt from the ZIP and returns key→value map.
func readMeta(zr *zip.Reader) (map[string]string, error) {
	for _, f := range zr.File {
		if f.Name != "META.txt" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(io.LimitReader(rc, maxUniversalMetaBytes+1))
		closeErr := rc.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if int64(len(data)) > maxUniversalMetaBytes {
			return nil, fmt.Errorf("META.txt превышает допустимый размер")
		}

		m := make(map[string]string)
		for _, line := range strings.Split(string(data), "\n") {
			if idx := strings.IndexByte(line, '='); idx > 0 {
				m[strings.TrimSpace(line[:idx])] = strings.TrimSpace(line[idx+1:])
			}
		}
		return m, nil
	}
	return map[string]string{}, nil // no META.txt → legacy format
}

// extractFile extracts one ZIP entry to outPath.
func extractFile(f *zip.File, outPath string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer closeRead("поток записи архива", rc)
	out, err := os.OpenFile(outPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, rc) //nolint:gosec // G110: суммарный распакованный объём проверяется до первой записи (validateArchiveEntries/validateUniversalArchive), а после копирования сверяется с UncompressedSize64
	if err != nil {
		_ = out.Close()
		return err
	}
	if uint64(n) != f.UncompressedSize64 { //nolint:gosec // G115: n — результат io.Copy, он неотрицателен по контракту
		_ = out.Close()
		return fmt.Errorf("неполная запись архива: %s", f.Name)
	}
	return out.Close()
}

// importConfig imports config files into the database or filesystem.
func importConfig(ctx context.Context, db *storage.DB, configDest, cfgFileDir, configDir string) error {
	if configDest == "database" {
		repo := configdb.New(db)
		return repo.ImportFromDir(ctx, configDir)
	}
	// File destination: copy YAML files to cfgFileDir.
	if err := os.MkdirAll(cfgFileDir, fsmode.Dir); err != nil {
		return err
	}
	root, err := filepath.EvalSymlinks(cfgFileDir)
	if err != nil {
		return err
	}
	return filepath.WalkDir(configDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(configDir, path)
		if err != nil {
			return err
		}
		// Defensive: older archives may have bundled the project's .git tree.
		// Skip it so restore does not try to overwrite read-only git objects.
		if skipConfigPath(filepath.ToSlash(rel)) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		dst, err := safeConfigDestination(root, rel)
		if err != nil {
			return err
		}
		return copyFilePublished(ctx, path, dst, 0o644)
	})
}

func safeConfigDestination(root, rel string) (string, error) {
	rel = filepath.Clean(rel)
	if rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("import config: unsafe destination %q", rel)
	}
	current := root
	parts := strings.Split(filepath.Dir(rel), string(filepath.Separator))
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		next := filepath.Join(current, part)
		info, err := os.Lstat(next)
		if os.IsNotExist(err) {
			if err := os.Mkdir(next, fsmode.Dir); err != nil {
				return "", err
			}
			current = next
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("import config: destination component is not a real directory: %s", next)
		}
		current = next
	}
	return filepath.Join(current, filepath.Base(rel)), nil
}

func copyFilePublished(ctx context.Context, srcPath, dst string, perm os.FileMode) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer closeRead("исходный файл", src)
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".onebase-config-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(perm); err != nil {
		return err
	}
	if _, err := io.Copy(tmp, contextReader{ctx: ctx, r: src}); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := publishRestoredFile(tmpName, dst); err != nil {
		return err
	}
	committed = true
	return nil
}

// migrateSchema creates all required tables in the target database by loading
// the project configuration and running all Migrate* calls.
// configDest: "database" → load from _onebase_config; "file" → load from cfgFileDir.
// loadProjectForImport поднимает конфигурацию восстановленной базы — из файлов
// или из _onebase_config, как указано в configDest. Вызывающий обязан закрыть
// проект.
func loadProjectForImport(ctx context.Context, db *storage.DB, configDest, cfgFileDir string) (*project.Project, error) {
	if configDest == "file" && cfgFileDir != "" {
		proj, err := project.Load(cfgFileDir)
		if err != nil {
			return nil, fmt.Errorf("load project from files: %w", err)
		}
		return proj, nil
	}
	repo := configdb.New(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		return nil, err
	}
	proj, err := project.LoadFromDB(ctx, repo)
	if err != nil {
		return nil, fmt.Errorf("load project for migration: %w", err)
	}
	return proj, nil
}

// rebuildSearchIndex пересобирает полнотекстовый индекс (план 82) после
// восстановления. Импорт заливает строки прямым INSERT мимо upsert, поэтому
// хук индексации не срабатывает: без пересборки поиск в восстановленной базе
// не находил бы ничего, а поверх существующей — держал бы в выдаче исчезнувшие
// объекты (очищаются только таблицы из архива). Возить индекс в архиве нельзя:
// над ним стоят надстройки диалекта (FTS5-триггеры в SQLite, генерируемая
// колонка в PostgreSQL), и сырые строки оставили бы их рассогласованными.
func rebuildSearchIndex(ctx context.Context, db *storage.DB, configDest, cfgFileDir string) error {
	proj, err := loadProjectForImport(ctx, db, configDest, cfgFileDir)
	if err != nil {
		return err
	}
	defer proj.Close()
	if len(proj.Entities) == 0 {
		// Конфигурации нет — индексировать нечего, а пересборка с пустым
		// списком объектов вычистила бы индекс целиком.
		return nil
	}
	if _, err := db.RebuildFullTextIndex(ctx, proj.Entities, 0, nil); err != nil {
		return err
	}
	return nil
}

func migrateSchema(ctx context.Context, db *storage.DB, configDest, cfgFileDir string) error {
	var proj *project.Project
	var err error

	if configDest == "file" && cfgFileDir != "" {
		proj, err = project.Load(cfgFileDir)
		if err != nil {
			return fmt.Errorf("load project from files: %w", err)
		}
	} else {
		repo := configdb.New(db)
		if err := repo.EnsureSchema(ctx); err != nil {
			return err
		}
		proj, err = project.LoadFromDB(ctx, repo)
		if err != nil {
			return fmt.Errorf("load project for migration: %w", err)
		}
	}
	defer proj.Close()

	if err := db.Migrate(ctx, proj.Entities); err != nil {
		return fmt.Errorf("migrate entities: %w", err)
	}
	if err := db.MigrateRegisters(ctx, proj.Registers); err != nil {
		return fmt.Errorf("migrate registers: %w", err)
	}
	if err := db.MigrateInfoRegisters(ctx, proj.InfoRegisters); err != nil {
		return fmt.Errorf("migrate info registers: %w", err)
	}
	if err := db.MigrateConstants(ctx, proj.Constants); err != nil {
		return fmt.Errorf("migrate constants: %w", err)
	}
	// _accounts не экспортируется (ни в data/, ни в system/) — таблицу нужно
	// создать здесь, иначе SyncAccounts падает с "no such table: _accounts" на
	// конфигурациях с планом счетов. Остальные пути (run/migrate/deploy/dev)
	// вызывают EnsureAccountsTable явно — импорт обязан делать то же самое.
	if err := db.EnsureAccountsTable(ctx); err != nil {
		return fmt.Errorf("ensure accounts table: %w", err)
	}
	if err := db.SyncAccounts(ctx, proj.ChartsOfAccounts); err != nil {
		return fmt.Errorf("sync accounts: %w", err)
	}
	if err := db.MigrateAccountRegisters(ctx, proj.AccountRegisters); err != nil {
		return fmt.Errorf("migrate account registers: %w", err)
	}
	if err := db.EnsureAuditSchema(ctx); err != nil {
		return fmt.Errorf("ensure audit schema: %w", err)
	}
	if err := db.EnsureScheduledRunsTable(ctx); err != nil {
		return fmt.Errorf("ensure scheduled runs: %w", err)
	}
	if err := db.EnsureReportPresetSchema(ctx); err != nil {
		return fmt.Errorf("ensure report presets: %w", err)
	}
	if err := db.EnsureAttachmentTable(ctx); err != nil {
		return fmt.Errorf("ensure attachments: %w", err)
	}
	if err := db.EnsureBlobTable(ctx); err != nil {
		return fmt.Errorf("ensure blobs: %w", err)
	}
	if err := db.EnsureExchangeSchema(ctx); err != nil {
		return fmt.Errorf("ensure exchange schema: %w", err)
	}
	authRepo := auth.NewRepo(db)
	if err := authRepo.EnsureSchema(ctx); err != nil {
		return fmt.Errorf("ensure auth schema: %w", err)
	}
	return nil
}

// importDir walks a directory of .jsonl files and imports each into the DB.
func importDir(ctx context.Context, db *storage.DB, dir string, report *ImportReport, skip map[string]bool, extraSkip ...[]string) error {
	// Build combined skip set from skip + optional extraSkip.
	allSkip := make(map[string]bool, len(skip))
	for k, v := range skip {
		allSkip[k] = v
	}
	if len(extraSkip) > 0 {
		for _, tbl := range extraSkip[0] {
			allSkip[strings.ToLower(tbl)] = true
		}
	}

	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		// Derive table name from filename: "Номенклатура.jsonl" → "номенклатура"
		base := filepath.Base(path)
		tableName := strings.TrimSuffix(base, ".jsonl")

		if allSkip[strings.ToLower(tableName)] {
			return nil
		}

		n, err := importTableJSONL(ctx, db, tableName, path)
		if err != nil {
			return fmt.Errorf("import table %s: %w", tableName, err)
		}
		report.Tables[tableName] = n
		return nil
	})
}

// importTableJSONL reads a JSONL file and inserts all rows into tableName.
// The first line must be a schema header ({"_schema":1,"btypes":[...]}).
func importTableJSONL(ctx context.Context, db *storage.DB, tableName, filePath string) (int, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return 0, err
	}
	defer closeRead("файл резервной копии", f)

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)

	// Read schema line.
	if !scanner.Scan() {
		return 0, nil // empty file
	}
	type schemaLine struct {
		Schema int      `json:"_schema"`
		Btypes []string `json:"btypes"`
	}
	var schema schemaLine
	if err := json.Unmarshal(scanner.Bytes(), &schema); err != nil {
		return 0, fmt.Errorf("schema line: %w", err)
	}
	btypes := make(map[string]bool)
	for _, c := range schema.Btypes {
		btypes[c] = true
	}

	// Check table exists; skip if not (e.g. a table from a different config version).
	if !tableExists(ctx, db, tableName) {
		return 0, nil
	}

	// Get the columns that actually exist in the target table.
	existingCols, err := getTableCols(ctx, db, tableName)
	if err != nil {
		return 0, fmt.Errorf("get columns for %s: %w", tableName, err)
	}

	// Detect JSON/JSONB columns so we can cast values on insert.
	jsonCols, _ := detectJSONCols(ctx, db, tableName)

	// Detect boolean columns so we can convert numeric 0/1 → bool (PG OID 16).
	boolCols, _ := detectBoolCols(ctx, db, tableName)

	// Detect bytea columns so we only base64-decode btypes for actual binary columns.
	byteaCols, _ := detectByteaCols(ctx, db, tableName)

	// Clear existing data — we do a full replace.
	if _, err := db.Exec(ctx, "DELETE FROM "+quotedIdent(db, tableName)); err != nil {
		return 0, fmt.Errorf("clear table %s: %w", tableName, err)
	}

	n := 0
	colsChecked := false
	for scanner.Scan() {
		line := scanner.Bytes()
		// json.Unmarshal replaces invalid UTF-8 inside JSON string
		// literals with U+FFFD, which then survives all the way to
		// PostgreSQL and triggers SQLSTATE 22021. We have to fix the
		// encoding BEFORE the parser sees the line, so transcode
		// from Windows-1251 (the legacy single-byte encoding used by
		// older OneBase builds and most pre-Unicode Russian tools)
		// up-front when the raw line isn't already valid UTF-8.
		if !utf8.Valid(line) {
			if decoded, err := charmap.Windows1251.NewDecoder().Bytes(line); err == nil {
				line = decoded
			}
		}
		if len(line) == 0 {
			continue
		}

		var raw map[string]json.RawMessage
		if err := json.Unmarshal(line, &raw); err != nil {
			return n, fmt.Errorf("parse row %d: %w", n+1, err)
		}

		// On first data row discover columns that exist in the archive but not in
		// the target table (e.g. stale columns from schema evolution in source DB).
		// Add them so no data is silently dropped during a full restore.
		if !colsChecked {
			colsChecked = true
			for col := range raw {
				if !existingCols[col] {
					_ = db.AddColumnIfMissing(ctx, tableName, col, db.Dialect().TypeText())
					existingCols[col] = true
				}
			}
		}

		if err := insertRow(ctx, db, tableName, raw, btypes, existingCols, jsonCols, boolCols, byteaCols); err != nil {
			return n, fmt.Errorf("insert row %d into %s: %w", n+1, tableName, err)
		}
		n++
	}
	return n, scanner.Err()
}

// insertRow builds and executes an INSERT statement for one JSON row.
// existingCols is the set of column names that exist in the target table;
// columns not in this set are skipped (handles source/target schema differences).
// jsonCols is the set of JSON/JSONB columns — their raw string value is passed
// directly so PostgreSQL can parse it as JSON.
// boolCols is the set of boolean columns — numeric 0/1 values are converted to bool
// so that the PG driver does not fail with "cannot find encode plan for bool (OID 16)".
func insertRow(ctx context.Context, db *storage.DB, tableName string, raw map[string]json.RawMessage, btypes map[string]bool, existingCols map[string]bool, jsonCols map[string]bool, boolCols map[string]bool, byteaCols map[string]bool) error {
	d := db.Dialect()

	cols := make([]string, 0, len(raw))
	args := make([]any, 0, len(raw))
	placeholders := make([]string, 0, len(raw))
	idx := 1

	for col, rawVal := range raw {
		if len(existingCols) > 0 && !existingCols[col] {
			continue // column absent in target table — skip
		}
		var goVal any

		if string(rawVal) == "null" {
			goVal = nil
		} else if btypes[col] && byteaCols[col] {
			// Column is in btypes AND is bytea in target PG — decode base64 → raw bytes.
			var b64 string
			if err := json.Unmarshal(rawVal, &b64); err != nil {
				return fmt.Errorf("col %s: base64 unmarshal: %w", col, err)
			}
			decoded, err := base64.StdEncoding.DecodeString(b64)
			if err != nil {
				goVal = b64
			} else {
				goVal = decoded
			}
		} else if btypes[col] {
			// Column is in btypes but target is NOT bytea (text/jsonb).
			// The source side stored a binary value as base64, so the JSONL
			// holds a JSON-escaped string containing the base64 text. Try
			// to base64-decode; if the value isn't base64 at all (e.g. it
			// is the literal string "null"/"true" written by export as a
			// JSON string, or any other valid JSON snippet), pass it
			// through — PostgreSQL's ::jsonb cast will then parse it.
			var s string
			if err := json.Unmarshal(rawVal, &s); err != nil {
				return fmt.Errorf("col %s: string unmarshal: %w", col, err)
			}
			if jsonCols[col] {
				if decoded, err := base64.StdEncoding.DecodeString(s); err == nil {
					// The base64 payload may carry Windows-1251 text
					// from legacy OneBase builds (PostgreSQL rejects
					// non-UTF-8 bytes with SQLSTATE 22021, e.g. 0x9E
					// for "О"). Transcode to UTF-8 when needed; if
					// the bytes are already UTF-8, keep them as-is.
					var utf8bytes []byte
					if utf8.Valid(decoded) {
						utf8bytes = decoded
					} else if cp1251, err2 := charmap.Windows1251.NewDecoder().Bytes(decoded); err2 == nil {
						utf8bytes = cp1251
					} else {
						utf8bytes = decoded
					}
					// PostgreSQL's ::jsonb cast requires a valid JSON
					// value. The legacy BLOB may hold plain text (e.g.
					// Python repr) rather than a JSON document, so
					// validate the payload. If it's already valid JSON
					// (object, array, number, bool, null, or quoted
					// string), pass it through. Otherwise wrap it as a
					// JSON string so the value is always well-formed.
					if json.Valid(utf8bytes) {
						goVal = string(utf8bytes)
					} else if wrapped, err3 := json.Marshal(string(utf8bytes)); err3 == nil {
						goVal = string(wrapped)
					} else {
						goVal = string(utf8bytes)
					}
				} else {
					goVal = s
				}
			} else {
				goVal = stripMonoClock(s)
			}
		} else if jsonCols[col] {
			// JSON/JSONB column: pass raw JSON object directly (unwrap if stringified).
			goVal = jsonColValue(rawVal)
		} else {
			// Decode the JSON value as a generic Go type.
			// Strings cover UUIDs, timestamps, numeric amounts.
			// Booleans and numbers are decoded natively.
			// PG handles implicit TEXT→UUID/TIMESTAMPTZ/NUMERIC casts.
			var v any
			if err := json.Unmarshal(rawVal, &v); err != nil {
				return fmt.Errorf("col %s: unmarshal: %w", col, err)
			}
			switch tv := v.(type) {
			case float64:
				// Preserve integer values as int64 to avoid ".0" in text columns.
				if tv == float64(int64(tv)) {
					goVal = int64(tv)
				} else {
					goVal = tv
				}
				// Boolean columns: numeric 0/1 must become Go bool,
				// otherwise the PG driver rejects int64 for OID 16.
				if boolCols[col] {
					switch iv := goVal.(type) {
					case int64:
						goVal = iv != 0
					case float64:
						goVal = iv != 0
					}
				}
			case string:
				// Raw bytes are already valid UTF-8 here — the line-level
				// transcode in importTableJSONL has already converted
				// any Windows-1251 source to UTF-8.
				goVal = stripMonoClock(tv)
			case bool:
				goVal = tv
			default:
				goVal = v
			}
		}

		cols = append(cols, quotedIdent(db, col))
		args = append(args, goVal)
		ph := d.Placeholder(idx)
		if jsonCols[col] && !db.IsSQLite() {
			ph += "::jsonb"
		}
		placeholders = append(placeholders, ph)
		idx++
	}

	if len(cols) == 0 {
		return nil
	}

	sql := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) ON CONFLICT DO NOTHING",
		quotedIdent(db, tableName),
		strings.Join(cols, ", "),
		strings.Join(placeholders, ", "),
	)
	_, err := db.Exec(ctx, sql, args...)
	return err
}

// getTableCols returns the set of column names that exist in tableName.
func getTableCols(ctx context.Context, db *storage.DB, tableName string) (map[string]bool, error) {
	cols := make(map[string]bool)
	if db.IsSQLite() {
		rows, err := db.Query(ctx, "PRAGMA table_info("+sqliteQuote(tableName)+")")
		if err != nil {
			return cols, err
		}
		defer rows.Close()
		for rows.Next() {
			var cid int
			var name, ctype string
			var notnull, pk int
			var dflt any
			if rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk) == nil {
				cols[name] = true
			}
		}
	} else {
		rows, err := db.Query(ctx,
			`SELECT column_name FROM information_schema.columns WHERE table_schema='public' AND table_name=$1`,
			tableName)
		if err != nil {
			return cols, err
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			if rows.Scan(&name) == nil {
				cols[name] = true
			}
		}
	}
	return cols, nil
}

// tableExists reports whether tableName exists in the target DB.
// Ошибка запроса трактуется как «таблицы нет» — так же, как было до проверки
// (Scan при ошибке оставлял exists=false), но теперь это записано явно, а не
// получается само собой из проигнорированного возврата.
func tableExists(ctx context.Context, db *storage.DB, tableName string) bool {
	var exists bool
	var err error
	if db.IsSQLite() {
		err = db.QueryRow(ctx,
			`SELECT COUNT(*)>0 FROM sqlite_master WHERE type='table' AND name=?`, tableName,
		).Scan(&exists)
	} else {
		err = db.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM pg_tables WHERE schemaname='public' AND tablename=$1)`, tableName,
		).Scan(&exists)
	}
	if err != nil {
		return false
	}
	return exists
}

// restoreAttachments copies attachment binary files from srcDir to dstDir.
// File structure in srcDir: owner/uuid or flat uuid.
func restoreAttachments(srcDir, dstDir string) (int, error) {
	if dstDir == "" {
		return 0, nil
	}
	count := 0
	err := filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(srcDir, path)
		rel = filepath.FromSlash(rel)
		dst := filepath.Join(dstDir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), fsmode.Dir); err != nil {
			return err
		}
		src, err := os.Open(path) //nolint:gosec // G122: обход идёт по каталогу проекта или по временному каталогу, который мы сами распаковали; переход на os.Root — отдельная задача, он меняет поведение
		if err != nil {
			return err
		}
		tmp, err := os.CreateTemp(filepath.Dir(dst), ".onebase-restore-*")
		if err != nil {
			closeRead("исходный файл", src)
			return err
		}
		tmpName := tmp.Name()
		defer removeTemp(tmpName)
		if err := tmp.Chmod(0o600); err != nil {
			// Файл ещё пуст — закрытие тут действительно вторично, основная
			// ошибка уже есть. Ниже, на пути записи, closeErr наоборот
			// проверяется: там Close сбрасывает буфер.
			closeRead("временный файл", tmp)
			closeRead("исходный файл", src)
			return err
		}
		_, copyErr := io.Copy(tmp, src)
		syncErr := tmp.Sync()
		closeErr := tmp.Close()
		srcErr := src.Close()
		if copyErr != nil {
			return copyErr
		}
		if syncErr != nil {
			return syncErr
		}
		if closeErr != nil {
			return closeErr
		}
		if srcErr != nil {
			return srcErr
		}
		if err := publishRestoredFile(tmpName, dst); err != nil {
			return err
		}
		count++
		return nil
	})
	return count, err
}

func publishRestoredFile(tmpName, dst string) error {
	if err := os.Rename(tmpName, dst); err == nil {
		syncParentDir(dst)
		return nil
	} else if _, statErr := os.Stat(dst); statErr != nil {
		return err
	}
	// Windows cannot rename over an existing file. Move the old file aside
	// first, and restore it if publishing the new one fails.
	backup, err := os.CreateTemp(filepath.Dir(dst), ".onebase-previous-*")
	if err != nil {
		return err
	}
	backupName := backup.Name()
	if err := backup.Close(); err != nil {
		_ = os.Remove(backupName)
		return err
	}
	_ = os.Remove(backupName)
	if err := os.Rename(dst, backupName); err != nil {
		return err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		restoreErr := os.Rename(backupName, dst)
		return errors.Join(err, restoreErr)
	}
	_ = os.Remove(backupName)
	syncParentDir(dst)
	return nil
}

func syncParentDir(path string) {
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
}
