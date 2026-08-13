package ui

import (
	"context"
	"strings"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
)

// wsRoot — DSL-глобал ВебСокет / WebSocket (план 120B): отправка в исходящее
// WS-соединение приёмки и проверка его состояния.
//
//	ВебСокет.СобытияОбмена.Отправить(ЗначениеВJSON(Данные));
//	Если НЕ ВебСокет.СобытияОбмена.Подключён() Тогда … КонецЕсли;
//
// Гарантий доставки нет по контракту (#738): обрыв — ошибка сразу
// (перехватывается Попытка/Исключение), буфера и повтора нет. Приём сообщений
// сюда не входит — входящие идут через обработчик шлюза (транспорт ws, план
// 120A).
type wsRoot struct {
	s      *Server
	ctxSrc docsCtxSource
}

func newWSRoot(s *Server, ctxSrc docsCtxSource) *wsRoot {
	return &wsRoot{s: s, ctxSrc: ctxSrc}
}

func (r *wsRoot) Get(name string) any {
	in := r.s.reg.GetIntake(name)
	if in == nil {
		return nil
	}
	if in.Transport != metadata.IntakeTransportWS {
		// Шлюз существует, но не ws: без подсказки пользователь получил бы
		// невнятное «метод у Неопределено».
		interpreter.RaiseUserError("ВебСокет." + in.Name + ": у шлюза transport: " + in.Transport +
			", объект ВебСокет доступен только для transport: ws")
	}
	return &wsProxy{s: r.s, ctxSrc: r.ctxSrc, in: in}
}

func (r *wsRoot) Set(_ string, _ any) {}

// wsProxy — один шлюз с transport: ws.
type wsProxy struct {
	s      *Server
	ctxSrc docsCtxSource
	in     *metadata.Intake
}

func (p *wsProxy) Get(_ string) any    { return nil }
func (p *wsProxy) Set(_ string, _ any) {}

func (p *wsProxy) CallMethod(method string, args []any) any {
	switch strings.ToLower(method) {
	case "отправить", "send":
		p.send(args)
		return nil
	case "подключён", "подключен", "connected":
		client := p.s.wsIntakeClient(p.in.Name)
		return client != nil && client.Status().Connected
	}
	interpreter.RaiseUserError("ВебСокет." + p.in.Name + ": неизвестный метод «" + method +
		"» (доступны Отправить, Подключён)")
	return nil
}

func (p *wsProxy) send(args []any) {
	if len(args) != 1 {
		interpreter.RaiseUserError("ВебСокет." + p.in.Name + ".Отправить: ожидается один аргумент — строка сообщения")
	}
	text, ok := args[0].(string)
	if !ok {
		// Не сериализуем молча: формат протокола — дело конфигурации
		// (ЗначениеВJSON рядом), а тихий fmt.Sprintf отправил бы наружу
		// внутреннее представление объекта.
		interpreter.RaiseUserError("ВебСокет." + p.in.Name + ".Отправить: аргумент должен быть строкой (используйте ЗначениеВJSON)")
	}
	client := p.s.wsIntakeClient(p.in.Name)
	if client == nil {
		interpreter.RaiseUserError("ВебСокет." + p.in.Name + ": соединение не запущено (шлюз работает только при запущенном сервере базы)")
	}
	if err := client.Send(p.ctx(), []byte(text)); err != nil {
		interpreter.RaiseUserError("ВебСокет." + p.in.Name + ".Отправить: " + err.Error())
	}
}

func (p *wsProxy) ctx() context.Context {
	if p.ctxSrc != nil {
		return p.ctxSrc.Ctx()
	}
	return context.Background()
}
