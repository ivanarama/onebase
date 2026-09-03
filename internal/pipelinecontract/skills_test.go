package pipelinecontract

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
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
	entry := repositoryFile(t, ".claude", "skills", name, "SKILL.md")
	legacyPath := filepath.Join(".claude", "skills", name, "references", "legacy-protocol.md")
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	if data, err := os.ReadFile(filepath.Join(root, legacyPath)); err == nil {
		return entry + "\n" + string(data)
	}
	return entry
}

func TestReviewAndMergeRouteThroughPipelinectlWithDiscoverableFallback(t *testing.T) {
	for _, name := range []string{"review-queue", "merge-shepherd"} {
		entry := repositoryFile(t, ".claude", "skills", name, "SKILL.md")
		requireAll(t, entry, "promptpilot.project_pipeline", "pipelinectl.json",
			"references/legacy-protocol.md", "action", "fallback")
	}
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

func TestEveryMutatingSkillFailsClosedOnWindowsEncodingDamage(t *testing.T) {
	for _, name := range []string{"triage-issues", "fix-approved", "review-queue", "merge-shepherd", "tail-issues"} {
		t.Run(name, func(t *testing.T) {
			requireAll(t, skill(t, name),
				"**до чтения любого файла**",
				"$OutputEncoding = $utf8",
				"Get-Content -LiteralPath <path> -Encoding UTF8 -Raw",
				"остановись **до любой GitHub-мутации**",
				"jq `@base64`",
				"сравни байт-в-байт с отправленным телом",
			)
		})
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
		"baseRefName:.base.ref",
		"baseRefName == \"main\"",
		"Объедини все страницы, оставь только `state == \"open\"` и\n   `baseRefName == \"main\"`",
		"--jq '{sha:.head.sha,state,baseRefName:.base.ref,labels:[.labels[].name]}'",
		"gh api --paginate",
		"comments?per_page=100",
	)
	rejectAll(t, review, "number,title,labels,isDraft,comments", "gh pr list --state open --limit 50")
}

func TestReviewQueueUsesTwoLaneExecutableAllowlist(t *testing.T) {
	review := skill(t, "review-queue")
	requireAllCompact(t, review,
		"Исполняемый preflight — единственный источник списка кандидатов",
		"Get-Command gh -ErrorAction SilentlyContinue",
		"C:\\Program Files\\GitHub CLI\\gh.exe",
		"GitHub CLI not found in PATH or the standard Windows location",
		"Get-Command go -ErrorAction SilentlyContinue",
		"C:\\Program Files\\Go\\bin\\go.exe",
		"Go not found in PATH or the standard Windows location",
		"& $goExe run ./tools/pipelinehealth -json",
		"`review_candidates` — **исключительный allowlist этого запуска**",
		"`single_flight_barrier` защищает только интеграционную полосу, а не всю очередь",
		"обычное содержательное REVIEW не блокируется",
		"Следующий интеграционный PR при этом брать нельзя",
		"бери до двух элементов stage `review` из `review_candidates`",
		"Непосредственно перед первой мутацией каждого выбранного PR повтори `pipelinehealth -json`",
		"Расхождение означает стоп без подстановки следующего PR",
	)
}

func TestIntegrationReviewReusesContentProofAndChecksOnlyBaseSyncDelta(t *testing.T) {
	review := skill(t, "review-queue")
	requireAllCompact(t, review,
		"Интеграционное REVIEW не повторяет содержательный аудит",
		"Валидный исходный committed-proof уже доказывает содержимое `from`",
		"Проверь только точную дельту перехода `from → to`",
		"разрешение конфликтов",
		"обязательные проверки CI",
		"Если между доказанным `from` и `to` есть что-либо кроме валидного base-sync либо собственный код PR изменён, carry недействителен",
	)
}

func TestReviewQueueUsesPriorityThenBreadthFirstAndAging(t *testing.T) {
	review := skill(t, "review-queue")
	requireAllCompact(t, review,
		"Не сортируй очередь только по номеру PR",
		"планировочный `review-depth`",
		"число уникальных числовых `review-comment`",
		"updated_at == created_at",
		"claim-less legacy markers не считай",
		"Это только безопасный приоритет планирования, а не proof для мутации",
		"manual `queue:p0`…`queue:p3` старше",
		"За каждые полные 168 часов с `created_at` подними на один уровень вплоть до P1",
		"(priority ASC, review-depth ASC, number ASC)",
		"Single-flight/recovery всё равно старше priority",
	)
	rejectAll(t, review, "Просматривай PR по возрастанию номера")
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
		headMarker+" <полный проверенный SHA> review-comment=<id заключения> claim=<id claim-комментария> epoch-sha256=<64hex> -->",
	)
	rejectAll(t, review, "pp:review-head")
}

func TestPRPipelineBindsEveryStageToMainBaseAndFencesBaseABA(t *testing.T) {
	for name, text := range map[string]string{
		"review": skill(t, "review-queue"),
		"fix":    skill(t, "fix-approved"),
		"merge":  skill(t, "merge-shepherd"),
		"tail":   skill(t, "tail-issues"),
	} {
		t.Run(name, func(t *testing.T) {
			requireAllCompact(t, text,
				"baseRefName",
				"main",
				"state",
				"BaseRefChangedEvent",
				"BaseRefForcePushedEvent",
				"BaseRefDeletedEvent",
			)
		})
	}
	review := skill(t, "review-queue")
	requireAll(t, review,
		"BASE_REF_CHANGED_EVENT",
		"BASE_REF_FORCE_PUSHED_EVENT",
		"BASE_REF_DELETED_EVENT",
		"main → release → main",
		"state == OPEN",
	)
	merge := skill(t, "merge-shepherd")
	requireAll(t, merge,
		"headRefOid baseRefOid baseRefName state labels(first:100)",
		"`baseRefName == \"main\"`",
		"`state == OPEN`",
	)
	tail := skill(t, "tail-issues")
	requireAll(t, tail,
		"gh pr list --state merged --base main",
		"--json number,title,baseRefName,mergedAt,labels,url",
		"`state == MERGED`",
		"mergedAt",
	)
}

func TestReviewDecisionTableCoversBehavioralScenarios(t *testing.T) {
	review := skill(t, "review-queue")
	cases := []string{
		"есть `hold` | пропустить",
		"есть `ship`, текущий HEAD ещё ни разу не проходил REVIEW | обычное содержательное REVIEW; при успехе `ship` сохраняется и второй клик не нужен",
		"есть `ship`, HEAD сменился после прежнего proof, но нет валидного carry/re-ship | пропустить как stale authorization",
		"есть `ship`, текущий HEAD равен `to` валидной carry-цепочки и ещё не имеет committed-пары | единственное интеграционное REVIEW запуска; после committed-пары закончить весь этап",
		"есть каноничный committed-маркер и `changes-requested` / `needs-decision`, более позднего override нет | пропустить",
		"после committed-пары есть непоглощённый override при `changes-requested` / `needs-decision` | REVIEW продолжает",
		"есть `changes-requested` без committed-маркера текущего SHA | FIX безопасно снимет",
		"текущий SHA уже отмечен, более позднего override нет | пропустить; лимит 2 не расходовать",
		"текущий SHA отмечен, позже есть доверенный override | ревьюить один раз",
		"текущий SHA не отмечен, в том числе при старой `reviewed` | не удалять общую метку; ревьюить",
		"override требует повтор, при этом осталась `reviewed` | не удалять общую метку; ревьюить",
		"marker/override написал не `ivanarama` | игнорировать событие",
		"комментариев больше 100 | прочитать все страницы REST",
		"первые обычные PR пропущены по маркеру | продолжать обычный список до 2 реальных аудитов",
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
		"Непосредственно перед **каждым внешним изменением** заново прочитай `.head.sha`, `.state`, `.base.ref`, актуальные метки и **все** комментарии",
		"`ship` не запрещает REVIEW того же HEAD",
		"`hold` всегда запрещает изменение",
		"<!-- pp:stale-review <проверенный SHA> -->",
		"после постановки",
		"**не удаляй общую метку**",
		"у GitHub-метки нет владельца",
		"review-комментарий → claim → подтверждённая итоговая метка → committed-маркер",
		"следующий прогон продолжит её по orphan-комментарию",
		"Круги считай только по таким committed-парам",
		"каждый уникальный `review-comment id` учитывай не больше одного раза",
		"Незавершённый review-комментарий\n   остаётся диагностикой попытки, но круг не увеличивает",
		"`review-comment` и `claim` указывают\n   на существующие более ранние не редактированные доверенные комментарии",
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
		"<!-- pp:review-claim <40-символьный SHA> review-comment=<числовой id> epoch-sha256=<64hex> -->",
		"Stale `reviewed` от прошлой эпохи или старого HEAD конфликтом не считается",
		"PP-Fix-Transition: from=<SHA> review-comment=<id> claim=<id>\n   epoch-sha256=<64hex>",
		"мяч у FIX/recovery",
		"также всегда запрещает REVIEW-мутацию",
		"Опасный необъяснимый конфликт — `needs-decision`",
		"активные блокирующие `changes-requested` + `needs-decision`",
		"{id,node_id,created_at,updated_at,author:.user.login,body}",
		"timelineItems(first:100,after:$cursor,itemTypes:",
		"[PULL_REQUEST_COMMIT,HEAD_REF_FORCE_PUSHED_EVENT,HEAD_REF_DELETED_EVENT,\n   HEAD_REF_RESTORED_EVENT,BASE_REF_CHANGED_EVENT,BASE_REF_FORCE_PUSHED_EVENT,\n   BASE_REF_DELETED_EVENT,MERGED_EVENT,ISSUE_COMMENT,\n   COMMENT_DELETED_EVENT,LABELED_EVENT,UNLABELED_EVENT]",
		"этот точный набор `itemTypes` используют и потребители proof",
		"`lastEditedAt != null`",
		"`timelineItems.updatedAt`",
		"**два полных последовательных прохода**",
		"всей упорядоченной\n   последовательности `(edge cursor, __typename, все выбранные поля node)`",
		"`updatedAt` имеет секундную точность",
		"Epoch — edges **строго после** выбранного anchor",
		"`H → deleted → restored H` не оживляет старый proof",
		"Git author/committer dates\n   вообще не участвуют",
		"`epoch-sha256` — SHA-256 ASCII/LF записи",
		"same-second edit/delete earliest claim не воскрешает\n   stale sibling",
		"Edit/delete в окне после последнего pre-POST gate",
	)
	requireCompactInOrder(t, review,
		"После публикации заключения перечитай HEAD",
		"опубликуй claim с SHA и id заключения",
		"только самый ранний валидный claim текущей эпохи вправе поставить",
		"ожидаемую `Outcome-Label`",
		"опубликуй **отдельный committed-комментарий**",
	)
}

