# Проверка маршрутизации CI для PR #1730

Реализация находится в отдельном проекте PromptPilot, а не в этом репозитории.
Исправление: [commit `aed8088`](https://github.com/ivanarama/PromptPilot/commit/aed8088)
ветки `fix/pipeline-human-handoff-repeat`.
В `promptpilot/project_pipeline.py:next_merge` неуспешный результат
`checks_ready` теперь возвращает `fallback_target` для того же проверенного PR.
Путь `checks_in_progress` по-прежнему завершает явное ожидание без провайдера.
Proof, trusted ship, HEAD, single-flight, подпись lease и fresh completion gates
не ослаблены; fallback не выполняет GitHub-мутаций автоматически.

Поведенческий тест:
`tests/test_merge_wait_dispatch.py::test_cli_and_dispatcher_route_required_ci_states`.
Он вызывает публичную команду `pipelinectl next merge`, затем передаёт её
результат в `execution_route`, используя реалистичный claim-bound review-proof
и trusted ship. Проверяются девять сценариев:

- FAILURE, CANCELLED, TIMED_OUT, UNKNOWN, отсутствующий обязательный build и
  пустой набор CI дают подписанный exact-target fallback и запуск полного скилла;
- IN_PROGRESS и PENDING дают wait и завершение без модели;
- успешный обязательный build с красным необязательным bench допускает обычный
  merge-маршрут.

До исправления тест воспроизвёл `wait` вместо fallback на FAILURE. После
исправления прошли все девять сценариев и 436 регрессионных тестов PromptPilot.
Тест не пишет в живой GitHub; отсутствие внешних записей проверяет test double.
Это свидетельство проверки внешней реализации, не подмена её тестами OneBase
и не независимое одобрение PR. При повторном REVIEW нужно сверить фактически
установленную сборку PromptPilot и заново проверить этот внешний контракт.

На Mac развёрнута сборка `efficient-merge-20260927/dist-v5/pp`; предыдущая
`dist-v4/pp` сохранена для отката. Worker заменён после завершения активных
задач, без прерывания агента. SHA-256 использованного исходного
`promptpilot/project_pipeline.py`:
`54d762d169cd4a3b3dc326b2858fecdccd487d2e18eadb3343bca5495525118c`.
Исходник на Mac побайтно совпал с проверенным локальным файлом. Это запись о
развёртывании 27 сентября 2026 года, а не гарантия неизменности будущей установки.
