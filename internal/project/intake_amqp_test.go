package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Шлюз приёмки с transport amqp отвергается на загрузке конфигурации.
//
// Транспорт заложен в модель шлюза (шов MessageSource, план 90), но потребителя
// очереди в платформе нет. Раньше такое объявление проходило и onebase check, и
// запуск: интеграция выглядела настроенной, а шлюз не принимал ни одного
// сообщения. Проверка идёт через Load — ту же точку входа, которой конфигурацию
// читают check, migrate и run.
func TestLoad_RejectsUnimplementedAMQPIntake(t *testing.T) {
	dir := t.TempDir()
	intakeDir := filepath.Join(dir, "intake")
	if err := os.MkdirAll(intakeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	decl := "name: ЗаказыИзОчереди\n" +
		"transport: amqp\n" +
		"handler: ЗаказыИзОчереди\n"
	if err := os.WriteFile(filepath.Join(intakeDir, "заказыизочереди.yaml"), []byte(decl), 0o644); err != nil {
		t.Fatal(err)
	}

	proj, err := Load(dir)
	if err == nil {
		proj.Close()
		t.Fatal("конфигурация со шлюзом transport amqp загрузилась: шлюз выглядел бы рабочим и молча ничего не принимал")
	}
	for _, want := range []string{"ЗаказыИзОчереди", "amqp", "не поддерживается"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("текст отказа %q не содержит %q", err.Error(), want)
		}
	}
}