func TestReviewProofIsClaimBoundAndRevalidatedByEveryConsumer(t *testing.T) {
	review := skill(t, "review-queue")
	fixer := skill(t, "fix-approved")
	merge := skill(t, "merge-shepherd")
	tail := skill(t, "tail-issues")
	requireAllCompact(t, review,
		"claim=<id claim-комментария> epoch-sha256=<64hex>",
		"`lastEditedAt == null`",
		"не принимается ни REVIEW, ни\n   FIX/MERGE/TAIL",
	)
	requireAllCompact(t, fixer,
		"claim-bound proof",
		"два полных\n   идентичных прохода пагинированного timeline",
		"`IssueComment.lastEditedAt`",
		"`COMMENT_DELETED_EVENT`",
		"Claim-less legacy completion",
	)
	requireAllCompact(t, merge,
		"claim=<числовой id> epoch-sha256=<64hex>",
		"`lastEditedAt != null`",
		"claim-less legacy completion",
		"claim:node(id:$claimNode)",
		"двумя полными идентичными проходами по ordered edges и node payload",
	)
	requireAllCompact(t, tail,
		"gh pr list --state merged --base main",
		"--json number,title,baseRefName,mergedAt,labels,url",
		"локально требуй `baseRefName == \"main\"`",
		"REST state — `closed`, `mergedAt` — непустым",
		"GraphQL-прохода обязаны возвращать `state == MERGED`",
		"claim=<id>\n     epoch-sha256=<64hex>",
		"`lastEditedAt == null`",
		"`COMMENT_DELETED_EVENT`",
		"claim-less completion",
		"двумя полными идентичными\n   проходами по ordered edges и node payload",
	)
}

func TestFixerSelectsExactPaginatedReviewConclusion(t *testing.T) {
	fixer := skill(t, "fix-approved")
	requireAll(t, fixer,
		"FIX не должен пушить в эту ветку\n   одновременно с MERGE",
		"получи **все** открытые PR пагинированным REST",
		"gh api --paginate \"repos/ivanarama/onebase/pulls?state=open&per_page=100\"",
		"baseRefName:.base.ref",
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
		"`<!-- pp:head-reviewed <SHA> review-comment=<id> claim=<id>\n     epoch-sha256=<64hex> -->`",
	)
	requireAllCompact(t, fixer,
		"Затем оставь только `state == \"open\"`, `baseRefName == \"main\"` и исключи `ship` и `hold`",
		"Успешный собственный push потребляет старое владение FIX",
		"PP-Fix-Transition: from=<SHA canonical completion> review-comment=<id заключения> claim=<id> epoch-sha256=<64hex>",
		"Claim-less legacy completion",
		"server-ordered GraphQL epoch",
		"`IssueComment.lastEditedAt`",
		"`COMMENT_DELETED_EVENT`",
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
		"**всегда** входят две независимые части",
		"точная версия каноничного triage-комментария",
		"Голая\n   метка `decision:N` не фиксирует смысл номера",
		"каноничен самый ранний по `created_at`,\n   затем по числовому `id`",
		"комментарий автора `ivanarama` с точной отдельной строкой",
		"перед **любой**\n   внешней мутацией issue",
		"при раннем переходе в п. 9",
		"`issue-handoff fingerprint`",
		"точный код причины\n   handoff",
		"pp-fix-issue-handoff-v1\n   issue=<decimal>\n   issue-updated=<RFC3339>",
		"triage-sha256=<64 lowercase hex>",
		"comments-sha256=<64 lowercase hex>",
		"events-watermark=<decimal|none>",
		"labels=<sorted comma-list|none>",
		"choice=<canonical ASCII choice>",
		"<!-- pp:fix-issue-handoff-claim fingerprint-sha256=<64hex> reason=<code> owner=<uuid> -->",
		"JSON, CRLF, BOM",
		"record и marker находятся в одном не редактированном комментарии",
		"pp-fix-comments-v1",
		"edit/delete любого старого комментария",
		"post-root `unlabeled` event",
		"поставлен заново: это новое решение человека",
		"<!-- pp:fix-issue-handoff-lease claim=<root-id> previous=<active-id> owner=<uuid> -->",
		"<!-- pp:fix-issue-handoff-question claim=<root-id> reason=<code> -->",
		"<!-- pp:fix-issue-handoff-done claim=<root-id> -->",
		"Root — начальная 30-минутная lease",
		"только процесс, чей **собственный возвращённый id** каноничен",
		"Crash восстанавливается takeover, два живых worker не ведут\n   handoff одновременно",
		"recovery-\n   очередь открытых issues с `needs-decision`",
		"crash после снятия `approved`/`ready-fix`",
		"перед **каждым внешним изменением** — как\n   до, так и после branch-claim",
		"Снятый `ready-fix`/`approved`, новый `hold`, закрытие issue, смена",
		"перед добавлением `in-work` и перед `pp:in-work`-комментарием",
		"issue допускается в FIX **только** при",
		"pp:triage-route-done claim=<canonical-root-id>",
		"Done обязан существовать **до**\n   создания persistent branch `fix/<N>`",
		"Canonical triage без route-claim — отдельный legacy fallback",
		"`approved` OR (`ready-fix` AND NOT `needs-decision`)",
		"`ready-fix + needs-decision` без `approved` — ход человека",
		"повторное выполнение predicate",
		"`equivalent diagnostic losers`",
		"Непосредственно перед каждым** renewal",
		"не блокирует обычную FIX-очередь",
		"два полных последовательных прохода server-ordered\n   GraphQL timeline",
		"timelineItems(first:100,after:$cursor,itemTypes:[ISSUE_COMMENT,COMMENT_DELETED_EVENT])",
		"Root, lease, question и done обязаны\n   существовать в GraphQL",
		"`lastEditedAt == null`",
		"навсегда закрывает handoff",
		"не может\n   переизбрать stale sibling",
		"Новый root не создавай",
		"даже если после edit в его body больше нет protocol marker",
		"Same-second delete также закрывает gate",
	)
	rejectAll(t, fixer,
		"ищи его по **префиксу**",
		"gh pr list --state open\n   --label <метка>",
		"gh pr list --state open --json number,title,body",
	)
}

func TestTriageAndFixShareDeterministicCanonicalCommentRule(t *testing.T) {
	triage := skill(t, "triage-issues")
	fixer := skill(t, "fix-approved")
	for name, text := range map[string]string{"triage": triage, "fix": fixer} {
		t.Run(name, func(t *testing.T) {
			requireAllCompact(t, text,
				"точной отдельной строкой",
				"`ivanarama`",
				"самый ранний по",
				"`created_at`",
				"числовому `id`",
			)
		})
	}
	requireAllCompact(t, triage,
		"если\n   собственный возвращённый id не каноничен, не ставь маршрутные метки",
		"`updated_at` входит в FIX-fingerprint",
		"пагинированным REST",
		"чужое или встроенное в текст упоминание не\n   блокирует triage",
		"**recovery-очередь**",
		"crash между\n   root-комментарием и labels обязан продолжить ту же транзакцию",
		"pp-triage-route-v1",
		"<!-- pp:triage-route-claim fingerprint-sha256=<64hex> owner=<uuid> -->",
		"<!-- pp:triage-route-lease claim=<root-id> fingerprint-sha256=<64hex> previous=<active-id> owner=<uuid> -->",
		"events-watermark=<decimal|none>",
		"<!-- pp:triage-route-labels claim=<root-id> fingerprint-sha256=<64hex> events-through=<decimal> labels-sha256=<64hex> -->",
		"<!-- pp:triage-route-done claim=<root-id> fingerprint-sha256=<64hex> -->",
		"author.login == ivanarama",
		"select(.pull_request == null)",
		"Перед **каждым внешним изменением** после root",
		"issue открыт; `hold` отсутствует",
		"human pre-add",
		"recovery никогда не возвращает снятую человеком `ready-fix`",
		"**одним** REST POST",
		"`equivalent diagnostic losers`",
		"**До каждого** renewal/takeover POST",
		"same-owner renewal",
		"takeover обязан использовать\n   новый случайный UUID",
		"**собственный возвращённый\n   root/lease id** равен active id",
		"локальный UUID равен active owner",
		"timelineItems(itemTypes:[COMMENT_DELETED_EVENT])",
		"pageInfo.hasNextPage == false",
		"после удаления winner проигравший\n   sibling не должен воскреснуть",
		"не считается одним из\n   пяти рабочих slots",
		"<!-- pp:triage-author-reply claim=<canonical-root-id> fingerprint-sha256=<точный-root-fingerprint> -->",
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
		"baseRefName:.base.ref",
		"Локально оставь только `state == \"open\"` и `baseRefName == \"main\"`",
		"30 припаркованных PR не должны скрыть 31-й допустимый",
		"исключи `hold` и `needs-decision`",
		"Перед **каждым внешним изменением PR**",
		"push разрешённого конфликта",
		"на каждом\n   опросе CI",
		"непосредственно перед compare-and-merge REST",
		"issues/<N> --jq '[.labels[].name]'",
		"`ship` присутствует",
		"`hold` и\n   `needs-decision` отсутствуют",
		"`<!-- pp:head-reviewed <текущий SHA> review-comment=<числовой id>\n   claim=<числовой id> epoch-sha256=<64hex> -->`",
		"earliest-claim комментарии server-ordered REVIEW epoch",
		"После completion не должно быть отдельной строки `pp:review-again`",
		"сними `ship` через REST",
		"комментарий является разрешённым завершающим шагом **этой же\n   транзакции**",
		"Никакие update/push/merge до\n   успешного SHA+authorization-гейта недопустимы",
		"После подтверждённого done метку `ship` **не\n     снимай**",
		"Подтверждённый push меняет HEAD",
		"Ждать CI и мержить новый SHA без\n     интеграционного REVIEW нельзя",
		"непосредственно перед мутацией ещё раз выполни полный label+SHA-гейт",
		"последний полный гейт",
		"timeline?per_page=100",
		"{id,node_id,created_at,updated_at,author:.user.login,body}",
		"**последний переход именно метки `ship`**",
		"строго по позиции server-ordered edge/cursor",
		"Он обязан быть `LabeledEvent` от `ivanarama`",
		"Никогда не сравнивай числовые REST ids комментариев и label events",
		"не оживает от повторной постановки другим actor",
		"**два последовательных одинаковых raw GraphQL-запроса**",
		"Принимай только побайтово одинаковые выбранные значения",
		"адресованный epoch anchor",
		"Вторая идентичная проверка этой пары — **точка невозврата**",
		"`node_id` review/claim/completion",
		"`fullDatabaseId`, автор, SHA, Outcome-Label",
		"32-битный диапазон GraphQL `databaseId`",
		"`fullDatabaseId: BigInt`",
		"сравнивай его строковое значение с REST id",
		"`labels.pageInfo.hasNextPage == false`",
		"**последний** ship-transition",
		"его edge расположен после anchor текущего HEAD",
		"Если ни одного ship-transition нет в epoch timeline",
		"после сохранённого anchor нет ни одного нового\n   `PullRequestCommit`/`HeadRefForcePushedEvent`/`HeadRefDeletedEvent`/\n   `HeadRefRestoredEvent`/`BaseRefChangedEvent`/`BaseRefForcePushedEvent`/\n   `BaseRefDeletedEvent`",
		"`H → X → H` текущий `headRefOid` снова равен проверенному SHA",
		"timelineItems(first:100,after:$epochCursor,itemTypes:[PULL_REQUEST_COMMIT,HEAD_REF_FORCE_PUSHED_EVENT,HEAD_REF_DELETED_EVENT,HEAD_REF_RESTORED_EVENT,BASE_REF_CHANGED_EVENT,BASE_REF_FORCE_PUSHED_EVENT,BASE_REF_DELETED_EVENT,MERGED_EVENT,ISSUE_COMMENT,COMMENT_DELETED_EVENT,LABELED_EVENT,UNLABELED_EVENT])",
		"... on PullRequestCommit{id commit{oid}}",
		"... on HeadRefForcePushedEvent{id createdAt afterCommit{oid}}",
		"... on HeadRefDeletedEvent{id createdAt}",
		"... on HeadRefRestoredEvent{id createdAt}",
		"... on BaseRefChangedEvent{id createdAt previousRefName currentRefName}",
		"... on BaseRefForcePushedEvent{id createdAt beforeCommit{oid} afterCommit{oid}}",
		"... on BaseRefDeletedEvent{id createdAt baseRefName}",
		"... on MergedEvent{id createdAt commit{oid}}",
		"`lastEditedAt == null`",
		"Предыдущий comment-watermark обязан присутствовать среди `comments(last:100)`",
		"требуется новый аудит/completion",
		"review:node(id:$reviewNode)",
		"claim:node(id:$claimNode)",
		"completion:node(id:$completionNode)",
		"epochAnchor:node(id:$epochAnchorNode)",
		"для override-\n   `IssueComment` также `lastEditedAt == null`",
		`{"merge_method":"merge","sha":"<проверенный SHA>"}`,
		"Успех — только ответ с `merged: true`",
		"`409` означает, что HEAD успел измениться",
		"`baseRefName == \"main\"`",
		"`state == OPEN`",
	)
	requireCompactInOrder(t, merge,
		"5. Мерж:",
		"два последовательных одинаковых raw GraphQL-запроса",
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
		"Если он равен `from`, это\n     validation/rate-limit отказ",
		"Если HEAD уже\n     другой, не объявляй гонку вслепую",
		"Валидна только первая completion-ссылка на данный review-comment",
		"между ними не должно быть `pp:review-again`",
		"Для одного SHA без разделяющего override канонична только самая ранняя валидная пара",
	)
}

