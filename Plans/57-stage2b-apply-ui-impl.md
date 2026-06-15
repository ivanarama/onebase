# UI генерации + применение — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Замкнуть генерацию каркаса end-to-end: эндпоинт применения diff в файловую конфигурацию + панель генерации в конфигураторе (показ diff → «Применить»).

**Architecture:** Бэкенд `ai-apply` (валидация пути + запись в `b.Path`, БД-базы отклоняются) тестируется через чистые `validApplyPath`/`applyChanges`. UI — отдельная плавающая панель `#cfggen-panel` в `configurator_tmpl.go` (изолированный IIFE), покрыта структурным тестом; визуальная приёмка — вручную.

**Tech Stack:** Go; embedded HTML/JS в Go-строках; тесты `testing` + `renderCfgFoot`.

**Дизайн:** [57-stage2b-apply-ui.md](57-stage2b-apply-ui.md). **Ветка:** `feature/57-stage2b-apply-ui`.

---

## Структура файлов

- Создать: `internal/launcher/ai_apply.go` — `validApplyPath`, `applyChanges`, `cfgAIApply`.
- Создать: `internal/launcher/ai_apply_test.go` — тесты бэкенда.
- Изменить: `internal/launcher/server.go` — маршрут `ai-apply`.
- Изменить: `internal/launcher/configurator_tmpl.go` — панель `#cfggen-panel` + IIFE.
- Создать: `internal/launcher/ai_generate_render_test.go` — структурный тест панели.

---

## Task 1: бэкенд применения

**Files:**
- Create: `internal/launcher/ai_apply.go`
- Modify: `internal/launcher/server.go`
- Test: `internal/launcher/ai_apply_test.go`

- [ ] **Step 1: Написать падающий тест**

Создать `internal/launcher/ai_apply_test.go`:

```go
package launcher

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidApplyPath(t *testing.T) {
	ok := []string{"catalogs/клиент.yaml", "documents/заявка.yaml", "registers/продажи.yaml", "enums/статус.yaml"}
	for _, p := range ok {
		if _, err := validApplyPath(p); err != nil {
			t.Errorf("ожидался валидный путь %q: %v", p, err)
		}
	}
	bad := []string{"", "../evil.yaml", "catalogs/../x.yaml", "secret/x.yaml", "catalogs/a/b.yaml", "catalogs/клиент.txt", "catalogs/.yaml", "app.yaml"}
	for _, p := range bad {
		if _, err := validApplyPath(p); err == nil {
			t.Errorf("ожидалась ошибка для пути %q", p)
		}
	}
}

func TestApplyChanges_WritesFile(t *testing.T) {
	dir := t.TempDir()
	n, err := applyChanges(dir, []GenChange{{Path: "catalogs/клиент.yaml", NewContent: "name: Клиент\n"}})
	if err != nil || n != 1 {
		t.Fatalf("applyChanges: n=%d err=%v", n, err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "catalogs", "клиент.yaml"))
	if err != nil || string(got) != "name: Клиент\n" {
		t.Fatalf("файл не записан правильно: %q err=%v", got, err)
	}
}

func TestApplyChanges_RejectsBadPath(t *testing.T) {
	dir := t.TempDir()
	n, err := applyChanges(dir, []GenChange{{Path: "../evil.yaml", NewContent: "x"}})
	if err == nil {
		t.Error("ожидалась ошибка для пути с ..")
	}
	if n != 0 {
		t.Errorf("ничего не должно примениться, applied=%d", n)
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(dir), "evil.yaml")); !os.IsNotExist(statErr) {
		t.Error("файл вне базы не должен быть создан")
	}
}
```

- [ ] **Step 2: Запустить — FAIL (нет validApplyPath/applyChanges):**

Run: `go test ./internal/launcher/ -run 'TestValidApplyPath|TestApplyChanges' -count=1`
Expected: FAIL — функций нет.

