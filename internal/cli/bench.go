package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/spf13/cobra"

	"github.com/ivantit66/onebase/internal/bench"
	"github.com/ivantit66/onebase/internal/storage"
)

var benchCmd = &cobra.Command{
	Use:   "bench",
	Short: "Синтетический нагрузочный тест (стиль Gilev): проведение документов",
	Long: `bench гоняет канонический сценарий «создать документ → провести» над
эталонной конфигурацией и выдаёт сравнимый балл: документов/сек, перцентили
латентности и APDEX.

По умолчанию база — временный SQLite (быстрый старт, но это НЕ абсолютная
оценка). Для значимых цифр укажите PostgreSQL через --db. По умолчанию
выполняются два прогона: 1 поток (латентность) и N=NumCPU потоков
(масштабируемость) — как однопоточный и многопоточный тесты Гилёва.`,
	RunE: runBench,
}

func init() {
	benchCmd.Flags().String("db", "", "PostgreSQL DSN (для абсолютной оценки; по умолчанию временный SQLite)")
	benchCmd.Flags().String("sqlite", "", "путь к файлу SQLite (по умолчанию временный файл)")
	benchCmd.Flags().Int("threads", 0, "число потоков; 0 = два прогона: 1 и NumCPU")
	benchCmd.Flags().Duration("duration", 10*time.Second, "длительность измеряемого окна")
	benchCmd.Flags().Int("count", 0, "гнать ровно N операций вместо --duration")
	benchCmd.Flags().Duration("warmup", 2*time.Second, "прогрев перед измерением")
	benchCmd.Flags().Duration("apdex-t", 200*time.Millisecond, "порог T для APDEX")
	benchCmd.Flags().Bool("json", false, "вывод в JSON (для дашборда динамики по версиям)")
}

func runBench(cmd *cobra.Command, _ []string) error {
	ctx := context.Background()

	dsn, _ := cmd.Flags().GetString("db")
	sqlitePath, _ := cmd.Flags().GetString("sqlite")
	threads, _ := cmd.Flags().GetInt("threads")
	duration, _ := cmd.Flags().GetDuration("duration")
	count, _ := cmd.Flags().GetInt("count")
	warmup, _ := cmd.Flags().GetDuration("warmup")
	apdexT, _ := cmd.Flags().GetDuration("apdex-t")
	asJSON, _ := cmd.Flags().GetBool("json")

	// Подключение: PostgreSQL (--db) → SQLite-файл (--sqlite) → временный SQLite.
	var (
		db     *storage.DB
		err    error
		dbKind string
	)
	switch {
	case dsn != "":
		db, err = storage.Connect(ctx, dsn)
		dbKind = "postgres"
	case sqlitePath != "":
		db, err = storage.ConnectSQLite(ctx, sqlitePath)
		dbKind = "sqlite"
	default:
		tmp := filepath.Join(os.TempDir(), fmt.Sprintf("onebase-bench-%d.db", time.Now().UnixNano()))
		db, err = storage.ConnectSQLite(ctx, tmp)
		dbKind = "sqlite(temp)"
		defer removeTemp(tmp)
	}
	if err != nil {
		return fmt.Errorf("bench: подключение к БД: %w", err)
	}
	defer db.Close()

	h, err := bench.NewHarness(ctx, db)
	if err != nil {
		return err
	}

	// Список конфигураций потоков: либо явная, либо [1, NumCPU].
	var threadSet []int
	if threads > 0 {
		threadSet = []int{threads}
	} else {
		threadSet = []int{1}
		if n := runtime.NumCPU(); n > 1 {
			threadSet = append(threadSet, n)
		}
	}

	results := make([]bench.Result, 0, len(threadSet))
	for _, n := range threadSet {
		res, err := h.Run(ctx, bench.Options{
			Threads:  n,
			Duration: duration,
			Count:    count,
			Warmup:   warmup,
			ApdexT:   apdexT,
		})
		if err != nil {
			return err
		}
		results = append(results, res)
	}

	env := benchEnv(dbKind)
	if asJSON {
		return printBenchJSON(env, results)
	}
	printBenchText(env, results)
	return nil
}

type benchEnvInfo struct {
	OnebaseVersion string `json:"onebase_version"`
	GoVersion      string `json:"go_version"`
	OS             string `json:"os"`
	Arch           string `json:"arch"`
	NumCPU         int    `json:"num_cpu"`
	DB             string `json:"db"`
}

func benchEnv(dbKind string) benchEnvInfo {
	ver := "(dev)"
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		ver = bi.Main.Version
	}
	return benchEnvInfo{
		OnebaseVersion: ver,
		GoVersion:      runtime.Version(),
		OS:             runtime.GOOS,
		Arch:           runtime.GOARCH,
		NumCPU:         runtime.NumCPU(),
		DB:             dbKind,
	}
}

func printBenchJSON(env benchEnvInfo, results []bench.Result) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{
		"env":     env,
		"results": results,
	})
}

func printBenchText(env benchEnvInfo, results []bench.Result) {
	outf("onebase bench — проведение документов (эталонная база)\n")
	outf("  версия:  %s (go %s, %s/%s, CPU=%d)\n", env.OnebaseVersion, env.GoVersion, env.OS, env.Arch, env.NumCPU)
	outf("  БД:      %s\n\n", env.DB)

	outf("%-8s %10s %8s %9s %9s %9s %7s\n", "потоки", "док/сек", "ошибки", "p50", "p95", "p99", "APDEX")
	outf("%s\n", "----------------------------------------------------------------------")
	for _, r := range results {
		outf("%-8d %10.1f %8d %9s %9s %9s %7.2f\n",
			r.Threads, r.Throughput, r.Errors,
			ms(r.P50), ms(r.P95), ms(r.P99), r.Apdex)
	}
	outln()
	for _, r := range results {
		outf("  [%d поток(ов)] операций=%d, время=%s, min=%s, среднее=%s, max=%s\n",
			r.Threads, r.Ops, r.Elapsed.Round(time.Millisecond), ms(r.Min), ms(r.Mean), ms(r.Max))
	}
}

// ms форматирует длительность как миллисекунды с двумя знаками.
func ms(d time.Duration) string {
	return fmt.Sprintf("%.2fms", float64(d.Microseconds())/1000)
}
