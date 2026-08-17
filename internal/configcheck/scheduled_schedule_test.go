package configcheck

// Проверка расписания регламентного задания (#965).
//
// Идёт публичным путём — через CheckDir, то есть тем же кодом, что и
// `onebase check`, а не вызовом metadata.ValidateSchedule напрямую (правило
// #611). Повод конкретный: расписание уже умел разбирать планировщик, но на
// пути `check` этого разбора не было — и именно этот разрыв заявка и чинит.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeJob(t *testing.T, dir, file, schedule string) {
	t.Helper()
	jobs := filepath.Join(dir, "scheduled")
	if err := os.MkdirAll(jobs, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "name: Задание\ntitle: Задание\nschedule: \"" + schedule + "\"\nprocessor: Обработка\nenabled: true\n"
	mustWrite(t, filepath.Join(jobs, file), body)
}

func scheduleIssues(t *testing.T, dir string) []Issue {
	t.Helper()
	issues, _ := CheckDir(dir)
	var out []Issue
	for _, is := range issues {
		if strings.Contains(is.Message, "расписание") {
			out = append(out, is)
		}
	}
	return out
}

// Тот самый случай из заявки: семь полей вместо пяти. Раньше `check` отвечал
// «ошибок не найдено», а `run` не поднимал базу вовсе.
func TestCheckDir_БитоеРасписаниеЛовится(t *testing.T) {
	dir := t.TempDir()
	writeJob(t, dir, "битое.yaml", "*/5 * * * * * *")

	found := scheduleIssues(t, dir)
	if len(found) == 0 {
		t.Fatal("check пропустил заведомо неверное расписание — ровно то поведение, из-за которого сервер не стартовал")
	}
	is := found[0]
	if !strings.Contains(is.File, "битое.yaml") {
		t.Errorf("проблема не привязана к файлу: %+v", is)
	}
	// Разработчику нужно видеть само значение, иначе искать опечатку он будет
	// глазами по всем заданиям.
	if !strings.Contains(is.Message, "*/5 * * * * * *") {
		t.Errorf("в сообщении нет самого расписания: %q", is.Message)
	}
}

// Опечатки, которые реально делают руками.
func TestCheckDir_ТипичныеОпечаткиВРасписании(t *testing.T) {
	for _, schedule := range []string{
		"*/5 * * *",  // потеряно поле
		"0 25 * * *", // часа 25 не бывает
		"@evry 5m",   // опечатка в сокращении
		"каждый час", // не cron вовсе
	} {
		t.Run(schedule, func(t *testing.T) {
			dir := t.TempDir()
			writeJob(t, dir, "job.yaml", schedule)
			if len(scheduleIssues(t, dir)) == 0 {
				t.Fatalf("расписание %q принято, хотя планировщик его не разберёт", schedule)
			}
		})
	}
}

// Рабочие расписания обязаны проходить: проверка, которая ругается на верное,
// хуже отсутствующей — её отключат.
func TestCheckDir_ВерныеРасписанияПроходят(t *testing.T) {
	for _, schedule := range []string{
		"0 9 * * 1-5",
		"*/15 * * * *",
		"0 0 1 * *",
		"@daily",
		"@every 30m",
		"", // задание без расписания: запускается только по требованию
	} {
		t.Run("«"+schedule+"»", func(t *testing.T) {
			dir := t.TempDir()
			writeJob(t, dir, "job.yaml", schedule)
			if found := scheduleIssues(t, dir); len(found) > 0 {
				t.Fatalf("верное расписание %q отвергнуто: %+v", schedule, found)
			}
		})
	}
}
