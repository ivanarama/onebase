package equipment

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// round2 округляет денежную величину до копеек. Все суммы чека (цена, сумма
// позиции, оплаты, итог) обязаны округляться одинаково: иначе шум float64
// (например, 12.45*7 = 87.14999…) переживёт json.Marshal и уйдёт в ФН на копейку
// ниже, тогда как QR-строка считается через %.2f — и реквизиты ФН разойдутся с QR.
func round2(x float64) float64 { return math.Round(x*100) / 100 }

func init() {
	Register("atol_kkt", func() Device {
		return &atolDevice{timeout: 30 * time.Second, reqTimeout: 10 * time.Second, poll: 50 * time.Millisecond}
	})
}

// FiscalStateUnknownError сигнализирует, что задание на пробитие чека УЖЕ
// отправлено в ФН (POST прошёл), но получить фискальный результат не удалось
// (сеть/таймаут опроса). Чек, возможно, пробит и ушёл в ОФД, поэтому повторная
// регистрация ОПАСНА (риск двойного чека по 54-ФЗ). Состояние нужно сверить по
// UUID через драйвер ККТ, а не повторять автоматически. Вызывающий код может
// отличить этот случай через errors.As.
type FiscalStateUnknownError struct {
	UUID string
	Err  error
}

func (e *FiscalStateUnknownError) Error() string {
	return fmt.Sprintf("ккт: состояние чека НЕИЗВЕСТНО (uuid=%s): %v; НЕ повторять регистрацию автоматически — сверьте чек по uuid", e.UUID, e.Err)
}

func (e *FiscalStateUnknownError) Unwrap() error { return e.Err }

// atolDevice — драйвер фискального регистратора через «Драйвер ККТ АТОЛ v10».
//
// Транспорт — HTTP к локальному сервису АТОЛ (по умолчанию 127.0.0.1:16732),
// который сам общается с фискальным накопителем и ОФД. Протокол асинхронный:
// POST задания на /api/v2/requests возвращает uuid, после чего статус и
// фискальный результат забираются опросом GET /api/v2/requests/<uuid>.
//
// Поэтому, в отличие от сокетных драйверов, Connect не открывает соединение —
// бэкенд без состояния; ошибки связи проявляются при RegisterReceipt.
//
// ВНИМАНИЕ: имена полей JSON соответствуют АТОЛ v10 и при интеграции с реальной
// ККТ должны быть сверены с актуальной документацией драйвера. Штрих-М имеет
// иной протокол — это отдельный драйвер shtrih_kkt по тому же интерфейсу.
type atolDevice struct {
	baseURL    string
	client     *http.Client
	timeout    time.Duration // общий дедлайн ожидания фискального результата
	reqTimeout time.Duration // таймаут одного HTTP-запроса (чтобы один зависший опрос не съел весь бюджет)
	poll       time.Duration // интервал опроса статуса
}

func (d *atolDevice) Kind() string { return "фискальный_регистратор" }

func (d *atolDevice) Connect(params map[string]string) error {
	addr := firstNonEmpty(params["порт"], params["port"], params["адрес"], params["address"])
	if addr == "" {
		addr = "127.0.0.1:16732"
	}
	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		addr = "http://" + addr
	}
	d.baseURL = strings.TrimRight(addr, "/")
	if t := firstNonEmpty(params["таймаут"], params["timeout"]); t != "" {
		if sec, err := strconv.Atoi(t); err == nil && sec > 0 {
			d.timeout = time.Duration(sec) * time.Second
		}
	}
	// Таймаут одного запроса отделён от общего дедлайна: один зависший GET не
	// должен выесть весь бюджет ожидания, иначе не останется попыток опроса.
	if d.reqTimeout <= 0 || d.reqTimeout > d.timeout {
		d.reqTimeout = d.timeout
	}
	d.client = &http.Client{Timeout: d.reqTimeout}
	return nil
}

func (d *atolDevice) Disconnect() error { return nil }

