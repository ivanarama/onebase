package metadata

import (
	"fmt"
	"strings"
)

// StatusBar — постоянная строка внизу каждой страницы интерфейса (ключ
// status_bar в config/home_page.yaml). Аналог панели внизу окна 1С: оператор
// работает с базой на весь экран, а состояние внешней системы (звонка,
// телефонии) должно оставаться на виду в любом режиме открытия форм.
//
// Содержимое строки целиком задаёт конфигурация: функция Source возвращает
// Структуру с полями Слева и Справа (текст) и, по желанию, Кнопки — массив
// имён показываемых кнопок. Неопределено скрывает строку у этого пользователя.
// Кнопки объявлены здесь, нажатие вызывает Action с именем кнопки.
type StatusBar struct {
	// Roles — кому показывать строку; пусто — всем вошедшим пользователям.
	Roles []string `yaml:"roles,omitempty"`
	// Source — «Модуль.Функция» общего модуля: содержимое строки.
	Source string `yaml:"source"`
	// Action — «Модуль.Процедура» общего модуля: нажатие кнопки, параметр —
	// имя кнопки. Функция может вернуть строку — она показывается в строке
	// как сообщение (например, почему смена статуса отклонена).
	Action string `yaml:"action,omitempty"`
	// Interval — период обновления в секундах, 1…60; 0 — 3 секунды.
	Interval int               `yaml:"interval,omitempty"`
	Buttons  []StatusBarButton `yaml:"buttons,omitempty"`
}

// StatusBarButton — кнопка строки: имя для обработчика и подпись.
type StatusBarButton struct {
	Name  string `yaml:"name"`
	Title string `yaml:"title,omitempty"`
}

// StatusBarDefaultInterval — период обновления строки по умолчанию, секунды.
const StatusBarDefaultInterval = 3

// IntervalSeconds — период обновления с учётом умолчания.
func (b *StatusBar) IntervalSeconds() int {
	if b == nil || b.Interval <= 0 {
		return StatusBarDefaultInterval
	}
	return b.Interval
}

// ForRoles — показывать ли строку пользователю с этими ролями (без учёта
// регистра). Пустой список ролей строки — всем.
func (b *StatusBar) ForRoles(roles []string) bool {
	if b == nil {
		return false
	}
	if len(b.Roles) == 0 {
		return true
	}
	for _, want := range b.Roles {
		for _, have := range roles {
			if strings.EqualFold(strings.TrimSpace(want), strings.TrimSpace(have)) {
				return true
			}
		}
	}
	return false
}

// SplitStatusBarProc разбирает «Модуль.Процедура» на части.
func SplitStatusBarProc(ref string) (module, proc string, ok bool) {
	ref = strings.TrimSpace(ref)
	i := strings.LastIndex(ref, ".")
	if i <= 0 || i == len(ref)-1 {
		return "", "", false
	}
	return strings.TrimSpace(ref[:i]), strings.TrimSpace(ref[i+1:]), true
}

// Validate проверяет форму объявления; существование процедур проверяет
// onebase check по загруженным модулям.
func (b *StatusBar) Validate() error {
	if b == nil {
		return nil
	}
	if _, _, ok := SplitStatusBarProc(b.Source); !ok {
		return fmt.Errorf("status_bar.source: нужно «Модуль.Функция», получено %q", b.Source)
	}
	if b.Action != "" {
		if _, _, ok := SplitStatusBarProc(b.Action); !ok {
			return fmt.Errorf("status_bar.action: нужно «Модуль.Процедура», получено %q", b.Action)
		}
	}
	if b.Interval < 0 || b.Interval > 60 {
		return fmt.Errorf("status_bar.interval: от 1 до 60 секунд, получено %d", b.Interval)
	}
	seen := map[string]bool{}
	for _, button := range b.Buttons {
		name := strings.ToLower(strings.TrimSpace(button.Name))
		if name == "" {
			return fmt.Errorf("status_bar.buttons: у кнопки нет name")
		}
		if seen[name] {
			return fmt.Errorf("status_bar.buttons: кнопка %q объявлена дважды", button.Name)
		}
		seen[name] = true
	}
	if len(b.Buttons) > 0 && b.Action == "" {
		return fmt.Errorf("status_bar: кнопки объявлены, но нет action — нажимать их некуда")
	}
	return nil
}
