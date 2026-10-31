package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/configcheck"
	"github.com/spf13/cobra"
)

// `onebase check` — главный инструмент отладки конфигураций и гейт pre-commit
// и CI: он единственный ловит «no such column»/«no such table» до рантайма, а
// его код возврата решает, пройдёт ли коммит. При этом файл был покрыт на 0%
// (#988, А6) — включая ветку «нашлись ошибки», ради которой команда и нужна.

// checkFixture раскладывает конфигурацию во временном каталоге и возвращает
// путь к ней. broken=true подсовывает запрос по несуществующей колонке — та
// самая ошибка, ради которой check компилирует запросы, а не только парсит.
func checkFixture(t *testing.T, broken bool) string {
	t.Helper()
	dir := t.TempDir()
	writeProcrunFixture(t, dir, "config/app.yaml", "name: check-test\nversion: \"1.0\"\n")
	writeProcrunFixture(t, dir, "catalogs/Товар.yaml",
		"name: Товар\nfields:\n  - name: Наименование\n    type: string\n")
	query := "ВЫБРАТЬ Наименование ИЗ Справочник.Товар"
	if broken {
		query = "ВЫБРАТЬ ЦенаКоторойНет ИЗ Справочник.Товар"
	}
	writeProcrunFixture(t, dir, "reports/ПоТовару.yaml",
		"name: ПоТовару\ntitle: По товару\nquery: |\n  "+query+"\n")
	return dir
}

// runCheckCmd вызывает команду так же, как cobra, и снимает stdout.
func runCheckCmd(t *testing.T, run func(*cobra.Command, []string) error, dir string, flags map[string]string) (string, error) {
	t.Helper()
	cmd := &cobra.Command{}
	addBaseFlags(cmd)
	cmd.Flags().Bool("json", false, "")
	cmd.Flags().Bool("lint", false, "")
	if err := cmd.Flags().Set("project", dir); err != nil {
		t.Fatal(err)
	}
	for k, v := range flags {
		if err := cmd.Flags().Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	out, err := captureStdout(t, func() error { return run(cmd, nil) })
	return out, err
}

func TestCheckCleanConfigReportsOK(t *testing.T) {
	out, err := runCheckCmd(t, runCheck, checkFixture(t, false), nil)
	if err != nil {
		t.Fatalf("check на исправной конфигурации вернул ошибку: %v", err)
	}
	if !strings.Contains(out, "OK: ошибок не найдено") {
		t.Errorf("нет отметки об успехе, вывод:\n%s", out)
	}
}

// TestCheckBrokenConfigExitsNonZero — ради этой ветки команда и существует.
// Раньше она была непроверяемой в принципе: os.Exit(1) убивал процесс прогона
// тестов вместе с самим тестом.
func TestCheckBrokenConfigExitsNonZero(t *testing.T) {
	out, err := runCheckCmd(t, runCheck, checkFixture(t, true), nil)
	if !errors.Is(err, errSilentExit) {
		t.Fatalf("ожидался errSilentExit (код возврата 1), получено: %v", err)
	}
	// Имя колонки приходит из СУБД в нижнем регистре: платформа кладёт
	// идентификаторы как lower(name), и текст ошибки SQLite это отражает.
	if !strings.Contains(strings.ToLower(out), "ценакоторойнет") {
		t.Errorf("в выводе нет причины отказа, вывод:\n%s", out)
	}
	if !strings.Contains(out, "reports/ПоТовару.yaml") {
		t.Errorf("в выводе не назван файл с ошибкой, вывод:\n%s", out)
	}
}

// TestCheckJSONIsParseable — этот JSON разбирает CI на чужой стороне, поэтому
// он обязан быть валидным целиком, а не «почти».
func TestCheckJSONIsParseable(t *testing.T) {
	out, err := runCheckCmd(t, runCheck, checkFixture(t, true), map[string]string{"json": "true"})
	if !errors.Is(err, errSilentExit) {
		t.Fatalf("ожидался errSilentExit, получено: %v", err)
	}
	var res configcheck.Result
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("вывод --json не разбирается: %v\n%s", err, out)
	}
	if res.OK {
		t.Error("ok=true при сломанной конфигурации")
	}
	if res.Total == 0 || len(res.Issues) == 0 {
		t.Errorf("в отчёте нет ошибок: total=%d, issues=%d", res.Total, len(res.Issues))
	}
	// Человекочитаемого текста в JSON-режиме быть не должно: он ломает разбор
	// у того, кто читает stdout целиком.
	if strings.Contains(out, "OK: ошибок не найдено") || strings.Contains(out, "Предупреждения:") {
		t.Errorf("в JSON-режиме подмешан текстовый вывод:\n%s", out)
	}
}

