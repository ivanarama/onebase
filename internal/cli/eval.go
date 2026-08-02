package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/spf13/cobra"
)

var evalCmd = &cobra.Command{
	Use:   "eval <выражение|snippet>",
	Short: "Выполнить фрагмент DSL в песочнице",
	Long: `Выполняет выражение или тело временной функции DSL с RestrictedProfile:
сеть, файлы, команды ОС и ИИ-вызовы запрещены. По умолчанию аргумент считается
выражением и оборачивается в Возврат <expr>. Для многострочного кода используйте
--snippet и явный Возврат.`,
	Args:          cobra.MaximumNArgs(1),
	RunE:          runEval,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	addBaseFlags(evalCmd)
	evalCmd.Flags().String("file", "", "прочитать выражение/snippet из файла")
	evalCmd.Flags().Bool("snippet", false, "считать ввод телом временной функции")
	evalCmd.Flags().Bool("json", false, "вывести результат и сообщения в JSON")
	rootCmd.AddCommand(evalCmd)
}

func runEval(cmd *cobra.Command, args []string) error {
	src, err := evalTextFromFlags(cmd, args)
	if err != nil {
		return err
	}
	src = strings.TrimSpace(src)
	if src == "" {
		return fmt.Errorf("пустой DSL-фрагмент")
	}
	snippet, _ := cmd.Flags().GetBool("snippet")
	wrapped := wrapEvalSource(src, snippet)
	prog, err := parser.New(lexer.New(wrapped, "<eval>")).ParseProgram()
	if err != nil {
		return err
	}
	if len(prog.Procedures) == 0 {
		return fmt.Errorf("внутренняя ошибка eval: процедура не создана")
	}
	proc := prog.Procedures[0]

	bc, err := resolveBase(cmd)
	if err != nil {
		return err
	}
	defer bc.Cleanup()

	proj, err := project.Load(bc.Dir)
	if err != nil {
		return fmt.Errorf("load project: %w", err)
	}
	defer proj.Close()

	reg := buildRuntimeRegistry(proj)
	interp := interpreter.New()
	interp.LookupProc = reg.GetModuleProc
	interp.LookupSiblingProc = reg.GetSiblingProc
	interp.LookupModuleProc = reg.GetModuleNamespacedProc
	appCfg, err := project.LoadConfig(proj.Dir)
	if err != nil {
		return fmt.Errorf("load app config: %w", err)
	}
	interp.StrictLexicalScope = appDSLStrictLexicalScope(appCfg)

	messages := []string{}
	msgFunc := interpreter.BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
		if len(args) > 0 {
			messages = append(messages, fmt.Sprintf("%v", args[0]))
		}
		return nil, nil
	})
	extra := map[string]any{
		"Сообщить": msgFunc,
		"Message":  msgFunc,
	}

	var dbClose func()
	if evalShouldOpenDB(cmd) {
		db, err := bc.OpenDB(context.Background())
		if err != nil {
			return err
		}
		dbClose = func() { db.Close() }
		factory := interpreter.NewQueryFactory(context.Background(), db, reg)
		extra["__factory_Запрос"] = factory
		extra["__factory_Query"] = factory
	}
	if dbClose != nil {
		defer dbClose()
	}

	var result any
	if err := interp.RunSandboxed(proc, nil, interpreter.RestrictedProfile(), &result, extra); err != nil {
		return err
	}

	jsonOut, _ := cmd.Flags().GetBool("json")
	payload := map[string]any{"result": dslJSONValue(result)}
	if len(messages) > 0 {
		payload["messages"] = messages
	}
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(payload)
	}
	for _, m := range messages {
		outln(m)
	}
	if result != nil {
		outln(formatCLIValue(result))
	}
	return nil
}

func evalTextFromFlags(cmd *cobra.Command, args []string) (string, error) {
	path, _ := cmd.Flags().GetString("file")
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	if len(args) > 0 {
		return args[0], nil
	}
	return "", fmt.Errorf("укажите выражение/snippet аргументом или через --file")
}

func wrapEvalSource(src string, snippet bool) string {
	if snippet {
		return "Функция __Eval()\n" + src + "\nКонецФункции"
	}
	return "Функция __Eval()\nВозврат " + strings.TrimSuffix(src, ";") + ";\nКонецФункции"
}

func evalShouldOpenDB(cmd *cobra.Command) bool {
	return cmd.Flags().Changed("id") || cmd.Flags().Changed("sqlite") || cmd.Flags().Changed("db")
}

func dslJSONValue(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case *interpreter.Array:
		items := t.Iterate()
		out := make([]any, len(items))
		for i, item := range items {
			out[i] = dslJSONValue(item)
		}
		return out
	case *interpreter.Struct:
		out := map[string]any{}
		for _, k := range t.Fields() {
			out[k] = dslJSONValue(t.Get(k))
		}
		return out
	case *interpreter.Map:
		out := map[string]any{}
		for _, k := range t.Keys() {
			out[fmt.Sprintf("%v", k)] = dslJSONValue(t.Get(k))
		}
		return out
	case *interpreter.Ref:
		return map[string]any{"uuid": t.UUID, "name": t.Name, "type": t.Type}
	default:
		return t
	}
}
