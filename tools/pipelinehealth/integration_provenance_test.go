package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Переход base-sync читается из формы коммита, а не из журнала: opt-in путь
// base_sync_merge намеренно ничего не публикует, чтобы конфликт 422 не оставлял
// ложное «готово». Поэтому снимок обязан отдавать доказательство перехода и для
// HEAD без маркеров — иначе механическая проверка перехода невозможна и стадия
// уходит в полный протокол.

func reviewConclusion(sha, outcome string) string {
	return fmt.Sprintf("**Ревью.**\nReviewed-SHA: %s\nOutcome-Label: %s\n<!-- pp:review pp:tail=0 -->", sha, outcome)
}

func integrationPR(number int, head string, parents [2]string, labels ...string) apiPull {
	item := testPR(number, head, labels...)
	item.HeadParents = []string{parents[0], parents[1]}
	return item
}

func transitionOf(t *testing.T, result report, number int) *transitionProof {
	t.Helper()
	groups := [][]candidate{result.ReviewCandidates, result.MergeCandidates, result.ContentReviewCandidates, result.ReviewBacklog}
	for _, group := range groups {
		for _, item := range group {
			if item.Number == number {
				return item.Transition
			}
		}
	}
	if result.IntegrationOwner != nil && result.IntegrationOwner.Number == number {
		return result.IntegrationOwner.Transition
	}
	t.Fatalf("PR #%d не попал ни в одну очередь: %+v", number, result)
	return nil
}

func TestIntegrationCandidateExposesGraphProvedTransition(t *testing.T) {
	item := integrationPR(99, headC, [2]string{headA, headB}, "ship")
	item = addComment(item, 10, reviewConclusion(headA, "reviewed"))
	item = addComment(item, 11, completion(headA, 10, 20))

	got := analyze([]apiPull{item}, "ivanarama")
	proof := transitionOf(t, got, 99)
	if proof == nil {
		t.Fatal("переход не отдан: механическую проверку провести нечем")
	}
	if proof.From != headA || proof.Base != headB || proof.To != headC {
		t.Fatalf("переход прочитан неверно: %+v", proof)
	}
	if proof.ProvedBy != "graph" {
		t.Fatalf("переход без маркеров обязан доказываться графом: %+v", proof)
	}
	if proof.CurrentHeadReviewed {
		t.Fatalf("у текущего HEAD ревью ещё нет: %+v", proof)
	}
	if proof.FromReview == nil || proof.FromReview.Outcome != "reviewed" ||
		proof.FromReview.ReviewComment != 10 || proof.FromReview.Claim != 20 {
		t.Fatalf("ревью исходной версии не доказано: %+v", proof.FromReview)
	}
}

func TestPublishedBaseSyncKeepsMarkersAsProofSource(t *testing.T) {
	item := integrationPR(99, headB, [2]string{headA, headB}, "ship", "reviewed")
	item = addComment(item, 10, reviewConclusion(headA, "reviewed"))
	item = addComment(item, 11, completion(headA, 10, 20))
	item = addComment(item, 30, syncIntent(headA, 10, 20, 11))
	item = addComment(item, 31, syncDone(30, headA, headB))

	got := analyze([]apiPull{item}, "ivanarama")
	proof := transitionOf(t, got, 99)
	if proof == nil || proof.ProvedBy != "markers" {
		t.Fatalf("опубликованный переход обязан быть отмечен как журнальный: %+v", proof)
	}
}

func TestOrdinaryRoundHasNoTransition(t *testing.T) {
	item := addComment(testPR(99, headA, "ship"), 11, completion(headA, 10, 20))

	got := analyze([]apiPull{item}, "ivanarama")
	if proof := transitionOf(t, got, 99); proof != nil {
		t.Fatalf("обычная доработка объявлена переходом base-sync: %+v", proof)
	}
}

func TestTransitionOutcomeIsEmptyWhenConclusionIsNotTrustworthy(t *testing.T) {
	tests := []struct {
		name   string
		mangle func(apiPull) apiPull
	}{
		{"заключение отредактировано", func(item apiPull) apiPull {
			item.Comments[0].UpdatedAt = "2026-09-02T00:00:00Z"
			return item
		}},
		{"заключение от чужого аккаунта", func(item apiPull) apiPull {
			item.Comments[0].User = apiUser{Login: "someone-else"}
			return item
		}},
		{"заключение названо для другой версии", func(item apiPull) apiPull {
			item.Comments[0].Body = reviewConclusion(headD, "reviewed")
			return item
		}},
		{"вердикта в заключении нет", func(item apiPull) apiPull {
			item.Comments[0].Body = "**Ревью.**\nReviewed-SHA: " + headA + "\n<!-- pp:review pp:tail=0 -->"
			return item
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			item := integrationPR(99, headC, [2]string{headA, headB}, "ship")
			item = addComment(item, 10, reviewConclusion(headA, "reviewed"))
			item = addComment(item, 11, completion(headA, 10, 20))
			item = test.mangle(item)

			got := analyze([]apiPull{item}, "ivanarama")
			proof := transitionOf(t, got, 99)
			if proof == nil {
				t.Fatal("переход должен остаться видимым: недоказан вердикт, а не форма коммита")
			}
			if proof.FromReview != nil && proof.FromReview.Outcome != "" {
				t.Fatalf("вердикт принят из ненадёжного заключения: %+v", proof.FromReview)
			}
		})
	}
}

func TestTransitionSurvivesJSONReport(t *testing.T) {
	item := integrationPR(99, headC, [2]string{headA, headB}, "ship")
	item = addComment(item, 10, reviewConclusion(headA, "reviewed"))
	item = addComment(item, 11, completion(headA, 10, 20))

	raw, err := json.Marshal(analyze([]apiPull{item}, "ivanarama"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{`"transition"`, `"proved_by":"graph"`, `"from":"` + headA + `"`, `"outcome_label":"reviewed"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("в JSON-отчёте нет %s:\n%s", want, text)
		}
	}
	ordinary := addComment(testPR(1, headA, "ship"), 11, completion(headA, 10, 20))
	raw, err = json.Marshal(analyze([]apiPull{ordinary}, "ivanarama"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"transition"`) {
		t.Fatalf("пустой переход попал в отчёт: %s", raw)
	}
}
