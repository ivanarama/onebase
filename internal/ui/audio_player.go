package ui

import (
	"fmt"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"strings"
)

func hasAudioPlayer(form *metadata.FormModule) bool {
	found := false
	walkBrowserFormElements(form, func(v browserFormElementVisit) {
		if v.element.Kind == metadata.FormElementAudio {
			found = true
		}
	})
	return found
}

func audioSource(ctx map[string]any, field string) string {
	id, err := uuid.Parse(audioValue(ctx, field))
	if err != nil || id == uuid.Nil {
		return ""
	}
	return id.String()
}

func audioValue(ctx map[string]any, field string) string {
	switch values := ctx["Values"].(type) {
	case map[string]string:
		return values[field]
	case map[string]any:
		if v := values[field]; v != nil {
			return fmt.Sprint(v)
		}
	}
	return ""
}

// A host with scoped UI options must pass an explicit false for excluded roles.
// Without a host override, audio_player is the portable opt-in for audio-only UI.
func audioOnlyMode(ctx map[string]any) bool {
	ent, _ := ctx["Entity"].(*metadata.Entity)
	if ent == nil || !ent.AudioPlayer {
		return false
	}
	if mode, ok := ctx["AudioOnlyPlayer"].(bool); ok {
		return mode
	}
	return true
}

func addFormAudioBuiltins(vars map[string]any, form *metadata.FormModule, stops *[]string) {
	stop := interpreter.BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("ОстановитьАудио: требуется имя аудиоплеера")
		}
		name, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("ОстановитьАудио: требуется строка")
		}
		matches := 0
		walkBrowserFormElements(form, func(v browserFormElementVisit) {
			if v.element.Kind == metadata.FormElementAudio && strings.EqualFold(v.element.Name, name) {
				matches++
			}
		})
		if matches != 1 {
			return nil, fmt.Errorf("ОстановитьАудио: аудиоплеер не найден или имя неоднозначно")
		}
		*stops = append(*stops, name)
		return nil, nil
	})
	vars["ОстановитьАудио"], vars["StopAudio"] = stop, stop
}