- [ ] **Step 3: Создать `internal/launcher/ai_apply.go`**

```go
package launcher

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
)

// metadataApplySubdirs — подкаталоги, в которые ai-apply разрешает запись
// (метаданные, генерируемые этапом 2a; как в kindSubdir).
var metadataApplySubdirs = map[string]bool{
	"catalogs": true, "documents": true, "registers": true,
	"inforegs": true, "enums": true, "accounts": true, "accountregs": true,
}

// validApplyPath проверяет, что rel — это <метаданные-подкаталог>/<безопасное>.yaml.
// Возвращает нормализованный относительный путь (slash) или ошибку.
func validApplyPath(rel string) (string, error) {
	rel = strings.TrimSpace(filepath.ToSlash(rel))
	parts := strings.Split(rel, "/")
	if len(parts) != 2 {
		return "", fmt.Errorf("недопустимый путь: %q", rel)
	}
	if !metadataApplySubdirs[parts[0]] {
		return "", fmt.Errorf("недопустимый подкаталог: %q", parts[0])
	}
	if !strings.HasSuffix(parts[1], ".yaml") {
		return "", fmt.Errorf("ожидался .yaml: %q", rel)
	}
	name := strings.TrimSuffix(parts[1], ".yaml")
	if _, err := safeFileName(name); err != nil {
		return "", err
	}
	return parts[0] + "/" + parts[1], nil
}

// applyChanges записывает изменения в файловую конфигурацию baseDir. Возвращает
// число применённых и первую ошибку. Пути валидируются; запись только внутрь baseDir.
func applyChanges(baseDir string, changes []GenChange) (int, error) {
	if strings.TrimSpace(baseDir) == "" {
		return 0, fmt.Errorf("пустой путь конфигурации")
	}
	applied := 0
	for _, ch := range changes {
		rel, err := validApplyPath(ch.Path)
		if err != nil {
			return applied, err
		}
		full := filepath.Join(baseDir, filepath.FromSlash(rel))
		cleanBase := filepath.Clean(baseDir)
		if !strings.HasPrefix(filepath.Clean(full), cleanBase+string(os.PathSeparator)) {
			return applied, fmt.Errorf("путь вне базы: %q", rel)
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return applied, err
		}
		if err := os.WriteFile(full, []byte(ch.NewContent), 0o644); err != nil {
			return applied, err
		}
		applied++
	}
	return applied, nil
}

// cfgAIApply применяет предложенный diff (из ai-generate) в файловую конфигурацию.
// БД-базы пока не поддерживаются.
func (h *handler) cfgAIApply(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	if b.ConfigSource == "database" {
		writeJSON(w, 200, map[string]any{"error": "Применение в БД-базы пока не поддерживается — используйте файловую базу."})
		return
	}
	var req struct {
		Changes []GenChange `json:"changes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"error": "Некорректный запрос"})
		return
	}
	if len(req.Changes) == 0 {
		writeJSON(w, 400, map[string]any{"error": "Нет изменений для применения"})
		return
	}
	applied, err := applyChanges(b.Path, req.Changes)
	if err != nil {
		writeJSON(w, 200, map[string]any{"error": err.Error(), "applied": applied})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "applied": applied})
}
```

ВАЖНО: `GenChange`, `safeFileName`, `writeJSON`, `h.store.Get`, `*Base` с полями
`Path`/`ConfigSource` — существующие (из 2a / пакета launcher). Подтверди, что у `Base`
есть строковые поля `Path` и `ConfigSource` (см. их использование в `forms_handlers.go`,
`widget_handlers.go`). Если имена иные — приведи к фактическим.

- [ ] **Step 4: Запустить тесты — PASS:**

Run: `go test ./internal/launcher/ -run 'TestValidApplyPath|TestApplyChanges' -count=1`
Expected: PASS (три теста).

- [ ] **Step 5: Зарегистрировать маршрут**

В `internal/launcher/server.go` найти `r.Post("/bases/{id}/configurator/ai-generate", s.h.cfgAIGenerate)` и добавить сразу после:
```go
		r.Post("/bases/{id}/configurator/ai-apply", s.h.cfgAIApply)
