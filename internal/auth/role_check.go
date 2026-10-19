package auth

import "fmt"

// Хелперы проверки матрицы прав роли — используются ассертами тест-харнесса
// (`Утверждать.РольМожет/РольНеМожет`, план 112) и CLI. Работают поверх того же
// PermissionHas, что и рантайм, поэтому тест видит ровно то поведение, что и
// боевой enforcement на HTTP-границе.

// KindFromWord нормализует пользовательское слово вида объекта
// (справочник/документ/регистр/регистрсведений/отчёт/обработка и их англо- и
// формо-варианты) к внутреннему ключу PermissionHas
// ("catalog"|"document"|"register"|"inforeg"|"report"|"processor"). Пустая
// строка — вид не распознан.
func KindFromWord(word string) string {
	return permissionKindFromKey(word)
}

// NormalizeOp приводит операцию к канону PermissionHas. Принимает и канон
// (read/write/post/unpost/delete/run/disclose/create), и русские синонимы; незнакомое
// слово возвращается как есть (в нижнем регистре) — операции сопоставляются
// литерально, конфигурация вправе объявлять свои.
func NormalizeOp(word string) string {
	w := normalizePermissionKey(word)
	switch w {
	case "read", "читать", "чтение", "прочитать", "просмотр", "смотреть":
		return "read"
	case "write", "писать", "запись", "записать", "изменять", "изменить", "редактировать", "менять":
		return "write"
	case "post", "провести", "проведение", "проводить":
		return "post"
	case "unpost", "отменитьпроведение", "отменапроведения", "распровести", "распроведение":
		return "unpost"
	case "delete", "удалять", "удалить", "удаление":
		return "delete"
	case "run", "запустить", "запуск", "выполнить", "выполнение":
		return "run"
	case "disclose", "раскрыть", "раскрытие":
		return "disclose"
	case "create", "создать", "создание", "добавить", "добавление":
		return "create"
	default:
		return w
	}
}

// Allows сообщает, разрешает ли роль операцию op над объектом entity вида
// kindWord. Ошибка — только для нераспознанного вида (опечатка автора теста);
// неизвестная операция ошибкой не считается (просто не совпадёт).
func (r *Role) Allows(kindWord, entity, op string) (bool, error) {
	if r == nil {
		return false, fmt.Errorf("роль не задана")
	}
	kind := KindFromWord(kindWord)
	if kind == "" {
		return false, fmt.Errorf("неизвестный вид объекта %q (доступны: справочник, документ, регистр, регистрсведений, отчёт, обработка)", kindWord)
	}
	return PermissionHas(r.Permissions, kind, entity, NormalizeOp(op)), nil
}
