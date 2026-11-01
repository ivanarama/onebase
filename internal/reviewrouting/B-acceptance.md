# Срез B: положительное REVIEW и межстадийная регрессия

Рабочая граница OneBase #1909 / PromptPilot #152 после совместной приёмки A.
Это описание Draft, а не утверждение о готовой реализации.

Положительный typed proof должен стать отдельным входом MERGE/TAIL. Нельзя
превращать его в synthetic legacy proof или считать technical/engineering
handoff успешным REVIEW. Parser, FIX и конечный бюджет уже приняты в A.

Checker описывает только совместимость и кандидатов. Исполнитель независимо
проверяет полную identity, автора, HEAD/epoch/source, trusted ship, CI, свежий
main и single-flight; подписанная lease имеет конечный срок. Неопределённая
мутация требует readback, а не повторного merge/create.

Приёмка включает публичные positive/negative next/complete MERGE, TAIL только
после доказанного merge, ABA/edit/delete, сохранение бюджета через два новых
HEAD и смену имени blocker, engineering resume и исполнение другой цели после
парковки. Существующий механизм PromptPilot #144 используется без дублирования.
Нужны общие fixtures, actual Go peer, независимые ревью точной пары, локальные
Go build/test/vet/lint, native PromptPilot и все обязательные CI обоих проектов.

Производственные флаги остаются выключены. Установленная пара, Dashboard и
пилот относятся к срезу C; #1836 этот PR не закрывает.
