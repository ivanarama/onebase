package interpreter_test

import "testing"

func TestUUIDValue_CollapseIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, first, second, columns, want string
	}{
		{"UUID then string", "Ключ", "Текст", "Ключ", "2|10|20"},
		{"string then UUID", "Текст", "Ключ", "Ключ", "2|10|20"},
		{"equal UUIDs", "Ключ", "Копия", "Ключ", "1|30"},
		{"different UUIDs", "Ключ", "Другой", "Ключ", "2|10|20"},
		{"equal strings", "Текст", "Текст", "Ключ", "1|30"},
		{"UUID in second column", "Ключ", "Текст", "Группа, Ключ", "2|10|20"},
		{"equal UUIDs in second column", "Ключ", "Копия", "Группа, Ключ", "1|30"},
		{"string resembles UUID tag", "Ключ", `"u36:" + Текст`, "Ключ", "2|10|20"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := runUUIDFunc(t, `Функция Тест()
	Ключ = Новый УникальныйИдентификатор("1d2c3b4a-5e6f-4a1b-9c2d-3e4f5a6b7c8d");
	Копия = Новый УникальныйИдентификатор("1D2C3B4A-5E6F-4A1B-9C2D-3E4F5A6B7C8D");
	Другой = Новый УникальныйИдентификатор("2d2c3b4a-5e6f-4a1b-9c2d-3e4f5a6b7c8d");
	Текст = Строка(Ключ);
	Т = Новый ТаблицаЗначений;
	Т.Колонки.Добавить("Группа");
	Т.Колонки.Добавить("Ключ");
	Т.Колонки.Добавить("Сумма");
	Р = Т.Добавить(); Р.Группа = "общая"; Р.Ключ = `+tc.first+`; Р.Сумма = 10;
	Р = Т.Добавить(); Р.Группа = "общая"; Р.Ключ = `+tc.second+`; Р.Сумма = 20;
	Т.Свернуть("`+tc.columns+`", "Сумма");
	Итог = Строка(Т.Количество());
	Для Каждого Р Из Т Цикл
		Итог = Итог + "|" + Строка(Р.Сумма);
	КонецЦикла;
	Возврат Итог;
КонецФункции`)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %v, want %s", got, tc.want)
			}
		})
	}
}
