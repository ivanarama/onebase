# План 99: Отладчик модулей (Debugger)

**Статус**: Реализовано (базовая версия)
**Приоритет**: Критический для разработки конфигураций
**Аналог**: Консоль кода / Отладчик в 1С:Предприятие

## Что реализовано

### Консоль кода (Console of Code)
- Страница `/ui/debug/console` — ввод выражения DSL, выполнение, результат
- POST `/debug/evaluate` — вычисление выражения через существующий lexer+parser+interpreter
- История команд (стрелки вверх/вниз)
- Поддержка любых DSL выражений: арифметика, конструкторы, вызовы функций

### Breakpoints + Pause/Resume
- `internal/debugger/controller.go` — `ActiveSession` с channel-based pause/resume
- Интерпретатор блокируется на канале при попадании в breakpoint
- HTTP API для управления: continue, step, stop
- Пошаговое выполнение: step into, step over, step out

### Хук в интерпретаторе
- `interpreter.DebugHook` интерфейс — nil = нет отладки, нулевые накладные расходы
- `execBlock` → `beforeStmt` — проверка breakpoints и step mode перед каждым оператором
- `callUserProc` → push/pop call stack — отслеживание глубины вызовов
- `evaluateExprString` — вычисление выражений через DSL parser

### UI
- Панель отладчика: консоль + переменные + точки останова + стек вызовов
- Кнопки: Start / Continue / Step Into / Step Over / Stop
- Polling статуса каждые 500ms

## Файлы

### Созданы
| Файл | Описание |
|------|----------|
| `internal/debugger/controller.go` | DebugController, ActiveSession, channel-based pause/resume |
| `internal/ui/debug_handlers.go` | HTTP API: evaluate, start, stop, status, breakpoint, continue, step |
| `internal/ui/tpl_debug.go` | HTML/JS шаблон панели отладчика |

### Изменены
| Файл | Изменение |
|------|-----------|
| `internal/dsl/parser/parser.go` | Экспортирован `ParseExpr()` |
| `internal/dsl/interpreter/interpreter.go` | DebugHook интерфейс, beforeStmt, EvalExpr, stackDepth |
| `internal/dsl/interpreter/debug_hooks.go` | Почищен: только getLocation, getExprLocation, env helpers |
| `internal/debugger/protocol.go` | Упрощён: только типы данных |
| `internal/debugger/evaluator.go` | Минимальный: только ParseUserValue |
| `internal/debugger/breakpoints.go` | Опустошён (логика в controller.go) |
| `internal/ui/server.go` | Маршруты отладчика |
| `internal/ui/templates.go` | Добавлен tplDebugConsole |

## Архитектура

```
Browser (HTML/JS)  →  HTTP API (debug_handlers.go)  →  DebugController (controller.go)
                                                        ↕ channels
                                                     Interpreter (beforeStmt hook)
```

- **HTTP polling** (не WebSocket) — проще, достаточно для десктопа
- **Каналы** для pause/resume — интерпретатор блокируется, HTTP handler разблокирует
- **DebugHook интерфейс** — нулевые накладные расходы когда nil
- **Expression evaluation** через DSL parser — не strings.Split

## HTTP API

| Маршрут | Метод | Описание |
|---------|-------|----------|
| `/ui/debug/console` | GET | Страница консоли кода |
| `/debug/evaluate` | POST | Вычислить выражение |
| `/debug/start` | POST | Начать отладку модуля |
| `/debug/stop` | POST | Остановить |
| `/debug/status` | GET | Текущее состояние |
| `/debug/breakpoint` | POST | Установить breakpoint |
| `/debug/breakpoint/{file}/{line}` | DELETE | Удалить breakpoint |
| `/debug/continue` | POST | Продолжить |
| `/debug/step` | POST | Шаг (mode: into/over/out) |

## Условные точки останова (сделано 2026-08-12)

Поле `Condition` наконец участвует в решении: `HookCheckBreakpoint` получает от
интерпретатора вычислитель, `ActiveSession.CheckBreakpoint` считает выражение в
окружении текущего оператора и приводит к булеву правилами `Если`. До этого
условие сохранялось, ездило в UI и не проверялось нигде.

Принятые решения:

- **сломанное условие останавливает** и показывает текст ошибки (`cond_error`).
  Молчаливый пропуск превращал бы точку в невидимо неработающую;
- **вычисление идёт без точек останова и шагов** (флаг `evaluating`): условие
  вида `Удвоить(Сч) > 6` само исполняет DSL и иначе входило бы в свою же строку;
- **синтаксис проверяется при установке** — 400 от `/debug/global/breakpoint`,
  а не сюрприз на первом проходе строки;
- счётчики `hit_count`/`skip_count` разделены: видно, что точка жива, но условие
  ложно;
- заодно выражения табло/консоли перестали ронять отлаживаемый прогон: паника
  внутри выражения отладчика превращается в ошибку (`evalDebugExpr`).

В конфигураторе: Alt+клик по колонке редактора, поле «Условие» в панели точек,
карандаш в списке, отдельный цвет маркера у условной точки.

## Что ещё не реализовано

- [ ] Monaco gutter click для breakpoints (сейчас через API)
- [ ] Hot reload при изменении кода
- [ ] Автосохранение breakpoints между сессиями
- [ ] F9/F5/F10/F11 hot keys
- [ ] Подсветка текущей строки в редакторе
- [ ] Редактирование значений переменных
- [ ] Watches (отслеживание выражений)
