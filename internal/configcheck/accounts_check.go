package configcheck

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

// CheckChartsOfAccounts проверяет планы счетов и ссылки регистров бухгалтерии на
// них — то, что загрузчик принимает молча, а учёт потом тихо искажает:
//
//   - вид счёта вне active/passive/active_passive (опечатка, «active-passive» через
//     дефис) остаток считал как у активно-пассивного счёта;
//   - повтор кода: при синхронизации плана в базу побеждала последняя запись;
//   - parent на несуществующий код: иерархия обрывалась без сообщения;
//   - accounts: регистра, который не называет план счетов конфигурации: остатки
//     фильтруются по плану регистра и выходили пустыми, а запись проводок
//     отказывала только в момент проведения.
//
// Имя плана в регистре сравнивается точно: оно становится значением _accounts.plan,
// а SQL сравнивает строки с учётом регистра.
func CheckChartsOfAccounts(proj *project.Project) []Issue {
	var issues []Issue
	add := func(file, object, kind, msg string) {
		issues = append(issues, Issue{File: file, Object: object, Kind: kind, Message: msg})
	}

	charts := map[string]bool{}
	chartsFold := map[string]string{}
	for _, c := range proj.ChartsOfAccounts {
		charts[c.Name] = true
		chartsFold[strings.ToLower(c.Name)] = c.Name

		codes := map[string]bool{}
		for _, a := range c.Accounts {
			if codes[a.Code] {
				add("accounts", c.Name, "План счетов",
					fmt.Sprintf("код счёта %q встречается дважды", a.Code))
			}
			codes[a.Code] = true
			if !metadata.ValidAccountKind(a.Kind) {
				add("accounts", c.Name, "План счетов",
					fmt.Sprintf("счёт %q: вид %q не распознан — допустимы %s, %s, %s", a.Code, a.Kind,
						metadata.AccountKindActive, metadata.AccountKindPassive, metadata.AccountKindActivePassive))
			}
		}
		for _, a := range c.Accounts {
			if a.Parent != "" && !codes[a.Parent] {
				add("accounts", c.Name, "План счетов",
					fmt.Sprintf("счёт %q: родитель %q не найден в плане счетов", a.Code, a.Parent))
			}
		}
	}

	for _, ar := range proj.AccountRegisters {
		name := strings.TrimSpace(ar.Accounts)
		switch {
		case name == "":
			add("accountregs", ar.Name, "Регистр бухгалтерии",
				"не указан план счетов (accounts:)")
		case charts[name]:
		case chartsFold[strings.ToLower(name)] != "":
			add("accountregs", ar.Name, "Регистр бухгалтерии",
				fmt.Sprintf("план счетов %q не найден: есть %q — имя плана сравнивается с учётом регистра букв",
					name, chartsFold[strings.ToLower(name)]))
		default:
			add("accountregs", ar.Name, "Регистр бухгалтерии",
				fmt.Sprintf("план счетов %q не найден в accounts/", name))
		}
	}
	return issues
}
