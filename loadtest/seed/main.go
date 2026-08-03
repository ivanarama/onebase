// Command seed наполняет запущенную базу onebase данными через REST API для
// последующего нагрузочного тестирования k6: создаёт N контрагентов и
// (опционально) M документов поступления, затем пишет список id контрагентов
// в JSON-файл, который k6-сценарии читают через open().
//
// Сидер ходит по тому же REST-контракту, что и реальные клиенты (в т.ч.
// проведение документа одним POST через __action), поэтому данные после сидинга
// неотличимы от «настоящих». Рассчитан на examples/minimal (сущности
// Контрагент / Поступление); под другой конфиг поменяйте имена сущностей и поля.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	oblog "github.com/ivantit66/onebase/internal/logging"
	"io"
	"math/rand"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"time"
)

func main() {
	var (
		base           = flag.String("url", "http://localhost:8080", "базовый URL запущенного onebase")
		counterparties = flag.Int("counterparties", 200, "сколько контрагентов создать")
		documents      = flag.Int("documents", 500, "сколько документов поступления создать и провести (0 — не создавать)")
		login          = flag.String("login", "", "логин для аутентификации (если в базе есть пользователи)")
		password       = flag.String("password", "", "пароль")
		out            = flag.String("out", "counterparties.json", "файл для списка id контрагентов (для k6 open())")
	)
	flag.Parse()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Timeout: 30 * time.Second, Jar: jar}

	if *login != "" {
		if err := doLogin(client, *base, *login, *password); err != nil {
			fail("login: %v", err)
		}
		fmt.Printf("аутентификация ок: %s\n", *login)
	}

	// 1. Контрагенты.
	ids := make([]string, 0, *counterparties)
	for i := 0; i < *counterparties; i++ {
		id, err := createCounterparty(client, *base, i)
		if err != nil {
			fail("создание контрагента %d: %v", i, err)
		}
		ids = append(ids, id)
		progress("контрагенты", i+1, *counterparties)
	}
	fmt.Println()

	if err := writeJSON(*out, ids); err != nil {
		fail("запись %s: %v", *out, err)
	}
	fmt.Printf("записано %d id контрагентов → %s\n", len(ids), *out)

	// 2. Документы поступления (создаём и сразу проводим).
	if *documents > 0 && len(ids) > 0 {
		for i := 0; i < *documents; i++ {
			cp := ids[rand.Intn(len(ids))]
			if err := createPosting(client, *base, cp, i); err != nil {
				fail("создание документа %d: %v", i, err)
			}
			progress("документы", i+1, *documents)
		}
		fmt.Println()
		fmt.Printf("создано и проведено %d документов\n", *documents)
	}
}

func doLogin(c *http.Client, base, login, password string) error {
	body, _ := json.Marshal(map[string]string{"login": login, "password": password})
	resp, err := c.Post(base+"/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck,gosec // G104: bodyclose распознаёт только прямой вызов; тело прочитано, закрытие вторично
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("статус %d", resp.StatusCode)
	}
	return nil
}

func createCounterparty(c *http.Client, base string, i int) (string, error) {
	body := map[string]any{
		"Наименование": fmt.Sprintf("ООО Контрагент %04d", i),
		"ИНН":          fmt.Sprintf("77%08d", i),
	}
	return postForID(c, base+"/catalogs/"+url.PathEscape("Контрагент"), body)
}

func createPosting(c *http.Client, base, counterpartyID string, i int) error {
	qty := float64(1 + rand.Intn(20))
	price := float64(10 + rand.Intn(990))
	body := map[string]any{
		"Дата":      time.Now().Format("2006-01-02"),
		"Поставщик": counterpartyID,
		"__tableparts": map[string]any{
			"Товары": []map[string]any{{
				"Номенклатура": fmt.Sprintf("Товар %d", i%50),
				"Количество":   qty,
				"Цена":         price,
				"Сумма":        qty * price,
			}},
		},
		"__action": "post",
	}
	_, err := postForID(c, base+"/documents/"+url.PathEscape("Поступление"), body)
	return err
}

func postForID(c *http.Client, endpoint string, body map[string]any) (string, error) {
	buf, _ := json.Marshal(body)
	resp, err := c.Post(endpoint, "application/json", bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close() //nolint:errcheck,gosec // G104: bodyclose распознаёт только прямой вызов; тело прочитано, закрытие вторично
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("статус %d: %s", resp.StatusCode, msg)
	}
	var res struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}
	return res.ID, nil
}

func writeJSON(path string, v any) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer oblog.CloseQuiet("loadtest", "файл", f)
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func progress(label string, done, total int) {
	if done == total || done%50 == 0 {
		fmt.Printf("\r%s: %d/%d", label, done, total)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "\nseed: "+format+"\n", args...)
	os.Exit(1)
}
