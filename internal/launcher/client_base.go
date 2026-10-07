package launcher

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Клиентское подключение: запись списка баз, которая описывает УЖЕ РАБОТАЮЩИЙ
// сервер onebase, а не базу, которую лаунчер поднимает сам.
//
// Зачем. Многопользовательская работа (контакт-центр, любой отдел) идёт против
// одной службы: `onebase service install --host 0.0.0.0`. До этого в списке баз
// можно было описать только то, что лаунчер запускает своим дочерним процессом,
// и обходной путь — прописать каждой станции DSN общего PostgreSQL — даёт
// «платформа на станции, СУБД на сервере». Для нескольких рабочих мест это
// неверно: регламентные задания координируются только внутри процесса
// (scheduler.beginJob держит activeJobs в памяти), `/hs/`-сервисы требуют
// стабильного адреса приёмки, а пароль СУБД расходится по станциям.
//
// Чем такая запись отличается от обычной: лаунчер ею НЕ ВЛАДЕЕТ. Он не
// запускает и не останавливает процесс, не усыновляет его по control-порту, не
// мигрирует схему и не судит о версии — он только открывает окно Предприятия на
// указанном адресе. Поэтому поля запуска (DB, DBPath, Port, Host, Path,
// ConfigSource) у неё не используются вовсе.

// ErrBaseNotOwned — операция жизненного цикла над записью клиентского
// подключения. Процесс сервера принадлежит не лаунчеру, и останавливать или
// мигрировать его он права не имеет.
var ErrBaseNotOwned = errors.New("launcher: base is a client connection, its server is not managed by the launcher")

// Client сообщает, что запись описывает подключение к чужому серверу.
// Признак — непустой ServerURL: отдельного поля-вида нет сознательно, иначе
// появилось бы второе место, где одно и то же состояние может разойтись.
func (b *Base) Client() bool {
	return b != nil && strings.TrimSpace(b.ServerURL) != ""
}

// normalizeServerURL приводит адрес сервера к виду «схема://хост[:порт]».
//
// Путь, строка запроса и фрагмент отвергаются, а не отбрасываются молча: адрес с
// префиксом пути выглядит рабочим, а лаунчер всё равно дописывает к нему свои
// («/ui»), и префикс молча терялся бы — ровно та ошибка, которую мы просили
// закрыть у Callista в её baseUrl. Схему требуем явной: угадывать http или https
// за пользователя нельзя, различие между ними — это различие между шифрованным и
// открытым каналом.
func normalizeServerURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("адрес сервера не задан")
	}
	if !strings.Contains(s, "://") {
		return "", errors.New("укажите адрес вместе со схемой: https://сервер:порт либо http://сервер:порт")
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", errors.New("адрес сервера не разбирается: " + err.Error())
	}
	switch u.Scheme {
	case "http", "https":
	default:
		return "", errors.New("адрес сервера поддерживает только схемы http и https, задана «" + u.Scheme + "»")
	}
	if u.Host == "" {
		return "", errors.New("в адресе сервера нет хоста")
	}
	if u.User != nil {
		return "", errors.New("логин и пароль в адресе сервера не поддерживаются: вход выполняется в самой базе")
	}
	if p := strings.Trim(u.Path, "/"); p != "" {
		return "", errors.New("адрес сервера задаётся без пути, а задан «" + u.Path + "»")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("адрес сервера задаётся без параметров запроса и якоря")
	}
	// Хвостовой слэш убираем здесь, чтобы ниже по коду адрес складывался с
	// путями («/ui») без двойного разделителя.
	return u.Scheme + "://" + u.Host, nil
}

// clientServerProbeTimeout — предел одной проверки доступности сервера. Список
// баз рисуется по кэшу статусов с общим TTL, но сама проверка не должна держать
// рендер: недоступный сервер обязан показаться недоступным быстро.
const clientServerProbeTimeout = 2 * time.Second

// clientServerReachable отвечает, отзывается ли сервер клиентского подключения.
//
// Это проверка ДОСТУПНОСТИ, а не подлинности: подтверждать identity здесь нечем
// и не нужно — процессом владеет не лаунчер, у него нет control-token, и
// открываем мы адрес, который администратор задал сам. Единственный смысл
// ответа — индикатор в списке: «сервер отвечает» против «не отвечает».
//
// Ошибку сознательно не различаем: недоступность по сети, отказ TLS и 5xx для
// индикатора — одно и то же состояние «работать нельзя». Разбираться, почему
// именно, пользователь пойдёт в журнал сервера, а не в список баз.
func clientServerReachable(ctx context.Context, base *Base, client *http.Client) bool {
	if base == nil || !base.Client() {
		return false
	}
	if client == nil {
		client = &http.Client{Timeout: clientServerProbeTimeout}
	}
	ctx, cancel := context.WithTimeout(ctx, clientServerProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.ServerURL+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode >= 200 && resp.StatusCode < 500
}

// baseKindClient — значение переключателя вида записи в форме базы.
const baseKindClient = "client"

// NewClientBase собирает запись клиентского подключения из имени и адреса.
// Экспортирована: ту же запись заводит `onebase ibases add --server`.
// Поля запуска сознательно остаются пустыми: порт не выделяется (см. Store.Add),
// DSN и каталог конфигурации у такой записи не используются.
func NewClientBase(name, serverURL string) (*Base, error) {
	normalized, err := normalizeServerURL(serverURL)
	if err != nil {
		return nil, err
	}
	return &Base{Name: strings.TrimSpace(name), ServerURL: normalized}, nil
}
