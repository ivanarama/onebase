package ui

import (
	"fmt"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"strings"
)

// Audio source/caption bindings are display state for the lifetime of the
// form, not only during the AudioPlay callback. Selecting a row, preparing a
// cached recording or clearing it on Close must not turn that state into an
// unsaved object edit. Persisted fields and editable bindings are never exempt.
func audioViewStateFields(form *metadata.FormModule, entity *metadata.Entity) map[string]bool {
	if form == nil {
		return nil
	}
	key := func(path string) string { return strings.ToLower(strings.TrimSpace(dpFieldName(path))) }
	wanted := map[string]bool{}
	walkBrowserFormElements(form, func(v browserFormElementVisit) {
		if v.element.Kind == metadata.FormElementAudio {
			wanted[key(v.element.DataPath)] = true
			wanted[key(v.element.TitleFrom)] = true
		}
	})
	if len(wanted) == 0 {
		return nil
	}
	fields := map[string]bool{}
	for _, attr := range form.Attributes {
		if attr == nil || attr.Save || attr.MainAttribute || !formAttrIsScalar(attr) || audioPlaybackServiceField(attr.Name) || entityField(entity, attr.Name) != nil {
			continue
		}
		typ := strings.ToLower(strings.TrimSpace(attr.TypeRef))
		if typ != "string" && !strings.HasPrefix(typ, "string(") && typ != "строка" && !strings.HasPrefix(typ, "строка(") {
			continue
		}
		if name := key(attr.Name); wanted[name] {
			fields[name] = true
		}
	}
	walkBrowserFormElements(form, func(v browserFormElementVisit) {
		if v.element.Kind == metadata.FormElementAudio || v.effectiveReadOnly || v.element.Kind == metadata.FormElementLabel {
			return
		}
		// A second editable control owns real user input, including a control
		// that is only conditionally readonly. Keep its ordinary dirty guard.
		delete(fields, key(v.element.DataPath))
	})
	return fields
}

func audioPlaybackServiceField(name string) bool {
	if entityServiceFieldName(name) {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "ссылка", "reference", "_id": // native form reference/identity carriers
		return true
	}
	return false
}

func transientAudioPlaybackDirty(obj *runtime.Object, before map[string]string, tables map[string][]map[string]any, excluded map[string]bool) bool {
	if len(excluded) == 0 || obj == nil {
		return transientManagedStateDirty(obj, before, tables)
	}
	// Read-only copies; never mutate the real object, its setter journal, the
	// response values or the caller's close baseline.
	projection := *obj
	projection.Fields = make(map[string]any, len(obj.Fields))
	for name, value := range obj.Fields {
		if !excluded[strings.ToLower(name)] {
			projection.Fields[name] = value
		}
	}
	prior := make(map[string]string, len(before))
	for name, value := range before {
		if !excluded[strings.ToLower(name)] {
			prior[name] = value
		}
	}
	return transientManagedStateDirty(&projection, prior, tables)
}

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
