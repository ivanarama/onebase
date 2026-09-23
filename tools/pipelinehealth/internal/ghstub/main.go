// ghstub — офлайн-заглушка gh для тестов публичной команды pipelinehealth.
// Отвечает только на чтение: список PR, комментарии PR, родители коммита.
// Каждый обработанный путь дописывается в файл GHSTUB_LOG, поэтому тест
// доказывает, что запрос родителей действительно был (или не был) выполнен.
// Формат ответа повторяет «gh api --paginate --jq '.[]'» — один JSON-объект
// на строку; для --jq '.parents[]' разворачивается массив parents коммита.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	dataDir := os.Getenv("GHSTUB_DATA")
	logPath := os.Getenv("GHSTUB_LOG")
	failCommit := os.Getenv("GHSTUB_FAIL_COMMIT") == "1"

	path := ""
	jq := ""
	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		switch {
		case strings.HasPrefix(arg, "repos/"):
			path = arg
		case arg == "--jq":
			if i+1 < len(os.Args) {
				i++
				jq = os.Args[i]
			}
		}
	}
	if path == "" {
		fmt.Fprintln(os.Stderr, "ghstub: no REST path in arguments")
		os.Exit(2)
	}
	if logPath != "" {
		appendLine(logPath, path)
	}
	if failCommit && strings.Contains(path, "/commits/") {
		fmt.Fprintln(os.Stderr, "ghstub: injected commit read failure")
		os.Exit(1)
	}

	name := ""
	switch {
	case strings.Contains(path, "/pulls?"):
		name = "pulls.json"
	case strings.Contains(path, "/commits/"):
		name = "commit.json"
	case strings.Contains(path, "/comments?"):
		name = "comments.json"
	default:
		fmt.Fprintf(os.Stderr, "ghstub: unexpected path %s\n", path)
		os.Exit(2)
	}

	//nolint:gosec // G703: dataDir задаёт сам тест через своё окружение (GHSTUB_DATA), это не вход prank data.
	data, err := os.ReadFile(filepath.Join(dataDir, name))
	if err != nil {
		fmt.Fprintf(os.Stderr, "ghstub: %v\n", err)
		os.Exit(2)
	}
	if name == "commit.json" && jq == ".parents[]" {
		// Разворачиваем parents в построчные объекты — то, что из этого ответа
		// оставил бы настоящий «gh api --jq '.parents[]'».
		var commit struct {
			Parents []json.RawMessage `json:"parents"`
		}
		if err := json.Unmarshal(data, &commit); err != nil {
			fmt.Fprintf(os.Stderr, "ghstub: decode commit: %v\n", err)
			os.Exit(2)
		}
		for _, parent := range commit.Parents {
			fmt.Println(string(parent))
		}
		return
	}
	// Списки уже лежат построчно: отдаём как есть.
	scanner := bufio.NewScanner(strings.NewReader(strings.TrimSpace(string(data))))
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			fmt.Println(line)
		}
	}
}

func appendLine(path, line string) {
	//nolint:gosec // G703: путь журнала задаёт сам тест через своё окружение (GHSTUB_LOG), это не вход prank data.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()
	_, _ = fmt.Fprintln(file, line)
}
