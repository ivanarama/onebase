package ui

// WS-транспорт приёмки (план 120A): исходящее WebSocket-соединение к внешнему
// серверу, входящие сообщения идут тем же конвейером Ingest, что и http-шлюзы —
// с идемпотентностью, карантином и обработчиком в транзакции. Транспортный слой
// (реконнект, выдержка, обратное давление) — internal/wsclient; здесь — связка
// с реестром, приёмкой и жизненным циклом сервера.
//
// Соединения принадлежат серверу базы: горутины учитываются в backgroundWG
// (Shutdown их дожидается), контексты — потомки backgroundCtx. Горячая
// перезагрузка (--watch) вызывает ResyncWSIntakes: изменённые соединения
// пересоздаются, нетронутые продолжают жить. Обработчик и правила
// идемпотентности при этом читаются из реестра на каждое сообщение, поэтому
// их правка вообще не требует переподключения.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/intake"
	oblog "github.com/ivantit66/onebase/internal/logging"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/secrets"
	"github.com/ivantit66/onebase/internal/wsclient"
)

// wsIntakeEntry — одно живое соединение и признаки, по которым решается,
// нужно ли пересоздавать его при resync.
type wsIntakeEntry struct {
	sig    string // подпись транспортной части конфигурации
	client *wsclient.Client
	cancel context.CancelFunc
}

// ResyncWSIntakes приводит набор WS-соединений к текущему реестру: поднимает
// новые, гасит удалённые, пересоздаёт изменённые. Идемпотентен; первый вызов —
// запуск. Вызывается при старте сервера и при горячей перезагрузке.
func (s *Server) ResyncWSIntakes() {
	if s == nil || s.store == nil {
		return
	}
	s.wsMu.Lock()
	defer s.wsMu.Unlock()
	// Снимок реестра — под wsMu: конкурентные resync (стартовый вызов run.go и
	// горутина --watch) иначе могли бы применить снимки в обратном порядке и
	// откатить соединения на устаревшую конфигурацию.
	desired := map[string]*metadata.Intake{}
	for _, in := range s.reg.Intakes() {
		if in.Transport == metadata.IntakeTransportWS {
			desired[strings.ToLower(in.Name)] = in
		}
	}
	if s.wsIntakes == nil {
		s.wsIntakes = map[string]*wsIntakeEntry{}
	}
	for key, entry := range s.wsIntakes {
		in, keep := desired[key]
		if keep && wsIntakeSig(in) == entry.sig {
			continue
		}
		entry.cancel()
		delete(s.wsIntakes, key)
	}
	for key, in := range desired {
		if _, running := s.wsIntakes[key]; running {
			continue
		}
		if entry := s.startWSIntake(in); entry != nil {
			s.wsIntakes[key] = entry
		}
	}
}

// startWSIntake поднимает соединение одного шлюза. nil — сервер закрывается.
func (s *Server) startWSIntake(in *metadata.Intake) *wsIntakeEntry {
	release, ok := s.beginBackgroundJob()
	if !ok {
		return nil
	}
	s.lifecycleMu.Lock()
	base := s.backgroundCtx
	s.lifecycleMu.Unlock()
	ctx, cancel := context.WithCancel(base)

	name := in.Name
	log := oblog.Component("wsintake")
	client := wsclient.New(wsclient.Config{
		Name:             name,
		URL:              in.URL,
		Header:           wsIntakeHeader(in),
		Subscribe:        wsIntakeSubscribe(in),
		ReconnectInitial: time.Duration(in.Reconnect.Initial) * time.Second,
		ReconnectMax:     time.Duration(in.Reconnect.Max) * time.Second,
		MaxMessageBytes:  s.maxFileSizeBytes,
		Gate: func() string {
			if s.netEnabled(ctx) {
				return ""
			}
			return ErrNetworkLocked.Error()
		},
		OnMessage: func(mctx context.Context, raw []byte) error {
			return s.ingestWS(mctx, name, raw)
		},
		Logf: func(format string, args ...any) {
			log.Info(fmt.Sprintf(format, args...))
		},
	})
	go func() {
		defer release()
		defer cancel() // Run вышел (shutdown) — контекст освобождается и без resync
		client.Run(ctx)
	}()
	return &wsIntakeEntry{sig: wsIntakeSig(in), client: client, cancel: cancel}
}

