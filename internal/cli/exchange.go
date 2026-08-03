package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/exchange"
	"github.com/ivantit66/onebase/internal/fsmode"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/ui"
	"github.com/spf13/cobra"
)

var exchangeCmd = &cobra.Command{
	Use:   "exchange",
	Short: "Обмен данными между базами OneBase (планы обмена, план 86)",
	Long: `Файловый обмен данными между базами OneBase по плану обмена.

Цикл: на каждой базе задаётся её узел (exchange init), затем изменения
выгружаются в файл пакета (.obx) для узла-получателя (exchange dump) и
загружаются на приёмнике (exchange load). Повторная загрузка идемпотентна.`,
}

var exchangeInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Задать код текущего узла базы для плана обмена",
	RunE:  runExchangeInit,
}

var exchangeDumpCmd = &cobra.Command{
	Use:   "dump",
	Short: "Выгрузить изменения для узла-получателя в файл пакета (.obx)",
	RunE:  runExchangeDump,
}

var exchangeLoadCmd = &cobra.Command{
	Use:   "load",
	Short: "Загрузить пакет обмена (.obx) в базу",
	RunE:  runExchangeLoad,
}

var exchangeStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Показать состояние обмена по плану: очередь и счётчики по узлам",
	RunE:  runExchangeStatus,
}

var exchangeSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Обменяться изменениями с узлом по сети (push + pull)",
	RunE:  runExchangeSync,
}

func init() {
	addBaseFlags(exchangeInitCmd)
	exchangeInitCmd.Flags().String("plan", "", "имя плана обмена (обязательно)")
	exchangeInitCmd.Flags().String("node", "", "код текущего узла (обязательно)")
	exchangeInitCmd.Flags().String("token", "", "общий токен плана для онлайн-обмена (одинаковый на всех участниках)")
	exchangeInitCmd.Flags().String("token-file", "", "прочитать токен онлайн-обмена из файла (предпочтительнее --token)")

	addBaseFlags(exchangeDumpCmd)
	exchangeDumpCmd.Flags().String("plan", "", "имя плана обмена (обязательно)")
	exchangeDumpCmd.Flags().String("to", "", "код узла-получателя (обязательно)")
	exchangeDumpCmd.Flags().String("out", "", "путь к файлу пакета .obx (обязательно)")

	addBaseFlags(exchangeLoadCmd)
	exchangeLoadCmd.Flags().String("in", "", "путь к файлу пакета .obx (обязательно)")

	addBaseFlags(exchangeStatusCmd)
	exchangeStatusCmd.Flags().String("plan", "", "имя плана обмена (обязательно)")

	addBaseFlags(exchangeSyncCmd)
	exchangeSyncCmd.Flags().String("plan", "", "имя плана обмена (обязательно)")
	exchangeSyncCmd.Flags().String("with", "", "код узла-партнёра для обмена по сети (обязательно)")

	exchangeCmd.AddCommand(exchangeInitCmd, exchangeDumpCmd, exchangeLoadCmd, exchangeStatusCmd, exchangeSyncCmd)
	rootCmd.AddCommand(exchangeCmd)
}

// openExchangeBase разрешает базу, открывает БД и грузит конфигурацию — общая
// часть всех подкоманд exchange.
func openExchangeBase(cmd *cobra.Command) (*baseConfig, *storageAndProject, error) {
	bc, err := resolveBase(cmd)
	if err != nil {
		return nil, nil, err
	}
	ctx := context.Background()
	db, err := bc.OpenDB(ctx)
	if err != nil {
		bc.Cleanup()
		return nil, nil, err
	}
	proj, err := project.Load(bc.Dir)
	if err != nil {
		db.Close()
		bc.Cleanup()
		return nil, nil, fmt.Errorf("load project: %w", err)
	}
	if err := db.EnsureExchangeSchema(ctx); err != nil {
		proj.Close()
		db.Close()
		bc.Cleanup()
		return nil, nil, err
	}
	return bc, &storageAndProject{db: db, proj: proj, ctx: ctx}, nil
}

