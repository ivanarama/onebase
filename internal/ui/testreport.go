package ui

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"time"
)

// Форматтеры отчёта о прогоне тестов (план 136, шаг 4): pretty для человека,
// tap/junit — машиночитаемые для CI.

// Форматы вывода.
const (
	FormatPretty = "pretty"
	FormatTAP    = "tap"
	FormatJUnit  = "junit"
)

// WriteReport пишет отчёт о прогоне в выбранном формате.
func WriteReport(w io.Writer, res TestRunResult, format string) error {
	switch format {
	case "", FormatPretty:
		return writePretty(w, res)
	case FormatTAP:
		return writeTAP(w, res)
	case FormatJUnit:
		return writeJUnit(w, res)
	}
	return fmt.Errorf("неизвестный формат отчёта %q (доступны pretty, tap, junit)", format)
}

// errWriter копит первую ошибку записи, чтобы не превращать формирование отчёта
// в лестницу проверок после каждой строки.
//
// Ошибка здесь не косметическая: WriteReport вызывается из `onebase test`, и
// отчёт уходит либо в stdout, либо в файл (internal/cli/test.go). Записи не
// проверялись вовсе, поэтому при заполненном диске команда сообщала об успехе, а
// на диске оставался обрезанный отчёт. Для TAP и JUnit это опаснее, чем для
// человекочитаемого вывода: CI разберёт усечённый файл как меньшее число тестов
// и покажет зелёную сборку.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) printf(format string, a ...any) {
	if e.err != nil {
		return
	}
	_, e.err = fmt.Fprintf(e.w, format, a...)
}

func (e *errWriter) println(a ...any) {
	if e.err != nil {
		return
	}
	_, e.err = fmt.Fprintln(e.w, a...)
}

// caseStatus классифицирует тест: "pass" | "fail" | "error". Пустой тест (без
// проверок и без ошибки) — это "fail".
func caseStatus(c TestCaseResult) string {
	switch {
	case c.Err != nil:
		return "error"
	case c.OK():
		return "pass"
	default:
		return "fail"
	}
}

// ─── pretty ───────────────────────────────────────────────────────────────────

func writePretty(w io.Writer, res TestRunResult) error {
	ew := &errWriter{w: w}
	for _, c := range res.Cases {
		ew.printf("▶ %s\n", c.Name)
		for _, o := range c.Asserts {
			if o.Passed {
				ew.printf("  ok    — %s\n", o.Desc)
			} else if o.Detail != "" {
				ew.printf("  ПРОВАЛ — %s (%s)\n", o.Desc, o.Detail)
			} else {
				ew.printf("  ПРОВАЛ — %s\n", o.Desc)
			}
		}
		if c.Err != nil {
			ew.printf("  ОШИБКА — %s\n", c.Err.Error())
		}
		if len(c.Asserts) == 0 && c.Err == nil {
			ew.println("  (без единой проверки — тест считается неуспешным)")
		}
		ew.printf("  %s  (%s)\n", caseSummary(c), fmtDuration(c.Duration))
	}

	tests, passedTests, asserts, failedAsserts := res.Totals()
	ew.println("── Итог ──")
	ew.printf("Тестов: %d, успешно: %d, провалено: %d\n", tests, passedTests, tests-passedTests)
	ew.printf("Проверок: %d, провалено: %d\n", asserts, failedAsserts)
	return ew.err
}

func caseSummary(c TestCaseResult) string {
	total := c.Passed + c.Failed
	if c.OK() {
		return fmt.Sprintf("OK: %d проверок", total)
	}
	if c.Err != nil {
		return fmt.Sprintf("ОШИБКА: %d/%d проверок прошло", c.Passed, total)
	}
	return fmt.Sprintf("ПРОВАЛ: %d из %d проверок", c.Failed, total)
}

func fmtDuration(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf("%dµs", d.Microseconds())
	}
	return fmt.Sprintf("%dms", d.Milliseconds())
}

// ─── TAP (Test Anything Protocol v13) ─────────────────────────────────────────

