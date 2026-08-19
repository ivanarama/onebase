package langref

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/token"
)

// backslash — один символ «\». Записан кодом, чтобы сам тест не зависел от
// того, как редактор или скрипт обошлись с экранированием.
const backslash = "\x5c"

// ellipsis — многоточие-заглушка вместо опущенного куска примера
// (Дв.Товар = …). Законно в тексте справки, лексеру DSL неизвестно.
const ellipsis = "…"

// Строки DSL — сырые: лексер не знает экранирования, только «""» для кавычки
// внутри строки. Из-за этого Go-привычка писать удвоенный бэкслеш превращает
// пример в шаблон с литеральным «\» и буквой «d» — Совпадает() молча вернёт
// Ложь, а пользователь скопировал пример из справки и получил неработающую
// проверку без единого сообщения (#998).
//
// Гейт лексирует каждый Example справочника: ловит и удвоенный бэкслеш внутри
// строкового литерала, и пример, который вообще не разбирается лексером.
// Примеры языка запросов (KindQuery) проверяются только на бэкслеш: у них своя
// грамматика ('строка', &Параметр), которую лексер DSL не обязан знать.
func TestExamplesLexAsDSL(t *testing.T) {
	for _, d := range All() {
		if strings.TrimSpace(d.Example) == "" {
			continue
		}
		lx := lexer.New(d.Example, "langref:"+d.Name)
		for i := 0; ; i++ {
			if i > 10000 {
				t.Fatalf("%s: лексер не дошёл до EOF за 10000 токенов", d.Display)
			}
			tk := lx.NextToken()
			if tk.Type == token.EOF {
				break
			}
			switch {
			case tk.Type == token.ILLEGAL && d.Kind != KindQuery && tk.Literal != ellipsis:
				t.Errorf("%s: пример не лексится как DSL — недопустимый символ %q\n  пример: %s",
					d.Display, tk.Literal, d.Example)
			case tk.Type == token.STRING && strings.Contains(tk.Literal, backslash+backslash):
				t.Errorf("%s: в строке примера удвоенный бэкслеш (%q) — строки DSL сырые, "+
					"в шаблон попадёт литеральный «\\», пиши одинарный\n  пример: %s",
					d.Display, tk.Literal, d.Example)
			}
		}
	}
}
