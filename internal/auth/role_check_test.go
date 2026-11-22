package auth

import "testing"

func TestKindFromWord(t *testing.T) {
	cases := map[string]string{
		"документ":        "document",
		"Документы":       "document",
		"document":        "document",
		"справочник":      "catalog",
		"регистр":         "register",
		"регистрсведений": "inforeg",
		"РегистрСведений": "inforeg",
		"отчёт":           "report",
		"обработка":       "processor",
		"ерунда":          "",
	}
	for in, want := range cases {
		if got := KindFromWord(in); got != want {
			t.Errorf("KindFromWord(%q) = %q, ожидалось %q", in, got, want)
		}
	}
}

func TestNormalizeOp(t *testing.T) {
	cases := map[string]string{
		"провести":  "post",
		"Провести":  "post",
		"изменять":  "write",
		"READ":      "read",
		"удалять":   "delete",
		"выполнить": "run",
		"custom":    "custom", // незнакомая операция проходит как есть
	}
	for in, want := range cases {
		if got := NormalizeOp(in); got != want {
			t.Errorf("NormalizeOp(%q) = %q, ожидалось %q", in, got, want)
		}
	}
}

func TestRoleAllows(t *testing.T) {
	role := &Role{
		Name: "Кладовщик",
		Permissions: Permission{
			Documents: map[string][]string{"Реализация": {"read", "write", "post", "unpost"}},
			Catalogs:  map[string][]string{"Организация": {"read"}},
		},
	}

	mustAllow := func(kind, entity, op string, want bool) {
		t.Helper()
		got, err := role.Allows(kind, entity, op)
		if err != nil {
			t.Fatalf("Allows(%q,%q,%q): неожиданная ошибка %v", kind, entity, op, err)
		}
		if got != want {
			t.Errorf("Allows(%q,%q,%q) = %v, ожидалось %v", kind, entity, op, got, want)
		}
	}

	mustAllow("документ", "Реализация", "провести", true) // синоним провести→post
	mustAllow("документ", "Реализация", "post", true)
	mustAllow("документ", "Реализация", "удалять", false) // delete не выдан
	mustAllow("справочник", "Организация", "читать", true)
	mustAllow("справочник", "Организация", "изменять", false)
	mustAllow("документ", "Поступление", "читать", false) // объекта нет в роли

	// Неизвестный вид — ошибка (опечатка автора теста должна быть громкой).
	if _, err := role.Allows("чтотоневерное", "Реализация", "read"); err == nil {
		t.Error("Allows с неизвестным видом должен вернуть ошибку")
	}

	// Обработки требуют явного разрешения, как остальные виды объектов.
	if ok, err := role.Allows("обработка", "Любая", "run"); err != nil || ok {
		t.Errorf("роль без секции processors должна запрещать обработку: ok=%v err=%v", ok, err)
	}
	// Явно пустая (non-nil) секция processors — запрещает все обработки.
	restricted := &Role{Permissions: Permission{Processors: map[string][]string{}}}
	if ok, _ := restricted.Allows("обработка", "Любая", "run"); ok {
		t.Error("пустая секция processors должна запрещать обработки")
	}
}
