package ui

// Кэш ответов HTTP-сервисов (план 126).
//
// serviceDispatch на каждый запрос строит DSL-переменные, исполняет обработчик
// и ходит в БД — включая одинаковые запросы подряд. Для учётной интеграции это
// терпимо, для публичной витрины нет: одна и та же страница пересобиралась на
// каждый заход, в том числе роботов.
//
// Кэш процессный (в памяти). При нескольких процессах каждый греет свой — это
// свойство, а не дефект: общий стор потребовал бы Redis (P3-1 плана 111).

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ivantit66/onebase/internal/httpservice"
	oblog "github.com/ivantit66/onebase/internal/logging"
)

// defaultServiceCacheMaxBytes — общий предел памяти под кэш ответов.
const defaultServiceCacheMaxBytes = 64 << 20

// cachedResponse — сохранённый ответ. Хранится НЕсжатым: сжатие (план 128)
// применяется на выдаче, иначе пришлось бы держать по две копии на ключ и
// считать разные ETag на один ресурс.
type cachedResponse struct {
	Status  int
	Header  http.Header
	Body    []byte
	ETag    string
	Expires time.Time
}

type cacheEntry struct {
	key  string
	root string // корневой URL сервиса — для точечного сброса
	resp *cachedResponse
	size int64
}

// serviceCache — LRU с TTL и общим лимитом по размеру.
type serviceCache struct {
	mu       sync.Mutex
	entries  map[string]*list.Element
	order    *list.List // фронт = самый свежий
	size     int64
	maxBytes int64
	// keyLocks сериализует промахи по одному ключу: пять одновременных
	// запросов на «холодную» популярную страницу не должны запускать пять
	// исполнений DSL — это ровно тот момент, когда сайт и падает.
	keyMu    sync.Mutex
	keyLocks map[string]*keyLock
	// uncacheable — ключи, ответ по которым уже оказался некэшируемым. Живёт
	// под тем же keyMu, что и keyLocks: обе карты про один ключ.
	uncacheable map[string]uncacheableMark
	// now подменяется в тестах, чтобы не спать по-настоящему.
	now func() time.Time

	hits      atomic.Uint64
	misses    atomic.Uint64
	evictions atomic.Uint64
}

func newServiceCache(maxBytes int64) *serviceCache {
	if maxBytes <= 0 {
		maxBytes = defaultServiceCacheMaxBytes
	}
	return &serviceCache{
		entries:     make(map[string]*list.Element),
		order:       list.New(),
		maxBytes:    maxBytes,
		keyLocks:    make(map[string]*keyLock),
		uncacheable: make(map[string]uncacheableMark),
		now:         time.Now,
	}
}

const (
	// uncacheableTTL — насколько долго ключ считается заведомо некэшируемым.
	// Короткий намеренно: страница может стать кэшируемой (404 исчез, тело
	// ужалось, обработчик перестал ставить Set-Cookie), и цена ошибки —
	// ровно один незащищённый промах.
	uncacheableTTL = 30 * time.Second
	// maxUncacheableKeys — потолок отрицательного списка. При переполнении он
	// чистится целиком: это оптимизация, а не источник истины, и терять её
	// безопаснее, чем расти без предела на потоке уникальных URL.
	maxUncacheableKeys = 4096
)

// uncacheableMark — отметка «ответ по этому ключу кэшировать нельзя». root
// хранится, чтобы точечный сброс сервиса снимал и отрицательные отметки.
type uncacheableMark struct {
	until time.Time
	root  string
}

// uncacheableRecently — правда ли, что ответ по ключу недавно оказался
// некэшируемым. Такой ключ кэш не наполнит никогда (тело больше max_body,
// Set-Cookie, горячий 404), и брать под него замок вредно: сериализация
// промахов из разовой защиты холодного старта превращается в постоянную
// очередь по одному — параллелизм хуже, чем с выключенным кэшем (#1000).
func (c *serviceCache) uncacheableRecently(key string) bool {
	c.keyMu.Lock()
	defer c.keyMu.Unlock()
	mark, ok := c.uncacheable[key]
	if !ok {
		return false
	}
	if c.timeNow().After(mark.until) {
		delete(c.uncacheable, key)
		return false
	}
	return true
}

// markUncacheable запоминает ключ как некэшируемый на uncacheableTTL.
func (c *serviceCache) markUncacheable(key, root string) {
	c.keyMu.Lock()
	defer c.keyMu.Unlock()
	now := c.timeNow()
	if len(c.uncacheable) >= maxUncacheableKeys {
		for k, m := range c.uncacheable {
			if now.After(m.until) {
				delete(c.uncacheable, k)
			}
		}
		if len(c.uncacheable) >= maxUncacheableKeys {
			c.uncacheable = make(map[string]uncacheableMark, maxUncacheableKeys)
		}
	}
	c.uncacheable[key] = uncacheableMark{until: now.Add(uncacheableTTL), root: root}
}