func TestAutomaticBaseSyncCarriesHumanShipWithoutPingPong(t *testing.T) {
	review := skill(t, "review-queue")
	merge := skill(t, "merge-shepherd")
	requireAllCompact(t, merge,
		"<!-- pp:base-sync-intent from=<40hex> base=<40hex> review-comment=<id> claim=<id> completion=<id> ship-event=<GraphQL node id> previous=<done id|none> -->",
		"<!-- pp:base-sync-done intent=<id> from=<40hex> to=<40hex> base=<40hex> previous=<done id|none> ship-event=<GraphQL node id> -->",
		"самый ранний валидный intent",
		"ровно двух родителей в порядке `[from, base]`",
		"при `HEAD == from` повторяет CAS update",
		"метку `ship` **не снимай**",
		"без второго клика человека",
		"обычная stale-ship передача",
	)
	requireAllCompact(t, review,
		"До выбора восстанови single-flight-владельца **интеграционной полосы**",
		"Если владелец ещё ждёт интеграционное REVIEW, выбери только его: это единственный аудит запуска",
		"Пока MERGE не вольёт владельца, нельзя заранее проверять следующий интеграционный PR",
		"Содержательное REVIEW других PR в это время безопасно",
		"Intent без done — незавершённая транзакция MERGE, её REVIEW не захватывает",
		"commit `to` имеет ровно двух родителей в порядке `[from, base]`",
		"outcome `reviewed` сохраняет `ship`",
		"`changes-requested` требует снять `ship`",
		"второй клик не нужен",
	)
}

func TestLegacyBaseSyncCanBeExplicitlyReauthorizedWithoutPingPong(t *testing.T) {
	review := skill(t, "review-queue")
	merge := skill(t, "merge-shepherd")
	docs := repositoryFile(t, "docs", "maintenance-pipeline.md")
	requireAllCompact(t, review,
		"Legacy re-ship нужен только для веток, которые MERGE обновил до внедрения intent/done",
		"merge-коммит ровно с двумя parents `[from, base]`",
		"**последний** ship-transition — новый trusted `LabeledEvent` от `ivanarama`, расположенный уже после anchor `to`",
		"похожий merge message доказательством не считается",
		"Новый label является явным разрешением проверить и затем влить точный уже существующий `to`, но не наследуется следующим push",
	)
	requireAllCompact(t, merge,
		"Разрешены ровно четыре способа связать этот ship-transition с текущим proof",
		"legacy reauthorized",
		"текущий HEAD `to` — merge-коммит ровно с двумя parents `[from, base]`",
		"последний ship-transition — новый trusted `LabeledEvent` от `ivanarama` после anchor `to`",
		"Новый push после re-ship отменяет разрешение",
		"либо является доказанным legacy reauthorization после anchor `from`",
		"Перед обычной сортировкой примени глобальный single-flight-барьер",
		"Если он ждёт REVIEW, не меняй **ни один** PR и закончи весь MERGE",
		"Нельзя заранее обновлять или мержить следующий PR",
	)
	requireAllCompact(t, docs,
		"Для PR, которые пастух обновил до внедрения intent/done, есть переходный путь",
		"любой новый push отменяет re-ship",
		"следующий base-sync уже записывается новым протоколом",
		"Одновременно активен только один такой handoff",
		"следующий MERGE сначала доводит владельца барьера до слияния",
	)
}

func TestMalformedProtocolCarryCanBeExplicitlyReauthorized(t *testing.T) {
	review := skill(t, "review-queue")
	merge := skill(t, "merge-shepherd")
	docs := repositoryFile(t, "docs", "maintenance-pipeline.md")
	requireAllCompact(t, review,
		"Protocol-recovery re-ship — отдельный узкий путь",
		"исходный carry оказался невалиден",
		"после edge самого done",
		"не делает старый carry валидным",
		"начинает новую carry-цепочку с `previous=none`",
	)
	requireAllCompact(t, merge,
		"Разрешены ровно четыре способа",
		"**protocol-recovery reauthorized:**",
		"последний trusted `ship` от `ivanarama` после edge done",
		"Для protocol-recovery всегда начни новую исправленную цепочку с `previous=none`",
	)
	requireAllCompact(t, docs,
		"действительно испорченной цепочки человек ставит `ship` после edge done",
		"protocol-recovery reauthorization точного текущего HEAD",
		"следующий base-sync начинает новую цепочку с `previous=none`",
	)
}

