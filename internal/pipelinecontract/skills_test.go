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
		"Если хотя бы один claim уже есть, восстанавливай **только orphan, на который он ссылается**",
		"Только при полном отсутствии валидных claims выбери самый ранний подходящий orphan",
		"может только orphan-владелец самого раннего claim",
		"порядок навсегда оставляет recovery без владельца",
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
		"PP-Fix-Transition: from=<SHA> review-comment=<id>",
		"мяч у FIX/recovery",
		"также всегда запрещает REVIEW-мутацию",
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
		"HEAD, **все комментарии** и labels через REST",
		"эта же completion/decision остаётся последним валидным переходом с владельцем\n   FIX",
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
		"Успешный собственный push потребляет старое владение FIX",
		"PP-Fix-Transition: from=<SHA canonical completion> review-comment=<id заключения>",
		"review-комментария/`pp:review-claim` этого HEAD",
		"CAS-loser не вправе снимать её маршрутную метку",
		"HEAD == отправленному SHA",
		"После push и непосредственно перед `gh pr create` повтори полную проверку ещё раз",
		"ветка строго детерминирована: `fix/<N>`",
		"gh api -X POST repos/ivanarama/onebase/git/refs --input -",
		"Только ответ `201 Created` делает worker владельцем branch-claim",
		"Любой иной HTTP-статус / ненулевой exit `gh` останавливает запуск",
		"`409`/`422`",
		"нет ложного успеха `Everything up-to-date`",
		"два worker могут одновременно увидеть отсутствие PR, но второй не получит branch-claim",
		"Незавершённый tail-комментарий без валидного completion",
		"сравни с текущим `.head.sha` через REST **до создания worktree**",
		"HEAD изменился после ревью; требуется новое заключение",
		"Непосредственно перед push перечитай HEAD, все comments и labels, пересчитай владельца",
		"При `pp:review-again`, новой completion или чужом push ничего не",
		"Lease failure означает чужой push",
		"**не** возвращай PR в REVIEW",
		"Для устаревшего ревью сними `changes-requested`, сверь удаление и только затем оставь диагностический комментарий",
		"issue-decision fingerprint",
		"перед **каждым внешним изменением** после branch-claim",
		"Снятый `ready-fix`/`approved`, новый `hold`, закрытие issue, смена",
		"перед добавлением `in-work` и перед `pp:in-work`-комментарием",
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
		"{id,node_id,created_at,updated_at,author:.user.login,body}",
		"**последний переход именно метки `ship`**",
		"Он обязан быть `labeled` от `ivanarama`",
		"не оживает от повторной постановки другим actor",
		"одним raw GraphQL-запросом",
		"точка невозврата",
		"`node_id` конкретных review-комментария и completion",
		"`fullDatabaseId`, автор, SHA, Outcome-Label",
		"32-битный диапазон GraphQL `databaseId`",
		"`fullDatabaseId: BigInt`",
		"сравнивай его строковое значение с REST id",
		"`labels.pageInfo.hasNextPage == false`",
		"**последний** ship-transition",
		"Если ни одного ship-transition нет в `timelineItems(last:100)`",
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
	if got := strings.Count(merge, "fullDatabaseId"); got < 4 {
		t.Fatalf("merge snapshot must use fullDatabaseId in the rule and all three GraphQL nodes, got %d mentions", got)
	}
	rejectAll(t, merge,
		"gh pr merge <N> --merge --delete-branch",
		"gh pr list --state open --label ship",
		"IssueComment{databaseId",
		"nodes{databaseId",
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
		"gh api repos/ivanarama/onebase/pulls/1261 --jq .merged_at",
		"именно это выбранное заключение создано строго раньше",
		"Время мержа самого исходного PR границей не является",
		"последнее заключение создано в момент границы или позже",
		"нет каноничной committed-пары для merged HEAD и не сработал описанный выше",
		"Для нового протокола возьми только заключение, чей числовой `id` указан",
		"Для legacy-drain возьми выбранное в п. 2 последнее доверенное legacy-заключение",
		"Более поздний orphan `pp:review` без валидной ссылки не является аудитом",
		"после выбранной committed-пары есть более поздняя доверенная отдельная",
		"Если после выбранного заключения остался непоглощённый `pp:review-again`, PR уже отброшен",
		"<!-- pp:tail-claim review-comment=<id> review-updated=<RFC3339> item=<N> item-sha256=<64hex> dedupe-sha256=<64hex> owner=<uuid> -->",
		"<!-- pp:tail-lease review-comment=<id> review-updated=<RFC3339> item=<N> item-sha256=<64hex> dedupe-sha256=<64hex> previous=<id активной lease> owner=<uuid> -->",
		"<!-- pp:tail-create-intent review-comment=<id> review-updated=<RFC3339> item=<N> item-sha256=<64hex> dedupe-sha256=<64hex> lease=<id активной lease> owner=<uuid> -->",
		"**собственный возвращённый comment id**",
		"**собственный возвращённый id intent**",
		"Lease действует 30 минут",
		"<!-- pp:tail-source pr=<M> review-comment=<id> review-updated=<RFC3339> item=<N> item-sha256=<64hex> dedupe-sha256=<64hex> -->",
		"<!-- pp:tail-item-done review-comment=<id> review-updated=<RFC3339> item=<N> item-sha256=<64hex> dedupe-sha256=<64hex> issue=<номер|none> -->",
		"<!-- pp:tail-dedupe sha256=<64hex> -->",
		"<!-- pp:tail-done review-comment=<id> review-updated=<RFC3339> -->",
		"updated_at",
		"item-sha256",
		"dedupe-sha256",
		"Перед **каждым внешним изменением**",
		"Не используй GitHub Search",
		"issues?state=all&since=<root-claim-created-at>&per_page=100",
		"автора issue `ivanarama`",
		"{number,author:.user.login,body}",
		"Создание exact-source issue — точка невозврата",
		"не запрещает **только** восстановительный item-done",
		"**никогда не\n   повторяй create автоматически**",
		"параллельный worker не\n   может выдать чужой claim за собственное владение",
		"refs/heads/pp-tail-dedupe/<dedupe-sha256>",
		"Только фактический `201 Created` этого **собственного вызова**",
		"**все** repository issues прямым пагинированным REST без\n   `since`",
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
		"Если валидный claim уже есть, recovery восстанавливает orphan, на который\nссылается **самый ранний claim**",
		"На PR это стоп по умолчанию, но есть два точных автоматических handoff:",
		"Успешный snapshot — точка невозврата",
		"Новый каноничный `pp:head-reviewed` после override поглощает его",
		"remote-ветка строго детерминирована как `fix/<N>`",
		"GitHub `POST /git/refs`",
		"`201` даёт branch-claim",
		"ложный успех `Everything up-to-date`",
		"Один raw GraphQL snapshot адресует оба комментария по\nnode ID",
		"`fullDatabaseId: BigInt`",
		"строка с REST comment id",
		"`hasNextPage` обязан быть false",
		"Watermark обязан оставаться в окне",
		"Последний переход `ship` среди событий\nвсех actors обязан быть `labeled` от `ivanarama`",
		"**PR, нужна доработка** → в комментарии с решением добавить отдельную",
		"строку `pp:fix-decision <текущий SHA>`",
		"**сначала поставить и проверить**\n     `changes-requested` и только после этого снять `needs-decision`",
		"**PR, код оставляем / повторяем аудит текущего SHA** → добавить отдельной",
		"`approved` на PR не ставьте",
		"Время мержа PR #1261 — точная\nграница включения committed-протокола",
		"само заключение**\nсоздано строго раньше границы",
		"Время мержа\nисходного PR не используется",
		"Initial `pp:tail-claim` с уникальным UUID воркера",
		"30-минутная цепочка\n`pp:tail-lease previous=<comment id>`",
		"постоянный `pp:tail-create-intent`",
		"version-key из `review comment id+updated_at`",
		"create-only\nref `pp-tail-dedupe/<sha256>`",
		"детерминированный `pp:tail-source`",
		"без eventually-consistent Search API",
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
		"последний переход метки `ship` среди событий всех actors обязан быть trusted `labeled`",
		"FIX до CAS-push перед каждым внешним изменением перечитывает HEAD, все comments и labels",
		"пересчитывает владельца",
		"детерминированную remote-ветку `fix/<N>` через GitHub `POST /git/refs`",
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

type orderedClaim struct {
	sequence int
	orphan   string
}

func recoveryTarget(orphans []orderedClaim, claims []orderedClaim) string {
	if len(claims) > 0 {
		winner := claims[0]
		for _, claim := range claims[1:] {
			if claim.sequence < winner.sequence {
				winner = claim
			}
		}
		return winner.orphan
	}
	winner := orphans[0]
	for _, orphan := range orphans[1:] {
		if orphan.sequence < winner.sequence {
			winner = orphan
		}
	}
	return winner.orphan
}

func TestReviewRecoveryInterleavingsFollowClaimOwner(t *testing.T) {
	tests := []struct {
		name    string
		orphans []orderedClaim
		claims  []orderedClaim
		want    string
	}{
		{name: "no claims chooses earliest orphan", orphans: []orderedClaim{{1, "R1"}, {2, "R2"}}, want: "R1"},
		{name: "later orphan with earlier claim owns recovery", orphans: []orderedClaim{{1, "R1"}, {2, "R2"}}, claims: []orderedClaim{{3, "R2"}, {4, "R1"}}, want: "R2"},
		{name: "claim order wins over orphan order", orphans: []orderedClaim{{1, "R1"}, {2, "R2"}}, claims: []orderedClaim{{5, "R1"}, {4, "R2"}}, want: "R2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := recoveryTarget(tt.orphans, tt.claims); got != tt.want {
				t.Fatalf("recoveryTarget() = %q, want %q", got, tt.want)
			}
		})
	}
}