```

- [ ] **Step 6: Сборка + gofmt + commit:**

Run: `go build ./internal/launcher/` → успех.
Run: `gofmt -d internal/launcher/ai_apply.go` → пусто (CRLF-артефакты игнор).
```
git add internal/launcher/ai_apply.go internal/launcher/server.go internal/launcher/ai_apply_test.go
git commit -m "feat(configurator): эндпоинт ai-apply — запись каркаса в файловую базу (план 57, этап 2b)"
```

---

## Task 2: панель генерации в конфигураторе + структурный тест

**Files:**
- Modify: `internal/launcher/configurator_tmpl.go`
- Test: `internal/launcher/ai_generate_render_test.go`

- [ ] **Step 1: Написать падающий структурный тест**

Создать `internal/launcher/ai_generate_render_test.go`:

```go
package launcher

import (
	"strings"
	"testing"
)

func TestConfigurator_GeneratePanelWired(t *testing.T) {
	html := renderCfgFoot(t)
	for _, sub := range []string{
		"cfggen-panel", "cfggen-prompt", "cfggen-send", "cfggen-apply", "cfggen-reject",
		"configurator/ai-generate", "configurator/ai-apply",
	} {
		if !strings.Contains(html, sub) {
			t.Errorf("в cfg-foot нет %q — панель генерации не подключена", sub)
		}
	}
}
```

(`renderCfgFoot` — существующий хелпер в `langref_render_test.go`.)

- [ ] **Step 2: Запустить — FAIL (панели нет):**

Run: `go test ./internal/launcher/ -run TestConfigurator_GeneratePanelWired -count=1`
Expected: FAIL.

- [ ] **Step 3: Вставить панель в `configurator_tmpl.go`**

Найди конец IIFE существующей панели ИИ-ассистента — строку
```
  copy.addEventListener('click',function(){try{navigator.clipboard.writeText(out.textContent);msg.textContent='Скопировано';msg.style.color='#16a34a';}catch(e){}});
```
за которой идут `  }`, `})();`, `</script>`. ВСТАВЬ сразу ПОСЛЕ этого `</script>`
(и перед `</body></html>`) следующий блок (кнопка + панель + изолированный IIFE):

```html
<button id="cfggen-btn" title="Генерация каркаса ИИ" style="display:none;position:fixed;right:18px;bottom:78px;z-index:9000;width:48px;height:48px;border-radius:50%;background:#0ea5e9;color:#fff;border:none;cursor:pointer;font-size:22px;box-shadow:0 4px 14px rgba(14,165,233,.4)">🏗️</button>
<div id="cfggen-panel" style="display:none;position:fixed;right:18px;bottom:18px;z-index:9001;width:460px;max-width:calc(100vw - 24px);height:600px;max-height:calc(100vh - 40px);background:#fff;border:1px solid #cbd5e1;border-radius:12px;box-shadow:0 8px 32px rgba(0,0,0,.22);flex-direction:column;overflow:hidden;font-family:system-ui,sans-serif">
  <div style="background:#0ea5e9;color:#fff;padding:10px 14px;display:flex;align-items:center;gap:8px;font-weight:600;font-size:14px">🏗️ Генерация каркаса<span style="flex:1"></span><button type="button" id="cfggen-close" style="background:none;border:none;color:#fff;cursor:pointer;font-size:18px">×</button></div>
  <textarea id="cfggen-prompt" placeholder="Опишите, что создать. Напр.: справочник Клиенты и документ Заявка с табличной частью Товары" style="margin:10px;height:70px;resize:vertical;border:1px solid #cbd5e1;border-radius:8px;padding:8px;font-size:13px;font-family:inherit"></textarea>
  <div style="margin:8px 10px;display:flex;gap:8px;align-items:center;flex-wrap:wrap"><button id="cfggen-send" type="button" style="background:#0ea5e9;color:#fff;border:none;border-radius:8px;padding:6px 16px;cursor:pointer;font-size:13px">Сгенерировать</button><button id="cfggen-apply" type="button" style="background:#16a34a;color:#fff;border:none;border-radius:8px;padding:6px 16px;cursor:pointer;font-size:13px;display:none">Применить</button><button id="cfggen-reject" type="button" style="background:#e2e8f0;border:none;border-radius:8px;padding:6px 14px;cursor:pointer;font-size:13px;display:none">Отклонить</button><span id="cfggen-msg" style="font-size:11px"></span></div>
  <div id="cfggen-out" style="flex:1;overflow:auto;margin:0 10px 10px;background:#0f172a;color:#e2e8f0;border-radius:8px;padding:10px;font-size:12px"></div>
