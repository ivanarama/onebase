package cli

import (
	"bytes"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Ошибка исполнения не должна тащить за собой справку по флагам.
//
// Повод — #1067: `onebase run` падал на миграции, cobra печатала два десятка
// строк usage, и лаунчер показывал в окне хвост лога, где от причины оставалась
// половина строки. Проверяем через rootCmd.Execute (argv → диспетчер cobra):
// признак SilenceUsage выставляется в PersistentPreRunE, вызов RunE напрямую
// этого пути не проходит.
func executeRootArgs(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd, _, err := rootCmd.Find(args)
	if err != nil {
		cmd = rootCmd
	}
	// Execute переиспользует значения локальных и унаследованных флагов.
	// Возвращаем снимок, а не DefValue: соседний тест мог задать свои значения.
	seen := make(map[*pflag.Flag]bool)
	for _, flags := range []*pflag.FlagSet{cmd.Flags(), cmd.PersistentFlags(), cmd.InheritedFlags()} {
		flags.VisitAll(func(flag *pflag.Flag) {
			if seen[flag] {
				return
			}
			seen[flag] = true
			value, changed := flag.Value.String(), flag.Changed
			slice, isSlice := flag.Value.(pflag.SliceValue)
			var items []string
			var sliceState reflect.Value
			if isSlice {
				items = slices.Clone(slice.GetSlice())
				// У списочных pflag.Value приватный changed независим от
				// Flag.Changed. Копируем весь value, сохраняя его указатель
				// на хранилище; элементы по нему восстанавливаем отдельно.
				value := reflect.ValueOf(slice)
				if value.Kind() != reflect.Pointer || value.IsNil() || value.Elem().Kind() != reflect.Struct {
					t.Fatalf("неподдерживаемый списочный Value --%s: %T", flag.Name, slice)
				}
				sliceState = reflect.New(value.Elem().Type()).Elem()
				sliceState.Set(value.Elem())
			}
			t.Cleanup(func() {
				var err error
				if isSlice {
					// String() списка не является входом Set(): скобки и CSV
					// могут исказить пустые значения и элементы с запятыми.
					reflect.ValueOf(slice).Elem().Set(sliceState)
					err = slice.Replace(items)
				} else {
					err = flag.Value.Set(value)
				}
				flag.Changed = changed
				if err != nil {
					t.Errorf("восстановление --%s: %v", flag.Name, err)
				}
			})
		})
	}
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs(args)
	// Признак ставится на исполняемой команде, поэтому соседний тест, дёрнувший
	// PersistentPreRunE прямо с rootCmd, оставляет его включённым на весь
	// процесс прогона. В отдельном процессе CLI это безразлично, здесь —
	// вопрос порядка тестов, поэтому исходное состояние восстанавливается.
	prevSilence := rootCmd.SilenceUsage
	rootCmd.SilenceUsage = false
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SilenceUsage = prevSilence
	})
	err = rootCmd.Execute()
	return out.String(), err
}

// addFailingCommand регистрирует команду, которая заведомо падает в RunE.
// Настоящие команды для этого не годятся: они либо ходят в БД, либо поднимают
// сервер, а проверяется здесь общий для всех путь вывода ошибки.
func addFailingCommand(t *testing.T) *cobra.Command {
	t.Helper()
	failing := &cobra.Command{
		Use: "failing-test-command",
		RunE: func(*cobra.Command, []string) error {
			return errors.New("подготовка базы не удалась")
		},
	}
	failing.Flags().String("вымышленный", "", "флаг ради непустой справки")
	rootCmd.AddCommand(failing)
	t.Cleanup(func() { rootCmd.RemoveCommand(failing) })
	return failing
}

func TestRunErrorDoesNotPrintFlagUsage(t *testing.T) {
	addFailingCommand(t)
	out, err := executeRootArgs(t, "failing-test-command")
	if err == nil {
		t.Fatal("команда обязана вернуть ошибку — иначе тест ничего не проверяет")
	}
	if strings.Contains(out, "Usage:") || strings.Contains(out, "Flags:") {
		t.Errorf("справка по флагам вытесняет причину отказа:\n%s", out)
	}
}

