package ui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Обработчик может изменить объект и тут же вызвать ОткрытьФорму. Сервер
// отвечает одновременно navigation и dirty=true, а клиент раньше смотрел только
// на прежний флаг формы: исходно чистая форма уходила по ссылке, и изменение
// обработчика терялось — вопреки обещанию «несохранённая форма остаётся на
// месте».

// Сервер: ответ формы обработки несёт и переход, и признак несохранённых
// изменений — на этой паре держится решение клиента.
func TestOpenForm_ProcessorFormReportsHandlerChangeWithNavigation(t *testing.T) {
	направления := &metadata.Entity{
		Name: "Направления", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	form := processorExecutionForm(&metadata.FormElement{
		Kind: metadata.FormElementButton, Name: "Открыть",
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "ОткрытьНажатие"},
	})
	form.ProgramAST = mustParse(t, `
Процедура ОткрытьНажатие()
	Объект.Имя = "изменил обработчик";
	ОткрытьФорму(Справочники.Направления.НайтиПоНаименованию("Север"));
КонецПроцедуры
`)
	proc := &processor.Processor{
		Name:   "ПоискНаправленияСИменем",
		Params: []processor.Param{{Name: "Имя", Type: "string"}},
		Forms:  []*metadata.FormModule{form},
	}

	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, []*metadata.Entity{направления}); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, направления.Name, uuid.New(), map[string]any{"Наименование": "Север"}, направления); err != nil {
		t.Fatal(err)
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{направления}})
	registry.LoadProcessors([]*processor.Processor{proc})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	srv := &Server{
		store: db, reg: registry, interp: interp,
		lockMgr: runtime.NewLockManager(), messages: NewMessageStore(), ops: newOperationLimiter(),
	}
	srv.entitySvc = srv.newEntityService(nil)

	rec := postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(processorClickBody("Открыть").Encode()))
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if !resp.OK {
		t.Fatalf("ok=false, error=%q", resp.Error)
	}
	if resp.Navigation == nil {
		t.Fatalf("обработчик вызвал ОткрытьФорму, а перехода в ответе нет: %s", rec.Body.String())
	}
	if resp.Dirty == nil || !*resp.Dirty {
		t.Fatalf("обработчик изменил объект, а ответ не несёт dirty=true: %s", rec.Body.String())
	}
}