type storageAndProject struct {
	db     *storage.DB
	proj   *project.Project
	ctx    context.Context
	reg    *runtime.Registry
	interp *interpreter.Interpreter
}

func (sp *storageAndProject) Close() {
	sp.proj.Close()
	sp.db.Close()
}

// ensureRuntime лениво строит реестр (сущности + модули + планы) и интерпретатор
// — нужны сборке/загрузке пакета (GetEntity) и обработчику конфликта hook.
func (sp *storageAndProject) ensureRuntime() {
	if sp.reg != nil {
		return
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: sp.proj.Entities})
	reg.LoadModules(sp.proj.Modules)
	reg.LoadExchangePlans(sp.proj.ExchangePlans)
	interp := interpreter.New()
	interp.LookupProc = reg.GetModuleProc
	interp.LookupSiblingProc = reg.GetSiblingProc
	interp.LookupModuleProc = reg.GetModuleNamespacedProc
	sp.reg = reg
	sp.interp = interp
}

// resolver возвращает реестр для сборки/загрузки пакета (GetEntity).
func (sp *storageAndProject) resolver() *runtime.Registry {
	sp.ensureRuntime()
	return sp.reg
}

// hook возвращает обработчик правила конфликта hook (запуск ПриКонфликтеОбмена).
func (sp *storageAndProject) hook() exchange.HookResolver {
	sp.ensureRuntime()
	return ui.NewExchangeHook(sp.db, sp.reg, sp.interp)
}

func runExchangeInit(cmd *cobra.Command, _ []string) error {
	planName, _ := cmd.Flags().GetString("plan")
	node, _ := cmd.Flags().GetString("node")
	if planName == "" || node == "" {
		return fmt.Errorf("укажите --plan <имя плана> и --node <код узла>")
	}
	bc, sp, err := openExchangeBase(cmd)
	if err != nil {
		return err
	}
	defer bc.Cleanup()
	defer sp.Close()

	plan := findExchangePlan(sp.proj, planName)
	if plan == nil {
		return fmt.Errorf("план обмена %q не найден в конфигурации", planName)
	}
	if plan.Node(node) == nil {
		return fmt.Errorf("узел %q не описан в плане %q (узлы: %s)", node, plan.Name, nodeCodes(plan))
	}
	if err := sp.db.SaveExchangeThisNode(sp.ctx, plan.Name, node); err != nil {
		return err
	}
	outf("Текущий узел для плана %q: %s\n", plan.Name, node)
	token, err := exchangeInitToken(cmd)
	if err != nil {
		return err
	}
	if token != "" {
		if err := sp.db.SaveExchangeToken(sp.ctx, plan.Name, token); err != nil {
			return err
		}
		outln("Токен онлайн-обмена сохранён.")
	}
	return nil
}

func exchangeInitToken(cmd *cobra.Command) (string, error) {
	if path, _ := cmd.Flags().GetString("token-file"); strings.TrimSpace(path) != "" {
		f, err := os.Open(path)
		if err != nil {
			return "", fmt.Errorf("чтение token-file: %w", err)
		}
		defer closeRead("файл обмена", f)
		const maxTokenBytes = 64 << 10
		data, err := io.ReadAll(io.LimitReader(f, maxTokenBytes+1))
		if err != nil {
			return "", fmt.Errorf("чтение token-file: %w", err)
		}
		if len(data) > maxTokenBytes {
			return "", fmt.Errorf("token-file превышает лимит %d байт", maxTokenBytes)
		}
		return strings.TrimSpace(string(data)), nil
	}
	token, _ := cmd.Flags().GetString("token")
	if strings.TrimSpace(token) != "" {
		fmt.Fprintln(os.Stderr, "ПРЕДУПРЕЖДЕНИЕ: --token виден в истории shell; используйте --token-file или ONEBASE_EXCHANGE_TOKEN.")
		return strings.TrimSpace(token), nil
	}
	return strings.TrimSpace(os.Getenv("ONEBASE_EXCHANGE_TOKEN")), nil
}

