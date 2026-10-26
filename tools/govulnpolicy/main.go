// Command govulnpolicy — фильтр потока `govulncheck -format json`: проверка
// падает только на ДОСТИЖИМЫХ (called) уязвимостях, кроме явно занесённых в
// allowlist — и те гасятся лишь при условии, что версия модуля в go.mod
// действительно содержит исправление.
//
// Исключение GO-2026-6452 удалено: vulndb теперь указывает fixed-версию
// 2.11.0, а запиненная excelize содержит исходное исправление f98df08.
// Allowlist пуст; все достижимые находки блокируют CI. Новое исключение
// потребовало бы отдельного обоснования и точного пина исправленной версии.
//
// Использование (см. job vuln в .github/workflows/ci.yml):
//
//	govulncheck -json ./... | go run ./tools/govulnpolicy
//
// Коды выхода: 0 — чисто или только разрешённые advisory; 1 — достижимая
// уязвимость вне allowlist, снятый/чужой пин или битый поток govulncheck.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// allowlist — advisory, разрешённые к подавлению. Ключ — OSV id.
//
// ВАЖНО: запись гасит advisory только при точном совпадении версии модуля с
// minVersion. Если vulndb уже знает исправленную версию, finding исчезает сам,
// и соответствующее исключение надо удалить.
var allowlist = map[string]allowEntry{}

type allowEntry struct {
	minVersion string
	reason     string
}

// finding/trace — подмножество схемы govulncheck -format json.
type message struct {
	Config  *json.RawMessage `json:"config"`
	Finding *finding         `json:"finding"`
}

type finding struct {
	OSV   string  `json:"osv"`
	Trace []frame `json:"trace"`
}

type frame struct {
	Module   string `json:"module"`
	Version  string `json:"version"`
	Function string `json:"function"`
}

type violation struct {
	osv     string
	version string
	detail  string
}

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "govulnpolicy:", err)
		os.Exit(1)
	}
}

func run(r io.Reader, out io.Writer) error {
	dec := json.NewDecoder(r)
	var (
		sawConfig    bool
		calledOSVs   = map[string]string{} // osv → версия модуля из trace[0]
		allowlisted  int
		skippedLevel int // module-level находки (не called) — govulncheck их не считает «affected»
		violations   []violation
	)
	for {
		var msg message
		if err := dec.Decode(&msg); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("битый поток govulncheck (обрыв или не JSON): %v", err)
		}
		if msg.Config != nil {
			sawConfig = true
			continue
		}
		if msg.Finding == nil {
			continue
		}
		f := msg.Finding
		var called bool
		for _, fr := range f.Trace {
			if fr.Function != "" {
				called = true
				break
			}
		}
		if !called {
			skippedLevel++
			continue
		}
		version := ""
		if len(f.Trace) > 0 {
			version = f.Trace[0].Version
		}
		if _, seen := calledOSVs[f.OSV]; !seen {
			calledOSVs[f.OSV] = version
		}
	}

	if !sawConfig {
		return fmt.Errorf("govulncheck не выдал config — запуск оборвался, считать результат нельзя")
	}

	for osv, version := range calledOSVs {
		entry, ok := allowlist[osv]
		if !ok {
			violations = append(violations, violation{osv: osv, version: version,
				detail: "достижимая уязвимость вне allowlist"})
			continue
		}
		if version != entry.minVersion {
			violations = append(violations, violation{osv: osv, version: version,
				detail: fmt.Sprintf("ожидалась версия с фиксом %s (см. allowlist в tools/govulnpolicy/main.go)", entry.minVersion)})
			continue
		}
		allowlisted++
	}

	for _, v := range violations {
		// stdout/stderr — не вердикт: ошибка записи не меняет исход проверки.
		_, _ = fmt.Fprintf(out, "ЗАПРЕЩЕНО: %s (модуль-версия %s): %s\n", v.osv, v.version, v.detail)
	}
	if len(violations) > 0 {
		return fmt.Errorf("%d достижимых уязвимостей нарушают политику", len(violations))
	}

	_, _ = fmt.Fprintf(out, "govulnpolicy: OK — достижимых уязвимостей вне allowlist нет; "+
		"подавлено %d (пин подтверждён), module-level пропущено %d.\n", allowlisted, skippedLevel)
	for osv := range calledOSVs {
		if entry, ok := allowlist[osv]; ok {
			_, _ = fmt.Fprintf(out, "  %s подавлен: %s\n", osv, entry.reason)
		}
	}
	return nil
}