// Клиент: публичный obFire из managed.js в детерминированном стенде.
// Ответ приходит после пользовательского ввода в ожидающую форму.
func TestManagedNavigationRespectsResponseDirtyState(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the browser-side navigation regression test")
	}
	dir := t.TempDir()
	managedPath := filepath.Join(dir, "managed.js")
	if err := os.WriteFile(managedPath, managedJS, 0o600); err != nil {
		t.Fatal(err)
	}
	const harness = `
const fs = require('fs');
const source = fs.readFileSync(process.argv[2], 'utf8');
const start = source.indexOf('  async function dispatchFormEvent(snapshot){');
const end = source.indexOf('\n  window.obFire = function', start);
const fireEnd = source.indexOf('\n  // One close controller', end);
const helpersStart = source.indexOf('  function closeBodyEntries(body, predicate){');
const helpersEnd = source.indexOf('  function setManagedFormDirty(dirty){', helpersStart);
const mergeStart = source.indexOf('  function applyCloseResponse(data, before, current){');
const mergeEnd = source.indexOf('\n  window.obRequestFormClose', mergeStart);
if ([start, end, fireEnd, helpersStart, helpersEnd, mergeStart, mergeEnd].some(i => i < 0)) {
  throw new Error('managed.js behavior slices not found');
}
const dirtyEnd = source.indexOf('  function applySavedIdentity(data){', helpersEnd);
const markStart = source.indexOf('  function _obMarkDirty(){');
const markEnd = source.indexOf('  document.addEventListener', markStart);
const gridHelpersStart = source.indexOf('  function reindexOrd(items) {');
const gridHelpersEnd = source.indexOf('  // obGridCopyRow', gridHelpersStart);
const moveStart = source.indexOf('  window.obGridMoveRow = function(tpName, delta) {');
const moveEnd = source.indexOf('  function rememberActiveGrid', moveStart);
if ([dirtyEnd, markStart, markEnd, gridHelpersStart, gridHelpersEnd, moveStart, moveEnd].some(i => i < 0)) {
  throw new Error('managed.js dirty/grid behavior slices not found');
}
const fnSource = source.slice(helpersEnd, dirtyEnd) + source.slice(markStart, markEnd) +
  source.slice(gridHelpersStart, gridHelpersEnd) + source.slice(moveStart, moveEnd) +
  source.slice(helpersStart, helpersEnd) + source.slice(mergeStart, mergeEnd) +
  source.slice(start, fireEnd);

async function run(response, formDirty, concurrentEdit) {
  const calls = { assign: [], flash: [], dirty: [], values: [], savedIdentity: 0 };
  const fields = { 'Имя': 'исходное', 'Описание': 'старое' };
  const formEditState = { revision: 0 };
  let rows = [{ id: 1, _ord: 0, Item: 'first' }, { id: 2, _ord: 1, Item: 'second' }];
  const bodyNow = () => new URLSearchParams({ ...fields,
    'tp_json.Items': JSON.stringify(rows.map(r => ({ Item: r.Item }))),
  });
  const grid = {
    dataView: {
      getItems: () => rows, getItem: i => rows[i], setItems: items => { rows = items; },
      getRowById: id => rows.findIndex(r => r.id === id),
    },
    grid: { getActiveCell: () => ({ row: 1, cell: 0 }), invalidate() {},
      scrollRowIntoView() {}, setActiveCell() {}, },
  };
  let fetchEntered, finishFetch;
  const entered = new Promise(resolve => { fetchEntered = resolve; });
  const waiting = new Promise(resolve => { finishFetch = resolve; });
  const win = {
    _obFormDirty: formDirty,
    location: { assign(url) { calls.assign.push(url); } },
    obSetEmbeddedDirty(v) { calls.dirty.push(v); },
    _obGrids: { Items: grid },
    obManagedApplyTablePartRefOptions() {},
    applyTableParts(parts) {
      if (parts && parts.Items) rows = parts.Items.map((r, i) => ({ ...r, id: i + 1, _ord: i }));
    },
    obOpenQuestion() {},
  };
  const env = {
    window: win, document: {}, _obBaseTitle: '',
    rememberActiveGrid() {}, updateTotals() {},
    reloadRequired: false, formEventWriteUnknown: false, manualReconcileRequired: false,
    formEditState,
    formEventQueue: Promise.resolve(), formEventPendingCount: 0, formEventPending: false,
    closePending: null, closeHandoffPending: false, retryableClose: null,
    DOC_ID: 'doc', URL: '/ui/form-event',
    fetch: () => { fetchEntered(); return waiting; },
    flash: (m, kind) => calls.flash.push([m, kind]),
    // Тексты сообщений приходят из конфига формы (closeMessages), а здесь
    // конфига нет: стенд вырезает из managed.js только логику события. Отдаём
    // фолбэк — этому тесту важен факт сообщения, а локализацию проверяет
    // TestManagedFormNavigationMessagesLocalized на рендере формы.
    closeMessage: (name, fallback) => fallback,
    applySavedIdentity() { calls.savedIdentity++; },
    openItemPicker() {}, applyFormConditionalCSS() {},
    applyElementStates() {}, applyChoiceList() {}, applyFormTables() {},
    applyValues: (v) => { if (v) { calls.values.push(v); Object.assign(fields, v); } },
    closeSnapshotBody: async () => bodyNow(),
    captureFormEventSelection() {},
    snapshotFormEvent: async () => ({
      body: bodyNow(), form: null, elementName: 'Открыть',
      extraParams: null, wasNew: false, editRevision: formEditState.revision,
    }),
    refreshQueuedFormEventIdentity() {},
  };
  const factory = new Function(...Object.keys(env), fnSource + '\nwindow.markNativeEdit = _obMarkDirty; return window.obFire;');
  const fire = factory(...Object.values(env));
  const pending = fire('Открыть', 'Нажатие');
  await entered;
  if (concurrentEdit === 'grid') {
    win.obGridMoveRow('Items', -1);
  } else if (concurrentEdit) {
    fields['Имя'] = 'ввод пользователя';
    win.markNativeEdit();
  }
  finishFetch({ ok: true, json: async () => response });
  await pending;
  calls.fields = fields;
  calls.isDirty = win._obFormDirty;
  calls.rowOrder = rows.map(r => r.Item);
  return calls;
}

function check(cond, msg) { if (!cond) throw new Error(msg); }
const nav = { url: '/ui/catalog/Направления/1' };
const blocked = 'Форма содержит несохранённые изменения — переход не выполнен';

(async () => {
  // Чистая форма, обработчик ничего не менял — переход.
  let c = await run({ ok: true, navigation: nav }, false);
  check(c.assign.length === 1 && c.assign[0] === nav.url, 'чистая форма не перешла: ' + JSON.stringify(c));

  // Чистая до ответа форма, но обработчик изменил объект (dirty=true) —
  // остаёмся, изменение видно, флаг поднят, сообщение последним.
  c = await run({ ok: true, dirty: true, navigation: nav, values: { 'Имя': 'изменил обработчик' } }, false);
  check(c.assign.length === 0, 'переход потерял изменение обработчика: ' + JSON.stringify(c));
  check(c.dirty.includes(true), 'флаг несохранённых изменений не поднят: ' + JSON.stringify(c));
  check(c.values.length === 1 && c.values[0]['Имя'] === 'изменил обработчик', 'изменение обработчика не показано: ' + JSON.stringify(c));
  check(c.flash.length > 0 && c.flash[c.flash.length - 1][0] === blocked, 'нет сообщения об отмене перехода: ' + JSON.stringify(c));

  // Правки пользователя до обработчика — переход отменён, ответ применён.
  c = await run({ ok: true, navigation: nav, values: { 'Имя': 'x' } }, true);
  check(c.assign.length === 0, 'несохранённая форма ушла по ссылке: ' + JSON.stringify(c));
  check(c.values.length === 1, 'ответ не применён при отмене перехода: ' + JSON.stringify(c));

  // Обработчик сам записал объект (dirty=false + version) — форма чиста.
  c = await run({ ok: true, dirty: false, version: 2, navigation: nav }, true);
  check(c.assign.length === 1, 'записанная обработчиком форма не перешла: ' + JSON.stringify(c));

  // Ввод после отправки snapshot старше ответа. Старые values не затирают
  // поле пользователя; независимое изменение обработчика всё ещё видно.
  c = await run({ ok: true, dirty: true, navigation: nav,
    values: { 'Имя': 'значение старого ответа', 'Описание': 'изменил обработчик' } }, false, true);
  check(c.assign.length === 0, 'переход потерял ввод во время ожидания: ' + JSON.stringify(c));
  check(c.fields['Имя'] === 'ввод пользователя', 'ответ затёр новый ввод: ' + JSON.stringify(c));
  check(c.fields['Описание'] === 'изменил обработчик', 'независимое поле не применено: ' + JSON.stringify(c));
  check(c.isDirty, 'после позднего ввода форма должна быть грязной: ' + JSON.stringify(c));

  // Даже доказанная запись старого snapshot не разрешает переход после ввода.
  c = await run({ ok: true, dirty: false, version: 2, navigation: nav,
    values: { 'Имя': 'сохранённое старое значение' } }, false, true);
  check(c.assign.length === 0, 'запись старого snapshot разрешила переход: ' + JSON.stringify(c));
  check(c.fields['Имя'] === 'ввод пользователя' && c.isDirty, 'поздний ввод потерян: ' + JSON.stringify(c));
  check(c.savedIdentity === 1, 'новая версия записи не принята: ' + JSON.stringify(c));
  // SlickGrid's structural operations raise dirty without native input/change.
  // Both a saved old snapshot and an unsaved response must preserve the new order.
  for (const dirty of [false, true]) {
    c = await run({ ok: true, dirty, version: 2, navigation: nav,
      tableparts: { Items: [{ Item: 'first' }, { Item: 'second' }] },
      values: { 'Описание': 'изменил обработчик' } }, true, 'grid');
    check(c.assign.length === 0, 'переход потерял перестановку строки: ' + JSON.stringify(c));
    check(c.rowOrder.join(',') === 'second,first', 'ответ затёр порядок строк: ' + JSON.stringify(c));
    check(c.isDirty, 'переставленная таблица должна остаться несохранённой: ' + JSON.stringify(c));
    check(c.fields['Описание'] === 'изменил обработчик', 'независимое поле не применено после перестановки: ' + JSON.stringify(c));
    check(c.savedIdentity === 1, 'версия ответа не принята после перестановки: ' + JSON.stringify(c));
  }
  console.log('ok');
})().catch((e) => { console.error(e && e.stack || e); process.exit(1); });
`
	harnessPath := filepath.Join(dir, "harness.js")
	if err := os.WriteFile(harnessPath, []byte(harness), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, harnessPath, managedPath).CombinedOutput() //nolint:gosec // test-only executable resolved by exec.LookPath
	if err != nil {
		t.Fatalf("стенд managed.js: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "ok") {
		t.Fatalf("стенд managed.js не дошёл до конца:\n%s", out)
	}
}
