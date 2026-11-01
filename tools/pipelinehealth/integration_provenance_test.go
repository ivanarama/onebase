package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Переход base-sync читается из формы коммита, а не из журнала: opt-in путь
// base_sync_merge намеренно ничего не публикует, чтобы конфликт 422 не оставлял
// ложное «готово». Поэтому снимок обязан отдавать данные и для HEAD без
// маркеров — но именно данные: кандидата и найденные свидетельства, а не
// готовое доказательство перехода.

const otherEpoch = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"

func reviewConclusion(sha, outcome string) string {
	return fmt.Sprintf("**Ревью.**\nReviewed-SHA: %s\nOutcome-Label: %s\n<!-- pp:review pp:tail=0 -->", sha, outcome)
}

func claimFor(sha string, reviewID int64, epochValue string) string {
	return fmt.Sprintf("<!-- pp:review-claim %s review-comment=%d epoch-sha256=%s -->", sha, reviewID, epochValue)
}

// reviewedFrom — исходная версия ветки с полной согласованной парой:
// заключение (10) → claim (20) → completion.
func reviewedFrom(item apiPull, sha string) apiPull {
	item = addComment(item, 10, reviewConclusion(sha, "reviewed"))
	item = addComment(item, 20, claimFor(sha, 10, epoch))
	return addComment(item, 11, completion(sha, 10, 20))
}

func integrationPR(number int, head string, parents []string, labels ...string) apiPull {
	item := testPR(number, head, labels...)
	item.HeadParents = parents
	return item
}

func candidateOf(t *testing.T, result report, number int) *baseSyncCandidate {
	t.Helper()
	groups := [][]candidate{result.ReviewCandidates, result.MergeCandidates, result.ContentReviewCandidates, result.ReviewBacklog, result.HumanWaiting}
	for _, group := range groups {
		for _, item := range group {
			if item.Number == number {
				return item.BaseSyncCandidate
			}
		}
	}
	if result.IntegrationOwner != nil && result.IntegrationOwner.Number == number {
		return result.IntegrationOwner.BaseSyncCandidate
	}
	t.Fatalf("PR #%d не попал ни в одну очередь: %+v", number, result)
	return nil
}

func TestBaseSyncCandidateIsDataNotProof(t *testing.T) {
	item := reviewedFrom(integrationPR(99, headC, []string{headA, headB}, "ship"), headA)

	got := analyze([]apiPull{item}, "ivanarama")
	found := candidateOf(t, got, 99)
	if found == nil {
		t.Fatal("кандидат не отдан: проверять переход нечем")
	}
	if found.From != headA || found.Base != headB || found.To != headC {
		t.Fatalf("форма коммита прочитана неверно: %+v", found)
	}
	if found.Source != "head_parents" {
		t.Fatalf("источник данных назван неверно: %+v", found)
	}
	if found.CurrentHeadReviewed {
		t.Fatalf("у текущего HEAD ревью ещё нет: %+v", found)
	}
	// Снимок не проверяет ни предка базы, ни дерево слияния, ни CI, ни
	// server-ordered epoch — и обязан сказать об этом машинно.
	want := map[string]bool{"base_ancestry": true, "merge_tree": true, "required_checks": true, "timeline_epoch": true}
	if len(found.ConsumerMustVerify) != len(want) {
		t.Fatalf("граница ответственности не полна: %+v", found.ConsumerMustVerify)
	}
	for _, name := range found.ConsumerMustVerify {
		if !want[name] {
			t.Fatalf("неожиданная проверка в границе: %q", name)
		}
	}
	if found.FromReview == nil || found.FromReview.State != reviewPairConsistent ||
		found.FromReview.Outcome != "reviewed" || found.FromReview.ReviewComment != 10 || found.FromReview.Claim != 20 {
		t.Fatalf("согласованная пара ревью не распознана: %+v", found.FromReview)
	}
}

func TestBaseSyncCandidateNeedsTwoParents(t *testing.T) {
	tests := []struct {
		name    string
		parents []string
	}{
		{"обычная доработка одним коммитом", []string{headA}},
		{"родители неизвестны", nil},
		{"осьминожье слияние", []string{headA, headB, headD}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			item := reviewedFrom(integrationPR(99, headC, test.parents, "ship"), headA)
			if found := candidateOf(t, analyze([]apiPull{item}, "ivanarama"), 99); found != nil {
				t.Fatalf("кандидатом объявлена не та форма коммита: %+v", found)
			}
		})
	}
}