func writeExchangePackage(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, fsmode.Dir); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()
	if err = tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmpName, path); err != nil {
		return err
	}
	if d, openErr := os.Open(dir); openErr == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func readExchangePackage(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer closeRead("файл обмена", f)
	data, err := io.ReadAll(io.LimitReader(f, exchange.MaxPackageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > exchange.MaxPackageBytes {
		return nil, fmt.Errorf("пакет превышает лимит %d байт", exchange.MaxPackageBytes)
	}
	return data, nil
}

func runExchangeDump(cmd *cobra.Command, _ []string) error {
	planName, _ := cmd.Flags().GetString("plan")
	to, _ := cmd.Flags().GetString("to")
	out, _ := cmd.Flags().GetString("out")
	if planName == "" || to == "" || out == "" {
		return fmt.Errorf("укажите --plan <имя>, --to <код узла> и --out <файл>")
	}
	bc, sp, err := openExchangeBase(cmd)
	if err != nil {
		return err
	}
	defer bc.Cleanup()
	defer sp.Close()

	plan := findExchangePlan(sp.proj, planName)
	if plan == nil {
		return fmt.Errorf("план обмена %q не найден в конфигурации", planName)
	}
	if plan.Node(to) == nil {
		return fmt.Errorf("узел-получатель %q не описан в плане %q (узлы: %s)", to, plan.Name, nodeCodes(plan))
	}
	data, err := exchange.BuildPackage(sp.ctx, sp.db, sp.resolver(), plan, to)
	if err != nil {
		return err
	}
	if err := writeExchangePackage(out, data); err != nil {
		return fmt.Errorf("запись пакета: %w", err)
	}
	pkg, _ := exchange.ParsePackage(data)
	outf("План %q → узел %q: выгружено объектов %d (сообщение №%d) в %s\n",
		plan.Name, to, len(pkg.Objects), pkg.MessageNo, out)
	return nil
}

func runExchangeLoad(cmd *cobra.Command, _ []string) error {
	in, _ := cmd.Flags().GetString("in")
	if in == "" {
		return fmt.Errorf("укажите --in <файл пакета>")
	}
	data, err := readExchangePackage(in)
	if err != nil {
		return fmt.Errorf("чтение пакета: %w", err)
	}
	pkg, err := exchange.ParsePackage(data)
	if err != nil {
		return err
	}
	bc, sp, err := openExchangeBase(cmd)
	if err != nil {
		return err
	}
	defer bc.Cleanup()
	defer sp.Close()

	plan := findExchangePlan(sp.proj, pkg.Plan)
	if plan == nil {
		return fmt.Errorf("план обмена %q из пакета не найден в конфигурации приёмника", pkg.Plan)
	}
	// Headless-загрузка без интерпретатора: правило hook (если задано) откатится
	// к by_time внутри движка.
	res, err := exchange.ApplyPackage(sp.ctx, sp.db, sp.resolver(), plan, data, exchange.ApplyOptions{Hook: sp.hook()})
	if err != nil {
		return err
	}
	outf("Пакет плана %q от узла %q (сообщение №%d): применено %d, пропущено %d, удалено %d, конфликтов %d\n",
		plan.Name, pkg.FromNode, pkg.MessageNo, res.Applied, res.Skipped, res.Deleted, res.Conflicts)
	return nil
}

func runExchangeStatus(cmd *cobra.Command, _ []string) error {
	planName, _ := cmd.Flags().GetString("plan")
	if planName == "" {
		return fmt.Errorf("укажите --plan <имя плана>")
	}
	bc, sp, err := openExchangeBase(cmd)
	if err != nil {
		return err
	}
	defer bc.Cleanup()
	defer sp.Close()

	plan := findExchangePlan(sp.proj, planName)
	if plan == nil {
		return fmt.Errorf("план обмена %q не найден в конфигурации", planName)
	}
	thisNode, err := sp.db.GetExchangeThisNode(sp.ctx, plan.Name)
	if err != nil {
		return err
	}
	counts, err := sp.db.ExchangePendingCounts(sp.ctx, plan.Name)
	if err != nil {
		return err
	}

	outf("План обмена: %s\n", plan.Name)
	if thisNode == "" {
		outln("Текущий узел: не задан — выполните `onebase exchange init`")
	} else {
		outf("Текущий узел: %s\n", thisNode)
	}
	outln("\nУзлы (очередь = ждут выгрузки; отпр./подтв. — номера сообщений):")
	for _, n := range plan.Nodes {
		peer, err := sp.db.GetExchangePeer(sp.ctx, plan.Name, n.Code)
		if err != nil {
			return err
		}
		mark := ""
		if thisNode != "" && strings.EqualFold(n.Code, thisNode) {
			mark = " ← этот узел"
		}
		outf("  %-10s %-22s очередь=%d отпр.=%d подтв.=%d принято=%d%s\n",
			n.Code, n.Name, counts[n.Code], peer.SentNo, peer.AckNo, peer.RecvNo, mark)
	}
	return nil
}

func runExchangeSync(cmd *cobra.Command, _ []string) error {
	planName, _ := cmd.Flags().GetString("plan")
	with, _ := cmd.Flags().GetString("with")
	if planName == "" || with == "" {
		return fmt.Errorf("укажите --plan <имя> и --with <код узла-партнёра>")
	}
	bc, sp, err := openExchangeBase(cmd)
	if err != nil {
		return err
	}
	defer bc.Cleanup()
	defer sp.Close()

	plan := findExchangePlan(sp.proj, planName)
	if plan == nil {
		return fmt.Errorf("план обмена %q не найден в конфигурации", planName)
	}
	peer := plan.Node(with)
	if peer == nil {
		return fmt.Errorf("узел %q не описан в плане %q (узлы: %s)", with, plan.Name, nodeCodes(plan))
	}
	if strings.TrimSpace(peer.URL) == "" {
		return fmt.Errorf("у узла %q нет url — онлайн-обмен недоступен (задайте nodes[].url в плане)", with)
	}
	token, err := sp.db.GetExchangeToken(sp.ctx, plan.Name)
	if err != nil {
		return err
	}
	if token == "" {
		return fmt.Errorf("токен плана не задан — выполните `onebase exchange init --token-file <файл>` или задайте ONEBASE_EXCHANGE_TOKEN")
	}
	thisNode, err := sp.db.GetExchangeThisNode(sp.ctx, plan.Name)
	if err != nil {
		return err
	}
	if thisNode == "" {
		return fmt.Errorf("узел этой базы не задан — выполните `onebase exchange init --node <код>`")
	}

	pushRes, loadRes, err := exchange.SyncWithNode(sp.ctx, sp.db, sp.resolver(), plan, thisNode, with, peer.URL, token, exchange.ApplyOptions{Hook: sp.hook()})
	if err != nil {
		return err
	}

	outf("Синхронизация с %q (%s):\n", with, peer.URL)
	outf("  отправлено → у партнёра применено %d, пропущено %d, конфликтов %d\n",
		pushRes.Applied+pushRes.Deleted, pushRes.Skipped, pushRes.Conflicts)
	outf("  получено → у нас применено %d, пропущено %d, конфликтов %d\n",
		loadRes.Applied+loadRes.Deleted, loadRes.Skipped, loadRes.Conflicts)
	return nil
}

func findExchangePlan(proj *project.Project, name string) *metadata.ExchangePlan {
	for _, p := range proj.ExchangePlans {
		if strings.EqualFold(p.Name, name) {
			return p
		}
	}
	return nil
}

func nodeCodes(plan *metadata.ExchangePlan) string {
	codes := make([]string, 0, len(plan.Nodes))
	for _, n := range plan.Nodes {
		codes = append(codes, n.Code)
	}
	return strings.Join(codes, ", ")
}