// RegisterReceipt собирает задание формата АТОЛ v10, POST-ит его и опрашивает
// статус до готовности, возвращая фискальные реквизиты пробитого чека.
func (d *atolDevice) RegisterReceipt(r FiscalReceipt) (FiscalResult, error) {
	if d.client == nil {
		return FiscalResult{}, fmt.Errorf("устройство не подключено")
	}
	if len(r.Items) == 0 {
		return FiscalResult{}, fmt.Errorf("ккт: чек без позиций")
	}
	// Валидация ДО обращения к ФН: некорректный чек должен отсекаться здесь, а не
	// всплывать ошибкой уже после попытки фискализации (см. FiscalStateUnknownError).
	if total := receiptTotal(r); total <= 0 {
		return FiscalResult{}, fmt.Errorf("ккт: некорректный итог чека (%.2f) — нечего фискализировать", total)
	}
	for _, it := range r.Items {
		if it.Qty < 0 || it.Price < 0 || it.Sum < 0 {
			return FiscalResult{}, fmt.Errorf("ккт: отрицательные значения в позиции %q", it.Name)
		}
	}
	for _, p := range r.Payments {
		if p.Sum < 0 {
			return FiscalResult{}, fmt.Errorf("ккт: отрицательная сумма оплаты (%s)", p.Type)
		}
	}
	// Сверка чека ДО обращения к ФН: сумма позиций, сумма оплат и итог обязаны
	// совпадать до копейки. Расхождение (опечатка/частичная оплата/неразнесённая
	// скидка) иначе ушло бы в ФН и всплыло уже как FiscalStateUnknownError.
	// Оплаты сверяем только если они заданы явно: пустой список автодополняется
	// до итога наличными (atolTaskFromReceipt), то есть сходится по построению.
	if err := reconcileReceipt(r); err != nil {
		return FiscalResult{}, err
	}
	// Идемпотентность ATOL v10: при повторе того же чека внешний код может задать
	// IdempotencyKey, чтобы задание ушло с тем же uuid и ФН не пробил дубль.
	uuid := r.IdempotencyKey
	if uuid == "" {
		uuid = newUUID()
	}
	body, err := json.Marshal(atolTaskFromReceipt(uuid, r))
	if err != nil {
		return FiscalResult{}, err
	}
	if err := d.post(d.baseURL+"/api/v2/requests", body); err != nil {
		return FiscalResult{}, err
	}
	return d.await(uuid, r)
}

// reconcileReceipt сверяет, что Σ сумм позиций == Σ оплат == итог чека с точностью
// до копейки. Если оплаты не заданы явно, сверяется только итог с позициями
// (оплаты будут автодополнены до итога при сборке задания).
func reconcileReceipt(r FiscalReceipt) error {
	items := itemsTotal(r)
	total := receiptTotal(r)
	if len(r.Payments) > 0 {
		pays := paymentsTotal(r)
		if pays != items {
			return fmt.Errorf("ккт: сумма оплат (%.2f) не совпадает с суммой позиций (%.2f) — чек не сбалансирован", pays, items)
		}
		if pays != total {
			return fmt.Errorf("ккт: сумма оплат (%.2f) не совпадает с итогом чека (%.2f)", pays, total)
		}
	}
	if total != items {
		return fmt.Errorf("ккт: итог чека (%.2f) не совпадает с суммой позиций (%.2f)", total, items)
	}
	return nil
}

// ResolveByUUID безопасно дозапрашивает фискальный результат задания по ранее
// сохранённому uuid (например, после FiscalStateUnknownError): переиспользует
// тот же опрос статуса, что и RegisterReceipt, не пробивая новый чек.
func (d *atolDevice) ResolveByUUID(uuid string) (FiscalResult, error) {
	if d.client == nil {
		return FiscalResult{}, fmt.Errorf("устройство не подключено")
	}
	if strings.TrimSpace(uuid) == "" {
		return FiscalResult{}, fmt.Errorf("ккт: пустой uuid для дозапроса результата")
	}
	return d.await(uuid, FiscalReceipt{})
}