// Содержательный коммит поверх слияния снимает двухродительскую форму: HEAD
// снова обычный, и механическим переходом это больше не является.
func TestSubstantiveCommitAfterMergeDropsCandidate(t *testing.T) {
	merged := reviewedFrom(integrationPR(99, headC, []string{headA, headB}, "ship"), headA)
	if candidateOf(t, analyze([]apiPull{merged}, "ivanarama"), 99) == nil {
		t.Fatal("подготовка теста неверна: кандидата нет и до нового коммита")
	}

	pushed := merged
	pushed.Head.SHA = headD
	pushed.HeadParents = []string{headC}
	if found := candidateOf(t, analyze([]apiPull{pushed}, "ivanarama"), 99); found != nil {
		t.Fatalf("новый коммит поверх слияния остался кандидатом: %+v", found)
	}
}

func TestReviewPairStatesAreReportedWithoutVerdict(t *testing.T) {
	tests := []struct {
		name  string
		build func() apiPull
		state reviewPairState
	}{
		{"доказательства нет вовсе", func() apiPull {
			return integrationPR(99, headC, []string{headA, headB}, "ship")
		}, reviewPairMissing},
		{"claim отсутствует", func() apiPull {
			item := integrationPR(99, headC, []string{headA, headB}, "ship")
			item = addComment(item, 10, reviewConclusion(headA, "reviewed"))
			return addComment(item, 11, completion(headA, 10, 20))
		}, reviewPairClaimMissing},
		{"claim подменён другой версией", func() apiPull {
			item := integrationPR(99, headC, []string{headA, headB}, "ship")
			item = addComment(item, 10, reviewConclusion(headA, "reviewed"))
			item = addComment(item, 20, claimFor(headD, 10, epoch))
			return addComment(item, 11, completion(headA, 10, 20))
		}, reviewPairClaimMissing},
		{"epoch claim и completion не совпали", func() apiPull {
			item := integrationPR(99, headC, []string{headA, headB}, "ship")
			item = addComment(item, 10, reviewConclusion(headA, "reviewed"))
			item = addComment(item, 20, claimFor(headA, 10, otherEpoch))
			return addComment(item, 11, completion(headA, 10, 20))
		}, reviewPairClaimEpochMismatch},
		{"заключения нет", func() apiPull {
			item := integrationPR(99, headC, []string{headA, headB}, "ship")
			item = addComment(item, 20, claimFor(headA, 10, epoch))
			return addComment(item, 11, completion(headA, 10, 20))
		}, reviewPairReviewMissing},
		{"заключение отредактировано", func() apiPull {
			item := reviewedFrom(integrationPR(99, headC, []string{headA, headB}, "ship"), headA)
			item.Comments[0].UpdatedAt = "2026-09-02T00:00:00Z"
			return item
		}, reviewPairReviewUntrusted},
		{"заключение названо для другой версии", func() apiPull {
			item := integrationPR(99, headC, []string{headA, headB}, "ship")
			item = addComment(item, 10, reviewConclusion(headD, "reviewed"))
			item = addComment(item, 20, claimFor(headA, 10, epoch))
			return addComment(item, 11, completion(headA, 10, 20))
		}, reviewPairSHAMismatch},
		{"вердикт не reviewed", func() apiPull {
			item := integrationPR(99, headC, []string{headA, headB}, "ship")
			item = addComment(item, 10, reviewConclusion(headA, "changes-requested"))
			item = addComment(item, 20, claimFor(headA, 10, epoch))
			return addComment(item, 11, completion(headA, 10, 20))
		}, reviewPairOutcomeNotReviewed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			found := candidateOf(t, analyze([]apiPull{test.build()}, "ivanarama"), 99)
			if found == nil {
				t.Fatal("кандидат должен остаться: форма коммита фактом быть не перестала")
			}
			if found.FromReview == nil || found.FromReview.State != test.state {
				t.Fatalf("состояние пары определено неверно: %+v", found.FromReview)
			}
			if found.FromReview.Outcome != "" {
				t.Fatalf("непроверенная пара отдана с вердиктом: %+v", found.FromReview)
			}
		})
	}
}

func TestCandidateSurvivesJSONReport(t *testing.T) {
	item := reviewedFrom(integrationPR(99, headC, []string{headA, headB}, "ship"), headA)

	raw, err := json.Marshal(analyze([]apiPull{item}, "ivanarama"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{
		`"base_sync_candidate"`, `"source":"head_parents"`, `"from":"` + headA + `"`,
		`"state":"consistent"`, `"outcome_label":"reviewed"`, `"consumer_must_verify"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("в JSON-отчёте нет %s:\n%s", want, text)
		}
	}
	// Слова «доказано» в контракте быть не должно: снимок отдаёт данные.
	if strings.Contains(text, `"proved_by"`) {
		t.Fatalf("контракт снова обещает доказательство: %s", text)
	}

	ordinary := addComment(testPR(1, headA, "ship"), 11, completion(headA, 10, 20))
	raw, err = json.Marshal(analyze([]apiPull{ordinary}, "ivanarama"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"base_sync_candidate"`) {
		t.Fatalf("пустой кандидат попал в отчёт: %s", raw)
	}
}