func TestBaseTipComesFromAuthoritativeRefAndDriftIsVisible(t *testing.T) {
	review := skill(t, "review-queue")
	merge := skill(t, "merge-shepherd")
	docs := repositoryFile(t, "docs", "maintenance-pipeline.md")
	requireAllCompact(t, merge,
		"gh api repos/ivanarama/onebase/git/ref/heads/main --jq .object.sha",
		"`PullRequest.baseRefOid` не используй как tip `main`",
		"`done.base` всегда равен фактическому второму parent",
		"`base_sync_base_advanced`",
	)
	requireAllCompact(t, review,
		"`intent.base` — наблюдавшийся tip `refs/heads/main` перед update",
		"`done.base` — фактический второй parent",
		"`intent.base` является предком `done.base`",
	)
	requireAllCompact(t, docs,
		"Tip `main` для intent читается напрямую из `git/ref/heads/main`",
		"ancestry `intent.base → done.base → current main`",
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
		"именно это выбранное заключение и создано, и последний раз\n   обновлено строго раньше",
		"`created_at < cutover` и `updated_at < cutover`",
		"Время мержа самого исходного PR границей не является",
		"последнее заключение создано в момент границы или позже",
		"нет каноничной committed-пары для merged HEAD и не сработал описанный выше",
		"`pp:head-reviewed <SHA> review-comment=<id> claim=<id>\n     epoch-sha256=<64hex>`",
		"`lastEditedAt == null`",
		"после anchor нет\n     `COMMENT_DELETED_EVENT`",
		"в timeline ровно один `MergedEvent`",
		"`anchor < review < earliest claim < completion < MergedEvent`",
		"`BaseRefChangedEvent`/\n     `BaseRefForcePushedEvent`/`BaseRefDeletedEvent`",
		"После merge допустим только\n     ноль событий lifecycle либо один **конечный** `HeadRefDeletedEvent`",
		"любой `HeadRefRestoredEvent`, в том числе post-merge",
		"same-second merge/delete однозначен",
		"post-merge synthetic proof не проходит",
		"claim-less completion допустим только",
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
		"<!-- pp:tail-task-v1 title-sha256=<64hex> task-sha256=<64hex> dedupe-sha256=<64hex> -->",
		"<!-- pp:tail-item-done review-comment=<id> review-updated=<RFC3339> item=<N> item-sha256=<64hex> dedupe-sha256=<64hex> issue=<номер|none> -->",
		"<!-- pp:tail-dedupe sha256=<64hex> -->",
		"<!-- pp:tail-done review-comment=<id> review-updated=<RFC3339> -->",
		"pp:tail-drop review-comment=<id> review-updated=<RFC3339> item=<N> item-sha256=<64hex>",
		"Короткая legacy-форма `pp:tail-drop 1,3` разрешена только",
		"updated_at",
		"item-sha256",
		"dedupe-sha256",
		"каноничную task identity без source-specific полей",
		"byte-portable нормализацию `pp-text-v1`",
		"никаких\n   NFKC/NFC, casefold, locale lower-case или Unicode whitespace tables",
		"Никакого JSON и Unicode escaping в dedupe-входе нет",
		"pp-tail-task-v1\n   title-sha256=<64 lowercase hex>\n   task-sha256=<64 lowercase hex>",
		"включая последний LF",
		"одинаковые общие заголовки у\n   разных подсистем — разные задачи",
		"одного\n     совпавшего title недостаточно",
		"Перед **каждым внешним изменением**",
		"initial claim, renewal/takeover lease, create-intent,\n   создание dedupe-ref, issue create, item-done, общий tail-done",
		"заново реконструируй полный стабильный\n   server-ordered GraphQL REVIEW epoch из п. 2",
		"Edit/delete proof между выбором пункта\n   и любой pre-create мутацией означает ноль новых issue",
		"стабильный GraphQL proof-гейт из п. 5",
		"нового HEAD/lifecycle-anchor **до\n   `MergedEvent`**",
		"После merge-edge разрешён только\n   необязательный конечный head delete",
		"Не используй GitHub Search",
		"issues?state=all&since=<root-claim-created-at>&per_page=100",
		"автора `ivanarama`, точный `pp:tail-source`",
		"Source marker без полного согласованного\n   payload",
		"совпадение обоих component hashes",
		"{number,title,author:.user.login,body}",
		"если winner упал сразу после успешного создания ref, но\n   **до** публикации create-intent",
		"orphan `pp-tail-dedupe/<hash>` ref без найденной issue",
		"Создание exact-source issue — точка невозврата",
		"не запрещает **только** восстановительный item-done",
		"единственное исключение из повторного proof-гейта после точки невозврата",
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
		"Обычный PR с\n`ship` пропускается, но доказанный автоматический base-sync и узкий legacy\nre-ship после синхронизации старым протоколом — исключения",
		"`changes-requested`/`needs-decision` обычно передают мяч дальше",
		"маркером `pp:head-reviewed`",
		"отдельная строка\nкоторого равна `pp:review-again`",
		"без `hold`/`needs-decision`",
		"непосредственно перед merge",
		"push разрешённого конфликта",
		"`<!-- pp:head-reviewed <SHA> review-comment=<id заключения> claim=<id claim>\nepoch-sha256=<64hex> -->`",
		"Если сбой случился до committed-\nмаркера, сорванная попытка круг не увеличивает",
		"пастух обычно снимает\nустаревший `ship`",
		"`BEHIND` → перед `update-branch` создаётся неизменяемый\n  `pp:base-sync-intent`",
		"После `422` HEAD перечитывается",
		"полный label+SHA-гейт непосредственно перед единственным\n  перезапуском",
		"Валидна только первая completion-ссылка на\nэтот id",
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
		"persistent root\n`pp:fix-issue-handoff-claim`",
		"30-минутной lease, renewal/takeover сериализуют recovery",
		"`pp:fix-issue-handoff-done`",
		"GitHub `POST /git/refs`",
		"`201` даёт branch-claim",
		"ложный успех `Everything up-to-date`",
		"Два последовательных\nпобайтово одинаковых raw GraphQL snapshot",
		"адресуют три комментария по node ID",
		"`fullDatabaseId: BigInt`",
		"строка с REST comment id",
		"`hasNextPage` обязан быть false",
		"Watermark обязан оставаться в окне",
		"Последний переход `ship` среди\nсобытий всех actors обязан быть `labeled` от `ivanarama`",
		"Числовые REST ids комментариев и label events не\nсравниваются",
		"**PR, нужна доработка** → в комментарии с решением добавить отдельную",
		"строку `pp:fix-decision <текущий SHA>`",
		"**сначала поставить и проверить**\n     `changes-requested` и только после этого снять `needs-decision`",
		"**PR, код оставляем / повторяем аудит текущего SHA** → добавить отдельной",
		"`approved` на PR не ставьте",
		"Время мержа PR #1261 — точная\nграница включения committed-протокола",
		"само заключение** и\nсоздано, и последний раз обновлено строго раньше границы",
		"Время мержа\nисходного PR не используется",
		"Initial `pp:tail-claim` с уникальным UUID воркера",
		"30-минутная цепочка\n`pp:tail-lease previous=<comment id>`",
		"постоянный `pp:tail-create-intent`",
		"version-key из `review comment id+updated_at`",
		"create-only\nref `pp-tail-dedupe/<sha256>`",
		"Один title без task-текста никогда не\nсчитается ключом",
		"не от JSON,\nа от точной ASCII-записи",
		"детерминированный `pp:tail-source`",
		"без eventually-consistent Search API",
	)
	rejectAll(t, docs, "Непосредственно перед ним один raw GraphQL snapshot")
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
		"Последний переход метки `ship` среди событий всех actors обязан быть trusted `labeled`",
		"Автоматический merge с `main` переносит разрешение через неизменяемую пару",
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

type reviewEpochEvent struct {
	sequence   int
	wallSecond int
	deleted    bool
	trusted    bool
	edited     bool
}

func reviewEpochGate(anchorSequence int, events []reviewEpochEvent) bool {
	for _, event := range events {
		if event.sequence <= anchorSequence {
			continue
		}
		if event.deleted || (event.trusted && event.edited) {
			return false
		}
	}
	return true
}

func reviewClaimsAfter(claims []orderedClaim, epochStart int) []orderedClaim {
	var current []orderedClaim
	for _, claim := range claims {
		if claim.sequence > epochStart {
			current = append(current, claim)
		}
	}
	return current
}

func modeledReviewEpochAnchor(headEdge, overrideEdge, untrustedGitCommitTime int) int {
	_ = untrustedGitCommitTime
	if overrideEdge > headEdge {
		return overrideEdge
	}
	return headEdge
}

func modeledHeadLifecycleAnchor(lastCommitAnchor, lastDelete, lastRestore int, headPresent bool) (int, bool) {
	if !headPresent || lastDelete > lastRestore {
		return 0, false
	}
	if lastRestore > lastCommitAnchor {
		return lastRestore, true
	}
	return lastCommitAnchor, lastCommitAnchor != 0
}

type modeledReviewProof struct {
	reviewPresent       bool
	claimPresent        bool
	completionPresent   bool
	reviewEdited        bool
	claimEdited         bool
	completionEdited    bool
	deletionAfter       bool
	fieldsMatch         bool
	claimIsEarliest     bool
	anchorInvalid       bool
	baseRefMain         bool
	baseLifecycleAfter  bool
	lifecycleStateValid bool
}

func reviewProofAcceptedByConsumer(proof modeledReviewProof) bool {
	return proof.reviewPresent && proof.claimPresent && proof.completionPresent &&
		!proof.reviewEdited && !proof.claimEdited && !proof.completionEdited &&
		!proof.deletionAfter && proof.fieldsMatch && proof.claimIsEarliest &&
		!proof.anchorInvalid && proof.baseRefMain && !proof.baseLifecycleAfter &&
		proof.lifecycleStateValid
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

func TestReviewDeletedOrEditedWinnerCannotResurrectStaleClaim(t *testing.T) {
	orphans := []orderedClaim{{1, "R1-changes"}, {2, "R2-reviewed"}}
	claims := []orderedClaim{{10, "R1-changes"}, {11, "R2-reviewed"}}
	if got := recoveryTarget(orphans, claims); got != "R1-changes" {
		t.Fatalf("initial claim winner = %q, want R1-changes", got)
	}

	visibleAfterWinnerRemoval := []orderedClaim{{11, "R2-reviewed"}}
	if got := recoveryTarget(orphans, visibleAfterWinnerRemoval); got != "R2-reviewed" {
		t.Fatalf("current bodies alone should expose stale resurrection, got %q", got)
	}
	if reviewEpochGate(0, []reviewEpochEvent{{sequence: 12, deleted: true}}) {
		t.Fatal("deleting the winning claim in the current epoch must fail closed")
	}
	if reviewEpochGate(0, []reviewEpochEvent{{sequence: 10, trusted: true, edited: true}}) {
		t.Fatal("editing the winning claim marker away in the current epoch must fail closed")
	}
	if reviewEpochGate(9, []reviewEpochEvent{{sequence: 10, wallSecond: 5, trusted: true, edited: true}}) {
		t.Fatal("GraphQL lastEditedAt must catch an edit in the same wall-clock second")
	}
	if reviewEpochGate(9, []reviewEpochEvent{{sequence: 10, wallSecond: 5, deleted: true}}) {
		t.Fatal("server edge order must catch a deletion in the same wall-clock second")
	}

	const freshOverride = 13
	oldMutationEvents := []reviewEpochEvent{
		{sequence: 10, trusted: true, edited: true},
		{sequence: 12, deleted: true},
	}
	if !reviewEpochGate(freshOverride, oldMutationEvents) {
		t.Fatal("a fresh human override after the mutation must start a clean epoch")
	}
	if current := reviewClaimsAfter(claims, freshOverride); len(current) != 0 {
		t.Fatalf("stale sibling claims before the fresh epoch must be excluded, got %+v", current)
	}
}

type modeledTimelinePass struct {
	headRefOid  string
	baseRefName string
	state       string
	updatedAt   string
	edgeDigest  string
}

func stableReviewTimelineSnapshot(first, second modeledTimelinePass) bool {
	return first == second
}

func TestReviewPaginationRejectsSameSecondMixedSnapshot(t *testing.T) {
	first := modeledTimelinePass{headRefOid: "H", baseRefName: "main", state: "OPEN", updatedAt: "2026-08-31T07:56:51Z", edgeDigest: "page-set-before-edit"}
	second := modeledTimelinePass{headRefOid: "H", baseRefName: "main", state: "OPEN", updatedAt: "2026-08-31T07:56:51Z", edgeDigest: "page-set-after-edit"}
	if stableReviewTimelineSnapshot(first, second) {
		t.Fatal("equal HEAD and second-resolution updatedAt must not hide a changed ordered edge/node payload")
	}
	second.edgeDigest = first.edgeDigest
	if !stableReviewTimelineSnapshot(first, second) {
		t.Fatal("two complete identical ordered passes should form a stable snapshot")
	}
	second.baseRefName = "release"
	if stableReviewTimelineSnapshot(first, second) {
		t.Fatal("a target-branch change must invalidate otherwise identical review snapshots")
	}
	second.baseRefName = first.baseRefName
	second.state = "CLOSED"
	if stableReviewTimelineSnapshot(first, second) {
		t.Fatal("closing a PR must invalidate otherwise identical review snapshots")
	}
}

func TestReviewConsumersRejectInvalidatedClaimBoundProof(t *testing.T) {
	valid := modeledReviewProof{
		reviewPresent: true, claimPresent: true, completionPresent: true,
		fieldsMatch: true, claimIsEarliest: true, baseRefMain: true, lifecycleStateValid: true,
	}
	if !reviewProofAcceptedByConsumer(valid) {
		t.Fatal("an intact claim-bound proof must be consumable")
	}
	tests := map[string]func(*modeledReviewProof){
		"review edited after completion": func(p *modeledReviewProof) { p.reviewEdited = true },
		"claim edited after completion":  func(p *modeledReviewProof) { p.claimEdited = true },
		"completion edited":              func(p *modeledReviewProof) { p.completionEdited = true },
		"claim deleted in TOCTOU window": func(p *modeledReviewProof) { p.claimPresent = false },
		"deletion after anchor":          func(p *modeledReviewProof) { p.deletionAfter = true },
		"stale sibling claim":            func(p *modeledReviewProof) { p.claimIsEarliest = false },
		"epoch mismatch":                 func(p *modeledReviewProof) { p.fieldsMatch = false },
		"override anchor edited":         func(p *modeledReviewProof) { p.anchorInvalid = true },
		"target branch is not main":      func(p *modeledReviewProof) { p.baseRefMain = false },
		"base changed after proof":       func(p *modeledReviewProof) { p.baseLifecycleAfter = true },
		"PR lifecycle state changed":     func(p *modeledReviewProof) { p.lifecycleStateValid = false },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			proof := valid
			mutate(&proof)
			if reviewProofAcceptedByConsumer(proof) {
				t.Fatal("FIX/MERGE/TAIL must reject an invalidated review proof")
			}
		})
	}
}

func mergeFinalEpochStillCurrent(savedAnchor int, laterHeadAnchors []int) bool {
	for _, anchor := range laterHeadAnchors {
		if anchor > savedAnchor {
			return false
		}
	}
	return true
}

func TestMergeFinalSnapshotRejectsHeadABA(t *testing.T) {
	const savedHeadAnchor = 10
	if !mergeFinalEpochStillCurrent(savedHeadAnchor, nil) {
		t.Fatal("an unchanged server HEAD epoch must remain valid")
	}
	// The SHA can be H again after H -> X -> H, but both transitions create
	// server-ordered HEAD anchors after the proof's saved anchor.
	if mergeFinalEpochStillCurrent(savedHeadAnchor, []int{11, 12}) {
		t.Fatal("a final MERGE snapshot must reject an ABA HEAD transition even when headRefOid is H again")
	}
}

func TestHeadDeleteRestoreStartsNewReviewEpoch(t *testing.T) {
	if _, ok := modeledHeadLifecycleAnchor(10, 11, 0, false); ok {
		t.Fatal("a deleted HEAD without a later restore must close the review gate")
	}
	anchor, ok := modeledHeadLifecycleAnchor(10, 11, 12, true)
	if !ok || anchor != 12 {
		t.Fatalf("restoring the same HEAD must start a new server epoch at edge 12, got anchor=%d ok=%v", anchor, ok)
	}
	if mergeFinalEpochStillCurrent(10, []int{11, 12}) {
		t.Fatal("MERGE must reject the old proof after H -> deleted -> restored H")
	}
}

