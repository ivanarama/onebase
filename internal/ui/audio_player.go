package ui

import (
	"fmt"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"strings"
)

// Only the exact native player's nonpersistent carriers are playback view
// state. Never exempt entity/service fields, saved attributes or a carrier
// shared with another control. This projection is for AudioPlay, not Close.
func audioPlaybackComparison(form *metadata.FormModule, entity *metadata.Entity, element, event string) (*metadata.FormModule, map[string]bool) {
	if form == nil || event != string(metadata.FormEventAudioPlay) {
		return form, nil
	}
	var player *metadata.FormElement
	matches := 0
	walkBrowserFormElements(form, func(v browserFormElementVisit) {
		if v.element.Kind == metadata.FormElementAudio && strings.EqualFold(v.element.Name, element) {
			player = v.element
			matches++
		}
	})
	if matches != 1 {
		return form, nil
	}
	key := func(path string) string { return strings.ToLower(strings.TrimSpace(dpFieldName(path))) }
	wanted := map[string]bool{key(player.DataPath): true, key(player.TitleFrom): true}
	fields := map[string]bool{}
	for _, attr := range form.Attributes {
		if attr == nil || attr.Save || attr.MainAttribute || !formAttrIsScalar(attr) || audioPlaybackServiceField(attr.Name) || (entity != nil && entityField(entity, attr.Name) != nil) {
			continue
		}
		typ := strings.ToLower(strings.TrimSpace(attr.TypeRef))
		if typ != "string" && !strings.HasPrefix(typ, "string(") && typ != "строка" && !strings.HasPrefix(typ, "строка(") {
			continue
		}
		if name := strings.ToLower(strings.TrimSpace(attr.Name)); wanted[name] {
			fields[name] = true
		}
	}
	walkBrowserFormElements(form, func(v browserFormElementVisit) {
		if v.element.Kind != metadata.FormElementAudio {
			delete(fields, key(v.element.DataPath))
		}
	})
	if len(fields) == 0 {
		return form, nil
	}
	projected := *form
	projected.Attributes = nil
	for _, attr := range form.Attributes {
		if attr == nil || !fields[strings.ToLower(strings.TrimSpace(attr.Name))] {
			projected.Attributes = append(projected.Attributes, attr)
		}
	}
	return &projected, fields
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
