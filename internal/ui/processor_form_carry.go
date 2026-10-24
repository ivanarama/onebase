package ui

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	processorpkg "github.com/ivantit66/onebase/internal/processor"
)

// Перенос состояния формы обработки между событиями.
//
// У формы документа реквизит без элемента переживает событие: значение лежит
// в базе или едет в браузер скрытым полем (formAttrs). У обработки базы нет, а
// скрытого поля не было: параметр без элемента (служебный флаг «СкрытьМастера»)
// или с нередактируемым элементом браузер обратно не присылает, следующее
// событие видело его пустым — и hidden_when/readonly_when откатывались, хотя
// обработчик флаг не трогал.
//
// Просто отправить такие значения в браузер нельзя: параметр обработки не
// знает полевых политик и маски, и сервер кладёт туда что угодно — так у
// оператора КЦ в ответе формы оказывался настоящий номер звонящего. Поэтому
// состояние едет запечатанным: AES-GCM с ключом процесса, привязанным к
// обработке и пользователю. Браузер его не читает и не подделывает (служебный
// флаг не поменять руками), сервер раскрывает его до обработчика. Ключ живёт
// до перезапуска сервера: после него старый токен не раскрывается, и форма
// ведёт себя как раньше — значения без элемента пусты.
//
// Значения реквизитов без элемента из открытых values ответа убираются: всё,
// что форме нужно о них знать, сервер уже учёл в elementStates.

// processorCarryField — служебное поле с запечатанным состоянием.
const processorCarryField = "_ob_carry"

// processorCarryMaxBytes — предел токена: состояние формы — флаги и ссылки, а
// не данные; больший токен отбрасывается, а не разбирается.
const processorCarryMaxBytes = 64 << 10

func (s *Server) processorCarryAEAD() cipher.AEAD {
	s.carryOnce.Do(func() {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return
		}
		if aead, err := cipher.NewGCM(block); err == nil {
			s.carryAEAD = aead
		}
	})
	return s.carryAEAD
}

func processorCarryAAD(r *http.Request, proc *processorpkg.Processor) []byte {
	return []byte("obcarry1|" + strings.ToLower(proc.Name) + "|" + userKeyFromRequest(r))
}

// processorCarryFieldName — имя служебного поля, не совпадающее с параметром.
func processorCarryFieldName(proc *processorpkg.Processor) string {
	return processorServiceFieldName(proc.Params, processorCarryField)
}

// sealProcessorCarry запечатывает состояние формы; пустая строка — нечего
// переносить или шифрование недоступно.
func (s *Server) sealProcessorCarry(r *http.Request, proc *processorpkg.Processor, values map[string]string) string {
	aead := s.processorCarryAEAD()
	if aead == nil || proc == nil || len(values) == 0 {
		return ""
	}
	plain, err := json.Marshal(values)
	if err != nil {
		return ""
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return ""
	}
	sealed := aead.Seal(nonce, nonce, plain, processorCarryAAD(r, proc))
	return base64.RawURLEncoding.EncodeToString(sealed)
}

// openProcessorCarry раскрывает присланное состояние. Подделка, чужая
// обработка или пользователь, токен до перезапуска — пустой результат.
func (s *Server) openProcessorCarry(r *http.Request, proc *processorpkg.Processor) map[string]string {
	aead := s.processorCarryAEAD()
	if aead == nil || proc == nil {
		return nil
	}
	token, _ := processorPostFormText(r, processorCarryFieldName(proc))
	if token == "" || len(token) > processorCarryMaxBytes {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) < aead.NonceSize() {
		return nil
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], processorCarryAAD(r, proc))
	if err != nil {
		return nil
	}
	var values map[string]string
	if json.Unmarshal(plain, &values) != nil {
		return nil
	}
	return values
}

// processorCarryParam — параметр, который переносится: скалярный (не файл).
func processorCarryParam(p processorpkg.Param) bool {
	return p.Type != "file" && p.Type != processorBinaryParamType
}

// processorUnplaced — имена параметров и реквизитов формы без элемента на
// форме: их значения в открытом ответе не нужны.
func processorUnplaced(proc *processorpkg.Processor, form *metadata.FormModule) map[string]bool {
	placed := map[string]bool{}
	if form != nil {
		form.Walk(func(el *metadata.FormElement) bool {
			if el != nil && strings.TrimSpace(el.DataPath) != "" {
				placed[strings.ToLower(dpFieldName(el.DataPath))] = true
			}
			return true
		})
	}
	out := map[string]bool{}
	for _, p := range proc.Params {
		if processorCarryParam(p) && !placed[strings.ToLower(p.Name)] {
			out[strings.ToLower(p.Name)] = true
		}
	}
	for _, name := range formAttrNames(form, processorVirtualEntity(proc)) {
		if !placed[strings.ToLower(name)] {
			out[strings.ToLower(name)] = true
		}
	}
	return out
}

