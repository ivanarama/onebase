package rowsource

import (
	"context"
	"fmt"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"strings"
)

// opaqueQuery is a kernel-owned readonly adapter. It never enters the ordinary
// Query proxy's typedempty codec and never formats host context or DB objects.
type opaqueQuery struct {
	execution             *execution
	ctx                   context.Context
	text                  string
	params                map[string]any
	identity, textVersion int
	textBinding           queryTextBinding
}

func (q *opaqueQuery) Get(name string) any {
	switch strings.ToLower(name) {
	case "текст", "text":
		return q.text
	}
	interpreter.RaiseUserError("row source: неизвестное свойство Запроса")
	return nil
}
func (q *opaqueQuery) Set(name string, value any) {
	switch strings.ToLower(name) {
	case "текст", "text":
		text, ok := value.(string)
		if !ok {
			interpreter.RaiseUserError("row source: текст запроса должен быть строкой")
		}
		q.text = text
		q.textVersion++
		q.textBinding = queryTextBinding{object: q.identity, version: q.textVersion, controlRequired: true, dataRequired: true}
		return
	}
	interpreter.RaiseUserError("row source: неизвестное свойство Запроса")
}
func (q *opaqueQuery) CallMethod(name string, args []any) any {
	switch strings.ToLower(name) {
	case "установитьпараметр", "setparameter":
		if len(args) != 2 {
			interpreter.RaiseUserError("row source: нужны имя и значение параметра")
		}
		key, ok := args[0].(string)
		if !ok || len(key) == 0 || len(key) > 128 {
			interpreter.RaiseUserError("row source: неверное имя параметра")
		}
		if _, exists := q.params[key]; !exists && len(q.params) >= 128 {
			interpreter.RaiseUserError("row source: слишком много параметров")
		}
		q.params[key] = args[1]
		return nil
	case "выполнить", "execute":
		if len(args) != 0 {
			interpreter.RaiseUserError("row source: Выполнить не принимает аргументы")
		}
		params := map[string]any{}
		budget := &parameterBudget{active: map[*interpreter.Array]bool{}, postgres: q.execution.db.Dialect().Name() == "postgres"}
		for key, value := range q.params {
			converted, err := budget.value(value, 0)
			if err != nil {
				interpreter.RaiseUserErrorWrap("row source: недопустимый параметр", err)
			}
			params[key] = converted
		}
		if err := q.recordTextRead(params); err != nil {
			interpreter.RaiseUserErrorWrap("row source: text trace rejected", err)
		}
		compiled, err := q.execution.compile(q.ctx, q.text, params)
		if err != nil {
			interpreter.RaiseUserErrorWrap("row source: запрос отклонён", err)
		}
		_, rows, _, err := q.execution.dataset(q.ctx, compiled)
		if err != nil {
			interpreter.RaiseUserErrorWrap("row source: чтение отклонено", err)
		}
		return rows
	}
	interpreter.RaiseUserError("row source: неизвестный метод Запроса")
	return nil
}
func (*opaqueQuery) String() string   { return "Запрос" }
func (*opaqueQuery) TypeName() string { return "Запрос" }

type parameterBudget struct {
	active       map[*interpreter.Array]bool
	items, bytes int
	postgres     bool
}

func (p *parameterBudget) value(value any, depth int) (any, error) {
	p.items++
	if p.items > maxReadRows || depth > 16 {
		return nil, fmt.Errorf("превышен предел параметра")
	}
	value = interpreter.ToHostValue(value)
	switch v := value.(type) {
	case *interpreter.Ref:
		if v == nil {
			return nil, nil
		}
		if v.Manager != nil || v.AttrResolver != nil {
			return nil, fmt.Errorf("capability в параметре ссылки")
		}
		if v.UUID == "" && p.postgres {
			return nil, nil
		}
		value = v.UUID
	case *interpreter.Array:
		if v == nil {
			return nil, nil
		}
		if len(v.Iterate()) > maxRows || p.active[v] {
			return nil, fmt.Errorf("циклический или слишком большой параметр")
		}
		p.active[v] = true
		defer delete(p.active, v)
		out := make([]any, 0, len(v.Iterate()))
		for _, item := range v.Iterate() {
			converted, err := p.value(item, depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, converted)
		}
		return out, nil
	}
	size, err := valueBytes(value)
	if err != nil {
		return nil, err
	}
	p.bytes += size
	if p.bytes > maxBytes {
		return nil, fmt.Errorf("превышен размер параметров")
	}
	return value, nil
}