func TestCheckJSONOnCleanConfig(t *testing.T) {
	out, err := runCheckCmd(t, runCheck, checkFixture(t, false), map[string]string{"json": "true"})
	if err != nil {
		t.Fatalf("check вернул ошибку: %v", err)
	}
	var res configcheck.Result
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("вывод --json не разбирается: %v\n%s", err, out)
	}
	if !res.OK || res.Total != 0 {
		t.Errorf("ожидался чистый результат, получено ok=%v total=%d", res.OK, res.Total)
	}
}

// TestLintCommandMatchesCheckLint — две точки входа обязаны давать один
// результат, иначе `onebase lint` и `onebase check --lint` тихо разойдутся.
func TestLintCommandMatchesCheckLint(t *testing.T) {
	dir := checkFixture(t, false)
	viaFlag, errFlag := runCheckCmd(t, runCheck, dir, map[string]string{"json": "true", "lint": "true"})
	viaCmd, errCmd := runCheckCmd(t, runLint, dir, map[string]string{"json": "true"})
	if errFlag != nil || errCmd != nil {
		t.Fatalf("ошибки: check --lint = %v, lint = %v", errFlag, errCmd)
	}
	if viaFlag != viaCmd {
		t.Errorf("`check --lint` и `lint` дали разный отчёт:\n--- check --lint ---\n%s\n--- lint ---\n%s", viaFlag, viaCmd)
	}
}

// TestLintWarningsDoNotFailCheck фиксирует договорённость из справки команды:
// предупреждения не меняют код возврата. Иначе включение --lint в CI
// превратило бы рекомендации в блокирующие ошибки.
func TestLintWarningsDoNotFailCheck(t *testing.T) {
	dir := checkFixture(t, false)
	// Роль без прав на объект — типовое lint-замечание, не ошибка.
	writeProcrunFixture(t, dir, "roles/Кладовщик.yaml", "name: Кладовщик\npermissions:\n  catalogs: {}\n")
	out, err := runCheckCmd(t, runLint, dir, map[string]string{"json": "true"})
	if err != nil {
		t.Fatalf("lint вернул ненулевой код при одних лишь предупреждениях: %v", err)
	}
	var res configcheck.Result
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("вывод не разбирается: %v\n%s", err, out)
	}
	if !res.OK {
		t.Errorf("ok=false при отсутствии ошибок: issues=%v", res.Issues)
	}
}

// TestPrintIssuesTextFormat — формат «file:line:col: message» разбирают
// редакторы и pre-commit-хуки; сдвиг в нём ломает переход к строке по клику.
func TestPrintIssuesTextFormat(t *testing.T) {
	res := configcheck.Result{
		OK:    false,
		Total: 1,
		Issues: []configcheck.Issue{{
			File:         "src/приходная.posting.os",
			Kind:         "dsl",
			Code:         "OB101",
			Message:      "неизвестная функция СоЗдать",
			SuggestedFix: "Создать",
			Line:         12,
			Column:       5,
		}},
		Warnings: []configcheck.Issue{{Message: "объект без прав в ролях"}},
	}
	out, _ := captureStdout(t, func() error { printIssuesText(res); return nil })

	for _, want := range []string{
		"src/приходная.posting.os:12:5: [dsl] [OB101] неизвестная функция СоЗдать",
		"  подсказка: Создать",
		"Предупреждения:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("в выводе нет %q, получено:\n%s", want, out)
		}
	}
}

// TestPrintIssuesTextWithoutLocation — у проблем уровня конфигурации файла нет,
// и раньше такая строка начиналась бы с двоеточия.
func TestPrintIssuesTextWithoutLocation(t *testing.T) {
	res := configcheck.Result{
		OK:     false,
		Total:  1,
		Issues: []configcheck.Issue{{Message: "константа Ставка объявлена дважды"}},
	}
	out, _ := captureStdout(t, func() error { printIssuesText(res); return nil })
	if !strings.Contains(out, "(конфигурация): константа Ставка объявлена дважды") {
		t.Errorf("проблема без файла отрендерена неверно:\n%s", out)
	}
	if strings.Contains(out, ":0:0") {
		t.Errorf("в выводе остались нулевые координаты:\n%s", out)
	}
}

