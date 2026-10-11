package ui

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

// Регрессия #1272: procrun/RunProcessorOffline с НачатьТранзакцию и запросом
// внутри неё не роняет процесс и не ждёт собственное соединение. Фабрика
// запросов серверного пути живая (#1259), поэтому запрос читает в транзакции;
// страховка от рассогласованного контекста (NewQueryFactoryWithTxState,
// #1272) закрывает статические пути сборки. Тест-канарейка: любой возврат
// дедлока сюда означает регрессию той или другой защиты.
func TestProcrun_QueryInsideTransactionDoesNotDeadlock(t *testing.T) {
	dir := t.TempDir()
	procDir := filepath.Join(dir, "processors")
	srcDir := filepath.Join(dir, "src")
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(procDir, "тесттранзакции.yaml"),
		[]byte("name: ТестТранзакции\ntitle: Тест\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	procOS := `Процедура Выполнить()
	НачатьТранзакцию();
	Запрос = Новый Запрос;
	Запрос.Текст = "ВЫБРАТЬ 1 AS Код";
	Рез = Запрос.Выполнить();
	ЗафиксироватьТранзакцию();
КонецПроцедуры
`
	if err := os.WriteFile(filepath.Join(srcDir, "тесттранзакции.proc.os"), []byte(procOS), 0o644); err != nil {
		t.Fatal(err)
	}
	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	db, err := storage.ConnectSQLite(context.Background(), filepath.Join(t.TempDir(), "repro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	messages, runErr, err := RunProcessorOffline(context.Background(), proj, db, "ТестТранзакции", nil, nil)
	if err != nil {
		t.Fatalf("прогон обработки: %v", err)
	}
	if runErr != nil {
		t.Fatalf("модуль обработки упал: %v", runErr)
	}
	if len(messages) != 0 {
		t.Logf("messages: %v", messages)
	}
}