// forgetUncacheable снимает отметку: ответ по ключу снова кэшируется, и
// защита холодного старта этому ключу опять полагается.
func (c *serviceCache) forgetUncacheable(key string) {
	c.keyMu.Lock()
	defer c.keyMu.Unlock()
	delete(c.uncacheable, key)
}

// clearUncacheable снимает отметки сервиса (или все при пустом root) — чтобы
// сброс кэша не оставлял за собой невидимый отрицательный хвост.
func (c *serviceCache) clearUncacheable(root string) {
	c.keyMu.Lock()
	defer c.keyMu.Unlock()
	root = strings.ToLower(strings.Trim(strings.TrimSpace(root), "/"))
	for k, m := range c.uncacheable {
		if root == "" || strings.ToLower(m.root) == root {
			delete(c.uncacheable, k)
		}
	}
}

// keyLock — мьютекс на ключ кэша со счётчиком ожидающих (чтобы карта не росла).
type keyLock struct {
	mu   sync.Mutex
	refs int
}

// lockKey захватывает право «собирать ответ» для этого ключа. Возвращает
// функцию освобождения. Ожидающие после освобождения находят готовый ответ в
// кэше и обработчик не запускают.
func (c *serviceCache) lockKey(key string) func() {
	c.keyMu.Lock()
	kl, ok := c.keyLocks[key]
	if !ok {
		kl = &keyLock{}
		c.keyLocks[key] = kl
	}
	kl.refs++
	c.keyMu.Unlock()

	kl.mu.Lock()
	return func() {
		kl.mu.Unlock()
		c.keyMu.Lock()
		kl.refs--
		if kl.refs <= 0 {
			delete(c.keyLocks, key)
		}
		c.keyMu.Unlock()
	}
}

func (c *serviceCache) timeNow() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// Get возвращает живую запись. Просроченная удаляется здесь же.
func (c *serviceCache) Get(key string) (*cachedResponse, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.entries[key]
	if !ok {
		c.misses.Add(1)
		return nil, false
	}
	entry, _ := el.Value.(*cacheEntry)
	if c.timeNow().After(entry.resp.Expires) {
		c.removeElement(el)
		c.misses.Add(1)
		return nil, false
	}
	c.order.MoveToFront(el)
	c.hits.Add(1)
	return entry.resp, true
}