// ingestWS проводит входящее сообщение через приёмку. Шлюз резолвится из
// реестра на каждое сообщение — --watch подхватывает правку обработчика и
// правил идемпотентности без переподключения.
func (s *Server) ingestWS(ctx context.Context, name string, raw []byte) error {
	in := s.reg.GetIntake(name)
	if in == nil || in.Transport != metadata.IntakeTransportWS {
		return fmt.Errorf("шлюз %s исчез из конфигурации", name)
	}
	env, err := intake.ParseEnvelope(raw)
	if err != nil {
		return err
	}
	handler, err := s.newIntakeHandler(in)
	if err != nil {
		return err
	}
	res, err := intake.New(s.store).Ingest(ctx, in, handler, env)
	if err != nil {
		return err
	}
	switch res.Status {
	case intake.StatusQuarantined:
		s.auditIntake(ctx, nil, "intake.quarantine", in.Name, env.Field(in.Idempotency.Key), res.Reason, env.CorrelationID(),
			map[string]any{"dlq_id": res.DLQID})
	case intake.StatusRejected:
		// Отклонённое не оставляет следа в журнале приёмки — фиксируем причину
		// хотя бы в логе процесса: у ws нет HTTP-ответа, который увидел бы отправитель.
		oblog.Component("wsintake").Warn("сообщение отклонено приёмкой",
			"шлюз", in.Name, "причина", res.Reason)
	}
	return nil
}

// wsIntakeClient возвращает живого клиента шлюза (для DSL и админки); nil —
// шлюз не ws или соединение не запущено.
func (s *Server) wsIntakeClient(name string) *wsclient.Client {
	s.wsMu.Lock()
	defer s.wsMu.Unlock()
	entry := s.wsIntakes[strings.ToLower(strings.TrimSpace(name))]
	if entry == nil {
		return nil
	}
	return entry.client
}

// wsIntakeSig — подпись транспортной части: совпала — соединение не трогаем
// при resync. Обработчик/идемпотентность сюда не входят намеренно (читаются
// вживую), заголовки зависят от auth+secret.
func wsIntakeSig(in *metadata.Intake) string {
	sub, _ := json.Marshal(in.Subscribe)
	return fmt.Sprintf("%s|%s|%s|%d|%d|%s", in.URL, in.Auth, in.Secret, in.Reconnect.Initial, in.Reconnect.Max, sub)
}

// wsIntakeHeader строит заголовки рукопожатия. token → Authorization: Bearer
// (стандарт для исходящих клиентов; X-Webhook-Token — соглашение нашего
// http-транспорта, чужой сервер о нём не знает). Секрет разыменовывается на
// каждом подключении.
func wsIntakeHeader(in *metadata.Intake) func() (http.Header, error) {
	if in.Auth != metadata.IntakeAuthToken {
		return nil
	}
	ref := in.Secret
	name := in.Name
	return func() (http.Header, error) {
		v, err := secrets.Default().Resolve(ref)
		if err != nil {
			return nil, fmt.Errorf("секрет шлюза %s не разыменован: %w", name, err)
		}
		if strings.TrimSpace(v) == "" {
			return nil, fmt.Errorf("секрет шлюза %s пуст", name)
		}
		h := http.Header{}
		h.Set("Authorization", "Bearer "+v)
		return h, nil
	}
}

// wsIntakeSubscribe сериализует подписочное сообщение из YAML в JSON.
func wsIntakeSubscribe(in *metadata.Intake) []byte {
	if len(in.Subscribe) == 0 {
		return nil
	}
	data, err := json.Marshal(in.Subscribe)
	if err != nil {
		oblog.Component("wsintake").Warn("subscribe шлюза не сериализуется в JSON",
			"шлюз", in.Name, "ошибка", err)
		return nil
	}
	return data
}
