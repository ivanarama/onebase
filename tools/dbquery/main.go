// dbquery — diagnostic tool for OneBase SQLite databases.
//
// Usage:
//
//	dbquery -db <path> -sql "SELECT ..."
//	dbquery -db <path> < query.sql
//	dbquery -db <path>          # interactive REPL
//	dbquery -db <path> --tables # list tables
//	dbquery -db <path> --schema # show full schema
//
// Output formats: table (default), csv, json.
package main

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	oblog "github.com/ivantit66/onebase/internal/logging"
	"io"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

func main() {
	dbPath := flag.String("db", "", "path to .db file")
	sqlStmt := flag.String("sql", "", "SQL query to execute")
	format := flag.String("f", "table", "output format: table, csv, json")
	listTables := flag.Bool("tables", false, "list all tables")
	showSchema := flag.Bool("schema", false, "show full schema")
	flag.Parse()

	if *dbPath == "" {
		fmt.Fprintln(os.Stderr, "Usage: dbquery -db <path> [-sql <query>] [-f table|csv|json] [--tables] [--schema]")
		os.Exit(1)
	}

	db, err := sql.Open("sqlite", *dbPath)
	if err != nil {
		fatal("open db: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "close db:", err)
		}
	}()

	if *listTables {
		printTables(db, *format)
		return
	}
	if *showSchema {
		printSchema(db)
		return
	}

	query := strings.TrimSpace(*sqlStmt)
	if query == "" {
		// read from stdin if piped, else enter REPL
		stat, _ := os.Stdin.Stat()
		if (stat.Mode() & os.ModeCharDevice) == 0 {
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				fatal("read stdin: %v", err)
			}
			query = strings.TrimSpace(string(data))
		}
	}

	if query != "" {
		execAndPrint(db, query, *format)
		return
	}

	// interactive REPL
	repl(db, *format)
}

func printTables(db *sql.DB, format string) {
	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")
	if err != nil {
		fatal("query tables: %v", err)
	}
	defer oblog.CloseQuiet("dbquery", "курсор запроса", rows)

	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			fatal("scan: %v", err)
		}
		names = append(names, n)
	}

	switch format {
	case "csv":
		w := csv.NewWriter(os.Stdout)
		// csv.Writer копит ошибку и отдаёт её из Flush/Error — проверяем там.
		_ = w.Write([]string{"table"})
		for _, n := range names {
			// csv.Writer копит ошибку и отдаёт её из Flush/Error — проверяем там.
			_ = w.Write([]string{n})
		}
		w.Flush()
		if err := w.Error(); err != nil {
			fatal("csv: %v", err)
		}
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(names); err != nil {
			fatal("encode: %v", err)
		}
	default:
		for _, n := range names {
			fmt.Println(n)
		}
	}
}

func printSchema(db *sql.DB) {
	rows, err := db.Query("SELECT sql FROM sqlite_master WHERE sql IS NOT NULL ORDER BY name")
	if err != nil {
		fatal("query schema: %v", err)
	}
	defer oblog.CloseQuiet("dbquery", "курсор запроса", rows)
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			fatal("scan: %v", err)
		}
		fmt.Println(s)
		fmt.Println()
	}
}

func execAndPrint(db *sql.DB, query, format string) {
	// split by ; for multiple statements
	stmts := splitStmts(query)
	if len(stmts) == 0 {
		return
	}
	// only last statement produces output
	for i, s := range stmts {
		if i < len(stmts)-1 {
			_, err := db.Exec(s)
			if err != nil {
				fmt.Fprintf(os.Stderr, "stmt %d: %v\n", i+1, err)
			}
			continue
		}
		printQuery(db, s, format)
	}
}

func printQuery(db *sql.DB, query, format string) {
	rows, err := db.Query(query)
	if err != nil {
		fatal("query: %v", err)
	}
	defer oblog.CloseQuiet("dbquery", "курсор запроса", rows)

	cols, err := rows.Columns()
	if err != nil {
		fatal("columns: %v", err)
	}

	var allRows [][]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			fatal("scan: %v", err)
		}
		allRows = append(allRows, vals)
	}

	switch format {
	case "csv":
		printCSV(cols, allRows)
	case "json":
		printJSON(cols, allRows)
	default:
		printTable(cols, allRows)
	}
}