// Обратная сторона: ошибка ВЫЗОВА (неизвестный флаг) — ровно тот случай, когда
// список флагов и нужен. Если бы SilenceUsage выставлялся у rootCmd целиком,
// подсказка пропала бы и здесь.
func TestUnknownFlagStillPrintsUsage(t *testing.T) {
	addFailingCommand(t)
	out, err := executeRootArgs(t, "failing-test-command", "--такого-флага-нет")
	if err == nil {
		t.Fatal("неизвестный флаг принят молча")
	}
	if !strings.Contains(out, "Usage:") {
		t.Errorf("на неизвестный флаг справка обязана печататься:\n%s", out)
	}
}

// Тот же контракт у настоящей команды: `run` — та самая, с которой начался
// #1067. Ошибку даёт несуществующий --id, до открытия порта дело не доходит.
func TestRunCommandErrorHasNoUsage(t *testing.T) {
	out, err := executeRootArgs(t, "run", "--id", "нет-такой-базы", "--no-gui")
	if err == nil {
		t.Fatal("запуск несуществующей базы обязан завершиться ошибкой")
	}
	if strings.Contains(out, "Usage:") || strings.Contains(out, "Global Flags:") {
		t.Errorf("`onebase run` печатает справку вместо причины:\n%s", out)
	}
}

func TestExecuteRootArgsRestoresFlagState(t *testing.T) {
	for _, changed := range []bool{false, true} {
		name := "unchanged"
		if changed {
			name = "changed"
		}
		t.Run(name, func(t *testing.T) {
			cmd := addFailingCommand(t)
			cmd.Flags().String("label", "default", "")
			cmd.Flags().Int("count", 1, "")
			cmd.Flags().Bool("enabled", true, "")
			cmd.Flags().StringSlice("items", []string{"a,b", "c"}, "")
			cmd.Flags().StringArray("entries", []string{"x,y", "z"}, "")
			for flagName, value := range map[string]string{"label": "original", "count": "7", "enabled": "false"} {
				if err := cmd.Flags().Lookup(flagName).Value.Set(value); err != nil {
					t.Fatal(err)
				}
			}
			previous := make(map[string]string)
			cmd.Flags().VisitAll(func(flag *pflag.Flag) {
				flag.Changed = changed
				previous[flag.Name] = flag.Value.String()
			})
			inherited := rootCmd.PersistentFlags().Lookup("no-gui")
			previousValue, previousChanged := inherited.Value.String(), inherited.Changed
			t.Cleanup(func() {
				if err := inherited.Value.Set(previousValue); err != nil {
					t.Error(err)
				}
				inherited.Changed = previousChanged
			})
			if err := inherited.Value.Set("false"); err != nil {
				t.Fatal(err)
			}
			inherited.Changed = changed

			t.Run("execute", func(t *testing.T) {
				_, err := executeRootArgs(t, cmd.Name(), "--label=new", "--count=42",
					"--enabled", "--items=new,values", "--entries=new,value", "--no-gui")
				if err == nil {
					t.Fatal("команда обязана вернуть ошибку")
				}
				label, _ := cmd.Flags().GetString("label")
				if label != "new" || !noGUI {
					t.Fatal("флаги не прошли через настоящий разбор cobra")
				}
			})

			for _, flagName := range []string{"label", "count", "enabled", "items", "entries"} {
				flag := cmd.Flags().Lookup(flagName)
				if flag.Value.String() != previous[flagName] || flag.Changed != changed {
					t.Errorf("--%s после cleanup: value=%s, Changed=%v; ожидались %s, %v",
						flagName, flag.Value.String(), flag.Changed, previous[flagName], changed)
				}
			}
			items, _ := cmd.Flags().GetStringSlice("items")
			entries, _ := cmd.Flags().GetStringArray("entries")
			if !slices.Equal(items, []string{"a,b", "c"}) || !slices.Equal(entries, []string{"x,y", "z"}) {
				t.Errorf("cleanup изменил элементы списков: items=%q, entries=%q", items, entries)
			}
			if noGUI || inherited.Changed != changed {
				t.Errorf("унаследованный --no-gui после cleanup: value=%v, Changed=%v", noGUI, inherited.Changed)
			}
		})
	}
}