func tailProofCompatibleWithMerge(anchor, review, claim, completion int, mergedEdges, commitOrForce, deleted, restored []int) bool {
	if len(mergedEdges) != 1 {
		return false
	}
	mergedEdge := mergedEdges[0]
	if anchor >= review || review >= claim || claim >= completion || completion >= mergedEdge {
		return false
	}
	for _, edge := range commitOrForce {
		if edge > anchor {
			return false
		}
	}
	for _, edge := range restored {
		if edge > anchor {
			return false
		}
	}
	postAnchorDeletes := 0
	for _, edge := range deleted {
		if edge <= anchor {
			continue
		}
		postAnchorDeletes++
		if edge <= mergedEdge {
			return false
		}
	}
	return postAnchorDeletes <= 1
}

func TestTailAllowsRoutineHeadDeleteOnlyAfterMergedEdge(t *testing.T) {
	if !tailProofCompatibleWithMerge(10, 11, 12, 13, []int{20}, nil, []int{21}, nil) {
		t.Fatal("routine branch deletion strictly after merge must not permanently disable TAIL")
	}
	if tailProofCompatibleWithMerge(10, 11, 12, 13, []int{20}, nil, []int{19}, nil) {
		t.Fatal("a HEAD lifecycle transition between review proof and merge must invalidate TAIL")
	}
	if tailProofCompatibleWithMerge(10, 11, 12, 13, []int{20}, nil, []int{21}, []int{22}) {
		t.Fatal("restoring HEAD after merge must remain fail-closed")
	}
	if tailProofCompatibleWithMerge(10, 11, 12, 21, []int{20}, nil, nil, nil) {
		t.Fatal("a synthetic proof completed after merge must not be accepted")
	}
	if tailProofCompatibleWithMerge(10, 11, 12, 13, nil, nil, []int{21}, nil) ||
		tailProofCompatibleWithMerge(10, 11, 12, 13, []int{20, 22}, nil, []int{23}, nil) {
		t.Fatal("TAIL must require exactly one canonical MergedEvent boundary")
	}
}

func tailPreCreateMutationAllowed(proof modeledReviewProof) bool {
	return reviewProofAcceptedByConsumer(proof)
}

func TestTailRechecksReviewProofImmediatelyBeforeCreate(t *testing.T) {
	proof := modeledReviewProof{
		reviewPresent: true, claimPresent: true, completionPresent: true,
		fieldsMatch: true, claimIsEarliest: true, baseRefMain: true, lifecycleStateValid: true,
	}
	if !tailPreCreateMutationAllowed(proof) {
		t.Fatal("an intact proof should allow the pre-create transaction to continue")
	}

	proof.deletionAfter = true
	if tailPreCreateMutationAllowed(proof) {
		t.Fatal("deleting proof after selection but before issue create must stop TAIL with zero new issues")
	}

	proof.deletionAfter = false
	proof.claimEdited = true
	if tailPreCreateMutationAllowed(proof) {
		t.Fatal("editing proof after selection but before issue create must stop TAIL with zero new issues")
	}
}

func TestReviewEpochUsesServerAnchorNotFutureGitDate(t *testing.T) {
	const serverHeadAnchor = 10
	const forgedFutureGitTime = 2_114_380_800 // 2037-01-01.
	if got := modeledReviewEpochAnchor(serverHeadAnchor, 0, forgedFutureGitTime); got != serverHeadAnchor {
		t.Fatalf("epoch anchor = %d, want server timeline edge %d", got, serverHeadAnchor)
	}
	claims := []orderedClaim{{sequence: 11, orphan: "current"}}
	if current := reviewClaimsAfter(claims, serverHeadAnchor); len(current) != 1 {
		t.Fatalf("server-ordered claim after HEAD anchor must remain eligible, got %+v", current)
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

type baseSyncLink struct {
	id, from, to, base, previous string
	intentValid, doneValid       bool
	parents                      []string
	singleHeadEvent              bool
}

func carriedShipGate(current string, rootProofAfter, rootShipSequence int, transitions []shipTransition, links []baseSyncLink, currentProof bool) bool {
	if !currentProof || !shipGate(transitions, rootProofAfter) || len(transitions) == 0 || len(links) == 0 {
		return false
	}
	lastShip := transitions[0]
	for _, transition := range transitions[1:] {
		if transition.sequence > lastShip.sequence {
			lastShip = transition
		}
	}
	if lastShip.sequence != rootShipSequence {
		return false
	}
	previous, expectedFrom := "none", links[0].from
	for _, link := range links {
		if !link.intentValid || !link.doneValid || !link.singleHeadEvent || link.previous != previous ||
			link.from != expectedFrom || len(link.parents) != 2 || link.parents[0] != link.from || link.parents[1] != link.base {
			return false
		}
		previous, expectedFrom = link.id, link.to
	}
	return current == expectedFrom
}

func legacyReShipGate(current, to string, currentAnchor int, transitions []shipTransition, parents []string,
	oldProofAndShip, singleHeadEvent, baseAncestor, currentProof bool) bool {
	if current != to || !oldProofAndShip || !singleHeadEvent || !baseAncestor || !currentProof || len(parents) != 2 {
		return false
	}
	if len(transitions) == 0 {
		return false
	}
	latest := transitions[0]
	for _, transition := range transitions[1:] {
		if transition.sequence > latest.sequence {
			latest = transition
		}
	}
	return latest.sequence > currentAnchor && latest.actor == "ivanarama" && latest.labeled
}

func TestLegacyReShipRequiresExactMergeLineageAndNewHumanLabel(t *testing.T) {
	validTransitions := []shipTransition{{20, "ivanarama", true}, {40, "ivanarama", false}, {50, "ivanarama", true}}
	if !legacyReShipGate("H1", "H1", 45, validTransitions, []string{"H0", "B1"}, true, true, true, true) {
		t.Fatal("an exact legacy base-sync with a new human ship must be reauthorized")
	}

	tests := []struct {
		name                            string
		current, to                     string
		anchor                          int
		transitions                     []shipTransition
		parents                         []string
		oldProof, single, ancestor, now bool
	}{
		{name: "author push", current: "AUTHOR", to: "H1", anchor: 45, transitions: validTransitions, parents: []string{"H0", "B1"}, oldProof: true, single: true, ancestor: true, now: true},
		{name: "ship before current head", current: "H1", to: "H1", anchor: 55, transitions: validTransitions, parents: []string{"H0", "B1"}, oldProof: true, single: true, ancestor: true, now: true},
		{name: "one parent", current: "H1", to: "H1", anchor: 45, transitions: validTransitions, parents: []string{"H0"}, oldProof: true, single: true, ancestor: true, now: true},
		{name: "old head unreviewed", current: "H1", to: "H1", anchor: 45, transitions: validTransitions, parents: []string{"H0", "B1"}, single: true, ancestor: true, now: true},
		{name: "base outside main", current: "H1", to: "H1", anchor: 45, transitions: validTransitions, parents: []string{"H0", "B1"}, oldProof: true, single: true, now: true},
		{name: "extra head event", current: "H1", to: "H1", anchor: 45, transitions: validTransitions, parents: []string{"H0", "B1"}, oldProof: true, ancestor: true, now: true},
		{name: "latest transition removes ship", current: "H1", to: "H1", anchor: 45, transitions: append(validTransitions, shipTransition{60, "ivanarama", false}), parents: []string{"H0", "B1"}, oldProof: true, single: true, ancestor: true, now: true},
		{name: "foreign relabel", current: "H1", to: "H1", anchor: 45, transitions: []shipTransition{{50, "bot", true}}, parents: []string{"H0", "B1"}, oldProof: true, single: true, ancestor: true, now: true},
		{name: "integration review incomplete", current: "H1", to: "H1", anchor: 45, transitions: validTransitions, parents: []string{"H0", "B1"}, oldProof: true, single: true, ancestor: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if legacyReShipGate(tt.current, tt.to, tt.anchor, tt.transitions, tt.parents,
				tt.oldProof, tt.single, tt.ancestor, tt.now) {
				t.Fatal("unproven legacy head inherited ship")
			}
		})
	}
}

func TestCarriedShipAcceptsOnlyVerifiedBaseSyncChain(t *testing.T) {
	transitions := []shipTransition{{20, "ivanarama", true}}
	links := []baseSyncLink{
		{id: "31", from: "H0", to: "H1", base: "B1", previous: "none", intentValid: true, doneValid: true, parents: []string{"H0", "B1"}, singleHeadEvent: true},
		{id: "41", from: "H1", to: "H2", base: "B2", previous: "31", intentValid: true, doneValid: true, parents: []string{"H1", "B2"}, singleHeadEvent: true},
	}
	if !carriedShipGate("H2", 10, 20, transitions, links, true) {
		t.Fatal("a continuous reviewed base-sync chain must preserve the human ship")
	}

	tests := []struct {
		name        string
		current     string
		transitions []shipTransition
		mutate      func([]baseSyncLink)
		proof       bool
	}{
		{name: "author push after sync", current: "AUTHOR", transitions: transitions, proof: true},
		{name: "integration review not complete", current: "H2", transitions: transitions},
		{name: "ship was removed", current: "H2", transitions: []shipTransition{{20, "ivanarama", true}, {50, "ivanarama", false}}, proof: true},
		{name: "ship was re-added", current: "H2", transitions: []shipTransition{{20, "ivanarama", true}, {50, "ivanarama", false}, {51, "ivanarama", true}}, proof: true},
		{name: "wrong merge parent", current: "H2", transitions: transitions, proof: true, mutate: func(items []baseSyncLink) { items[1].parents[1] = "EVIL" }},
		{name: "broken previous link", current: "H2", transitions: transitions, proof: true, mutate: func(items []baseSyncLink) { items[1].previous = "other" }},
		{name: "extra head event", current: "H2", transitions: transitions, proof: true, mutate: func(items []baseSyncLink) { items[1].singleHeadEvent = false }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			copyLinks := make([]baseSyncLink, len(links))
			copy(copyLinks, links)
			for i := range copyLinks {
				copyLinks[i].parents = append([]string(nil), links[i].parents...)
			}
			if tt.mutate != nil {
				tt.mutate(copyLinks)
			}
			if carriedShipGate(tt.current, 10, 20, tt.transitions, copyLinks, tt.proof) {
				t.Fatal("unverified head inherited human ship")
			}
		})
	}
}

func shipAfterCompletionByServerEdges(shipEdge, completionEdge, shipRESTID, completionRESTID int) bool {
	_ = shipRESTID
	_ = completionRESTID
	return shipEdge > completionEdge
}