func printTable(cols []string, rows [][]any) {
	strRows := make([][]string, len(rows)+1)
	strRows[0] = cols

	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = len(c)
	}

	for ri, row := range rows {
		sr := make([]string, len(cols))
		for ci, v := range row {
			s := formatVal(v)
			sr[ci] = s
			if len(s) > widths[ci] {
				widths[ci] = len(s)
			}
		}
		strRows[ri+1] = sr
	}

	// header
	for i, c := range cols {
		if i > 0 {
			fmt.Print(" | ")
		}
		fmt.Printf("%-*s", widths[i], c)
	}
	fmt.Println()

	// separator
	for i, w := range widths {
		if i > 0 {
			fmt.Print("-+-")
		}
		fmt.Print(strings.Repeat("-", w))
	}
	fmt.Println()

	// data
	for _, sr := range strRows[1:] {
		for i, s := range sr {
			if i > 0 {
				fmt.Print(" | ")
			}
			fmt.Printf("%-*s", widths[i], s)
		}
		fmt.Println()
	}

	fmt.Printf("(%d rows)\n", len(rows))
}

func printCSV(cols []string, rows [][]any) {
	w := csv.NewWriter(os.Stdout)
	// csv.Writer копит ошибку и отдаёт её из Flush/Error — проверяем там.
	_ = w.Write(cols)
	for _, row := range rows {
		sr := make([]string, len(cols))
		for i, v := range row {
			sr[i] = formatVal(v)
		}
		// csv.Writer копит ошибку и отдаёт её из Flush/Error — проверяем там.
		_ = w.Write(sr)
	}
	w.Flush()
	if err := w.Error(); err != nil {
		fatal("csv: %v", err)
	}
}

func printJSON(cols []string, rows [][]any) {
	result := make([]map[string]any, len(rows))
	for ri, row := range rows {
		m := make(map[string]any, len(cols))
		for ci, col := range cols {
			m[col] = jsonVal(row[ci])
		}
		result[ri] = m
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		fatal("encode: %v", err)
	}
}

func formatVal(v any) string {
	switch t := v.(type) {
	case nil:
		return "NULL"
	case time.Time:
		return t.Format("2006-01-02 15:04:05")
	case []byte:
		return string(t)
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%g", t)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func jsonVal(v any) any {
	switch t := v.(type) {
	case []byte:
		return string(t)
	default:
		return t
	}
}

func repl(db *sql.DB, format string) {
	fmt.Println("dbquery — type SQL or \\q to quit, \\tables, \\schema, \\format table|csv|json")
	fmt.Println()

	for {
		fmt.Print("sql> ")
		var line string
		if _, err := fmt.Scanln(&line); err != nil {
			break
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == "\\q" || line == "quit" || line == "exit" {
			break
		}
		if line == "\\tables" {
			printTables(db, format)
			continue
		}
		if line == "\\schema" {
			printSchema(db)
			continue
		}
		if strings.HasPrefix(line, "\\format") {
			f := strings.TrimSpace(strings.TrimPrefix(line, "\\format"))
			if f == "table" || f == "csv" || f == "json" {
				format = f
				fmt.Println("format:", format)
			} else {
				fmt.Println("formats: table, csv, json")
			}
			continue
		}
		execAndPrint(db, line, format)
	}
}

func splitStmts(s string) []string {
	var stmts []string
	var cur strings.Builder
	inStr := false
	for _, ch := range s {
		if ch == '\'' {
			inStr = !inStr
		}
		if ch == ';' && !inStr {
			t := strings.TrimSpace(cur.String())
			if t != "" {
				stmts = append(stmts, t)
			}
			cur.Reset()
			continue
		}
		cur.WriteRune(ch)
	}
	t := strings.TrimSpace(cur.String())
	if t != "" {
		stmts = append(stmts, t)
	}
	return stmts
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ERROR: "+format+"\n", args...)
	os.Exit(1)
}