func (c *serviceCache) Put(key, root string, resp *cachedResponse, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	resp.Expires = c.timeNow().Add(ttl)
	// Ключ и заголовки входят в учёт обязательно: при vary по query ключ несёт
	// весь query-string, и мусорные параметры атакующего раздували бы память
	// мимо лимита — фактический RSS в разы больше «размера кэша» из метрики.
	size := int64(len(resp.Body)) + int64(len(key)) + 512 // 512 — служебные структуры
	for k, vals := range resp.Header {
		size += int64(len(k))
		for _, v := range vals {
			size += int64(len(v))
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[key]; ok {
		c.removeElement(el)
	}
	el := c.order.PushFront(&cacheEntry{key: key, root: root, resp: resp, size: size})
	c.entries[key] = el
	c.size += size
	for c.size > c.maxBytes && c.order.Len() > 0 {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		c.removeElement(oldest)
		c.evictions.Add(1)
	}
}

// Clear сбрасывает кэш сервиса (root == "" — весь). Возвращает число
// выброшенных записей.
func (c *serviceCache) Clear(root string) int {
	// Отрицательные отметки снимаем вместе с записями: после правки модуля
	// страница может стать кэшируемой, и хвост отметок это бы скрыл.
	c.clearUncacheable(root)
	c.mu.Lock()
	defer c.mu.Unlock()
	root = strings.ToLower(strings.Trim(strings.TrimSpace(root), "/"))
	n := 0
	for el := c.order.Front(); el != nil; {
		next := el.Next()
		entry, _ := el.Value.(*cacheEntry)
		if root == "" || strings.ToLower(entry.root) == root {
			c.removeElement(el)
			n++
		}
		el = next
	}
	return n
}

// Size — суммарный объём кэша в байтах (для диагностики).
func (c *serviceCache) Size() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.size
}

// removeElement вызывается под удерживаемым c.mu.
func (c *serviceCache) removeElement(el *list.Element) {
	entry, _ := el.Value.(*cacheEntry)
	c.order.Remove(el)
	delete(c.entries, entry.key)
	c.size -= entry.size
	if c.size < 0 {
		c.size = 0
	}
}

// serviceCacheKey строит ключ запроса.
//
// Параметры запроса сортируются: без этого «?a=1&b=2» и «?b=2&a=1» дали бы две
// записи с одинаковым содержимым.
func serviceCacheKey(svc *httpservice.Service, r *http.Request, lang string) string {
	var b strings.Builder
	b.WriteString(svc.RootURL)
	b.WriteByte('|')
	b.WriteString(strings.ToUpper(r.Method))
	b.WriteByte('|')
	b.WriteString(r.URL.Path)
	if svc.Cache.VaryBy("query") {
		b.WriteByte('|')
		b.WriteString(sortedQuery(r.URL.Query()))
	}
	if svc.Cache.VaryBy("host") {
		b.WriteByte('|')
		b.WriteString(strings.ToLower(r.Host))
	}
	if svc.Cache.VaryBy("lang") {
		b.WriteByte('|')
		b.WriteString(lang)
	}
	return b.String()
}

func sortedQuery(q url.Values) string {
	if len(q) == 0 {
		return ""
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		vals := append([]string(nil), q[k]...)
		sort.Strings(vals)
		for _, v := range vals {
			b.WriteString(url.QueryEscape(k))
			b.WriteByte('=')
			b.WriteString(url.QueryEscape(v))
			b.WriteByte('&')
		}
	}
	return b.String()
}

func computeETag(body []byte) string {
	sum := sha256.Sum256(body)
	return `W/"` + hex.EncodeToString(sum[:8]) + `"`
}

// cacheCapture собирает ответ обработчика в память вместо отправки клиенту.
type cacheCapture struct {
	header http.Header
	status int
	body   []byte
}

func newCacheCapture() *cacheCapture {
	return &cacheCapture{header: make(http.Header), status: http.StatusOK}
}

func (c *cacheCapture) Header() http.Header { return c.header }

func (c *cacheCapture) WriteHeader(status int) { c.status = status }

func (c *cacheCapture) Write(p []byte) (int, error) {
	c.body = append(c.body, p...)
	return len(p), nil
}

// cacheable решает, можно ли сохранить собранный ответ.
//
// Только 200 и только без Set-Cookie: «404 залип на час» — типовой инцидент
// CMS, а кэшированный Set-Cookie раздал бы одну сессию нескольким клиентам.
func (c *cacheCapture) cacheable(limit int64) bool {
	if c.status != http.StatusOK {
		return false
	}
	if len(c.header.Values("Set-Cookie")) > 0 {
		return false
	}
	return int64(len(c.body)) <= limit
}

func (c *cacheCapture) toCachedResponse() *cachedResponse {
	h := make(http.Header, len(c.header))
	for k, v := range c.header {
		h[k] = append([]string(nil), v...)
	}
	return &cachedResponse{Status: c.status, Header: h, Body: c.body, ETag: computeETag(c.body)}
}

// writeCachedResponse отдаёт сохранённый ответ клиенту. Возвращает 304, если
// клиент прислал совпавший If-None-Match.
func writeCachedResponse(w http.ResponseWriter, r *http.Request, resp *cachedResponse, svc *httpservice.Service) {
	h := w.Header()
	for k, vals := range resp.Header {
		for _, v := range vals {
			h.Add(k, v)
		}
	}
	if resp.ETag != "" {
		h.Set("ETag", resp.ETag)
	}
	if svc.Cache.VaryBy("lang") {
		// Ключ кэша дробится по языку из Accept-Language. Внешние кэши обязаны
		// узнать об этом из Vary, иначе public-ответ на языке первого клиента
		// достанется из прокси клиентам с другим языком.
		h.Add("Vary", "Accept-Language")
	}
	if svc.Cache.Public {
		h.Set("Cache-Control", "public, max-age="+strconv.Itoa(svc.Cache.TTL))
	}
	if match := r.Header.Get("If-None-Match"); match != "" && resp.ETag != "" && etagMatches(match, resp.ETag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(resp.Status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(resp.Body) //nolint:gosec // G705: тело сформировал обработчик сервиса, тип он же и объявил
	}
}

// flushCapture отдаёт некэшируемый ответ клиенту как есть.
func flushCapture(w http.ResponseWriter, c *cacheCapture) {
	h := w.Header()
	for k, vals := range c.header {
		for _, v := range vals {
			h.Add(k, v)
		}
	}
	w.WriteHeader(c.status)
	_, _ = w.Write(c.body) //nolint:gosec // G705: ответ уже сформирован обработчиком сервиса
}

// serviceCacheUsable — можно ли обслужить этот запрос из кэша. Кэш при
// auth ≠ none игнорируется намеренно (см. CacheUsable), но молчать об этом
// нельзя: владелец будет уверен, что кэш работает.
func (s *Server) serviceCacheUsable(svc *httpservice.Service, r *http.Request) bool {
	if s.svcCache == nil || !svc.Cache.Enabled() {
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if !svc.CacheUsable() {
		warnCacheIgnoredOnce(svc)
		return false
	}
	return true
}

var cacheWarned sync.Map // имя сервиса → уже предупреждали

func warnCacheIgnoredOnce(svc *httpservice.Service) {
	if _, seen := cacheWarned.LoadOrStore(svc.Name, true); seen {
		return
	}
	oblog.Component("http").Warn(
		"кэш ответов отключён: он допустим только при auth: none, иначе ответ одного пользователя достанется другому",
		"сервис", svc.Name, "auth", svc.Auth)
}

// etagMatches сравнивает If-None-Match (возможно, список) с нашим ETag.
func etagMatches(header, etag string) bool {
	for _, part := range strings.Split(header, ",") {
		if strings.TrimSpace(part) == etag {
			return true
		}
	}
	return false
}