// processorCarryValues собирает переносимое состояние из полей объекта.
func processorCarryValues(proc *processorpkg.Processor, form *metadata.FormModule, fields map[string]any) map[string]string {
	out := map[string]string{}
	add := func(name string) {
		for key, v := range fields {
			if strings.EqualFold(key, name) {
				if text, ok := processorCarryString(v); ok {
					out[name] = text
				}
				return
			}
		}
	}
	for _, p := range proc.Params {
		if processorCarryParam(p) {
			add(p.Name)
		}
	}
	for _, name := range formAttrNames(form, processorVirtualEntity(proc)) {
		add(name)
	}
	return out
}

// processorCarryString — значение строкой, которую понимает parseParamValue
// (и typeFormAttrValue). Составные значения (таблицы, структуры) не
// переносятся: это не состояние формы.
func processorCarryString(v any) (string, bool) {
	switch t := v.(type) {
	case nil:
		return "", true
	case string:
		return t, true
	case bool:
		return strconv.FormatBool(t), true
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), true
	case int:
		return strconv.Itoa(t), true
	case int64:
		return strconv.FormatInt(t, 10), true
	case time.Time:
		if t.IsZero() {
			return "", true
		}
		return t.In(time.Local).Format("2006-01-02T15:04:05"), true
	case *interpreter.Ref:
		if t == nil {
			return "", true
		}
		return t.UUID, true
	}
	return "", false
}

// applyProcessorCarryParams подставляет перенесённые значения параметров,
// которые браузер не прислал: элемента нет, он нередактируемый или скрыт.
// Присланное значение сильнее: его только что изменил пользователь.
func applyProcessorCarryParams(proc *processorpkg.Processor, carry map[string]string, paramValues map[string]any) {
	for _, p := range proc.Params {
		if !processorCarryParam(p) {
			continue
		}
		if _, sent := paramValues[p.Name]; sent {
			continue
		}
		if text, ok := carry[p.Name]; ok {
			paramValues[p.Name] = parseParamValue(text, p.Type)
		}
	}
}

// applyProcessorCarryAttrs — то же для реквизитов формы обработки.
func (s *Server) applyProcessorCarryAttrs(r *http.Request, proc *processorpkg.Processor, form *metadata.FormModule, carry map[string]string, fields map[string]any) {
	if form == nil || fields == nil {
		return
	}
	submitted := submittedFormKeys(r)
	for _, a := range form.Attributes {
		if !formAttrIsScalar(a) || a.MainAttribute || formKeySubmitted(submitted, a.Name) {
			continue
		}
		text, ok := carry[a.Name]
		if !ok {
			continue
		}
		val := typeFormAttrValue(a.TypeRef, text)
		if refName := attrRefEntityName(a.TypeRef); refName != "" && val != nil {
			if ref := s.formAttrRef(r.Context(), refName, text); ref != nil {
				fields[a.Name] = ref
				continue
			}
		}
		fields[a.Name] = val
	}
}

// finishProcessorCarry кладёт в ответ события новое состояние и убирает из
// открытых values реквизиты без элемента.
func (s *Server) finishProcessorCarry(r *http.Request, proc *processorpkg.Processor, form *metadata.FormModule, fields map[string]any, resp *formEventResponse) {
	if resp == nil || proc == nil {
		return
	}
	unplaced := processorUnplaced(proc, form)
	for key := range resp.Values {
		if unplaced[strings.ToLower(key)] {
			delete(resp.Values, key)
		}
	}
	token := s.sealProcessorCarry(r, proc, processorCarryValues(proc, form, fields))
	if token == "" {
		return
	}
	if resp.Values == nil {
		resp.Values = map[string]any{}
	}
	resp.Values[processorCarryFieldName(proc)] = token
}

// setProcessorCarryContext — начальное состояние формы обработки для
// разметки: служебное поле создаёт managed.js (formAttrs).
func (s *Server) setProcessorCarryContext(r *http.Request, data map[string]any, proc *processorpkg.Processor) {
	if proc == nil {
		return
	}
	fields := map[string]any{}
	if values, ok := data["Values"].(map[string]string); ok {
		for k, v := range values {
			fields[k] = v
		}
	}
	name := processorCarryFieldName(proc)
	data["ProcessorCarryAttrs"] = []string{name}
	data["ProcessorCarryValues"] = map[string]string{name: s.sealProcessorCarry(r, proc, processorCarryValues(proc, proc.ManagedForm(), fields))}
}