// post отправляет задание и проверяет, что сервис принял его в очередь.
func (d *atolDevice) post(url string, body []byte) error {
	resp, err := d.client.Post(url, "application/json; charset=utf-8", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("ккт: отправка задания: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck,gosec // G104: bodyclose распознаёт только прямой вызов; тело прочитано, закрытие вторично
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("ккт: сервис вернул %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var ack struct {
		IsError bool   `json:"isError"`
		Error   string `json:"error"`
	}
	// Отсутствие полей — не ошибка: пустой ack означает «отказа нет».
	_ = json.Unmarshal(raw, &ack)
	if ack.IsError {
		return fmt.Errorf("ккт: задание отклонено: %s", ack.Error)
	}
	return nil
}

// await опрашивает /api/v2/requests/<uuid> до готовности результата или дедлайна.
func (d *atolDevice) await(uuid string, r FiscalReceipt) (FiscalResult, error) {
	deadline := time.Now().Add(d.timeout)
	url := d.baseURL + "/api/v2/requests/" + uuid
	for {
		// Задание уже отправлено в ФН — любой сбой опроса означает «состояние
		// неизвестно» (чек мог пробиться), поэтому возвращаем FiscalStateUnknownError,
		// а не обычную ошибку, чтобы вызывающий не повторил пробитие вслепую.
		resp, err := d.client.Get(url)
		if err != nil {
			return FiscalResult{}, &FiscalStateUnknownError{UUID: uuid, Err: fmt.Errorf("опрос статуса: %w", err)}
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close() //nolint:errcheck,gosec // G104: bodyclose распознаёт только прямой вызов; тело прочитано, закрытие вторично
		if resp.StatusCode >= 300 {
			return FiscalResult{}, &FiscalStateUnknownError{UUID: uuid, Err: fmt.Errorf("опрос статуса вернул %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))}
		}
		var st atolStatus
		if err := json.Unmarshal(raw, &st); err != nil {
			return FiscalResult{}, &FiscalStateUnknownError{UUID: uuid, Err: fmt.Errorf("разбор статуса: %w", err)}
		}
		if st.IsError {
			// ФН/драйвер сообщил об ошибке регистрации — чек НЕ пробит, повтор безопасен.
			return FiscalResult{}, fmt.Errorf("ккт: ошибка регистрации чека: %s", st.firstError())
		}
		if st.Ready && len(st.Results) > 0 {
			return st.Results[0].Result.toFiscalResult(r), nil
		}
		if time.Now().After(deadline) {
			return FiscalResult{}, &FiscalStateUnknownError{UUID: uuid, Err: fmt.Errorf("превышено время ожидания фискального результата")}
		}
		time.Sleep(d.poll)
	}
}

// ─── формат задания АТОЛ v10 ────────────────────────────────────────────────

type atolTask struct {
	UUID    string         `json:"uuid"`
	Request []atolDocument `json:"request"`
}

type atolDocument struct {
	Type           string        `json:"type"`         // sell | sellReturn | buy | buyReturn
	TaxationType   string        `json:"taxationType"` // osn | usnIncome | ...
	Items          []atolItem    `json:"items"`
	Payments       []atolPayment `json:"payments"`
	Total          float64       `json:"total"`
	Electronically bool          `json:"electronically"` // только электронный чек, без печати
	ClientInfo     *atolClient   `json:"clientInfo,omitempty"`
}

type atolItem struct {
	Type          string  `json:"type"` // всегда "position"
	Name          string  `json:"name"`
	Price         float64 `json:"price"`
	Quantity      float64 `json:"quantity"`
	Amount        float64 `json:"amount"`
	Tax           atolTax `json:"tax"`
	PaymentObject string  `json:"paymentObject"` // признак предмета расчёта, тег 1212
	PaymentMethod string  `json:"paymentMethod"` // признак способа расчёта, тег 1214
}

type atolTax struct {
	Type string `json:"type"` // vat20 | vat10 | vat0 | none | ...
}

type atolPayment struct {
	Type string  `json:"type"` // cash | electronically | prepaid | credit
	Sum  float64 `json:"sum"`
}

type atolClient struct {
	Email string `json:"email,omitempty"`
	Phone string `json:"phone,omitempty"`
}

// atolStatus — ответ опроса статуса задания.
type atolStatus struct {
	Ready   bool         `json:"ready"`
	IsError bool         `json:"isError"`
	Results []atolResult `json:"results"`
}

type atolResult struct {
	Result atolFiscal `json:"result"`
	Error  *struct {
		Description string `json:"description"`
	} `json:"error"`
}

func (s atolStatus) firstError() string {
	for _, r := range s.Results {
		if r.Error != nil && r.Error.Description != "" {
			return r.Error.Description
		}
	}
	return "детали не сообщены"
}

// atolFiscal — фискальные реквизиты из результата регистрации.
type atolFiscal struct {
	FNNumber             string  `json:"fnNumber"`
	FiscalDocumentNumber int64   `json:"fiscalDocumentNumber"`
	FiscalDocumentSign   string  `json:"fiscalDocumentSign"`
	ReceiptDatetime      string  `json:"receiptDatetime"`
	Total                float64 `json:"total"`
}

// toFiscalResult переносит реквизиты в доменную модель и строит строку QR-кода
// чека в формате ФНС: t=<дата>&s=<сумма>&fn=<ФН>&i=<ФД>&fp=<ФП>&n=1.
func (f atolFiscal) toFiscalResult(r FiscalReceipt) FiscalResult {
	res := FiscalResult{
		FN: f.FNNumber,
		FD: fmt.Sprintf("%d", f.FiscalDocumentNumber),
		FP: f.FiscalDocumentSign,
	}
	total := f.Total
	if total == 0 {
		total = receiptTotal(r)
	}
	// QR считаем из округлённой суммы, чтобы s=… в QR совпадало с Total задания,
	// ушедшим в ФН (оба проходят round2 — иначе шум float64 даст расхождение).
	total = round2(total)
	if f.FNNumber != "" && f.FiscalDocumentSign != "" {
		t := strings.NewReplacer("-", "", ":", "", "+", "T", " ", "T").Replace(f.ReceiptDatetime)
		if i := strings.IndexByte(t, 'T'); i >= 0 && len(t) > i+5 {
			t = t[:i+5] // YYYYMMDDTHHMM
		}
		res.QR = fmt.Sprintf("t=%s&s=%.2f&fn=%s&i=%d&fp=%s&n=1", t, total, f.FNNumber, f.FiscalDocumentNumber, f.FiscalDocumentSign)
	}
	return res
}

// ─── маппинг доменной модели в формат АТОЛ ──────────────────────────────────

func atolTaskFromReceipt(uuid string, r FiscalReceipt) atolTask {
	doc := atolDocument{
		Type:         atolOperationType(r.Type),
		TaxationType: atolTaxation(r.Taxation),
		Total:        receiptTotal(r),
	}
	for _, it := range r.Items {
		// Все денежные величины округляем до копеек: иначе шум float64
		// (12.45*7 = 87.14999…) уйдёт в ФН и разойдётся с QR (см. round2).
		doc.Items = append(doc.Items, atolItem{
			Type:          "position",
			Name:          it.Name,
			Price:         round2(it.Price),
			Quantity:      it.Qty,
			Amount:        itemAmount(it),
			Tax:           atolTax{Type: atolVAT(it.VAT)},
			PaymentObject: atolPaymentObject(it.ItemType),
			PaymentMethod: atolPaymentMethod(it.PaymentType),
		})
	}
	for _, p := range r.Payments {
		doc.Payments = append(doc.Payments, atolPayment{Type: atolPaymentMode(p.Type), Sum: round2(p.Sum)})
	}
	// Чек без явных оплат считаем оплаченным наличными на сумму итога.
	if len(doc.Payments) == 0 {
		doc.Payments = []atolPayment{{Type: "cash", Sum: doc.Total}}
	}
	if r.Email != "" || r.Phone != "" {
		doc.Electronically = true
		doc.ClientInfo = &atolClient{Email: r.Email, Phone: r.Phone}
	}
	return atolTask{UUID: uuid, Request: []atolDocument{doc}}
}

// itemAmount — сумма позиции в копейках: явная Sum, иначе Qty*Price; всегда
// округляется до копеек, чтобы совпадать с тем, что уходит в ФН и в QR.
func itemAmount(it FiscalItem) float64 {
	if it.Sum != 0 {
		return round2(it.Sum)
	}
	return round2(it.Qty * it.Price)
}

// itemsTotal — сумма позиций чека (округлённая до копеек).
func itemsTotal(r FiscalReceipt) float64 {
	var total float64
	for _, it := range r.Items {
		total += itemAmount(it)
	}
	return round2(total)
}

// paymentsTotal — сумма оплат чека (округлённая до копеек).
func paymentsTotal(r FiscalReceipt) float64 {
	var total float64
	for _, p := range r.Payments {
		total += round2(p.Sum)
	}
	return round2(total)
}

// receiptTotal — итог чека: сумма оплат, иначе сумма позиций. Округляется до
// копеек, чтобы Total в задании ФН совпадал с QR (оба считаются из round2).
func receiptTotal(r FiscalReceipt) float64 {
	if t := paymentsTotal(r); t > 0 {
		return t
	}
	return itemsTotal(r)
}

func atolOperationType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "возвратприхода", "sellreturn":
		return "sellReturn"
	case "расход", "buy":
		return "buy"
	case "возвратрасхода", "buyreturn":
		return "buyReturn"
	default: // "приход" и пустое
		return "sell"
	}
}

func atolTaxation(t string) string {
	// «УСН_Доход» из перечисления и «уснДоход» из DSL приводим к одному виду.
	switch strings.ReplaceAll(strings.ToLower(strings.TrimSpace(t)), "_", "") {
	case "усндоход", "usnincome":
		return "usnIncome"
	case "усндоходрасход", "usnincomeoutcome":
		return "usnIncomeOutcome"
	case "есхн", "esn":
		return "esn"
	case "патент", "patent":
		return "patent"
	case "envd":
		return "envd"
	default: // "осн" и пустое
		return "osn"
	}
}

func atolVAT(v string) string {
	// Нормализуем «НДС20_120» (перечисление) и «ндс20/120» (DSL) к одному виду.
	norm := strings.ToLower(strings.TrimSpace(v))
	norm = strings.NewReplacer(" ", "", "_", "/").Replace(norm)
	switch norm {
	case "ндс20", "vat20":
		return "vat20"
	case "ндс10", "vat10":
		return "vat10"
	case "ндс20/120", "vat120":
		return "vat120"
	case "ндс10/110", "vat110":
		return "vat110"
	case "ндс0", "vat0":
		return "vat0"
	default: // "безндс" и пустое — без НДС
		return "none"
	}
}

func atolPaymentObject(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "услуга", "service":
		return "service"
	case "работа", "job":
		return "job"
	case "платёж", "платеж", "payment":
		return "payment"
	default: // "товар" и пустое
		return "commodity"
	}
}

func atolPaymentMethod(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "аванс", "prepayment100", "предоплата100":
		return "prepayment100"
	case "частичныйрасчёт", "частичныйрасчет", "advance":
		return "advance"
	case "кредит", "credit":
		return "credit"
	default: // "полныйРасчёт" и пустое
		return "fullPayment"
	}
}

func atolPaymentMode(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "безналичные", "карта", "electronically":
		return "electronically"
	case "аванс", "предоплата", "prepaid":
		return "prepaid"
	case "кредит", "credit":
		return "credit"
	default: // "наличные" и пустое
		return "cash"
	}
}

// newUUID генерирует случайный uuid v4 для идентификации задания.
func newUUID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
