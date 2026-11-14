package ui

import (
	"fmt"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"strings"
)

// Unsaved form attributes are presentation state, independently of controls
// bound to them. Entity fields, service fields and persistent table parts keep
// their normal dirty comparison even if an attribute reuses their name.
func formViewStateFields(form *metadata.FormModule, entity *metadata.Entity) map[string]bool {
	if form == nil {
		return nil
	}
	fields := map[string]bool{}
	for _, attr := range form.Attributes {
		if attr == nil || attr.Save || attr.MainAttribute || audioPlaybackServiceField(attr.Name) || entityField(entity, attr.Name) != nil {
			continue
		}
		persistentTable := false
		if entity != nil {
			for _, tp := range entity.TableParts {
				persistentTable = persistentTable || strings.EqualFold(tp.Name, attr.Name)
			}
		}
		if !persistentTable {
			fields[strings.ToLower(strings.TrimSpace(attr.Name))] = true
		}
	}
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

func transientManagedFormDirty(obj *runtime.Object, before map[string]string, tables map[string][]map[string]any, excluded map[string]bool) bool {
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
	projection.TablePartRows = make(map[string][]map[string]any, len(obj.TablePartRows))
	for name, rows := range obj.TablePartRows {
		if !excluded[strings.ToLower(name)] {
			projection.TablePartRows[name] = rows
		}
	}
	priorTables := make(map[string][]map[string]any, len(tables))
	for name, rows := range tables {
		if !excluded[strings.ToLower(name)] {
			priorTables[name] = rows
		}
	}
	return transientManagedStateDirty(&projection, prior, priorTables)
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