func writeTAP(w io.Writer, res TestRunResult) error {
	ew := &errWriter{w: w}
	ew.println("TAP version 13")
	ew.printf("1..%d\n", len(res.Cases))
	for i, c := range res.Cases {
		status := "ok"
		if caseStatus(c) != "pass" {
			status = "not ok"
		}
		ew.printf("%s %d - %s\n", status, i+1, c.Name)
		// Диагностика проваленного/ошибочного теста — YAML-блок.
		if caseStatus(c) == "pass" {
			continue
		}
		ew.println("  ---")
		if c.Err != nil {
			ew.printf("  error: %s\n", tapScalar(c.Err.Error()))
		}
		var failed []string
		for _, o := range c.Asserts {
			if !o.Passed {
				d := o.Desc
				if o.Detail != "" {
					d += " (" + o.Detail + ")"
				}
				failed = append(failed, d)
			}
		}
		if len(failed) > 0 {
			ew.println("  failures:")
			for _, f := range failed {
				ew.printf("    - %s\n", tapScalar(f))
			}
		}
		if len(c.Asserts) == 0 && c.Err == nil {
			ew.println("  error: тест без единой проверки")
		}
		ew.println("  ...")
	}
	return ew.err
}

// tapScalar экранирует значение для YAML-скаляра TAP-диагностики: кавычит и
// эскейпит, если есть спецсимволы.
func tapScalar(s string) string {
	if strings.ContainsAny(s, ":#\"'\n") {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", " ").Replace(s) + `"`
	}
	return s
}

// ─── JUnit XML ────────────────────────────────────────────────────────────────

type junitTestsuites struct {
	XMLName  xml.Name     `xml:"testsuites"`
	Tests    int          `xml:"tests,attr"`
	Failures int          `xml:"failures,attr"`
	Errors   int          `xml:"errors,attr"`
	Time     string       `xml:"time,attr"`
	Suites   []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	Name     string      `xml:"name,attr"`
	Tests    int         `xml:"tests,attr"`
	Failures int         `xml:"failures,attr"`
	Errors   int         `xml:"errors,attr"`
	Time     string      `xml:"time,attr"`
	Cases    []junitCase `xml:"testcase"`
}

type junitCase struct {
	Name      string       `xml:"name,attr"`
	Classname string       `xml:"classname,attr"`
	Time      string       `xml:"time,attr"`
	Failure   *junitDetail `xml:"failure,omitempty"`
	Error     *junitDetail `xml:"error,omitempty"`
	SystemOut string       `xml:"system-out,omitempty"`
}

type junitDetail struct {
	Message string `xml:"message,attr"`
	Body    string `xml:",chardata"`
}

func writeJUnit(w io.Writer, res TestRunResult) error {
	suite := junitSuite{Name: "onebase"}
	var totalTime time.Duration
	for _, c := range res.Cases {
		totalTime += c.Duration
		jc := junitCase{
			Name:      c.Name,
			Classname: "onebase",
			Time:      fmt.Sprintf("%.6f", c.Duration.Seconds()),
			SystemOut: strings.Join(c.Messages, "\n"),
		}
		switch caseStatus(c) {
		case "error":
			jc.Error = &junitDetail{Message: c.Err.Error(), Body: c.Err.Error()}
			suite.Errors++
		case "fail":
			jc.Failure = &junitDetail{Message: caseSummary(c), Body: failureBody(c)}
			suite.Failures++
		}
		suite.Cases = append(suite.Cases, jc)
	}
	suite.Tests = len(res.Cases)
	suite.Time = fmt.Sprintf("%.6f", totalTime.Seconds())

	doc := junitTestsuites{
		Tests:    suite.Tests,
		Failures: suite.Failures,
		Errors:   suite.Errors,
		Time:     suite.Time,
		Suites:   []junitSuite{suite},
	}
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}

// failureBody собирает список проваленных проверок для тела <failure>.
func failureBody(c TestCaseResult) string {
	if len(c.Asserts) == 0 {
		return "тест без единой проверки"
	}
	var b strings.Builder
	for _, o := range c.Asserts {
		if o.Passed {
			continue
		}
		b.WriteString(o.Desc)
		if o.Detail != "" {
			b.WriteString(" (")
			b.WriteString(o.Detail)
			b.WriteString(")")
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}
