// Command govulnpolicy — фильтр потока `govulncheck -format json`: проверка
// падает только на ДОСТИЖИМЫХ (called) уязвимостях, кроме явно занесённых в
// allowlist — и те гасятся лишь при условии, что версия модуля в go.mod
// действительно содержит исправление.
//
// Зачем это не просто «govulncheck ./...»: GO-2026-6452 (excelize,
// CVE-2026-59162, GHSA-fx5j-qcqg-grpf) в vulndb записан без fixed-версии, поэтому
// govulncheck считает уязвимой ЛЮБУЮ версию библиотеки — в том числе запиненный
// в go.mod upstream-коммит f98df08 с исправлением (в релизы excelize фикс ещё
// не вышел). Пока vulndb не обновится, гасим конкретно этот advisory, но
// жёстко требуем пин: если кто-то откатит excelize на релиз без фикса,
// проверка снова станет красной. Условие снятия записи — релиз excelize с
// фиксом и обновление vulndb: после этого finding исчезает сам, и запись можно
// удалить (она перестаёт использоваться).
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

// pinExcelize — минимальная версия github.com/xuri/excelize/v2, содержащая
// исправление GO-2026-6452 (коммит f98df08a8f6aed8bc3b193115d1abb5b1ea2e433).
const pinExcelize = "v2.11.1-0.20260728235842-f98df08a8f6a"

// allowlist — advisory, разрешённые к подавлению. Ключ — OSV id.
//
// ВАЖНО: запись гасит advisory только при точном совпадении версии модуля с
// minVersion. После апгрейда excelize проверка снова потребует внимания:
// если новая версия содержит фикс и vulndb это уже знает — finding исчезнет
// сам, и запись надо удалить; если нет — исправлять надо зависимость, а не фильтр.
var allowlist = map[string]allowEntry{
	"GO-2026-6452": {
		module:     "github.com/xuri/excelize/v2",
		minVersion: pinExcelize,
		reason: "CVE-2026-59162: паника excelize на отрицательном индексе shared-строк " +
			"(недоверенный .xlsx → отказ обслуживания). В релизах фикса нет, поэтому в go.mod " +
			"запинен upstream-коммит f98df08 с исправлением; запись снять после релиза excelize " +
			"с фиксом и обновления vulndb.",
	},
}

type allowEntry struct {
	module     string
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