func TestMergeDoesNotOrderCrossTypeEventsByRESTID(t *testing.T) {
	// GitHub REST ids for label events and comments come from different tables.
	// The numerically larger label id must not make an earlier same-second label
	// appear after the completion edge.
	if shipAfterCompletionByServerEdges(9, 10, 30_249_148_727, 5_471_832_206) {
		t.Fatal("a ship edge before completion must remain stale regardless of its larger cross-table REST id")
	}
	if !shipAfterCompletionByServerEdges(11, 10, 1, 9_999_999_999) {
		t.Fatal("server edge order must accept a later ship even when its REST id is numerically smaller")
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

type modeledTriageComment struct {
	id        int
	createdAt string
	trusted   bool
	exactMark bool
}

func modeledCanonicalTriage(comments []modeledTriageComment) (modeledTriageComment, bool) {
	var best modeledTriageComment
	found := false
	for _, comment := range comments {
		if !comment.trusted || !comment.exactMark {
			continue
		}
		if !found || comment.createdAt < best.createdAt ||
			(comment.createdAt == best.createdAt && comment.id < best.id) {
			best = comment
			found = true
		}
	}
	return best, found
}

func TestConcurrentTriageHasOneDeterministicCanonicalComment(t *testing.T) {
	first := modeledTriageComment{id: 10, createdAt: "2026-08-30T10:00:00Z", trusted: true, exactMark: true}
	second := modeledTriageComment{id: 11, createdAt: "2026-08-30T10:00:00Z", trusted: true, exactMark: true}
	untrusted := modeledTriageComment{id: 1, createdAt: "2026-08-29T10:00:00Z", exactMark: true}

	for _, comments := range [][]modeledTriageComment{
		{second, untrusted, first},
		{first, second, untrusted},
	} {
		canonical, ok := modeledCanonicalTriage(comments)
		if !ok || canonical.id != first.id {
			t.Fatalf("canonical triage = %+v, %v; want trusted earliest id %d", canonical, ok, first.id)
		}
	}
}

type modeledTriageRoute struct {
	canonicalRoot  int
	open           bool
	hold           bool
	inputUnchanged bool
	labelsApplied  bool
	replyApplied   bool
	done           bool
}

func modeledTriageRecoveryCandidate(state modeledTriageRoute) bool {
	return state.canonicalRoot != 0 && !state.done
}

func modeledAdvanceTriageRoute(state modeledTriageRoute, ownedRoot, phases int) modeledTriageRoute {
	if state.canonicalRoot != ownedRoot || state.done || !state.open || state.hold || !state.inputUnchanged {
		return state
	}
	if phases > 0 {
		state.labelsApplied = true
	}
	if phases > 1 {
		state.replyApplied = true
	}
	if phases > 2 && state.labelsApplied && state.replyApplied {
		state.done = true
	}
	return state
}

func TestTriageRouteRecoversAfterCommentAndStopsForLateHumanGate(t *testing.T) {
	rootOnly := modeledTriageRoute{canonicalRoot: 10, open: true, inputUnchanged: true}
	if !modeledTriageRecoveryCandidate(rootOnly) {
		t.Fatal("a canonical root without done must remain in the TRIAGE recovery queue")
	}
	recovered := modeledAdvanceTriageRoute(rootOnly, 10, 3)
	if !recovered.labelsApplied || !recovered.replyApplied || !recovered.done {
		t.Fatalf("recovered TRIAGE route = %+v, want all idempotent phases complete", recovered)
	}

	lateHold := rootOnly
	lateHold.hold = true
	if got := modeledAdvanceTriageRoute(lateHold, 10, 3); got != lateHold {
		t.Fatal("late hold must stop TRIAGE before every route mutation")
	}
	lateClose := rootOnly
	lateClose.open = false
	if got := modeledAdvanceTriageRoute(lateClose, 10, 3); got != lateClose {
		t.Fatal("late close must stop TRIAGE before every route mutation")
	}
	edited := rootOnly
	edited.inputUnchanged = false
	if got := modeledAdvanceTriageRoute(edited, 10, 3); got != edited {
		t.Fatal("edited issue input must invalidate unfinished TRIAGE routing")
	}
	if got := modeledAdvanceTriageRoute(rootOnly, 11, 3); got != rootOnly {
		t.Fatal("a non-canonical concurrent root must not apply route labels")
	}
}

func modeledTriageLabelGate(initialSnapshotExact, labelCommitTrusted, committedSnapshotExact, postWatermarkEvent, postCommitEvent bool) bool {
	if !labelCommitTrusted {
		return initialSnapshotExact && !postWatermarkEvent
	}
	return committedSnapshotExact && !postCommitEvent
}

func modeledTrustedTriageMarker(authorTrusted, exactLine, canonicalClaim, fingerprintMatches bool) bool {
	return authorTrusted && exactLine && canonicalClaim && fingerprintMatches
}

func modeledFixAcceptsTriage(hasRouteClaim, trustedDone, routeConsistent, legacyTriage bool) bool {
	if hasRouteClaim {
		return trustedDone && routeConsistent
	}
	return legacyTriage
}

func modeledTriageRepositoryItemCandidate(isPullRequest bool) bool {
	return !isPullRequest
}

func modeledLegacyTriageMayPostRecoveryMarker(open, hold, inputUnchanged, needsDecisionPresent bool) bool {
	return open && !hold && inputUnchanged && needsDecisionPresent
}

func TestTriageLabelEventsAndFixHandoffFailClosed(t *testing.T) {
	if !modeledTriageLabelGate(true, false, false, false, false) {
		t.Fatal("unchanged pre-root labels with no events must allow the first atomic label POST")
	}
	if modeledTriageLabelGate(false, false, false, true, false) {
		t.Fatal("labels visible after a crash without a commit marker are ambiguous and must fail closed")
	}
	if modeledTriageLabelGate(false, false, false, true, false) {
		t.Fatal("a human pre-add of an expected label must not impersonate the TRIAGE label phase")
	}
	if modeledTriageLabelGate(false, true, false, true, true) {
		t.Fatal("a human removal after the committed label phase must stop recovery")
	}
	if !modeledTriageLabelGate(false, true, true, true, false) {
		t.Fatal("an exact trusted label commit without later events must allow reply/done")
	}

	if modeledFixAcceptsTriage(true, false, true, false) {
		t.Fatal("FIX must not claim ready-fix before the new TRIAGE route-done")
	}
	if !modeledFixAcceptsTriage(true, true, true, false) {
		t.Fatal("matching trusted route-done must hand the issue to FIX")
	}
	if !modeledFixAcceptsTriage(false, false, false, true) {
		t.Fatal("a genuine legacy triage without route-claim must remain supported")
	}
	if modeledFixAcceptsTriage(true, false, false, true) {
		t.Fatal("a malformed new route-claim must never fall back to legacy")
	}
	if modeledTriageRepositoryItemCandidate(true) || !modeledTriageRepositoryItemCandidate(false) {
		t.Fatal("repository Issues REST must exclude pull requests from TRIAGE")
	}
	if !modeledLegacyTriageMayPostRecoveryMarker(true, false, true, true) {
		t.Fatal("legacy recovery marker may follow a fresh full gate")
	}
	if modeledLegacyTriageMayPostRecoveryMarker(false, false, true, true) ||
		modeledLegacyTriageMayPostRecoveryMarker(true, true, true, true) {
		t.Fatal("late close or hold must stop the legacy recovery marker POST")
	}
}

func TestEveryTriageProtocolMarkerUsesTheSameTrustPredicate(t *testing.T) {
	if !modeledTrustedTriageMarker(true, true, true, true) {
		t.Fatal("a trusted exact marker bound to the canonical root must be accepted")
	}
	for name, trusted := range map[string]bool{
		"foreign author":    modeledTrustedTriageMarker(false, true, true, true),
		"embedded marker":   modeledTrustedTriageMarker(true, false, true, true),
		"wrong claim":       modeledTrustedTriageMarker(true, true, false, true),
		"wrong fingerprint": modeledTrustedTriageMarker(true, true, true, false),
	} {
		t.Run(name, func(t *testing.T) {
			if trusted {
				t.Fatal("untrusted TRIAGE protocol marker must be ignored")
			}
		})
	}
}

type modeledConcurrentRoot struct {
	id          int
	fingerprint string
	record      string
	reason      string
}

func modeledCanonicalEquivalentRoots(roots []modeledConcurrentRoot) (modeledConcurrentRoot, bool) {
	if len(roots) == 0 {
		return modeledConcurrentRoot{}, false
	}
	canonical := roots[0]
	for _, root := range roots[1:] {
		if root.id < canonical.id {
			canonical = root
		}
	}
	for _, root := range roots {
		if root.fingerprint != canonical.fingerprint || root.record != canonical.record || root.reason != canonical.reason {
			return modeledConcurrentRoot{}, false
		}
	}
	return canonical, true
}

func modeledMayPostRecoveryLease(open, hold, inputUnchanged, onlyProtocolComments bool) bool {
	return open && !hold && inputUnchanged && onlyProtocolComments
}

type modeledTriageLease struct {
	id       int
	previous int
	owner    string
	created  int
}

func modeledActiveTriageLease(root modeledTriageLease, children []modeledTriageLease) modeledTriageLease {
	active := root
	for {
		var winner modeledTriageLease
		found := false
		for _, child := range children {
			if child.previous != active.id {
				continue
			}
			if child.created < active.created || (child.created == active.created && child.id <= active.id) {
				continue
			}
			remaining := active.created + 30 - child.created
			renewal := remaining > 0 && remaining <= 5 && child.owner == active.owner
			takeover := remaining <= 0 && child.owner != active.owner
			if !renewal && !takeover {
				continue
			}
			if !found || child.created < winner.created ||
				(child.created == winner.created && child.id < winner.id) {
				winner, found = child, true
			}
		}
		if !found {
			return active
		}
		active = winner
	}
}

func modeledOwnsTriageLease(active modeledTriageLease, returnedID int, localOwner string, now int) bool {
	return active.id == returnedID && active.owner == localOwner && now < active.created+30
}

func modeledRecoverActiveTriageLease(root modeledTriageLease, visibleChildren []modeledTriageLease, commentDeletedAfterRoot bool) (modeledTriageLease, bool) {
	if commentDeletedAfterRoot {
		return modeledTriageLease{}, false
	}
	return modeledActiveTriageLease(root, visibleChildren), true
}

func modeledTriageDeletionFence(rootCreated, deletionCreated int) bool {
	return deletionCreated < rootCreated
}

func TestEquivalentConcurrentRootsDoNotDeadlockWinner(t *testing.T) {
	roots := []modeledConcurrentRoot{
		{id: 101, fingerprint: "same", record: "same-record", reason: "same-reason"},
		{id: 100, fingerprint: "same", record: "same-record", reason: "same-reason"},
	}
	canonical, ok := modeledCanonicalEquivalentRoots(roots)
	if !ok || canonical.id != 100 {
		t.Fatalf("equivalent roots = %+v/%v, want earliest root 100 and a live winner", canonical, ok)
	}
	changed := append([]modeledConcurrentRoot(nil), roots...)
	changed[0].fingerprint = "different"
	if _, ok := modeledCanonicalEquivalentRoots(changed); ok {
		t.Fatal("a genuinely different concurrent root must remain a human-change stop")
	}
}

func TestRecoveryLeaseRunsLateHumanGateBeforePosting(t *testing.T) {
	if !modeledMayPostRecoveryLease(true, false, true, true) {
		t.Fatal("unchanged expired transaction must allow a recovery lease")
	}
	for name, allowed := range map[string]bool{
		"late close":    modeledMayPostRecoveryLease(false, false, true, true),
		"late hold":     modeledMayPostRecoveryLease(true, true, true, true),
		"edited input":  modeledMayPostRecoveryLease(true, false, false, true),
		"human comment": modeledMayPostRecoveryLease(true, false, true, false),
	} {
		t.Run(name, func(t *testing.T) {
			if allowed {
				t.Fatal("late human change must block lease POST itself")
			}
		})
	}
}

func TestTriageLeaseRequiresOwnActiveIDOwnerAndExpiry(t *testing.T) {
	root := modeledTriageLease{id: 10, owner: "owner-a", created: 0}
	if modeledOwnsTriageLease(root, 10, "owner-b", 10) {
		t.Fatal("a recovery worker must not mutate under a foreign live root")
	}
	if !modeledOwnsTriageLease(root, 10, "owner-a", 10) {
		t.Fatal("the canonical root owner must own its unexpired initial lease")
	}

	invalidForeignRenewal := modeledTriageLease{id: 11, previous: 10, owner: "owner-b", created: 26}
	if got := modeledActiveTriageLease(root, []modeledTriageLease{invalidForeignRenewal}); got.id != root.id {
		t.Fatal("a foreign owner cannot renew before expiry")
	}
	validRenewal := modeledTriageLease{id: 12, previous: 10, owner: "owner-a", created: 26}
	if got := modeledActiveTriageLease(root, []modeledTriageLease{validRenewal}); got.id != validRenewal.id {
		t.Fatal("same owner must be able to renew within the five-minute threshold")
	}

	takeovers := []modeledTriageLease{
		{id: 21, previous: 10, owner: "owner-c", created: 31},
		{id: 20, previous: 10, owner: "owner-b", created: 31},
		{id: 19, previous: 10, owner: "owner-a", created: 31},
	}
	active := modeledActiveTriageLease(root, takeovers)
	if active.id != 20 || active.owner != "owner-b" {
		t.Fatalf("competing takeover winner = %+v, want earliest valid new-owner child", active)
	}
	if modeledOwnsTriageLease(active, 21, "owner-c", 32) {
		t.Fatal("a losing takeover cannot mutate under the winner's lease")
	}
	if modeledOwnsTriageLease(active, active.id, active.owner, 61) {
		t.Fatal("an expired active lease must not authorize a phase")
	}
}

func TestDeletedTriageLeaseWinnerCannotResurrectStaleSibling(t *testing.T) {
	root := modeledTriageLease{id: 10, owner: "owner-a", created: 0}
	winner := modeledTriageLease{id: 20, previous: 10, owner: "owner-b", created: 31}
	staleSibling := modeledTriageLease{id: 21, previous: 10, owner: "owner-c", created: 31}
	if active := modeledActiveTriageLease(root, []modeledTriageLease{winner, staleSibling}); active.id != winner.id {
		t.Fatalf("initial takeover winner = %+v, want earliest child", active)
	}
	if resurrected := modeledActiveTriageLease(root, []modeledTriageLease{staleSibling}); resurrected.id != staleSibling.id {
		t.Fatalf("the current comment list alone should expose the resurrection hazard, got %+v", resurrected)
	}
	if _, ok := modeledRecoverActiveTriageLease(root, []modeledTriageLease{staleSibling}, true); ok {
		t.Fatal("a paginated post-root comment deletion event must fail closed before stale election")
	}
	if modeledTriageDeletionFence(31, 31) {
		t.Fatal("a same-second deletion at the canonical root boundary must fail closed")
	}
}

type modeledIssueDecision struct {
	open        bool
	titleBody   string
	eligibleBy  string
	triage      string
	decision    string
	hold        bool
	manual      bool
	allowInWork bool
}

func modeledNewIssueEligible(approved, readyFix, needsDecision, hold, manual bool) bool {
	return !hold && !manual && (approved || (readyFix && !needsDecision))
}

func TestFixEligibilityPreservesNeedsDecisionAsHumanTurn(t *testing.T) {
	if !modeledNewIssueEligible(false, true, false, false, false) {
		t.Fatal("ready-fix without a human stop must remain automatic")
	}
	if modeledNewIssueEligible(false, true, true, false, false) {
		t.Fatal("ready-fix plus needs-decision without approved must remain the human turn")
	}
	if !modeledNewIssueEligible(true, true, true, false, false) {
		t.Fatal("approved must explicitly override needs-decision")
	}
	if modeledNewIssueEligible(true, true, true, true, false) ||
		modeledNewIssueEligible(true, true, true, false, true) {
		t.Fatal("hold and manual remain hard stops even with approved")
	}
}

func modeledIssueGate(expected, current modeledIssueDecision) bool {
	return current.open && !current.hold && !current.manual &&
		expected.titleBody == current.titleBody &&
		expected.eligibleBy == current.eligibleBy &&
		expected.triage == current.triage &&
		expected.decision == current.decision &&
		expected.allowInWork == current.allowInWork
}

func TestNewIssueMutationsRevalidateDecisionFingerprint(t *testing.T) {
	expected := modeledIssueDecision{
		open: true, titleBody: "title\x00body", eligibleBy: "approved",
		triage:   "triage:17@v1:sha256-plan-a",
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
		{name: "triage edited with same decision label", mutate: func(state *modeledIssueDecision) { state.triage = "triage:17@v2:sha256-plan-b" }},
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

func TestEarlyIssueHandoffUsesDecisionGateBeforeBranchClaim(t *testing.T) {
	expected := modeledIssueDecision{
		open: true, titleBody: "title\x00body", eligibleBy: "approved",
		triage: "triage:17@v1:sha256-plan-a", decision: "decision:2",
	}
	lateEdit := expected
	lateEdit.triage = "triage:17@v2:sha256-plan-b"
	if modeledIssueGate(expected, lateEdit) {
		t.Fatal("early needs-decision handoff must not mutate after a late triage edit")
	}
}

type modeledIssueHandoff struct {
	canonicalRoot int
	questionCount int
	needsDecision bool
	routeLabels   bool
	done          bool
}

func modeledAdvanceIssueHandoff(state modeledIssueHandoff, ownedRoot, phases int) modeledIssueHandoff {
	if ownedRoot != state.canonicalRoot || state.done {
		return state
	}
	if phases > 0 && state.questionCount == 0 {
		state.questionCount++
	}
	if phases > 1 {
		state.needsDecision = true
	}
	if phases > 2 {
		state.routeLabels = false
	}
	if phases > 3 && state.needsDecision && !state.routeLabels {
		state.done = true
	}
	return state
}

func modeledIssueHandoffRecoveryCandidate(state modeledIssueHandoff) bool {
	return !state.done && (state.routeLabels || state.needsDecision)
}

func modeledRecoverIssueHandoffLease(root modeledTriageLease, visibleChildren []modeledTriageLease, deletionAfterCanonicalTriage, trustedCommentEditedAfterTriage bool) (modeledTriageLease, bool) {
	if deletionAfterCanonicalTriage || trustedCommentEditedAfterTriage {
		return modeledTriageLease{}, false
	}
	return modeledActiveTriageLease(root, visibleChildren), true
}

func modeledCanCreateIssueHandoffRoot(deletionAfterCanonicalTriage bool) bool {
	return !deletionAfterCanonicalTriage
}

func modeledIssueHandoffPostTriageFence(canonicalTriage int, deletionEdges, editedTrustedCommentEdges []int) bool {
	for _, edge := range append(append([]int(nil), deletionEdges...), editedTrustedCommentEdges...) {
		if edge > canonicalTriage {
			return false
		}
	}
	return true
}

func TestIssueHandoffDeletedWinnerCannotReelectStaleSibling(t *testing.T) {
	root := modeledTriageLease{id: 100, owner: "root-owner", created: 0}
	winner := modeledTriageLease{id: 110, previous: 100, owner: "owner-a", created: 31}
	staleSibling := modeledTriageLease{id: 111, previous: 100, owner: "owner-b", created: 31}
	if active, ok := modeledRecoverIssueHandoffLease(root, []modeledTriageLease{winner, staleSibling}, false, false); !ok || active.id != winner.id {
		t.Fatalf("initial FIX handoff lease = %+v, ok=%v; want earliest child", active, ok)
	}
	if active := modeledActiveTriageLease(root, []modeledTriageLease{staleSibling}); active.id != staleSibling.id {
		t.Fatalf("REST-only election should expose stale resurrection, got %+v", active)
	}
	if _, ok := modeledRecoverIssueHandoffLease(root, []modeledTriageLease{staleSibling}, true, false); ok {
		t.Fatal("a GraphQL deletion edge after canonical triage must close recovery even when a later root is visible")
	}
	if _, ok := modeledRecoverIssueHandoffLease(root, []modeledTriageLease{winner}, false, true); ok {
		t.Fatal("editing a trusted post-triage comment must close recovery even if its protocol marker disappears")
	}
	if modeledCanCreateIssueHandoffRoot(true) {
		t.Fatal("a deletion after canonical triage must prevent recreating a missing/deleted handoff root")
	}
}

func TestIssueHandoffRejectsDeletionBeforeVisibleReplacementRootAndMarkerAwayEdit(t *testing.T) {
	const (
		canonicalTriage = 10
		deletedRoot     = 20
		deletionEvent   = 21
		replacementRoot = 22
	)
	if canonicalTriage >= deletedRoot || deletedRoot >= deletionEvent || deletionEvent >= replacementRoot {
		t.Fatal("invalid modeled triage < R1 < delete < R2 ordering")
	}
	if modeledIssueHandoffPostTriageFence(canonicalTriage, []int{deletionEvent}, nil) {
		t.Fatal("a visible R2 must not hide deletion of R1 between triage and R2")
	}
	if modeledIssueHandoffPostTriageFence(canonicalTriage, nil, []int{deletedRoot}) {
		t.Fatal("editing R1 marker away must remain a trusted post-triage edit fence")
	}
	if !modeledIssueHandoffPostTriageFence(canonicalTriage, []int{9}, []int{8}) {
		t.Fatal("events entirely before canonical triage do not belong to this handoff epoch")
	}
}

type modeledIssueComment struct {
	id        int
	createdAt string
	updatedAt string
	author    string
	body      string
}

func modeledIssueCommentsDigest(comments []modeledIssueComment) string {
	snapshot := append([]modeledIssueComment(nil), comments...)
	sort.Slice(snapshot, func(i, j int) bool {
		if snapshot[i].createdAt == snapshot[j].createdAt {
			return snapshot[i].id < snapshot[j].id
		}
		return snapshot[i].createdAt < snapshot[j].createdAt
	})
	var record strings.Builder
	record.WriteString("pp-fix-comments-v1\n")
	for _, comment := range snapshot {
		authorSum := sha256.Sum256([]byte(comment.author))
		bodySum := sha256.Sum256([]byte(comment.body))
		fmt.Fprintf(&record, "comment=%d@%s@%s@author-sha256=%s@body-sha256=%s\n",
			comment.id, comment.createdAt, comment.updatedAt,
			hex.EncodeToString(authorSum[:]), hex.EncodeToString(bodySum[:]))
	}
	digest := sha256.Sum256([]byte(record.String()))
	return hex.EncodeToString(digest[:])
}

func modeledCanRemoveRouteLabel(initialPresent, currentPresent, postRootUnlabeled bool) bool {
	return initialPresent && currentPresent && !postRootUnlabeled
}

func modeledCanRestoreNeedsDecision(currentPresent, postRootHumanRemoval bool) bool {
	return !currentPresent && !postRootHumanRemoval
}

func TestIssueHandoffSerializesWorkersAndRecoversPhases(t *testing.T) {
	record := "pp-fix-issue-handoff-v1\n" +
		"issue=42\n" +
		"issue-updated=2026-08-30T10:00:00Z\n" +
		"title-sha256=" + strings.Repeat("a", 64) + "\n" +
		"body-sha256=" + strings.Repeat("b", 64) + "\n" +
		"triage-comment=17\n" +
		"triage-updated=2026-08-30T09:00:00Z\n" +
		"triage-sha256=" + strings.Repeat("c", 64) + "\n" +
		"comments-sha256=" + strings.Repeat("d", 64) + "\n" +
		"events-watermark=900\n" +
		"labels=approved,decision:2\n" +
		"choice=decision:2\n" +
		"reason=missing-plan\n"
	recordSum := sha256.Sum256([]byte(record))
	if got := hex.EncodeToString(recordSum[:]); got != "b62eae410f86dc086883fa83c88f6108eb26ae82573fb267feaea9747e17bb22" {
		t.Fatalf("canonical issue handoff hash vector = %s", got)
	}

	initial := modeledIssueHandoff{canonicalRoot: 10, routeLabels: true}
	loser := modeledAdvanceIssueHandoff(initial, 11, 4)
	if loser != initial {
		t.Fatal("non-canonical root must not mutate issue handoff state")
	}

	crashedAfterQuestion := modeledAdvanceIssueHandoff(initial, 10, 1)
	if crashedAfterQuestion.questionCount != 1 || crashedAfterQuestion.done {
		t.Fatal("first owner must persist exactly one recoverable question before crash")
	}
	crashedAfterRouteRemoval := modeledAdvanceIssueHandoff(initial, 10, 3)
	if crashedAfterRouteRemoval.routeLabels || !crashedAfterRouteRemoval.needsDecision ||
		!modeledIssueHandoffRecoveryCandidate(crashedAfterRouteRemoval) {
		t.Fatal("needs-decision recovery queue must retain a handoff after route labels are removed")
	}
	recovered := modeledAdvanceIssueHandoff(crashedAfterQuestion, 10, 4)
	if recovered.questionCount != 1 || !recovered.needsDecision || recovered.routeLabels || !recovered.done {
		t.Fatalf("recovered handoff = %+v, want one question and completed label transition", recovered)
	}
}

func TestIssueHandoffRejectsEditedCommentsAndHumanLabelReadd(t *testing.T) {
	original := []modeledIssueComment{
		{id: 8, createdAt: "2026-08-30T08:00:00Z", updatedAt: "2026-08-30T08:00:00Z", author: "ivanarama", body: "context"},
		{id: 9, createdAt: "2026-08-30T09:00:00Z", updatedAt: "2026-08-30T09:00:00Z", author: "ivanarama", body: "decision"},
	}
	originalDigest := modeledIssueCommentsDigest(original)
	edited := append([]modeledIssueComment(nil), original...)
	edited[0].body = "edited context"
	if modeledIssueCommentsDigest(edited) == originalDigest {
		t.Fatal("editing a pre-root comment must invalidate handoff recovery")
	}
	if modeledIssueCommentsDigest(original[1:]) == originalDigest {
		t.Fatal("deleting a pre-root comment must invalidate handoff recovery")
	}

	if modeledCanRemoveRouteLabel(true, true, true) {
		t.Fatal("recovery must not remove approved after worker removal and human re-add")
	}
	if !modeledCanRemoveRouteLabel(true, true, false) {
		t.Fatal("the original approved may be removed before any post-root unlabeled event")
	}
	if modeledCanRemoveRouteLabel(false, true, false) {
		t.Fatal("a route label absent from the root snapshot is a later human change")
	}
	if modeledCanRestoreNeedsDecision(false, true) {
		t.Fatal("recovery must not restore needs-decision after a human removed it")
	}
}

func modeledTailDropApplies(current, dropped modeledTailVersionKey) bool {
	return current.reviewID == dropped.reviewID &&
		current.reviewUpdated == dropped.reviewUpdated &&
		current.item == dropped.item &&
		current.itemSHA256 == dropped.itemSHA256
}

func TestTailDropIsBoundToEditableCommentAndItemVersion(t *testing.T) {
	dropped := modeledTailVersionKey{
		reviewID: 42, reviewUpdated: "2026-08-30T10:00:00Z", item: 1,
		itemSHA256: "item-a", dedupeSHA256: "task-a",
	}
	if !modeledTailDropApplies(dropped, dropped) {
		t.Fatal("exact versioned drop must apply")
	}

	for name, current := range map[string]modeledTailVersionKey{
		"edited review":  {reviewID: 42, reviewUpdated: "2026-08-30T10:01:00Z", item: 1, itemSHA256: "item-a"},
		"reordered item": {reviewID: 42, reviewUpdated: "2026-08-30T10:00:00Z", item: 2, itemSHA256: "item-a"},
		"rewritten item": {reviewID: 42, reviewUpdated: "2026-08-30T10:00:00Z", item: 1, itemSHA256: "item-b"},
	} {
		t.Run(name, func(t *testing.T) {
			if modeledTailDropApplies(current, dropped) {
				t.Fatal("stale drop must not apply to a different review/item version")
			}
		})
	}
}

func modeledLegacyTailEligible(createdAt, updatedAt, cutover int) bool {
	return createdAt < cutover && updatedAt < cutover
}

func TestLegacyTailFreezesCreatedAndUpdatedAtBeforeCutover(t *testing.T) {
	if !modeledLegacyTailEligible(10, 11, 20) {
		t.Fatal("unchanged pre-cutover legacy review must remain drainable")
	}
	if modeledLegacyTailEligible(10, 20, 20) {
		t.Fatal("review edited at cutover must not use legacy short drop")
	}
	if modeledLegacyTailEligible(10, 21, 20) {
		t.Fatal("post-cutover edit must invalidate legacy fallback")
	}
}

func modeledTailCreateFenceNeedsHuman(globalRef, createIntent, issueFound bool) bool {
	_ = createIntent // both sides of the ref->intent crash window are fenced
	return globalRef && !issueFound
}

func TestTailOrphanGlobalRefNeedsHumanBeforeOrAfterIntent(t *testing.T) {
	if !modeledTailCreateFenceNeedsHuman(true, false, false) {
		t.Fatal("orphan global ref must stop for human recovery even before create-intent")
	}
	if !modeledTailCreateFenceNeedsHuman(true, true, false) {
		t.Fatal("orphan global ref must stop for human recovery after create-intent")
	}
	if modeledTailCreateFenceNeedsHuman(true, true, true) {
		t.Fatal("an issue found behind the permanent fence is recoverable automatically")
	}
	if modeledTailCreateFenceNeedsHuman(false, false, false) {
		t.Fatal("absence of both ref and issue is not an orphan-fence recovery case")
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

func modeledPortableTailText(value string) (string, bool) {
	if !utf8.ValidString(value) {
		return "", false
	}
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	var normalized strings.Builder
	pendingSpace := false
	for i := 0; i < len(value); {
		b := value[i]
		if b == 0x20 || (b >= 0x09 && b <= 0x0d) {
			if normalized.Len() > 0 {
				pendingSpace = true
			}
			i++
			continue
		}
		if pendingSpace {
			normalized.WriteByte(' ')
			pendingSpace = false
		}
		if b < utf8.RuneSelf {
			normalized.WriteByte(b)
			i++
			continue
		}
		_, size := utf8.DecodeRuneInString(value[i:])
		normalized.WriteString(value[i : i+size])
		i += size
	}
	return normalized.String(), true
}

func modeledTailTaskDedupe(title, task string) (string, string, string) {
	title, titleOK := modeledPortableTailText(title)
	task, taskOK := modeledPortableTailText(task)
	if !titleOK || !taskOK {
		return "", "", ""
	}
	titleSum := sha256.Sum256([]byte(title))
	taskSum := sha256.Sum256([]byte(task))
	titleHash := hex.EncodeToString(titleSum[:])
	taskHash := hex.EncodeToString(taskSum[:])
	record := "pp-tail-task-v1\n" +
		"title-sha256=" + titleHash + "\n" +
		"task-sha256=" + taskHash + "\n"
	dedupeSum := sha256.Sum256([]byte(record))
	return titleHash, taskHash, hex.EncodeToString(dedupeSum[:])
}

func modeledAcquireCanonicalTaskClaim(claims map[string]bool, title, task string) bool {
	_, _, key := modeledTailTaskDedupe(title, task)
	if key == "" {
		return false
	}
	if claims[key] {
		return false
	}
	claims[key] = true
	return true
}

func modeledTailExactSourceRecoverable(author, source, title, canonicalTask, taskMarker, dedupeMarker, hashes bool) bool {
	return author && source && title && canonicalTask && taskMarker && dedupeMarker && hashes
}

func TestPortableTailTextAvoidsRuntimeDependentUnicodeFolding(t *testing.T) {
	got, ok := modeledPortableTailText(" \tStraße\r\nreview\u00a0text  ")
	if !ok || got != "Straße review\u00a0text" {
		t.Fatalf("portable normalization = %q/%v", got, ok)
	}
	street, _, streetKey := modeledTailTaskDedupe("Straße", "task")
	upper, _, upperKey := modeledTailTaskDedupe("STRASSE", "task")
	if street == upper || streetKey == upperKey {
		t.Fatal("pp-text-v1 must preserve Unicode case instead of runtime-dependent casefold")
	}
	composed, _, composedKey := modeledTailTaskDedupe("é", "task")
	decomposed, _, decomposedKey := modeledTailTaskDedupe("e\u0301", "task")
	if composed == decomposed || composedKey == decomposedKey {
		t.Fatal("pp-text-v1 must not depend on runtime Unicode normalization tables")
	}
	if _, ok := modeledPortableTailText(string([]byte{0xff})); ok {
		t.Fatal("invalid UTF-8 must fail closed")
	}
}

func TestTailExactSourceRecoveryRequiresCompleteImmutablePayload(t *testing.T) {
	if modeledTailExactSourceRecoverable(true, true, false, false, false, false, false) {
		t.Fatal("a source marker alone must not complete a TAIL item")
	}
	if !modeledTailExactSourceRecoverable(true, true, true, true, true, true, true) {
		t.Fatal("a fully consistent exact-source issue must remain recoverable")
	}
}

func TestCanonicalTailTaskClaimUsesTitleAndTaskContent(t *testing.T) {
	titleHash, taskHash, dedupeHash := modeledTailTaskDedupe("проверка", "ошибка")
	if titleHash != "dbdd1f31e722086974ba86e32d48bea04cc01601a390091c51d76efe1d590eb2" ||
		taskHash != "44d4090ae9e4fae861c4ed1418d3557dc340d0afe55e6744ae4155412b88425a" ||
		dedupeHash != "e8881813f61fdbb58d3b00eb1e7a9c8e5006a1fae2aaebe114d963da05f03e3d" {
		t.Fatalf("canonical Cyrillic hash vector = %s/%s/%s", titleHash, taskHash, dedupeHash)
	}

	claims := map[string]bool{}
	firstSource := modeledAcquireCanonicalTaskClaim(claims, "add error check", "subsystem a must reject malformed input")
	sameTaskOtherSource := modeledAcquireCanonicalTaskClaim(claims, "add error check", "subsystem a must reject malformed input")
	differentTaskSameTitle := modeledAcquireCanonicalTaskClaim(claims, "add error check", "subsystem b must retry network timeout")
	if !firstSource || sameTaskOtherSource || !differentTaskSameTitle {
		t.Fatalf("canonical task claims = first:%v same:%v different:%v, want true/false/true", firstSource, sameTaskOtherSource, differentTaskSameTitle)
	}
}