type shipTransition struct {
	sequence int
	actor    string
	labeled  bool
}

func shipGate(transitions []shipTransition, proofAfter int) bool {
	if len(transitions) == 0 {
		return false
	}
	last := transitions[0]
	for _, transition := range transitions[1:] {
		if transition.sequence > last.sequence {
			last = transition
		}
	}
	return last.labeled && last.actor == "ivanarama" && last.sequence > proofAfter
}

func TestMergeShipTransitionInterleavings(t *testing.T) {
	tests := []struct {
		name        string
		transitions []shipTransition
		want        bool
	}{
		{name: "trusted current approval", transitions: []shipTransition{{20, "ivanarama", true}}, want: true},
		{name: "trusted removal cancels approval", transitions: []shipTransition{{20, "ivanarama", true}, {21, "ivanarama", false}}},
		{name: "untrusted re-add cannot revive approval", transitions: []shipTransition{{20, "ivanarama", true}, {21, "ivanarama", false}, {22, "app", true}}},
		{name: "approval before edited proof is stale", transitions: []shipTransition{{9, "ivanarama", true}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shipGate(tt.transitions, 10); got != tt.want {
				t.Fatalf("shipGate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func latestOwner(transitions []string) string {
	if len(transitions) == 0 {
		return ""
	}
	return transitions[len(transitions)-1]
}

func TestFixMustStillOwnHeadImmediatelyBeforeMutation(t *testing.T) {
	tests := []struct {
		name        string
		transitions []string
		mayPush     bool
	}{
		{name: "unchanged completion remains FIX", transitions: []string{"FIX"}, mayPush: true},
		{name: "review-again transfers to REVIEW", transitions: []string{"FIX", "REVIEW"}},
		{name: "new positive completion transfers away", transitions: []string{"FIX", "WAIT_SHIP"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := latestOwner(tt.transitions) == "FIX"; got != tt.mayPush {
				t.Fatalf("mayPush = %v, want %v", got, tt.mayPush)
			}
		})
	}
}

func TestLegacyTailCutoffUsesReviewTimeNotMergeTime(t *testing.T) {
	const cutoff = 100
	tests := []struct {
		name          string
		reviewCreated int
		prMerged      int
		want          bool
	}{
		{name: "in-flight legacy merged after cutover survives", reviewCreated: 99, prMerged: 101, want: true},
		{name: "new review at cutover has no fallback", reviewCreated: 100, prMerged: 101},
		{name: "new review after cutover has no fallback", reviewCreated: 101, prMerged: 102},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.reviewCreated < cutoff
			if got != tt.want {
				t.Fatalf("legacy fallback = %v, want %v (merged=%d)", got, tt.want, tt.prMerged)
			}
		})
	}
}

func acquireCreateOnlyBranch(remoteExists *bool) bool {
	if *remoteExists {
		return false
	}
	*remoteExists = true
	return true
}

func TestTwoFixWorkersCannotBothAcquireIssueBranch(t *testing.T) {
	remoteExists := false
	first := acquireCreateOnlyBranch(&remoteExists)
	second := acquireCreateOnlyBranch(&remoteExists)
	if !first || second {
		t.Fatalf("create-only CAS winners = first:%v second:%v, want true/false", first, second)
	}
}

func tailNeedsCreate(exactSourceIssueExists, itemDone bool) bool {
	return !exactSourceIssueExists && !itemDone
}

func TestTailCrashAfterIssueCreateDoesNotCreateDuplicate(t *testing.T) {
	if tailNeedsCreate(true, false) {
		t.Fatal("exact source issue must be recovered into item-done, not created again")
	}
	if tailNeedsCreate(false, true) {
		t.Fatal("completed item must not be created again")
	}
	if !tailNeedsCreate(false, false) {
		t.Fatal("unclaimed source without completion still needs create")
	}
}

type modeledTailLease struct {
	id       int
	previous int
	owner    string
	created  int
}

func activeModeledTailLease(transitions []modeledTailLease) (modeledTailLease, bool) {
	var active modeledTailLease
	found := false
	for _, transition := range transitions {
		if transition.previous != 0 {
			continue
		}
		if !found || transition.created < active.created ||
			(transition.created == active.created && transition.id < active.id) {
			active, found = transition, true
		}
	}
	if !found {
		return modeledTailLease{}, false
	}

	for {
		var child modeledTailLease
		childFound := false
		for _, transition := range transitions {
			if transition.previous != active.id {
				continue
			}
			isRenewal := transition.owner == active.owner
			isTakeover := transition.created >= active.created+30
			if !isRenewal && !isTakeover {
				continue
			}
			if !childFound || transition.created < child.created ||
				(transition.created == child.created && transition.id < child.id) {
				child, childFound = transition, true
			}
		}
		if !childFound {
			return active, true
		}
		active = child
	}
}

func modeledTailWorkerOwns(active modeledTailLease, ownID int, ownOwner string, now int) bool {
	return active.id == ownID && active.owner == ownOwner && now < active.created+30
}

func TestTailLeaseInterleavingsHaveOneCreateOwner(t *testing.T) {
	t.Run("simultaneous initial claims", func(t *testing.T) {
		active, ok := activeModeledTailLease([]modeledTailLease{
			{id: 11, owner: "worker-a", created: 1},
			{id: 12, owner: "worker-b", created: 1},
		})
		if !ok || !modeledTailWorkerOwns(active, 11, "worker-a", 2) {
			t.Fatalf("first returned comment must own lease: %#v", active)
		}
		if modeledTailWorkerOwns(active, 12, "worker-b", 2) {
			t.Fatal("loser must not turn the observed earliest claim into its ownership")
		}
	})

	t.Run("earliest takeover after expiry", func(t *testing.T) {
		active, _ := activeModeledTailLease([]modeledTailLease{
			{id: 20, owner: "crashed", created: 0},
			{id: 21, previous: 20, owner: "worker-b", created: 31},
			{id: 22, previous: 20, owner: "worker-c", created: 31},
		})
		if !modeledTailWorkerOwns(active, 21, "worker-b", 32) {
			t.Fatalf("earliest takeover must be sole owner: %#v", active)
		}
		if modeledTailWorkerOwns(active, 22, "worker-c", 32) {
			t.Fatal("competing takeover must lose")
		}
	})

	t.Run("renewal fences stale takeover", func(t *testing.T) {
		active, _ := activeModeledTailLease([]modeledTailLease{
			{id: 30, owner: "worker-a", created: 0},
			{id: 31, previous: 30, owner: "worker-a", created: 25},
			{id: 32, previous: 30, owner: "worker-b", created: 31},
		})
		if !modeledTailWorkerOwns(active, 31, "worker-a", 32) {
			t.Fatalf("renewal must move active lineage and fence stale child: %#v", active)
		}
	})
}

func modeledTailMayCreate(active modeledTailLease, ownLeaseID int, ownOwner string, canonicalIntentID, ownIntentID int, now int) bool {
	return modeledTailWorkerOwns(active, ownLeaseID, ownOwner, now) &&
		canonicalIntentID == ownIntentID && ownIntentID != 0
}

func TestTailCreateIntentIsPermanentFence(t *testing.T) {
	active := modeledTailLease{id: 41, owner: "worker-a", created: 10}
	if !modeledTailMayCreate(active, 41, "worker-a", 50, 50, 11) {
		t.Fatal("the process owning both active lease and returned canonical intent may create once")
	}
	if modeledTailMayCreate(active, 41, "worker-a", 50, 51, 11) {
		t.Fatal("a process must not reuse an observed intent that was not returned by its POST")
	}
	if modeledTailMayCreate(active, 42, "worker-b", 50, 0, 45) {
		t.Fatal("a later worker must not create after unresolved intent, even after the old lease expired")
	}
}

type modeledFixPostPush struct {
	transitionValid bool
	routeLabel      bool
	currentReview   bool
}

func (state modeledFixPostPush) reviewMayStart() bool {
	return !state.transitionValid || !state.routeLabel || state.currentReview
}

func (state modeledFixPostPush) fixerMayDeleteRoute() bool {
	return state.transitionValid && state.routeLabel && !state.currentReview
}

func TestFixPostPushTransitionSerializesReviewHandoff(t *testing.T) {
	pushed := modeledFixPostPush{transitionValid: true, routeLabel: true}
	if pushed.reviewMayStart() {
		t.Fatal("REVIEW must not enter while the atomic FIX post-push phase is open")
	}
	if !pushed.fixerMayDeleteRoute() {
		t.Fatal("winner or recovery must be able to finalize the pushed transition")
	}

	withInFlightReview := pushed
	withInFlightReview.currentReview = true
	if withInFlightReview.fixerMayDeleteRoute() {
		t.Fatal("FIX must not delete a route label after a current-HEAD review transition appears")
	}

	finalized := pushed
	finalized.routeLabel = false
	if !finalized.reviewMayStart() {
		t.Fatal("confirmed label removal must hand the new HEAD to REVIEW")
	}
}

type modeledIssueDecision struct {
	open        bool
	titleBody   string
	eligibleBy  string
	decision    string
	hold        bool
	manual      bool
	allowInWork bool
}

func modeledIssueGate(expected, current modeledIssueDecision) bool {
	return current.open && !current.hold && !current.manual &&
		expected.titleBody == current.titleBody &&
		expected.eligibleBy == current.eligibleBy &&
		expected.decision == current.decision &&
		expected.allowInWork == current.allowInWork
}

func TestNewIssueMutationsRevalidateDecisionFingerprint(t *testing.T) {
	expected := modeledIssueDecision{
		open: true, titleBody: "title\x00body", eligibleBy: "approved",
		decision: "comment:42@v1:sha256-a",
	}
	if !modeledIssueGate(expected, expected) {
		t.Fatal("unchanged issue decision must remain eligible")
	}

	tests := []struct {
		name   string
		mutate func(*modeledIssueDecision)
	}{
		{name: "late hold", mutate: func(state *modeledIssueDecision) { state.hold = true }},
		{name: "closed issue", mutate: func(state *modeledIssueDecision) { state.open = false }},
		{name: "eligibility removed", mutate: func(state *modeledIssueDecision) { state.eligibleBy = "" }},
		{name: "decision edited", mutate: func(state *modeledIssueDecision) { state.decision = "comment:42@v2:sha256-b" }},
		{name: "decision label changed", mutate: func(state *modeledIssueDecision) { state.decision = "decision:3" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current := expected
			tt.mutate(&current)
			if modeledIssueGate(expected, current) {
				t.Fatal("late issue change must close every subsequent mutation gate")
			}
		})
	}
}

type modeledTailVersionKey struct {
	reviewID      int
	reviewUpdated string
	item          int
	itemSHA256    string
	dedupeSHA256  string
}

func modeledTailCompletionApplies(current, completed modeledTailVersionKey) bool {
	return current == completed
}

func TestTailCompletionIsBoundToEditableCommentVersion(t *testing.T) {
	completed := modeledTailVersionKey{
		reviewID: 42, reviewUpdated: "2026-08-30T10:00:00Z", item: 1,
		itemSHA256: "item-a", dedupeSHA256: "task-a",
	}
	if !modeledTailCompletionApplies(completed, completed) {
		t.Fatal("exact versioned completion must apply")
	}

	edited := completed
	edited.reviewUpdated = "2026-08-30T10:01:00Z"
	if modeledTailCompletionApplies(edited, completed) {
		t.Fatal("edited review comment must invalidate old completion")
	}

	reordered := completed
	reordered.item = 2
	if modeledTailCompletionApplies(reordered, completed) {
		t.Fatal("reordered item must not be closed by its old item number")
	}

	rewritten := completed
	rewritten.itemSHA256 = "item-b"
	if modeledTailCompletionApplies(rewritten, completed) {
		t.Fatal("rewritten item text must not reuse old completion")
	}
}

func TestSemanticTailSourcesShareOneGlobalCreateClaim(t *testing.T) {
	globalRefExists := false
	firstSource := acquireCreateOnlyBranch(&globalRefExists)
	secondSource := acquireCreateOnlyBranch(&globalRefExists)
	if !firstSource || secondSource {
		t.Fatalf("global semantic claim winners = first:%v second:%v, want true/false", firstSource, secondSource)
	}
}