</div>
<script>
(function(){
  if(window.__cfgGenInit)return;window.__cfgGenInit=true;
  var base='{{.Base.ID}}';
  fetch('/bases/'+base+'/configurator/ai-enabled').then(function(r){return r.json();}).then(function(d){if(d&&d.enabled)init();}).catch(function(){});
  function init(){
    var btn=document.getElementById('cfggen-btn'),panel=document.getElementById('cfggen-panel');
    var prompt=document.getElementById('cfggen-prompt'),send=document.getElementById('cfggen-send'),out=document.getElementById('cfggen-out');
    var msg=document.getElementById('cfggen-msg'),applyBtn=document.getElementById('cfggen-apply'),rejectBtn=document.getElementById('cfggen-reject');
    var pending=null;
    btn.style.display='';
    btn.addEventListener('click',function(){panel.style.display='flex';btn.style.display='none';prompt.focus();});
    document.getElementById('cfggen-close').addEventListener('click',function(){panel.style.display='none';btn.style.display='';});
    function renderDiff(changes){
      out.textContent='';
      changes.forEach(function(ch){
        var box=document.createElement('div');box.style.marginBottom='8px';
        var head=document.createElement('div');head.style.fontWeight='600';head.style.fontSize='12px';head.style.color='#7dd3fc';
        head.textContent=(ch.kind||'')+' '+ch.path;
        var pre=document.createElement('pre');pre.style.margin='4px 0 0';pre.style.whiteSpace='pre-wrap';pre.style.wordBreak='break-word';pre.style.fontSize='11px';
        pre.textContent=ch.newContent||'';
        box.appendChild(head);box.appendChild(pre);out.appendChild(box);
      });
    }
    send.addEventListener('click',function(){
      var p=prompt.value.trim();if(!p){msg.textContent='Введите ТЗ';msg.style.color='#c00';return;}
      msg.textContent='Генерация...';msg.style.color='#666';out.textContent='';applyBtn.style.display='none';rejectBtn.style.display='none';send.disabled=true;pending=null;
      fetch('/bases/'+base+'/configurator/ai-generate',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({prompt:p})})
        .then(function(r){return r.json();})
        .then(function(d){
          if(d&&d.changes&&d.changes.length){
            pending=d.changes;renderDiff(d.changes);
            msg.textContent='Предложено объектов: '+d.changes.length;msg.style.color='#16a34a';
            applyBtn.style.display='';rejectBtn.style.display='';
          }else{
            out.textContent=(d&&d.text)||(d&&d.error)||'Модель не предложила объектов';
            msg.textContent=(d&&d.error)?'Ошибка':'Готово';msg.style.color=(d&&d.error)?'#c00':'#666';
          }
        })
        .catch(function(){msg.textContent='Ошибка сети';msg.style.color='#c00';})
        .finally(function(){send.disabled=false;});
    });
    rejectBtn.addEventListener('click',function(){pending=null;out.textContent='';applyBtn.style.display='none';rejectBtn.style.display='none';msg.textContent='Отклонено';msg.style.color='#666';});
    applyBtn.addEventListener('click',function(){
      if(!pending)return;
      applyBtn.disabled=true;msg.textContent='Применение...';msg.style.color='#666';
      fetch('/bases/'+base+'/configurator/ai-apply',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({changes:pending})})
        .then(function(r){return r.json();})
        .then(function(d){
          if(d&&d.ok){msg.textContent='Применено: '+(d.applied||0)+'. Обновление...';msg.style.color='#16a34a';setTimeout(function(){location.reload();},800);}
          else{msg.textContent=(d&&d.error)||'Ошибка применения';msg.style.color='#c00';}
        })
        .catch(function(){msg.textContent='Ошибка сети';msg.style.color='#c00';})
        .finally(function(){applyBtn.disabled=false;});
    });
  }
})();
</script>
```

- [ ] **Step 4: Запустить структурный тест — PASS:**

Run: `go test ./internal/launcher/ -run TestConfigurator_GeneratePanelWired -count=1`
Expected: PASS.

- [ ] **Step 5: Прогнать render-тесты пакета (шаблон не сломан):**

Run: `go test ./internal/launcher/ -run 'Render|Configurator|Langref|Layout' -count=1`
Expected: PASS (существующие render-тесты cfg-foot зелёные).

- [ ] **Step 6: Сборка + commit:**

Run: `go build ./cmd/onebase` → успех (если залочен — `taskkill /IM onebase.exe /F`).
```
git add internal/launcher/configurator_tmpl.go internal/launcher/ai_generate_render_test.go
git commit -m "feat(configurator): панель генерации каркаса с показом diff и применением (план 57, этап 2b)"
```

---

## Task 3: Верификация и статус

**Files:**
- Modify: `Plans/57-stage2b-apply-ui.md`

- [ ] **Step 1: Полный прогон**

Run: `go test ./... -count=1` → PASS (без FAIL).
Run: `go vet ./...` → чисто.
Run: `gofmt -d internal/launcher/ai_apply.go` → пусто.

- [ ] **Step 2: Обновить статус**

В `Plans/57-stage2b-apply-ui.md` заменить `**Статус:** дизайн утверждён, ожидает плана реализации` на `**Статус:** ✅ Реализовано (этап 2b; визуальная приёмка в браузере — отдельно)`.

- [ ] **Step 3: Commit:**

```
git add Plans/57-stage2b-apply-ui.md
git commit -m "docs(plans): этап 2b плана 57 (UI генерации + применение) реализован"
```

---

## Self-Review

**Spec coverage:**
- `validApplyPath` (метаданные-подкаталог + safeFileName) + `applyChanges` (запись в baseDir, защита границы) + `cfgAIApply` (БД-базы отклоняются) + маршрут → Task 1.
- Панель генерации (prompt→ai-generate→diff→Применить/Отклонить→ai-apply→reload), изолированный IIFE, экранирование через textContent → Task 2; структурный тест рендера.
- Файловые базы; БД — отклоняются; выборочное применение/hot-reload — вне 2b (YAGNI).
- Визуальная приёмка — ручной шаг (отмечено в статусе/тестах).

**Placeholder scan:** код приведён целиком (Go + HTML/JS). Точка вставки панели задана по
уникальному якорю (строка `copy.addEventListener` + последующий `</script>`).

**Type consistency:** `GenChange` (2a) переиспользуется в `applyChanges`/`cfgAIApply` и в
JS (`ch.path`/`ch.newContent`/`ch.kind` ↔ json-теги `path`/`newContent`/`kind`).
`safeFileName` (2a) — в `validApplyPath`. `renderCfgFoot` — существующий тест-хелпер.
Маршрут `ai-apply` рядом с `ai-generate` (та же admin-группа).
