package interpreter

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AIRequest — запрос к ИИ-помощнику из DSL. Если ImageB64 непуст, это
// мультимодальный (vision) запрос распознавания.
type AIRequest struct {
	Task        string // профиль маршрутизации: "анализ" | "документы" | ...
	System      string // системная инструкция
	Prompt      string // пользовательский промпт
	JSON        bool   // запросить строгий JSON
	Temperature float64
	ImageB64    string // base64 изображения/PDF (для vision)
	MimeType    string
}

// AIAssistant — минимальный интерфейс ИИ-помощника для DSL-функций. Конкретная
// реализация (поверх internal/llm) строится в слое обвязки из конфига базы, так
// что пакет interpreter не зависит от llm/storage.
type AIAssistant interface {
	Ask(req AIRequest) (string, error)
	Configured() bool
}

// Профили задач по умолчанию для builtin'ов (соответствуют llm.Task*).
const (
	aiTaskAnalysis  = "анализ"
	aiTaskDocuments = "документы"
)

// NewLLMFunctions возвращает DSL-функции ИИ-помощника для инъекции в extraVars.
// При ai == nil (или не настроенном помощнике) функции дают понятную ошибку.
func NewLLMFunctions(ai AIAssistant) map[string]any {
	ensure := func() {
		if ai == nil || !ai.Configured() {
			panic(userError{Msg: "ИИ-помощник не настроен — укажите провайдера, модель и ключ в настройках базы"})
		}
	}

	// askParams читает необязательную Структуру параметров второго аргумента.
	askParams := func(args []any, prompt string, jsonMode bool) string {
		ensure()
		req := AIRequest{Task: aiTaskAnalysis, Prompt: prompt, JSON: jsonMode}
		if len(args) >= 2 {
			if p, ok := args[1].(*Struct); ok && p != nil {
				if v := p.Get("задача"); v != nil {
					req.Task = fmt.Sprintf("%v", v)
				}
				if v := p.Get("система"); v != nil {
					req.System = fmt.Sprintf("%v", v)
				}
				if v := p.Get("формат"); v != nil && strings.EqualFold(fmt.Sprintf("%v", v), "json") {
					req.JSON = true
				}
				if v := p.Get("температура"); v != nil {
					if f, ok := toFloat(v); ok {
						req.Temperature = f
					}
				}
			}
		}
		out, err := ai.Ask(req)
		if err != nil {
			panic(userError{Msg: "ЗапросИИ: " + err.Error()})
		}
		return out
	}

	запросИИ := BuiltinFunc(func(args []any, file string, line int) (any, error) {
		return askParams(args, strArg(args, 0), false), nil
	})

	запросИИДжейсон := BuiltinFunc(func(args []any, file string, line int) (any, error) {
		return askParams(args, strArg(args, 0), true), nil
	})

	// recognize выполняет vision-запрос по готовым данным изображения.
	recognize := func(b64, mime, prompt string) string {
		ensure()
		out, err := ai.Ask(AIRequest{Task: aiTaskDocuments, Prompt: prompt, ImageB64: b64, MimeType: mime})
		if err != nil {
			panic(userError{Msg: "РаспознатьДокумент: " + err.Error()})
		}
		return out
	}

	распознатьДокумент := BuiltinFunc(func(args []any, file string, line int) (any, error) {
		path := strArg(args, 0)
		prompt := strArg(args, 1)
		data, err := os.ReadFile(path) //nolint:gosec // G703: explicit trusted DSL file capability; DenyFile sandbox overlays RecognizeDocument
		if err != nil {
			panic(userError{Msg: "РаспознатьДокумент: не удалось прочитать файл: " + err.Error()})
		}
		return recognize(base64.StdEncoding.EncodeToString(data), mimeByExt(path), prompt), nil
	})

	// РаспознатьИзображение(ДанныеBase64, ТипMIME, Промпт) — vision по данным из
	// памяти (загруженный файл/вложение), без записи на диск. Нужна для умной
	// загрузки документов из UI (план 51, F6).
	распознатьИзображение := BuiltinFunc(func(args []any, file string, line int) (any, error) {
		b64 := strArg(args, 0)
		mime := strArg(args, 1)
		if mime == "" {
			mime = "image/png"
		}
		prompt := strArg(args, 2)
		return recognize(b64, mime, prompt), nil
	})

	return map[string]any{
		"ЗапросИИ":              запросИИ,
		"AIQuery":               запросИИ,
		"ЗапросИИДжейсон":       запросИИДжейсон,
		"AIQueryJSON":           запросИИДжейсон,
		"РаспознатьДокумент":    распознатьДокумент,
		"RecognizeDocument":     распознатьДокумент,
		"РаспознатьИзображение": распознатьИзображение,
		"RecognizeImage":        распознатьИзображение,
	}
}

// mimeByExt угадывает MIME по расширению файла для vision-вызова.
func mimeByExt(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".pdf":
		return "application/pdf"
	default:
		return "application/octet-stream"
	}
}
