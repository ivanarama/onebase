package ui

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
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

// Верхняя граница приёма токена. При выдаче дополнительно учитываются
// остальные поля следующего события и URL-кодирование: токен не занимает
// весь бюджет обычного POST. Лимит тела запроса не увеличивается.
const processorCarryMaxBytes = int(defaultFormMemoryBytes)

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

// processorCarryFieldName выделяет имя только для carry: параметры и реквизиты
// конкретной формы делят пространство имён с этим скрытым полем. Остальные
// serviceFields сохраняют прежнее распределение.
func processorCarryFieldName(proc *processorpkg.Processor, form *metadata.FormModule) string {
	params := append([]processorpkg.Param(nil), proc.Params...)
	if form != nil {
		for _, attr := range form.Attributes {
			if attr != nil {
				params = append(params, processorpkg.Param{Name: attr.Name})
			}
		}
	}
	return processorServiceFieldName(params, processorCarryField)
}

// sealProcessorCarry запечатывает состояние формы; пустая строка — нечего
// переносить или шифрование недоступно.
func (s *Server) sealProcessorCarry(r *http.Request, proc *processorpkg.Processor, form *metadata.FormModule, values map[string]string) (string, error) {
	aead := s.processorCarryAEAD()
	if aead == nil || proc == nil || len(values) == 0 {
		return "", nil
	}
	plain, err := json.Marshal(values)
	if err != nil {
		return "", nil
	}
	tokenBytes := base64.RawURLEncoding.EncodedLen(len(plain) + aead.NonceSize() + aead.Overhead())
	bodyLimit := processorFormBodyLimit(r, s.effectiveUploadLimit(), processorRequestControlsForForm(proc, form))
	if tokenBytes > processorCarryMaxBytes || int64(processorCarryPostBytes(r, proc, form, values, tokenBytes)) > bodyLimit {
		return "", i18nerr.New("Состояние формы обработки превышает допустимый размер. Сократите значения полей и повторите действие.")
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", nil
	}
	sealed := aead.Seal(nonce, nonce, plain, processorCarryAAD(r, proc))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

// processorCarryPostBytes оценивает повторную отправку неизменённой формы.
// Старый токен заменяется новым; значения контролов берутся ПОСЛЕ обработчика.
// Учитываем также служебные поля, имена и повторяющиеся поля исходного POST.
// На начальной странице POST ещё нет: envelope строится из метаданных формы.
// Скрытые/readonly скалярные контролы тоже считаются — это верхняя граница,
// безопасная при изменении hidden_when/readonly_when в ответе обработчика.
func processorCarryPostBytes(r *http.Request, proc *processorpkg.Processor, form *metadata.FormModule, values map[string]string, tokenBytes int) int {
	body := make(url.Values)
	for name, texts := range r.PostForm {
		body[name] = append([]string(nil), texts...)
	}
	// RawURL base64 использует только безопасные символы URLSearchParams.
	body.Set(processorCarryFieldName(proc, form), strings.Repeat("a", tokenBytes))
	escapedBytes := func(text string) int {
		return len(url.QueryEscape(text)) + 2*strings.Count(text, "~")
	}
	setLongest := func(name, text string) {
		name = processorServiceFieldName(proc.Params, name)
		if escapedBytes(text) > escapedBytes(body.Get(name)) {
			body.Set(name, text)
		}
	}
	setLongest("_kind", "object")
	controlCounts := make(map[string]int)
	walkBrowserFormElements(form, func(visit browserFormElementVisit) {
		el := visit.element
		setLongest("_element", el.Name)
		for event := range el.Handlers {
			setLongest("_event", string(event))
		}
		if visit.parentTablePart != nil || !processorElementPostsParam(el.Kind) || el.DataPath == "" || strings.Count(el.DataPath, ".") > 1 {
			return
		}
		name := dpFieldName(el.DataPath)
		controlCounts[name]++
		for key, text := range values {
			if strings.EqualFold(key, name) {
				// Preserve duplicate successful controls in the original POST.
				count := max(controlCounts[name], len(body[name]))
				body[name] = make([]string, count)
				for i := range body[name] {
					body[name][i] = text
				}
				break
			}
		}
		if el.Kind == metadata.FormElementCheckbox {
			body.Set(processorParamPresenceName(proc.Params, name), "1")
		}
	})
	// Go leaves '~' unescaped; browser URLSearchParams encodes it as %7E.
	// Go's extra escaping of '*' is conservative for this size check.
	encoded := body.Encode()
	return len(encoded) + 2*strings.Count(encoded, "~")
}

// openProcessorCarry раскрывает присланное состояние. Подделка, чужая
// обработка или пользователь, токен до перезапуска — пустой результат.
func (s *Server) openProcessorCarry(r *http.Request, proc *processorpkg.Processor, form *metadata.FormModule) map[string]string {
	aead := s.processorCarryAEAD()
	if aead == nil || proc == nil {
		return nil
	}
	token, _ := processorPostFormText(r, processorCarryFieldName(proc, form))
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
	// Колонки и произвольные потомки таблицы не размещают корневое поле,
	// даже когда их короткий data_path совпадает с его именем.
	walkBrowserFormElements(form, func(visit browserFormElementVisit) {
		el := visit.element
		if visit.parentTablePart != nil || el.Kind == metadata.FormElementTablePart {
			return
		}
		path := strings.TrimSpace(el.DataPath)
		scope, name, qualified := strings.Cut(path, ".")
		if qualified {
			if !strings.EqualFold(scope, "Объект") && !strings.EqualFold(scope, "Object") &&
				!strings.EqualFold(scope, "Форма") && !strings.EqualFold(scope, "Form") {
				return
			}
			if strings.Contains(name, ".") {
				return
			}
		} else {
			name = path
		}
		if name != "" {
			placed[strings.ToLower(name)] = true
		}
	})
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
	unchecked := map[string]bool{}
	walkBrowserFormElements(form, func(visit browserFormElementVisit) {
		el := visit.element
		if el.Kind != metadata.FormElementCheckbox || visit.parentTablePart != nil || visit.effectiveReadOnly || el.DataPath == "" || strings.Count(el.DataPath, ".") > 1 {
			return
		}
		name := dpFieldName(el.DataPath)
		// Только реальный POST-marker допустимого контрола. Query string,
		// readonly-контрол и совпавший с helper обычный параметр не снимают флаг.
		if _, present := processorPostFormText(r, processorParamPresenceName(proc.Params, name)); present {
			unchecked[strings.ToLower(name)] = true
		}
	})
	for _, a := range form.Attributes {
		if !formAttrIsScalar(a) || a.MainAttribute || formKeySubmitted(submitted, a.Name) {
			continue
		}
		if strings.HasPrefix(strings.ToLower(a.TypeRef), "bool") && unchecked[strings.ToLower(a.Name)] {
			fields[a.Name] = false
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
	token, err := s.sealProcessorCarry(r, proc, form, processorCarryValues(proc, form, fields))
	if err != nil {
		resp.OK = false
		if resp.Error != "" {
			resp.Error += "\n"
		}
		resp.Error += s.errText(r, err)
		return
	}
	if token == "" {
		return
	}
	if resp.Values == nil {
		resp.Values = map[string]any{}
	}
	resp.Values[processorCarryFieldName(proc, form)] = token
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
	form := proc.ManagedForm()
	name := processorCarryFieldName(proc, form)
	data["ProcessorCarryAttrs"] = []string{name}
	token, err := s.sealProcessorCarry(r, proc, form, processorCarryValues(proc, form, fields))
	if err != nil {
		message := s.errText(r, err)
		if previous, ok := data["RunError"].(string); ok && previous != "" {
			message = previous + "\n" + message
		}
		data["RunError"] = message
	}
	data["ProcessorCarryValues"] = map[string]string{name: token}
}
