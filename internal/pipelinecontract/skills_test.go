package pipelinecontract

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func repositoryFile(t *testing.T, parts ...string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	data, err := os.ReadFile(filepath.Join(append([]string{root}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func skill(t *testing.T, name string) string {
	t.Helper()
	return repositoryFile(t, ".claude", "skills", name, "SKILL.md")
}

func requireAll(t *testing.T, text string, fragments ...string) {
	t.Helper()
	for _, fragment := range fragments {
		if !strings.Contains(text, fragment) {
			t.Errorf("pipeline contract is missing %q", fragment)
		}
	}
}

func rejectAll(t *testing.T, text string, fragments ...string) {
	t.Helper()
	for _, fragment := range fragments {
		if strings.Contains(text, fragment) {
			t.Errorf("pipeline contract still contains forbidden fragment %q", fragment)
		}
	}
}

func requireAllCompact(t *testing.T, text string, fragments ...string) {
	t.Helper()
	compact := strings.Join(strings.Fields(text), " ")
	for _, fragment := range fragments {
		if !strings.Contains(compact, strings.Join(strings.Fields(fragment), " ")) {
			t.Errorf("pipeline contract is missing compact fragment %q", fragment)
		}
	}
}

func requireCompactInOrder(t *testing.T, text string, fragments ...string) {
	t.Helper()
	compact := strings.Join(strings.Fields(text), " ")
	position := 0
	for _, fragment := range fragments {
		needle := strings.Join(strings.Fields(fragment), " ")
		relative := strings.Index(compact[position:], needle)
		if relative < 0 {
			t.Fatalf("pipeline contract is missing ordered compact fragment %q after offset %d", fragment, position)
		}
		position += relative + len(needle)
	}
}

func TestReviewQueueUsesGH240CompatiblePaginatedREST(t *testing.T) {
	review := skill(t, "review-queue")
	requireAll(t, review,
		"repos/ivanarama/onebase/pulls?state=open&per_page=100&sort=created&direction=asc",
		"Объедини все страницы, отсортируй по числовому `number`",
		"--jq '{sha:.head.sha,labels:[.labels[].name]}'",
		"gh api --paginate",
		"comments?per_page=100",
	)
	rejectAll(t, review, "headRefOid", "number,title,labels,isDraft,comments", "gh pr list --state open --limit 50")
}

func TestReviewMarkersCannotCollideWithTailMarker(t *testing.T) {
	review := skill(t, "review-queue")
	tailPrefix := "<!-- pp:review"
	headMarker := "<!-- pp:head-reviewed"
	if strings.HasPrefix(headMarker, tailPrefix) {
		t.Fatalf("head marker %q collides with tail prefix %q", headMarker, tailPrefix)
	}
	requireAll(t, review,
		"^<!-- pp:review pp:tail=[0-9]+ -->$",
		headMarker+" <полный проверенный SHA> review-comment=<id заключения> -->",
	)
	rejectAll(t, review, "pp:review-head")
}

func TestReviewDecisionTableCoversBehavioralScenarios(t *testing.T) {
	review := skill(t, "review-queue")
	cases := []string{
		"есть `ship` или `hold` | пропустить",
		"есть каноничный committed-маркер и `changes-requested` / `needs-decision`, более позднего override нет | пропустить",
		"после committed-пары есть непоглощённый override при `changes-requested` / `needs-decision` | REVIEW продолжает",
		"есть `changes-requested` без committed-маркера текущего SHA | FIX безопасно снимет",
		"текущий SHA уже отмечен, более позднего override нет | пропустить; лимит 2 не расходовать",
		"текущий SHA отмечен, позже есть доверенный override | ревьюить один раз",
		"текущий SHA не отмечен, в том числе при старой `reviewed` | не удалять общую метку; ревьюить",
		"override требует повтор, при этом осталась `reviewed` | не удалять общую метку; ревьюить",
		"marker/override написал не `ivanarama` | игнорировать событие",
		"комментариев больше 100 | прочитать все страницы REST",
		"первые PR пропущены по маркеру | продолжать список до 2 реальных аудитов",
	}
	for _, scenario := range cases {
		t.Run(scenario, func(t *testing.T) {
			requireAll(t, review, scenario)
		})
	}
	requireAllCompact(t, review,
		"`reviewed` здесь не жёсткий фильтр",
		"pp:review-again",
		"Упорядочь события по `created_at`, при равенстве — по числовому `id`",
		"**Не удаляй её перед аудитом**",
		"пока не начнёшь **2 реальных аудита**",
		"Пропущенный по head-маркеру PR лимит не расходует",
	)
}

func TestReviewBindsVerdictToCheckedHead(t *testing.T) {
	review := skill(t, "review-queue")
	requireAllCompact(t, review,
		"git fetch origin pull/<M>/head",
		"<сохранённый SHA>",
		"Непосредственно перед **каждым внешним изменением** заново прочитай `.head.sha`, актуальные метки и **все** комментарии",
		"`ship`/`hold` всегда запрещают изменение",
		"<!-- pp:stale-review <проверенный SHA> -->",
		"после постановки",
		"**не удаляй общую метку**",
		"у GitHub-метки нет владельца",
		"review-комментарий → claim → подтверждённая итоговая метка → committed-маркер",
		"следующий прогон продолжит её по orphan-комментарию",
		"Круги считай только по таким committed-парам",
		"каждый уникальный `review-comment id` учитывай не больше одного раза",
		"Незавершённый review-комментарий\n   остаётся диагностикой попытки, но круг не увеличивает",
		"review-comment` указывает на существующий\n   более ранний доверенный комментарий",
	)
	rejectAll(t, review,
		"немедленно сними только свою метку",
		"немедленно сними только что поставленную метку",
	)
}

func TestReviewCompletionIsRecoverableAndCannotConsumeNewerOverride(t *testing.T) {
	review := skill(t, "review-queue")
	requireAllCompact(t, review,
		"Каждый review-комментарий обязан содержать `Reviewed-SHA: <40-символьный SHA>`",
		"`Outcome-Label: reviewed|changes-requested|needs-decision`",
		"Незавершённую транзакцию восстанавливай до обычного фильтра",
		"если для текущего SHA в этой эпохе уже есть committed-пара",
		"не ставь по ним метку и completion",
		"выбери самый ранний подходящий orphan текущей эпохи",
		"Поставить/подтвердить ожидаемую метку может только orphan, чей claim оказался самым ранним",
		"это **первая** ссылка completion на данный `review-comment id`",
		"между review-комментарием и completion нет доверенной строки `pp:review-again`",
		"Для одного SHA без разделяющего override канонична только самая ранняя валидная пара",
		"его может поглотить лишь новый review-комментарий, опубликованный **после** override",
		"Перед committed-маркером комментарий должен оставаться каноничным",
		"перед **каждым внешним изменением**",
		"после любой каноничной committed-пары человек опубликовал доверенный `pp:review-again`",
		"это явный recoverable handoff обратно в REVIEW",
		"Более поздний непоглощённый override отменяет маршрутный стоп `changes-requested` / `needs-decision`",
		"<!-- pp:review-claim <40-символьный SHA> review-comment=<числовой id> -->",
		"Stale `reviewed` от прошлой эпохи или старого HEAD конфликтом не считается",
		"Опасный необъяснимый конфликт — `needs-decision`",
		"активные блокирующие `changes-requested` + `needs-decision`",
	)
	requireCompactInOrder(t, review,
		"После публикации заключения перечитай HEAD",
		"опубликуй claim с SHA и id заключения",
		"только самый ранний валидный claim текущей эпохи вправе поставить",
		"ожидаемую `Outcome-Label`",
		"опубликуй **отдельный committed-комментарий**",
	)
}

func TestFixerSelectsExactPaginatedReviewConclusion(t *testing.T) {
	fixer := skill(t, "fix-approved")
	requireAll(t, fixer,
		"FIX не должен пушить в эту ветку\n   одновременно с MERGE",
		"получи **все** открытые PR пагинированным REST",
		"gh api --paginate \"repos/ivanarama/onebase/pulls?state=open&per_page=100\"",
		"припаркованные PR не должны навсегда скрывать более поздний crash-handoff",
		"уже полученного в п. 1 **полного пагинированного списка**",
		"перед **каждым\n   внешним изменением**",
		"`changes-requested` присутствует, а `ship`,\n   `hold`, `needs-decision` отсутствуют",
		"Сразу после push проверь\n   гейт снова",
		"Исключения — только две восстанавливаемые передачи из п. 8",
		"устаревшего `changes-requested` обратно в REVIEW",
		"поставь и\n   сверь `needs-decision`, сними `changes-requested`",
		"В финале обязаны остаться\n   `needs-decision` и отсутствовать `changes-requested`",
		"^<!-- pp:review pp:tail=[0-9]+ -->$",
		"gh api --paginate",
		"`<!-- pp:head-reviewed <SHA> review-comment=<id> -->`",
	)
	requireAllCompact(t, fixer,
		"Затем исключи `ship` и `hold`",
		"После push и непосредственно перед `gh pr create` повтори полную проверку ещё раз",
		"Незавершённый tail-комментарий без валидного completion",
		"сравни с текущим `.head.sha` через REST **до создания worktree**",
		"HEAD изменился после ревью; требуется новое заключение",
		"Непосредственно перед push ещё раз сравни удалённый `.head.sha` с SHA завершённого review",
		"Для устаревшего ревью сними `changes-requested`, сверь удаление и только затем оставь диагностический комментарий",
	)
	rejectAll(t, fixer,
		"ищи его по **префиксу**",
		"gh pr list --state open\n   --label <метка>",
		"gh pr list --state open --json number,title,body",
	)
}

func TestFixerReturnsOrphanReviewAndConsumesExplicitHumanDecision(t *testing.T) {
	fixer := skill(t, "fix-approved")
	requireAllCompact(t, fixer,
		"Если committed-маркера для текущего SHA нет",
		"`changes-requested` снять → сверить",
		"нет завершённого ревью текущего HEAD; возвращено в REVIEW",
		"Комментарий человека с отдельной строкой `pp:fix-decision <текущий SHA>`",
		"его текст старше исходных блокеров и задаёт фактический объём доработки",
		"`Outcome-Label` не `changes-requested`",
		"текущая `changes-requested` — stale маршрутная подсказка",
	)
}

func TestFixerHandoffsAreCrashRecoverable(t *testing.T) {
	fixer := skill(t, "fix-approved")
	requireAllCompact(t, fixer,
		"Объедини два списка: PR с `changes-requested` и PR с `needs-decision`",
		"`<!-- pp:fix-handoff needs-decision head=<SHA> -->`",
		"точным маркером `<!-- pp:fix-handoff needs-decision head=<текущий SHA> -->`",
		"поставь и сверь `needs-decision`, сними `changes-requested`",
		"`pp:fix-decision <SHA>`",
		"`review-again` передаёт REVIEW, поэтому FIX не меняет ни метки, ни код",
		"перед **каждой** мутацией recovery заново прочитай одним циклом HEAD, все комментарии и labels",
		"Если HEAD или владелец изменились, остановись без мутации",
		"каждая каноничная committed-пара `pp:head-reviewed`",
		"новый completion после `review-again` поглощает override",
	)
	requireCompactInOrder(t, fixer,
		"`fix-decision` возвращает в FIX",
		"сначала поставить/сверить `changes-requested`",
		"затем снять `needs-decision`",
	)
}

func TestMergeRechecksHumanGateUntilMerge(t *testing.T) {
	merge := skill(t, "merge-shepherd")
	requireAllCompact(t, merge,
		"получи **все** открытые PR пагинированным REST",
		"gh api --paginate \"repos/ivanarama/onebase/pulls?state=open&per_page=100\"",
		"30 припаркованных PR не должны скрыть 31-й допустимый",
		"исключи `hold` и `needs-decision`",
		"Перед **каждым внешним изменением PR**",
		"push разрешённого конфликта",
		"на каждом\n   опросе CI",
		"непосредственно перед compare-and-merge REST",
		"issues/<N> --jq '[.labels[].name]'",
		"`ship` присутствует",
		"`hold` и\n   `needs-decision` отсутствуют",
		"`<!-- pp:head-reviewed <текущий SHA> review-comment=<числовой id> -->`",
		"ссылается на существующий более ранний доверенный review-комментарий",
		"После completion не должно быть отдельной строки `pp:review-again`",
		"сними `ship` через REST",
		"комментарий является разрешённым завершающим шагом **этой же\n   транзакции**",
		"Никакие update/push/merge до успешного SHA-гейта недопустимы",
		"Успешная команда\n     меняет HEAD, поэтому старое ревью больше недействительно",
		"Подтверждённый push меняет HEAD",
		"Ждать CI и мержить новый SHA без повторного REVIEW нельзя",
		"непосредственно перед мутацией ещё раз выполни полный label+SHA-гейт",
		"последний полный гейт",
		"timeline?per_page=100",
		"event `labeled` для `ship`, расположенный **после** каноничного",
		"одним raw GraphQL-запросом",
		"точка невозврата",
		"`node_id` конкретных review-комментария и completion",
		"`databaseId`, автор, SHA, Outcome-Label",
		"`lastEditedAt` обоих комментариев",
		"Предыдущий comment-watermark обязан присутствовать среди `comments(last:100)`",
		"требуется новый аудит/completion",
		"review:node(id:$reviewNode)",
		"completion:node(id:$completionNode)",
		`{"merge_method":"merge","sha":"<проверенный SHA>"}`,
		"Успех — только ответ с `merged: true`",
		"`409` означает, что HEAD успел измениться",
	)
	requireCompactInOrder(t, merge,
		"5. Мерж:",
		"одним raw GraphQL-запросом",
		"**точка невозврата**",
		"compare-and-merge REST",
	)
	rejectAll(t, merge,
		"gh pr merge <N> --merge --delete-branch",
		"gh pr list --state open --label ship",
	)
}

func TestMergeUsesCompareAndUpdateForReviewedHead(t *testing.T) {
	merge := skill(t, "merge-shepherd")
	requireAllCompact(t, merge,
		`{"expected_head_sha":"<проверенный SHA>"}`,
		"pulls/<N>/update-branch --input -",
		"При `422` сначала снова прочитай HEAD",
		"Только несовпадение с сохранённым SHA означает гонку",
		"Валидна только первая completion-ссылка на данный review-comment",
		"между ними не должно быть `pp:review-again`",
		"Для одного SHA без разделяющего override канонична только самая ранняя валидная пара",
	)
}

func TestFixAndMergeCheckoutExactlyReviewedHead(t *testing.T) {
	fixer := skill(t, "fix-approved")
	merge := skill(t, "merge-shepherd")
	requireAllCompact(t, fixer,
		"git rev-parse FETCH_HEAD # обязан совпасть с SHA completion",
		"git worktree add -B pp-rework-<M> ../pp-rework-<M> <SHA completion>",
		"git push --force-with-lease=refs/heads/<ветка-PR>:<SHA completion>",
		"origin HEAD:refs/heads/<ветка-PR>",
		"Lease failure означает чужой push",
	)
	requireAllCompact(t, merge,
		"git fetch origin main:refs/remotes/origin/main",
		"`git rev-parse FETCH_HEAD` равен сохранённому SHA",
		"git worktree add -B pp-mrg-<N> ../pp-mrg-<N> <сохранённый SHA>",
		"exit code 0 и HEAD не изменился — это настоящий no-op",
		"git diff --name-only --diff-filter=U",
		"ненулевой exit code без unmerged-файлов — это ошибка команды, а не конфликт",
		"Не классифицируй результат только по неизменившемуся HEAD",
		"git push --force-with-lease=refs/heads/<ветка-PR>:<сохранённый SHA> origin HEAD:refs/heads/<ветка-PR>",
		"Lease failure означает гонку",
		"новый `.head.sha` равен локальному `git rev-parse HEAD`",
	)
}

func TestTailUsesCanonicalPaginatedCommittedReview(t *testing.T) {
	tail := skill(t, "tail-issues")
	requireAllCompact(t, tail,
		"gh api --paginate",
		"comments?per_page=100",
		"нет каноничной committed-пары для merged HEAD",
		"Возьми только заключение, чей числовой `id` указан последней каноничной committed-парой merged HEAD",
		"Более поздний orphan `pp:review` без валидной ссылки не является аудитом",
		"после выбранной committed-пары есть более поздняя доверенная отдельная",
		"Если после неё остался непоглощённый `pp:review-again`, PR уже отброшен",
	)
	rejectAll(t, tail, "--json number,title,mergedAt,labels,url,comments")
}

func TestDetailedMaintenanceGuideMatchesQueueContracts(t *testing.T) {
	docs := repositoryFile(t, "docs", "maintenance-pipeline.md")
	requireAll(t, docs,
		"`changes-requested`, но без `ship`/`hold`/`needs-decision`",
		"без `ship`/`hold` и не черновики",
		"`changes-requested`/`needs-decision` обычно передают мяч дальше",
		"маркером `pp:head-reviewed`",
		"отдельная строка\nкоторого равна `pp:review-again`",
		"без `hold`/`needs-decision`",
		"непосредственно перед merge",
		"push разрешённого конфликта",
		"`<!-- pp:head-reviewed <SHA> review-comment=<id заключения> -->`",
		"Если сбой случился до committed-\nмаркера, сорванная попытка круг не увеличивает",
		"пастух снимает устаревший\n`ship`",
		"`BEHIND` → `update-branch` вызывается с `expected_head_sha` проверенного HEAD",
		"После `422` HEAD перечитывается",
		"полный label+SHA-гейт непосредственно перед единственным\n  перезапуском",
		"Валидна только\nпервая completion-ссылка на этот id",
		"SHA с удалённым HEAD до создания worktree и ещё раз непосредственно перед push",
		"REST-запросом compare-and-merge с полем\n`sha=<проверенный HEAD>`",
		"Оба этапа отправляют изменения атомарным CAS-push",
		"--force-with-lease=refs/heads/<ветка-PR>:<проверенный SHA>",
		"последняя каноничная committed-пара merged HEAD после последнего поглощённого\noverride",
		"Открытые PR для обычной доработки и восстановления handoff FIX получает одним\nпагинированным REST-списком",
		"Начальный список MERGE также читается целиком пагинированным REST",
		"маркер. Восстановление orphan тоже сначала создаёт/проверяет claim. Если в той\nже эпохе после последнего override уже есть каноничная",
		"На PR это стоп по умолчанию, но есть два точных автоматических handoff:",
		"Успешный snapshot — точка невозврата",
		"Новый каноничный `pp:head-reviewed` после override поглощает его",
		"повторяется перед push и непосредственно перед `gh pr create`",
		"Один raw GraphQL snapshot адресует оба комментария по\nnode ID",
		"Watermark обязан оставаться в окне",
		"Ship-event должен быть позже создания и\nпоследнего edit обоих адресованных комментариев",
		"**PR, нужна доработка** → в комментарии с решением добавить отдельную",
		"строку `pp:fix-decision <текущий SHA>`",
		"**сначала поставить и проверить**\n     `changes-requested` и только после этого снять `needs-decision`",
		"**PR, код оставляем / повторяем аудит текущего SHA** → добавить отдельной",
		"`approved` на PR не ставьте",
	)
	rejectAll(t, docs,
		"без `ship`, `reviewed`, `changes-requested`, `hold`",
		"Вливает только PR с `ship` и без `hold`,",
		"не вливает PR без `ship` и не трогает такой PR вовсе",
	)
}

func TestTopLevelNeedsDecisionMatchesAutomaticHandoffs(t *testing.T) {
	guide := repositoryFile(t, "CLAUDE.md")
	requireAllCompact(t, guide,
		"На PR это стоп по умолчанию",
		"`pp:review-again` разрешает REVIEW снять парковку",
		"`pp:fix-decision <SHA>` возвращает PR в FIX crash-safe порядком",
	)
	rejectAll(t, guide, "на PR эту метку после решения снимает человек")
	rejectAll(t, guide, "без `ship` не вливается никогда и не трогается вовсе")
}

func TestTopLevelInstructionsDoNotBypassPRStops(t *testing.T) {
	merge := skill(t, "merge-shepherd")
	guide := repositoryFile(t, "CLAUDE.md")
	requireAllCompact(t, merge,
		"без `hold`/`needs-decision`",
		"`ship` — разрешение человека, но `hold` и `needs-decision` старше него",
	)
	requireAllCompact(t, guide,
		"`hold` и `needs-decision` — стопы даже при `ship`",
		"перед мержем они проверяются в одном согласованном GraphQL snapshot",
		"Этот snapshot — точка невозврата",
		"timeline обязана доказывать, что человек поставил `ship` после этого completion",
		"FIX перепроверяет `ship` перед каждым внешним изменением",
		"ветку больше не меняет",
		"Узкое окно, где push уже завершился перед появлением `ship`, закрывает SHA-гейт MERGE",
		"REVIEW получает полный пагинированный список PR",
		"FIX применяет замечания только к SHA из завершённой пары",
		"атомарно защищён HEAD, а labels защищены предшествующей точкой невозврата",
	)
	rejectAll(t, merge, "`ship` — единственный гейт")
	rejectAll(t, guide, "Гейт мержа один")
	rejectAll(t, merge,
		"дальше ждать CI (п. 4)",
		"После разрешения — пуш в ветку PR, worktree убрать, ждать CI",
	)
}

func TestMergeEnvironmentMatchesCommandsUsedByProcedure(t *testing.T) {
	merge := skill(t, "merge-shepherd")
	requireAllCompact(t, merge,
		"`gh run view` и `gh run rerun`",
		"используют явные поля либо REST",
	)
	rejectAll(t, merge,
		"Не проверялись только `gh pr merge`",
		"Упадут с той же ошибкой",
		"gh run rerun <id> --failed",
	)
	requireAllCompact(t, merge,
		"`gh run rerun <id>`",
		"`--failed` в `gh` 2.4.0 ещё не поддерживается",
	)
}
