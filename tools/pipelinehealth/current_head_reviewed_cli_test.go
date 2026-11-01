package main

// current_head_reviewed — машинный сигнал, и проверять его надо так же, как его
// читает потребитель: через ПУБЛИЧНУЮ команду на офлайн-фикстурах, а не через
// внутреннюю функцию. Именно этим путём ревью круга 1 и показало прежний
// дефект: одиночный pp:head-reviewed текущего SHA давал true, хотя ни claim, ни
// заключения за ним не стояло.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// canonicalCurrentPair — полная каноничная пара текущей версии:
// заключение (30) → claim (40) → completion (31).
func canonicalCurrentPair(item apiPull, sha string) apiPull {
	item = addComment(item, 30, reviewConclusion(sha, "reviewed"))
	item = addComment(item, 40, claimFor(sha, 30, epoch))
	return addComment(item, 31, completion(sha, 30, 40))
}

// editComment портит доверие к маркеру ровно одним способом за раз: правка
// после публикации или чужое авторство.
func editComment(item apiPull, id int64, edited bool, author string) apiPull {
	for i := range item.Comments {
		if item.Comments[i].ID != id {
			continue
		}
		if edited {
			item.Comments[i].UpdatedAt = "2026-09-02T00:00:00Z"
		}
		if author != "" {
			item.Comments[i].User = apiUser{Login: author}
		}
	}
	return item
}

// pipelinehealthJSON запускает публичную команду и возвращает разобранный отчёт.
func pipelinehealthJSON(t *testing.T, pulls []apiPull) report {
	t.Helper()
	dir := t.TempDir()
	prsPath := filepath.Join(dir, "prs.json")
	issuesPath := filepath.Join(dir, "issues.json")
	// Комментарии в файле-фикстуре лежат отдельным ключом: именно так их читает
	// readPullFixture, и именно этим путём они доезжают до публичной команды.
	type pullFixture struct {
		apiPull
		Comments []apiComment `json:"comments"`
	}
	fixtures := make([]pullFixture, 0, len(pulls))
	for _, item := range pulls {
		fixtures = append(fixtures, pullFixture{apiPull: item, Comments: item.Comments})
	}
	raw, err := json.Marshal(fixtures)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prsPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(issuesPath, []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // Исполняемый файл и флаги фиксированы; переменные аргументы — временные пути самого теста.
	command := exec.Command("go", "run", ".", "-json", "-prs", prsPath, "-issues", issuesPath,
		"-contract", filepath.Join("..", "..", ".claude", "skills", "review-queue", "SKILL.md"))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("pipelinehealth не отработал: %v\n%s", err, output)
	}
	var got report
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("разбор вывода pipelinehealth: %v\n%s", err, output)
	}
	return got
}

// integrationBase — двухродительский HEAD с согласованным ревью ИСХОДНОЙ версии.
// Всё, что меняется дальше, касается только пары текущего HEAD.
func integrationBase() apiPull {
	return reviewedFrom(integrationPR(99, headC, []string{headA, headB}, "ship"), headA)
}

func TestCurrentHeadReviewedNeedsWholePairThroughCLI(t *testing.T) {
	tests := []struct {
		name  string
		build func() apiPull
		want  bool
	}{
		{"каноничная пара текущего HEAD", func() apiPull {
			return canonicalCurrentPair(integrationBase(), headC)
		}, true},
		{"completion без claim", func() apiPull {
			item := addComment(integrationBase(), 30, reviewConclusion(headC, "reviewed"))
			return addComment(item, 31, completion(headC, 30, 40))
		}, false},
		{"claim без заключения", func() apiPull {
			item := addComment(integrationBase(), 40, claimFor(headC, 30, epoch))
			return addComment(item, 31, completion(headC, 30, 40))
		}, false},
		{"заключение названо для другой версии", func() apiPull {
			item := addComment(integrationBase(), 30, reviewConclusion(headD, "reviewed"))
			item = addComment(item, 40, claimFor(headC, 30, epoch))
			return addComment(item, 31, completion(headC, 30, 40))
		}, false},
		{"epoch claim и completion не совпали", func() apiPull {
			item := addComment(integrationBase(), 30, reviewConclusion(headC, "reviewed"))
			item = addComment(item, 40, claimFor(headC, 30, otherEpoch))
			return addComment(item, 31, completion(headC, 30, 40))
		}, false},
		{"completion отредактирован после публикации", func() apiPull {
			return editComment(canonicalCurrentPair(integrationBase(), headC), 31, true, "")
		}, false},
		{"заключение опубликовано не тем аккаунтом", func() apiPull {
			return editComment(canonicalCurrentPair(integrationBase(), headC), 30, false, "someone-else")
		}, false},
		{"вердикт текущего ревью не reviewed", func() apiPull {
			item := addComment(integrationBase(), 30, reviewConclusion(headC, "changes-requested"))
			item = addComment(item, 40, claimFor(headC, 30, epoch))
			return addComment(item, 31, completion(headC, 30, 40))
		}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			found := candidateOf(t, pipelinehealthJSON(t, []apiPull{test.build()}), 99)
			if found == nil {
				t.Fatal("кандидат пропал: форма коммита фактом быть не перестала")
			}
			if found.CurrentHeadReviewed != test.want {
				t.Fatalf("current_head_reviewed=%v, ожидалось %v: %+v",
					found.CurrentHeadReviewed, test.want, found)
			}
			// Ревью ИСХОДНОЙ версии ни в одном случае не трогалось: порча пары
			// текущего HEAD не должна пачкать соседнее поле.
			if found.FromReview == nil || found.FromReview.State != reviewPairConsistent {
				t.Fatalf("пара исходной версии пострадала: %+v", found.FromReview)
			}
		})
	}
}

// Снимок остаётся ОПИСАНИЕМ: даже у полной пары текущего HEAD он перечисляет то,
// чего не проверял, и не выдаёт разрешения на carry.
func TestCurrentHeadReviewedStaysDescriptiveThroughCLI(t *testing.T) {
	found := candidateOf(t, pipelinehealthJSON(t, []apiPull{canonicalCurrentPair(integrationBase(), headC)}), 99)
	if found == nil || !found.CurrentHeadReviewed {
		t.Fatalf("каноничная пара не признана: %+v", found)
	}
	want := map[string]bool{"base_ancestry": true, "merge_tree": true, "required_checks": true, "timeline_epoch": true}
	if len(found.ConsumerMustVerify) != len(want) {
		t.Fatalf("граница ответственности изменилась: %+v", found.ConsumerMustVerify)
	}
	for _, name := range found.ConsumerMustVerify {
		if !want[name] {
			t.Fatalf("неожиданная проверка в границе: %q", name)
		}
	}
}
