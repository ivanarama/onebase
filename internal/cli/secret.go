package cli

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ivantit66/onebase/internal/httpservice"
	"github.com/ivantit66/onebase/internal/llm"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/secrets"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/spf13/cobra"
)

// Команда `onebase secret` (план 83) — обслуживание секретов базы и конфигурации.
//
// Секрет здесь — это ключ ИИ-провайдера, пароль SMTP, токен вебхука или узла
// обмена, ключи S3. Хранить их значением плохо не «в теории»: обычная резервная
// копия снимается с базы целиком (pg_dump на PostgreSQL, VACUUM INTO на SQLite),
// а значит уносит и всё, что лежит в _settings, открытым текстом.
//
// Команда даёт три способа этого избежать: вынести секрет в окружение
// (env:ИМЯ), в файл (file:/путь) или зашифровать значение на мастер-ключе,
// который живёт вне базы (enc:…). Мастер-ключ задаётся переменной
// ONEBASE_MASTER_KEY или файлом ONEBASE_MASTER_KEY_FILE.

var secretCmd = &cobra.Command{
	Use:   "secret",
	Short: "Секреты базы: шифрование, ротация, инвентаризация",
	Long: `Обслуживание секретов (ключи ИИ, токены обмена и вебхуков, пароль SMTP,
креды S3).

Секрет хранится не значением, а ссылкой:

  env:ИМЯ       — переменная окружения процесса;
  file:/путь    — файл (docker/k8s secrets);
  enc:<base64>  — зашифрованное значение; ключ живёт вне базы, в
                  ONEBASE_MASTER_KEY (или ONEBASE_MASTER_KEY_FILE).

Ссылки работают и внутри строки — ${env:ИМЯ}, ${file:/путь}, ${enc:…}, — когда
секрет является частью значения (URL вебхука с токеном, заголовок авторизации).

Примеры:
  onebase secret keygen                                  # новый мастер-ключ
  onebase secret encrypt --stdin < ключ.txt              # enc:… для вставки в YAML
  onebase secret set llm.z_ai.api_key --stdin            # положить в базу зашифрованным
  onebase secret set exchange.token.обмен --generate     # сгенерировать и положить
  onebase secret list --sqlite base.db                   # что где лежит
  onebase secret rotate --new-key-stdin < новый.key      # перешифровать на новый ключ`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

var secretKeygenCmd = &cobra.Command{
	Use:   "keygen",
	Short: "Сгенерировать мастер-ключ",
	Args:  cobra.NoArgs,
	RunE:  runSecretKeygen,
}

var secretEncryptCmd = &cobra.Command{
	Use:   "encrypt",
	Short: "Зашифровать значение и напечатать enc:-ссылку (для вставки в YAML)",
	Args:  cobra.NoArgs,
	RunE:  runSecretEncrypt,
}

var secretSetCmd = &cobra.Command{
	Use:   "set <путь>",
	Short: "Положить секрет в базу зашифрованным",
	Long: `Пути:

  llm.<endpoint>.api_key    — ключ провайдера ИИ в _settings.llm.config;
  exchange.token.<план>     — общий токен плана обмена;
  _settings:<ключ>          — произвольный ключ служебной таблицы.

Значение читается из stdin (--stdin) либо генерируется (--generate) — в
аргументы командной строки секрет не передаётся, иначе он осел бы в history и
в выводе ps.`,
	Args: cobra.ExactArgs(1),
	RunE: runSecretSet,
}

var secretListCmd = &cobra.Command{
	Use:   "list",
	Short: "Показать, где лежат секреты базы и конфигурации и в каком они виде",
	Args:  cobra.NoArgs,
	RunE:  runSecretList,
}

var secretRotateCmd = &cobra.Command{
	Use:   "rotate",
	Short: "Перешифровать enc:-значения базы на новый мастер-ключ",
	Args:  cobra.NoArgs,
	RunE:  runSecretRotate,
}

func init() {
	fs := secretCmd.PersistentFlags()
	fs.String("id", "", "ID базы из реестра ibases")
	fs.String("project", ".", "путь к каталогу конфигурации")
	fs.String("sqlite", "", "путь к файлу SQLite (альтернатива --db)")
	fs.String("db", "", "PostgreSQL DSN (или переменная DATABASE_URL)")

	secretEncryptCmd.Flags().Bool("stdin", false, "прочитать значение из stdin")
	secretSetCmd.Flags().Bool("stdin", false, "прочитать значение из stdin")
	secretSetCmd.Flags().Bool("generate", false, "сгенерировать случайное значение и напечатать его один раз")

	secretRotateCmd.Flags().Bool("new-key-stdin", false, "прочитать новый мастер-ключ из stdin")
	secretRotateCmd.Flags().String("new-key-file", "", "файл с новым мастер-ключом")
	secretRotateCmd.Flags().Bool("dry-run", false, "показать, что будет перешифровано, ничего не меняя")

	secretCmd.AddCommand(secretKeygenCmd, secretEncryptCmd, secretSetCmd, secretListCmd, secretRotateCmd)
	rootCmd.AddCommand(secretCmd)
}

func runSecretKeygen(_ *cobra.Command, _ []string) error {
	key, err := secrets.GenerateKey()
	if err != nil {
		return err
	}
	outln(key.Hex())
	outln("")
	outln("Отпечаток: " + key.ID() + " (им помечены зашифрованные значения)")
	outln("Задайте ключ процессу базы одним из способов:")
	outln("  " + secrets.EnvMasterKey + "=<ключ>")
	outln("  " + secrets.EnvMasterKeyFile + "=/путь/к/файлу")
	outln("")
	outln("Ключ не хранится в базе. Потеряете — enc:-значения не расшифруются;")
	outln("держите копию там же, где остальные учётные данные инфраструктуры.")
	return nil
}

func runSecretEncrypt(cmd *cobra.Command, _ []string) error {
	key, err := masterKeyFromEnv()
	if err != nil {
		return err
	}
	value, _, err := secretValue(cmd)
	if err != nil {
		return err
	}
	ref, err := key.Encrypt(value)
	if err != nil {
		return err
	}
	outln(ref)
	return nil
}

func runSecretSet(cmd *cobra.Command, args []string) error {
	key, err := masterKeyFromEnv()
	if err != nil {
		return err
	}
	value, generated, err := secretValue(cmd)
	if err != nil {
		return err
	}
	ref, err := key.Encrypt(value)
	if err != nil {
		return err
	}

	bc, err := resolveBase(cmd)
	if err != nil {
		return err
	}
	defer bc.Cleanup()
	ctx := context.Background()
	db, err := bc.OpenDB(ctx)
	if err != nil {
		return err
	}
	defer db.Close()

	path := strings.TrimSpace(args[0])
	if err := storeSecret(ctx, db, path, ref); err != nil {
		return err
	}
	if generated {
		// Сгенерированное значение печатается ровно один раз: в базе оно уже
		// зашифровано, и восстановить его оттуда без мастер-ключа нельзя.
		outln("Значение (сохраните, больше не покажем): " + value)
	}
	outf("Секрет %s сохранён зашифрованным (ключ %s).\n", path, key.ID())
	return nil
}

// storeSecret кладёт готовую enc:-ссылку по логическому пути.
func storeSecret(ctx context.Context, db *storage.DB, path, ref string) error {
	switch {
	case strings.HasPrefix(path, "_settings:"):
		key := strings.TrimSpace(strings.TrimPrefix(path, "_settings:"))
		if key == "" {
			return fmt.Errorf("не указан ключ _settings")
		}
		return db.SaveSetting(ctx, key, ref)

	case strings.HasPrefix(path, "exchange.token."):
		plan := strings.TrimSpace(strings.TrimPrefix(path, "exchange.token."))
		if plan == "" {
			return fmt.Errorf("не указан план обмена")
		}
		return db.SaveExchangeToken(ctx, plan, ref)

	case strings.HasPrefix(path, "llm.") && strings.HasSuffix(path, ".api_key"):
		name := strings.TrimSuffix(strings.TrimPrefix(path, "llm."), ".api_key")
		if name == "" {
			return fmt.Errorf("не указан endpoint ИИ")
		}
		cfg, err := db.GetLLMConfig(ctx)
		if err != nil {
			return err
		}
		found := false
		for i := range cfg.Endpoints {
			if strings.EqualFold(cfg.Endpoints[i].Name, name) {
				cfg.Endpoints[i].APIKey = ref
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("в настройках ИИ нет endpoint %q — заведите его в конфигураторе или в app.yaml", name)
		}
		return db.SaveLLMConfig(ctx, cfg)
	}
	return fmt.Errorf("неизвестный путь %q (см. onebase secret set --help)", path)
}

func runSecretList(cmd *cobra.Command, _ []string) error {
	bc, err := resolveBase(cmd)
	if err != nil {
		return err
	}
	defer bc.Cleanup()

	if id, err := secrets.LoadKey(os.Getenv, os.ReadFile); err != nil {
		outf("Мастер-ключ: ОШИБКА — %v\n", err)
	} else if id == nil {
		outln("Мастер-ключ: не задан (enc:-значения не разыменуются)")
	} else {
		outf("Мастер-ключ: задан, отпечаток %s\n", id.ID())
	}

	ctx := context.Background()
	db, err := bc.OpenDB(ctx)
	if err != nil {
		return err
	}
	defer db.Close()

	outln("")
	outln("Секреты базы:")
	rows, err := baseSecrets(ctx, db)
	if err != nil {
		return err
	}
	printSecretRows(rows)

	outln("")
	outf("Секреты конфигурации (%s):\n", bc.Dir)
	rows, err = configSecrets(bc.Dir)
	if err != nil {
		return err
	}
	printSecretRows(rows)
	return nil
}

// secretRow — одна строка инвентаризации: где лежит секрет и что там записано.
//
// Значение хранится сырым, а наружу идёт только его вид (secrets.Describe):
// печатать сам секрет нельзя никогда, но ротации нужно отличить «зашифровано
// старым ключом» от «зашифровано новым», а по описанию этого не сделать.
type secretRow struct {
	Path  string
	Value string
}

func printSecretRows(rows []secretRow) {
	if len(rows) == 0 {
		outln("  (нет)")
		return
	}
	width := 0
	for _, r := range rows {
		if len(r.Path) > width {
			width = len(r.Path)
		}
	}
	for _, r := range rows {
		outf("  %-*s  %s\n", width, r.Path, secrets.Describe(r.Value))
	}
}

// baseSecrets собирает носители секретов из _settings.
func baseSecrets(ctx context.Context, db *storage.DB) ([]secretRow, error) {
	carriers, err := db.SecretCarriers(ctx)
	if err != nil {
		return nil, err
	}
	var out []secretRow
	for _, c := range carriers {
		out = append(out, secretRow{c.Path, c.Value})
	}
	// Произвольные ключи, положенные через `secret set _settings:…`: носителями
	// их не считает никто, кроме администратора, — показываем по факту шифрования.
	entries, err := db.ListSettings(ctx)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if secrets.Classify(e.Value) == secrets.KindEnc {
			out = append(out, secretRow{"_settings:" + e.Key, e.Value})
		}
	}
	return out, nil
}

// configSecrets собирает носители секретов из файлов конфигурации. Читаем их
// напрямую загрузчиками — он оставляет ссылки нераскрытыми (план 83), так что
// в отчёт попадает именно то, что записано в YAML.
func configSecrets(dir string) ([]secretRow, error) {
	var out []secretRow
	appCfg, err := project.LoadConfig(dir)
	if err != nil {
		return nil, err
	}
	if appCfg.Email != nil && appCfg.Email.SMTPPass != "" {
		out = append(out, secretRow{"email.smtp_password", appCfg.Email.SMTPPass})
	}
	if appCfg.LLM != nil {
		for _, ep := range appCfg.LLM.Endpoints {
			if ep.APIKey != "" {
				out = append(out, secretRow{"llm." + ep.Name + ".api_key", ep.APIKey})
			}
		}
	}
	if appCfg.Backup != nil && appCfg.Backup.S3 != nil {
		out = append(out, s3Rows("backup.s3", appCfg.Backup.S3)...)
	}
	if appCfg.FileStorage != nil && appCfg.FileStorage.S3 != nil {
		out = append(out, s3Rows("file_storage.s3", appCfg.FileStorage.S3)...)
	}
	for _, h := range appCfg.Webhooks {
		for name, v := range h.Headers {
			if secrets.Classify(v) != secrets.KindEmpty {
				out = append(out, secretRow{"webhook." + h.Name + ".headers." + name, v})
			}
		}
	}
	services, err := httpservice.LoadDir(filepath.Join(dir, "services"))
	if err == nil {
		for _, s := range services {
			if s.Secret != "" {
				out = append(out, secretRow{"service." + s.Name + ".secret", s.Secret})
			}
		}
	}
	intakes, err := metadata.LoadIntakeDir(filepath.Join(dir, "intake"))
	if err == nil {
		for _, in := range intakes {
			if in.Secret != "" {
				out = append(out, secretRow{"intake." + in.Name + ".secret", in.Secret})
			}
		}
	}
	return out, nil
}

func s3Rows(prefix string, s3 *project.S3Config) []secretRow {
	var out []secretRow
	if s3.AccessKey != "" {
		out = append(out, secretRow{prefix + ".access_key", s3.AccessKey})
	}
	if s3.SecretKey != "" {
		out = append(out, secretRow{prefix + ".secret_key", s3.SecretKey})
	}
	return out
}

func runSecretRotate(cmd *cobra.Command, _ []string) error {
	oldKey, err := masterKeyFromEnv()
	if err != nil {
		return err
	}
	newKey, err := newMasterKey(cmd)
	if err != nil {
		return err
	}
	if newKey.ID() == oldKey.ID() {
		return fmt.Errorf("новый мастер-ключ совпадает со старым — ротация не нужна")
	}
	dry, _ := cmd.Flags().GetBool("dry-run")

	bc, err := resolveBase(cmd)
	if err != nil {
		return err
	}
	defer bc.Cleanup()
	ctx := context.Background()
	db, err := bc.OpenDB(ctx)
	if err != nil {
		return err
	}
	defer db.Close()

	entries, err := db.ListSettings(ctx)
	if err != nil {
		return err
	}
	changed := 0
	for _, e := range entries {
		if e.Key == "llm.config" {
			n, value, err := rotateLLMConfig(e.Value, oldKey, newKey)
			if err != nil {
				return fmt.Errorf("llm.config: %w", err)
			}
			if n == 0 {
				continue
			}
			changed += n
			outf("  llm.config: %d значен. → ключ %s\n", n, newKey.ID())
			if !dry {
				if err := db.SaveSetting(ctx, e.Key, value); err != nil {
					return err
				}
			}
			continue
		}
		if secrets.Classify(e.Value) != secrets.KindEnc {
			continue
		}
		reenc, err := reencrypt(e.Value, oldKey, newKey)
		if err != nil {
			return fmt.Errorf("%s: %w", e.Key, err)
		}
		if reenc == "" {
			continue // уже под новым ключом
		}
		changed++
		outf("  %s → ключ %s\n", e.Key, newKey.ID())
		if !dry {
			if err := db.SaveSetting(ctx, e.Key, reenc); err != nil {
				return err
			}
		}
	}

	// Файлы конфигурации команда не трогает — и не должна: YAML лежит в git и в
	// поставке клиенту, переписывать его за администратора нельзя. Но и молчать
	// нельзя: ниже мы советуем сменить мастер-ключ процесса, а `secret encrypt`
	// прямо предлагает класть enc: в YAML. Смена ключа без этого предупреждения
	// оставляет такие значения нечитаемыми — и узнаётся это уже по отвалившейся
	// почте или выбывшей из профиля модели ИИ, а не здесь.
	warnConfigSecretsUnderOldKey(bc.Dir, newKey)

	if changed == 0 {
		outln("Перешифровывать нечего: enc:-значений под старым ключом в базе нет.")
		return nil
	}
	if dry {
		outf("Будет перешифровано значений: %d (это пробный прогон, база не изменена).\n", changed)
		return nil
	}
	outf("Перешифровано значений: %d.\n", changed)
	outln("Замените мастер-ключ процесса базы на новый и перезапустите её —")
	outln("старый ключ больше не откроет эти значения.")
	return nil
}

// configSecretsNotUnderKey возвращает enc:-значения файлов конфигурации,
// которые заданным ключом не откроются.
//
// Отбор по отпечатку, а не по «зашифровано старым»: значение под третьим, давно
// забытым ключом после смены мастер-ключа не откроется ровно так же. А уже
// перешифрованное вручную молчит — иначе повторный прогон ротации пугал бы
// администратора тем, что он только что починил.
func configSecretsNotUnderKey(dir string, key *secrets.Key) ([]secretRow, error) {
	rows, err := configSecrets(dir)
	if err != nil {
		return nil, err
	}
	var stale []secretRow
	for _, r := range rows {
		if secrets.Classify(r.Value) != secrets.KindEnc {
			continue
		}
		if id, ok := secrets.RefKeyID(r.Value); ok && id == key.ID() {
			continue
		}
		stale = append(stale, r)
	}
	return stale, nil
}

// warnConfigSecretsUnderOldKey печатает это предупреждение.
//
// Ошибка чтения конфигурации наверх не возвращается: база к этому моменту уже
// перешифрована, и ронять команду из-за нечитаемого app.yaml значило бы
// закончить успешную операцию ненулевым кодом. Но и проглатывать нельзя —
// администратор должен знать, что конфигурацию проверить не удалось.
func warnConfigSecretsUnderOldKey(dir string, newKey *secrets.Key) {
	stale, err := configSecretsNotUnderKey(dir, newKey)
	if err != nil {
		outf("\nВнимание: конфигурацию (%s) проверить не удалось: %v\n", dir, err)
		outln("Проверьте enc:-значения в YAML вручную — эта команда их не перешифровывает.")
		return
	}
	if len(stale) == 0 {
		return
	}
	outf("\nВнимание: в конфигурации (%s) есть enc:-значения, зашифрованные не новым ключом.\n", dir)
	outln("Эта команда перешифровывает только базу — файлы остаются как есть:")
	printSecretRows(stale)
	outln("Перешифруйте их до смены мастер-ключа процесса:")
	outln("  ONEBASE_MASTER_KEY=<новый> onebase secret encrypt --stdin")
	outln("и замените значения в YAML — иначе подсистемы, которым они нужны, выключатся.")
}

// rotateLLMConfig перешифровывает enc:-значения внутри JSON настроек ИИ.
// Возвращает число изменённых значений и новый JSON.
func rotateLLMConfig(raw string, oldKey, newKey *secrets.Key) (int, string, error) {
	cfg, err := llm.ParseConfig(raw)
	if err != nil {
		return 0, "", err
	}
	changed := 0
	for i := range cfg.Endpoints {
		reenc, err := reencrypt(cfg.Endpoints[i].APIKey, oldKey, newKey)
		if err != nil {
			return 0, "", fmt.Errorf("endpoint %s: %w", cfg.Endpoints[i].Name, err)
		}
		if reenc != "" {
			cfg.Endpoints[i].APIKey = reenc
			changed++
		}
		for h, v := range cfg.Endpoints[i].Headers {
			reenc, err := reencrypt(v, oldKey, newKey)
			if err != nil {
				return 0, "", fmt.Errorf("endpoint %s, заголовок %s: %w", cfg.Endpoints[i].Name, h, err)
			}
			if reenc != "" {
				cfg.Endpoints[i].Headers[h] = reenc
				changed++
			}
		}
	}
	if changed == 0 {
		return 0, raw, nil
	}
	out, err := cfg.JSON()
	if err != nil {
		return 0, "", err
	}
	return changed, out, nil
}

// reencrypt перешифровывает одно значение. Возвращает "" (без ошибки), если
// перешифровывать нечего: значение не enc: или уже под новым ключом — так
// повторный запуск ротации безопасен.
func reencrypt(value string, oldKey, newKey *secrets.Key) (string, error) {
	if secrets.Classify(value) != secrets.KindEnc {
		return "", nil
	}
	if id, ok := secrets.RefKeyID(value); ok && id == newKey.ID() {
		return "", nil
	}
	plain, err := oldKey.Decrypt(value)
	if err != nil {
		return "", err
	}
	return newKey.Encrypt(plain)
}

// masterKeyFromEnv читает текущий мастер-ключ, требуя его наличия.
func masterKeyFromEnv() (*secrets.Key, error) {
	key, err := secrets.LoadKey(os.Getenv, os.ReadFile)
	if err != nil {
		return nil, err
	}
	if key == nil {
		return nil, fmt.Errorf("мастер-ключ не задан: задайте %s или %s (новый ключ — onebase secret keygen)",
			secrets.EnvMasterKey, secrets.EnvMasterKeyFile)
	}
	return key, nil
}

// newMasterKey читает ключ, на который ротируем: stdin, файл или переменная
// ONEBASE_NEW_MASTER_KEY. Аргументом командной строки ключ не принимается —
// он остался бы в history и в выводе ps.
func newMasterKey(cmd *cobra.Command) (*secrets.Key, error) {
	fromStdin, _ := cmd.Flags().GetBool("new-key-stdin")
	file, _ := cmd.Flags().GetString("new-key-file")
	switch {
	case fromStdin && file != "":
		return nil, fmt.Errorf("--new-key-stdin и --new-key-file взаимоисключающи")
	case fromStdin:
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fmt.Errorf("чтение ключа из stdin: %w", err)
		}
		return secrets.ParseKey(string(b))
	case file != "":
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("чтение ключа из файла: %w", err)
		}
		return secrets.ParseKey(string(b))
	}
	if v := os.Getenv(envNewMasterKey); strings.TrimSpace(v) != "" {
		return secrets.ParseKey(v)
	}
	return nil, fmt.Errorf("новый мастер-ключ не задан: --new-key-stdin, --new-key-file или переменная %s", envNewMasterKey)
}

// envNewMasterKey — переменная с ключом-получателем ротации.
const envNewMasterKey = "ONEBASE_NEW_MASTER_KEY"

// secretValue читает значение секрета: из stdin или сгенерированное. Вторым
// значением возвращает признак генерации (тогда значение печатается один раз).
func secretValue(cmd *cobra.Command) (string, bool, error) {
	fromStdin, _ := cmd.Flags().GetBool("stdin")
	gen, _ := cmd.Flags().GetBool("generate")
	switch {
	case fromStdin && gen:
		return "", false, fmt.Errorf("--stdin и --generate взаимоисключающи")
	case fromStdin:
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", false, fmt.Errorf("чтение значения из stdin: %w", err)
		}
		v := strings.TrimRight(string(b), "\r\n")
		if v == "" {
			return "", false, fmt.Errorf("пустое значение из stdin")
		}
		return v, false, nil
	case gen:
		v, err := generateSecretValue()
		return v, true, err
	}
	return "", false, fmt.Errorf("укажите источник значения: --stdin или --generate")
}

// generateSecretValue возвращает случайный токен (256 бит в base64url) — годится
// для общего секрета обмена, токена вебхука и подобных «просто длинных строк».
func generateSecretValue() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("генерация значения: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
