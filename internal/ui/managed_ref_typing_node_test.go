package ui

import (
	"os/exec"
	"testing"
)

// Ввод по строке у ссылочного поля (input_by_string). Проверяется поведение
// настоящих функций managed.js: порог в три символа, единственное совпадение,
// выпадающий список с выбором с клавиатуры и «не найдено» на пустой выдаче.
func TestManagedRefTypingBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the reference typing regression test")
	}
	cmd := exec.Command(node, "--test", "static/managed_ref_typing_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node reference typing behavior test: %v\n%s", err, output)
	}
}