// TestCheckThroughRootCommand — единственный тест, идущий полным путём
// пользователя: разбор argv, диспетчеризация cobra, RunE. Остальные зовут RunE
// напрямую, и такой набор не заметил бы, что команда отвязана от имени или что
// флаг переименован.
func TestCheckThroughRootCommand(t *testing.T) {
	dir := checkFixture(t, true)
	defer resetCheckFlags(t)

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	rootCmd.SetArgs([]string{"check", "--project", dir, "--json"})
	runErr := rootCmd.Execute()
	_ = w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	_ = r.Close()

	if !errors.Is(runErr, errSilentExit) {
		t.Fatalf("`onebase check` на сломанной конфигурации вернул %v, ожидался errSilentExit", runErr)
	}
	var res configcheck.Result
	if err := json.Unmarshal(buf.Bytes(), &res); err != nil {
		t.Fatalf("вывод не разбирается: %v\n%s", err, buf.String())
	}
	if res.OK {
		t.Error("ok=true при сломанной конфигурации")
	}
}

// resetCheckFlags возвращает глобальной команде исходное состояние: значения
// флагов cobra живут в объекте команды, и следующий тест увидел бы чужой
// --project.
func resetCheckFlags(t *testing.T) {
	t.Helper()
	rootCmd.SetArgs(nil)
	for _, name := range []string{"project", "id", "sqlite", "db"} {
		if f := checkCmd.Flags().Lookup(name); f != nil {
			if err := checkCmd.Flags().Set(name, ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, name := range []string{"json", "lint"} {
		if f := checkCmd.Flags().Lookup(name); f != nil {
			if err := checkCmd.Flags().Set(name, "false"); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestCheckRelatedEntityListCLI(t *testing.T) {
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS("../../examples/related-lists")); err != nil {
		t.Fatal(err)
	}
	out, err := runCheckCmd(t, runCheck, dir, nil)
	if err != nil || !strings.Contains(out, "OK:") {
		t.Fatalf("valid related list: %v %s", err, out)
	}
	path := filepath.Join(dir, "forms", "обращение", "object.form.yaml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b = []byte(strings.Replace(string(b), "limit: 5", "limit: 0", 1))
	if err := os.WriteFile(path, b, 0600); err != nil { //nolint:gosec // G703: path is a constant relative fixture name under t.TempDir(), not CLI input.
		t.Fatal(err)
	}
	out, err = runCheckCmd(t, runCheck, dir, nil)
	if !errors.Is(err, errSilentExit) || !strings.Contains(out, "limit") {
		t.Fatalf("invalid list accepted: %v %s", err, out)
	}
}

func TestCheckRelatedListAndTableColumnsCLI(t *testing.T) {
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS("../../examples/related-lists")); err != nil {
		t.Fatal(err)
	}
	entity := filepath.Join(dir, "documents", "Обращение.yaml")
	b, err := os.ReadFile(entity)
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, []byte("tableparts:\n - name: Items\n   fields: [{name: Клиент, type: 'reference:Клиент'}]\n")...)
	if err := os.WriteFile(entity, b, 0600); err != nil { // #nosec G703 -- Fixed documents/Обращение.yaml fixture path under t.TempDir(); no CLI or HTTP input.
		t.Fatal(err)
	}
	path := filepath.Join(dir, "forms", "обращение", "object.form.yaml")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(original) + `
  - kind: ГруппаФормы
    name: Row
    orientation: horizontal
    equal_columns: true
  - kind: ТабличнаяЧасть
    name: Items
    data_path: Объект.Items
    columns:
      - kind: ПолеВвода
        name: Клиент
        id: items-client
        data_path: Объект.Items.Клиент
        choice_filter:
          - query: ВЫБРАТЬ Ссылка ИЗ Справочник.Клиент
            params: {Клиент: Объект.Клиент}
`
	check := func(text string) (string, error) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0600); err != nil { // #nosec G703 -- Fixed forms/обращение/object.form.yaml fixture path under t.TempDir(); no CLI or HTTP input.
			t.Fatal(err)
		}
		return runCheckCmd(t, runCheck, dir, map[string]string{"lint": "true"})
	}
	out, err := check(text)
	if err != nil || !strings.Contains(out, "OK:") || strings.Contains(out, "unknown") || strings.Contains(out, "неизвестн") {
		t.Fatalf("combined columns/check/lint: %v %s", err, out)
	}
	for _, tc := range []struct{ name, text, want string }{
		{"list mixed", strings.Replace(text, "columns: [Номер, ДатаВызова, Состояние, Клиент]", "columns: [Номер, {name: Клиент}]", 1), "columns"},
		{"list unknown", strings.Replace(text, "limit: 5", "limti: 5", 1), "limti"},
		{"filter unknown", strings.Replace(text, "params:", "paramz:", 1), "paramz"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := check(tc.text)
			if !strings.Contains(out, tc.want) {
				t.Fatalf("missing %q diagnostic: %v %s", tc.want, err, out)
			}
		})
	}
}