// Повторный Execute той же команды должен разбирать списки как до первого
// запуска, включая приватный признак changed в pflag.Value (#2061).
func TestExecuteRootArgsRestoresRepeatedSliceParsing(t *testing.T) {
	for _, defaults := range []struct {
		name  string
		items []string
	}{
		{name: "defaults", items: []string{"default-item"}},
		{name: "empty", items: []string{}},
		{name: "commas", items: []string{"default,item", ""}},
	} {
		t.Run(defaults.name, func(t *testing.T) {
			for _, state := range []struct {
				name            string
				parsed, changed bool
			}{
				{name: "first-parse"},
				{name: "already-parsed", parsed: true, changed: true},
				{name: "only-public-changed", changed: true},
				{name: "only-private-changed", parsed: true},
			} {
				t.Run(state.name, func(t *testing.T) {
					cmd := addFailingCommand(t)
					var items, entries []string
					cmd.Flags().StringSliceVar(&items, "probe-items", slices.Clone(defaults.items), "")
					cmd.Flags().StringArrayVar(&entries, "probe-entries", slices.Clone(defaults.items), "")
					if state.parsed {
						for _, flagName := range []string{"probe-items", "probe-entries"} {
							if err := cmd.Flags().Set(flagName, "seed"); err != nil {
								t.Fatal(err)
							}
						}
					}
					for _, flagName := range []string{"probe-items", "probe-entries"} {
						cmd.Flags().Lookup(flagName).Changed = state.changed
					}
					previousItems, previousEntries := slices.Clone(items), slices.Clone(entries)
					itemValue := cmd.Flags().Lookup("probe-items").Value
					entryValue := cmd.Flags().Lookup("probe-entries").Value
					for _, arg := range []string{"first", "second"} {
						t.Run(arg, func(t *testing.T) {
							_, err := executeRootArgs(t, cmd.Name(), "--probe-items="+arg+",value", "--probe-entries="+arg+",value")
							if err == nil {
								t.Fatal("команда обязана вернуть ошибку")
							}
							wantItems, wantEntries := []string{arg, "value"}, []string{arg + ",value"}
							if state.parsed {
								wantItems = append(slices.Clone(previousItems), wantItems...)
								wantEntries = append(slices.Clone(previousEntries), wantEntries...)
							}
							// Проверяем исходные переменные String*Var: восстановление
							// не должно отвязать Value от хранилища команды.
							if !slices.Equal(items, wantItems) || !slices.Equal(entries, wantEntries) {
								t.Errorf("Execute: items=%q, entries=%q; ожидались %q, %q", items, entries, wantItems, wantEntries)
							}
							if !cmd.Flags().Lookup("probe-items").Changed || !cmd.Flags().Lookup("probe-entries").Changed {
								t.Error("Execute не выставил Changed")
							}
						})
						if !slices.Equal(items, previousItems) || !slices.Equal(entries, previousEntries) {
							t.Errorf("cleanup изменил исходные списки: items=%q, entries=%q", items, entries)
						}
						for _, flagName := range []string{"probe-items", "probe-entries"} {
							if cmd.Flags().Lookup(flagName).Changed != state.changed {
								t.Errorf("cleanup изменил Changed у --%s", flagName)
							}
						}
						if cmd.Flags().Lookup("probe-items").Value != itemValue || cmd.Flags().Lookup("probe-entries").Value != entryValue {
							t.Error("cleanup заменил исходный Value")
						}
					}
				})
			}
		})
	}
}
