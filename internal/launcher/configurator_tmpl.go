package launcher

import (
	"encoding/json"
	"strings"
	"text/template"

	"github.com/ivantit66/onebase/internal/i18n"
)

var launcherBundle *i18n.Bundle

var cfgTmpl = template.Must(template.New("cfg").Funcs(template.FuncMap{
	"t": func(lang, key string) string {
		if launcherBundle != nil {
			return launcherBundle.T(lang, key)
		}
		return key
	},
		"selIf": func(a, b string) string { if a == b { return " selected" }; return "" },
	"dict": func(pairs ...any) map[string]any {
		m := make(map[string]any, len(pairs)/2)
		for i := 0; i+1 < len(pairs); i += 2 {
			if k, ok := pairs[i].(string); ok {
				m[k] = pairs[i+1]
			}
		}
		return m
	},
	"lower": strings.ToLower,
	"js": func(v any) string {
		// json.Marshal по умолчанию экранирует <, >, & в \uXXXX — безопасно
		// для вставки в <script> (text/template не экранирует сам).
		b, err := json.Marshal(v)
		if err != nil {
			return "null"
		}
		return string(b)
	},
	"fieldTypeLabel": func(typ, ref string) string {
		switch typ {
		case "string":
			return "строка"
		case "number":
			return "число"
		case "date":
			return "дата"
		case "bool":
			return "булево"
		case "reference":
			return "→ " + ref
		case "enum":
			return "перечисление"
		default:
			return typ
		}
	},
	"fieldTypeClass": func(typ string) string {
		switch typ {
		case "reference":
			return "ft-ref"
		case "number":
			return "ft-num"
		case "date":
			return "ft-date"
		case "bool":
			return "ft-bool"
		case "enum":
			return "ft-ref"
		default:
			return "ft-str"
		}
	},
	// filterFormsByEntity — фильтрует срез cfgManagedForm по имени сущности
	// (без учёта регистра). Возвращает новый срез; в шаблоне используется
	// {{$mine := filterFormsByEntity .ManagedForms $e.Name}} вместо
	// присваивания флага внутри {{range}} (которое в Go templates не
	// «вытекает» из цикла), что устраняет ложный «нет управляемых форм».
	"filterFormsByEntity": func(forms []cfgManagedForm, entity string) []cfgManagedForm {
		entLower := strings.ToLower(entity)
		out := make([]cfgManagedForm, 0, len(forms))
		for _, f := range forms {
			if strings.ToLower(f.Entity) == entLower {
				out = append(out, f)
			}
		}
		return out
	},
	"formLabel": func(name string) string {
			lower := strings.ToLower(name)
			switch lower {
			case "формаобъекта":
				return "Форма объекта"
			case "формасписка":
				return "Форма списка"
			case "формавыбора":
				return "Форма выбора"
			case "форма":
				return "Форма"
			default:
				return name
			}
		},
}).Parse(cfgCSS + cfgHead + cfgMain + cfgTabTree + cfgRegDetail + cfgTabConvert + cfgTabFiles + cfgTabBackup + cfgFoot))

// ── CSS ───────────────────────────────────────────────────────────────────────

const cfgCSS = `{{define "css"}}
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:'Segoe UI',Arial,sans-serif;font-size:13px;background:#f0f2f5;height:100vh;display:flex;flex-direction:column;overflow:hidden}

.topbar{background:linear-gradient(to bottom,#2c5f9e,#1a4a80);color:#fff;padding:7px 14px;display:flex;align-items:center;gap:12px;flex-shrink:0}
.topbar a{color:#b8d4ff;text-decoration:none;font-size:12px}.topbar a:hover{color:#fff}
.topbar h1{font-size:14px;font-weight:600;flex:1}

.tabs{display:flex;background:#fff;border-bottom:2px solid #d0d7e3;padding:0 14px;flex-shrink:0}
.tab{padding:8px 18px;cursor:pointer;font-size:13px;color:#666;border-bottom:2px solid transparent;margin-bottom:-2px;text-decoration:none;display:inline-block}
.tab:hover{color:#1a4a80;background:#f5f8ff}
.tab.active{color:#1a4a80;border-bottom-color:#1a4a80;font-weight:600}

.cfg-body{flex:1;overflow:hidden;display:flex;flex-direction:column}

.err-box{background:#fff0f0;border:1px solid #ffb3b3;color:#c00;padding:10px 14px;margin:10px;border-radius:5px;font-size:13px}

/* ── Two-panel tree ─────────────────────────────────── */
.cfg-split{display:flex;flex:1;overflow:hidden}
.cfg-left{width:220px;flex-shrink:0;background:#fff;border-right:1px solid #d8dde8;overflow-y:auto;padding:6px 0;transition:width .2s}
.cfg-left.collapsed{width:0;padding:0;overflow:hidden;border:none}
.sidebar-toggle{position:absolute;left:220px;top:50%;z-index:10;width:16px;height:40px;background:#e8ecf2;border:1px solid #d8dde8;border-left:none;border-radius:0 4px 4px 0;cursor:pointer;display:flex;align-items:center;justify-content:center;font-size:10px;color:#666;transition:left .2s}
.sidebar-toggle.collapsed{left:0}
.cfg-group{font-size:11px;font-weight:700;color:#888;text-transform:uppercase;letter-spacing:.5px;padding:10px 12px 4px;margin-top:4px}
.cfg-tree details summary{font-size:11px;font-weight:700;color:#888;text-transform:uppercase;letter-spacing:.5px;padding:10px 12px 4px;margin-top:4px;cursor:pointer;list-style:none;display:flex;align-items:center;gap:2px}
.cfg-tree details summary::-webkit-details-marker{display:none}

.cfg-sub{padding:2px 12px 2px 36px;font-size:11px;color:#6b7280;cursor:pointer;border-left:2px solid transparent}
.cfg-sub:hover{background:#f0f4ff;color:#1a4a80}
.cfg-sub-label{font-size:10px;color:#94a3b8;padding:2px 12px 2px 36px;border-left:2px solid transparent}
.cfg-group:first-child{margin-top:0}
.cfg-item{padding:6px 12px 6px 12px;cursor:pointer;font-size:13px;color:#333;display:flex;align-items:center;gap:4px;border-left:2px solid transparent}
.cfg-item:hover{background:#f0f4ff;color:#1a4a80}
.cfg-item.sel{background:#e8eeff;color:#1a4a80;font-weight:600;border-left-color:#1a4a80}
.cfg-item .ic{font-size:14px;flex-shrink:0;width:20px;text-align:center;line-height:1}
.cfg-item .bp{background:#dbeafe;color:#1d4ed8;font-size:9px;font-weight:700;padding:1px 5px;border-radius:8px;margin-left:2px}
.cfg-item[draggable=true]{cursor:grab}
.cfg-item[draggable=true]:active{cursor:grabbing}
summary.cfg-group-hd[draggable=true]{cursor:grab}
.cfg-dirty{color:#e8b400;font-weight:700;margin-left:4px;font-size:14px;cursor:help}

.cfg-right{flex:1;overflow-y:auto;padding:16px}

.cfg-panel{display:none}
.cfg-panel.active{display:block}

/* ── Layout tabs ────────────────────────────────────── */
.ltab{padding:6px 16px;font-size:12px;font-weight:600;color:#64748b;cursor:pointer;border-bottom:2px solid transparent;margin-bottom:-2px;transition:color .15s,border-color .15s}
.ltab:hover{color:#1a4a80}
.ltab.active{color:#1a4a80;border-bottom-color:#1a4a80}

/* ── Panel content ──────────────────────────────────── */
.panel-title{font-size:16px;font-weight:700;color:#1a3a6a;margin-bottom:4px;display:flex;align-items:center;gap:8px}
.panel-kind{font-size:11px;color:#888;font-weight:400;margin-bottom:14px}

.section-hd{font-size:11px;font-weight:700;color:#888;text-transform:uppercase;letter-spacing:.4px;margin:14px 0 6px;border-top:1px solid #eef0f5;padding-top:10px}
.section-hd:first-child{border-top:none;margin-top:0;padding-top:0}
/* drag-конструктор раскладки рабочего стола */
.ob-row{display:flex;align-items:flex-start;gap:8px;margin:6px 0}
.ob-zone{flex:1;display:flex;flex-wrap:wrap;gap:6px;min-height:38px;padding:6px;border:1px dashed #c3c9d4;border-radius:6px;background:#fafbfc}
.ob-pool{margin-top:2px;background:#f1f5f9}
.ob-chip{display:inline-flex;align-items:center;background:#fff;border:1px solid #ccd0d8;border-radius:14px;padding:3px 10px;font-size:12px;color:#334155;cursor:grab;user-select:none;box-shadow:0 1px 1px rgba(0,0,0,.05)}
.ob-chip.dragging{opacity:.4}
.ob-row-del{background:none;border:none;color:#c00;font-size:13px;cursor:pointer;padding:6px 4px;line-height:1}

.fields-tbl{width:100%;border-collapse:collapse;font-size:12px;margin-bottom:4px}
.fields-tbl th{text-align:left;padding:5px 8px;color:#999;font-weight:600;font-size:11px;border-bottom:1px solid #eef0f5}
.fields-tbl td{padding:5px 8px;border-bottom:1px solid #f7f8fb;color:#333}
.fields-tbl tr:last-child td{border-bottom:none}
.fields-tbl tr:hover td{background:#f8f9fc}
.ft-str{color:#059669}.ft-num{color:#7c3aed}.ft-date{color:#b45309}.ft-bool{color:#0284c7}.ft-ref{color:#1a4a80;font-weight:500}
.fields-tbl select{padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px;background:#fff;color:#333}
.fields-tbl select:focus{border-color:#1a4a80;outline:none}
.success-box{background:#f0fdf4;border:1px solid #86efac;color:#15803d;padding:10px 14px;margin:10px;border-radius:5px;font-size:13px}

.tp-block{margin-bottom:8px;background:#f8f9fc;border:1px solid #e8ecf4;border-radius:5px;overflow:hidden}
.tp-hd{padding:6px 10px;font-size:12px;font-weight:600;color:#334;background:#f0f3f8}

/* ── Module editor ───────────────────────────────────── */
.code-wrap{margin-top:8px}
.edit-hint{font-size:11px;color:#94a3b8;margin-left:6px}
.module-tabs{display:flex;gap:0;margin-top:16px;border-bottom:1px solid #d8dde8}
.module-tab{padding:6px 14px;cursor:pointer;font-size:12px;color:#666;border-bottom:2px solid transparent;margin-bottom:-1px}
.module-tab.active{color:#1a4a80;border-bottom-color:#1a4a80;font-weight:600}
.module-pane{display:none;margin-top:0}
.module-pane.active{display:block}

.module-editor-wrap{position:relative;margin-top:8px}
pre.os-code{
  background:#1e1e2e;color:#cdd6f4;
  font-family:'Cascadia Code','Fira Code','Consolas','Courier New',monospace;
  font-size:12px;line-height:1.6;padding:14px 16px;border-radius:6px;
  overflow:auto;white-space:pre;min-height:100px;tab-size:2;margin:0;cursor:pointer
}
.os-edit{
  width:100%;min-height:200px;
  background:#1e1e2e;color:#cdd6f4;
  font-family:'Cascadia Code','Fira Code','Consolas','Courier New',monospace;
  font-size:12px;line-height:1.6;padding:14px 16px;border-radius:6px;
  border:2px solid #3070d8;resize:vertical;outline:none;tab-size:2;
  white-space:pre;display:none
}
.module-save-row{margin-top:8px;display:flex;align-items:center;gap:10px}
.btn-save{background:#1a4a80;color:#fff;border:none;padding:7px 16px;border-radius:4px;cursor:pointer;font-size:12px}
.btn-save:hover{background:#15396a}
.save-ok{color:#059669;font-size:12px}
.btn-check{background:#fff;border:1px solid #1a4a80;color:#1a4a80;padding:4px 12px;border-radius:3px;cursor:pointer;font-size:12px;margin-left:6px}
.btn-check:hover{background:#eaf2fa}
.check-result{display:inline-block;margin-left:10px;font-size:11px;padding:3px 8px;border-radius:3px;vertical-align:middle;max-width:480px;line-height:1.4}
.check-result:empty{display:none}
.check-pending{background:#fef7ed;color:#9a3412}
.check-ok{background:#ecfdf5;color:#047857}
.check-err{background:#fef2f2;color:#b91c1c;display:block;margin:6px 0 0 0;padding:8px 10px}
#check-all-panel{display:none;position:fixed;right:20px;bottom:20px;width:520px;max-height:60vh;background:#fff;border:1px solid #c8cdd8;border-radius:6px;box-shadow:0 6px 24px rgba(0,0,0,.18);flex-direction:column;z-index:1000}
#check-all-panel header{padding:8px 12px;background:#1a4a80;color:#fff;border-radius:6px 6px 0 0;font-size:12px;font-weight:600;display:flex;justify-content:space-between;align-items:center}
#check-all-panel header button{background:none;border:none;color:#fff;cursor:pointer;font-size:14px;padding:0 4px}
#check-all-body{padding:6px;overflow-y:auto;flex:1}
#check-all-body .check-row{padding:8px 10px;border-bottom:1px solid #eef1f6;font-size:12px}
#check-all-body .check-row:last-child{border-bottom:none}
#check-all-body .check-ok{background:#ecfdf5;color:#047857;border-radius:4px;margin:4px 0}
.module-empty{color:#888;font-size:12px;padding:10px 0;font-style:italic}

/* ── Syntax colours ─────────────────────────────────── */
.hl-kw{color:#c792ea;font-weight:600}
.hl-fn{color:#82aaff}
.hl-sp{color:#ff5370;font-weight:600}
.hl-str{color:#c3e88d}
.hl-num{color:#f78c6c}
.hl-cmt{color:#546e7a;font-style:italic}

/* ── New object form ─────────────────────────────────── */
.cfg-group-hd{display:flex;align-items:center;padding-right:6px}
.cfg-add-btn{cursor:pointer;color:#1a4a80;font-size:17px;line-height:1;padding:0 4px;border-radius:3px;font-weight:400;opacity:.7;margin-left:auto}
.cfg-add-btn:hover{background:#e0e8ff;opacity:1}
.tree-toggle{font-size:10px;margin-right:4px;user-select:none}
.cfg-new-form{padding:8px 10px 10px;border-top:1px solid #d8dde8;margin-top:4px}
.cfg-new-form input[type=text]{width:100%;padding:5px 6px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px;margin-bottom:6px;box-sizing:border-box}
.cfg-new-form input[type=text]:focus{border-color:#1a4a80;outline:none}
.cfg-new-form .row{display:flex;gap:4px}
.cfg-new-form .btn-create{flex:1;padding:5px;background:#1a4a80;color:#fff;border:none;border-radius:3px;font-size:12px;cursor:pointer}
.cfg-new-form .btn-create:hover{background:#15396a}
.cfg-new-form .btn-cancel{padding:5px 8px;background:#e8ecf2;border:1px solid #ccd0d8;border-radius:3px;font-size:12px;cursor:pointer}

/* ── Converter / Files ───────────────────────────────── */
.pad{padding:16px}
.convert-form,.file-card{background:#fff;border:1px solid #d8dde8;border-radius:6px;padding:18px;margin-bottom:14px}
.convert-form h3,.file-card h3{font-size:13px;font-weight:700;color:#1a3a6a;margin-bottom:12px}
.fg{margin-bottom:12px}
.fg label{display:block;font-size:11px;font-weight:700;color:#555;margin-bottom:4px;text-transform:uppercase;letter-spacing:.3px}
.fg input[type=text],.fg textarea{width:100%;padding:7px 10px;border:1px solid #c8d0de;border-radius:4px;font-size:13px}
.fg input:focus,.fg textarea:focus{border-color:#1a4a80;outline:none}
.fg .hint{font-size:11px;color:#888;margin-top:3px}
.input-browse{display:flex;gap:4px}.input-browse input{flex:1;padding:7px 10px;border:1px solid #c8d0de;border-radius:4px;font-size:13px}
.btn-browse{flex-shrink:0;padding:7px 10px;border:1px solid #ACA899;border-radius:4px;background:linear-gradient(to bottom,#F5F4EE,#E0DDD2);cursor:pointer;font-size:13px}
.btn-browse:hover{background:linear-gradient(to bottom,#EAF3FF,#C5DCFF);border-color:#7EAFF5}
.form-btns{display:flex;gap:8px}
.btn-primary{background:#1a4a80;color:#fff;border:none;padding:7px 16px;border-radius:4px;cursor:pointer;font-size:13px}
.btn-primary:hover{background:#15396a}
.btn-secondary{background:#e8ecf2;color:#333;border:1px solid #c8d0de;padding:7px 14px;border-radius:4px;cursor:pointer;font-size:13px}
.convert-result{background:#fff;border:1px solid #d8dde8;border-radius:6px;padding:14px;margin-bottom:14px}
pre.convert-out{background:#f5f7fa;border:1px solid #e2e6ed;padding:12px;border-radius:4px;font-size:12px;white-space:pre-wrap;max-height:280px;overflow-y:auto}
.applied{background:#dcfce7;color:#15803d;padding:8px 12px;border-radius:4px;font-size:13px;margin-bottom:12px;font-weight:500}
.files-grid{display:grid;grid-template-columns:1fr 1fr;gap:14px}
.file-card p{font-size:12px;color:#666;margin-bottom:12px;line-height:1.5}

/* ── Context menu ──────────────────────────────────── */
.cfg-ctx-menu{position:fixed;background:#fff;border:1px solid #d0d7e3;border-radius:6px;box-shadow:0 4px 16px rgba(0,0,0,.12);padding:4px 0;z-index:9999;min-width:220px;display:none}
.cfg-ctx-item{padding:7px 14px;cursor:pointer;font-size:13px;color:#333;display:flex;align-items:center;gap:8px}
.cfg-ctx-item:hover{background:#f0f4ff;color:#1a4a80}

/* ── Query builder modal ──────────────────────────── */
.qb-overlay{position:fixed;inset:0;background:rgba(0,0,0,.4);z-index:10000;display:none}
.qb-overlay.active{display:flex;align-items:flex-start;justify-content:center;padding:16px}
.qb-modal{background:#fff;border-radius:10px;width:100%;max-width:1180px;max-height:calc(100vh - 32px);overflow-y:auto;box-shadow:0 8px 32px rgba(0,0,0,.2);display:flex;flex-direction:column}
.qb-modal-hd{display:flex;align-items:center;justify-content:space-between;padding:12px 18px;border-bottom:1px solid #e2e8f0;background:#f8fafc;border-radius:10px 10px 0 0;flex-shrink:0}
.qb-modal-hd h2{font-size:15px;margin:0}
.qb-modal-bd{padding:14px 18px;overflow-y:auto;flex:1}
.qb-card{background:#f8fafc;border:1px solid #e2e8f0;border-radius:6px;padding:10px 12px;margin-bottom:10px}
.qb-card h3{font-size:13px;margin:0 0 8px}
.qb-grid{display:grid;grid-template-columns:380px 1fr;gap:16px;align-items:start}
.qb-fl{max-height:220px;overflow-y:auto}
.qb-row{display:flex;gap:4px;margin-bottom:5px;align-items:center;flex-wrap:wrap}

/* ── Monaco editor ─────────────────────────────────── */
.monaco-target{width:100%;min-height:400px;height:calc(100vh - 340px);border-radius:6px;overflow:hidden}
.code-wrap{position:relative}

/* ── Debug panel ─────────────────────────────────── */
.run-enterprise-btn{width:28px;height:28px;border-radius:50%;background:#f5c518;border:2px solid #d4a800;cursor:pointer;display:inline-flex;align-items:center;justify-content:center;padding:0;transition:background .2s,transform .1s;flex-shrink:0}
.run-enterprise-btn:hover{background:#ffe44d;transform:scale(1.08)}
.run-enterprise-btn:active{transform:scale(0.95)}
.run-enterprise-btn svg{width:14px;height:14px;margin-left:2px}
.dbg-topbar-btn{background:rgba(255,255,255,.12);border:1px solid rgba(255,255,255,.25);color:#fff;padding:3px 10px;border-radius:4px;cursor:pointer;font-size:11px;white-space:nowrap;transition:background .2s}
.dbg-topbar-btn:hover{background:rgba(255,255,255,.22)}
.dbg-topbar-btn.dbg-on{background:#16a34a;border-color:#22c55e}
.dbg-topbar-btn.dbg-paused{background:#d97706;border-color:#f59e0b}
.cfg-save-topbar{background:#16a34a;border:1px solid #22c55e;color:#fff;padding:4px 12px;border-radius:4px;cursor:pointer;font-size:11px;white-space:nowrap;transition:background .2s,opacity .2s;flex-shrink:0}
.cfg-save-topbar:hover{background:#15803d}
.cfg-save-topbar:disabled{opacity:.6;cursor:default}

.cfg-menu-wrap{position:relative}
.cfg-menu-btn{background:rgba(255,255,255,.15);border:1px solid rgba(255,255,255,.25);color:#fff;padding:3px 10px;border-radius:4px;cursor:pointer;font-size:11px}
.cfg-menu-btn:hover{background:rgba(255,255,255,.25)}
.cfg-menu-dropdown{display:none;position:absolute;top:100%;left:0;background:#fff;border:1px solid #d0d7e3;border-radius:6px;box-shadow:0 8px 24px rgba(0,0,0,.15);min-width:200px;z-index:1000;overflow:hidden;margin-top:2px}
.cfg-menu-dropdown.open{display:block}
.cfg-menu-dropdown a{display:block;padding:8px 14px;color:#333;text-decoration:none;font-size:12px;border-bottom:1px solid #f0f2f5}
.cfg-menu-dropdown a:last-child{border-bottom:none}
.cfg-menu-dropdown a:hover{background:#f0f4ff;color:#1a4a80}

.dbg-panel{width:320px;flex-shrink:0;background:#fff;border-left:1px solid #d8dde8;display:flex;flex-direction:column;overflow:hidden}
.dbg-status{padding:8px 12px;font-size:12px;font-weight:600;border-bottom:1px solid #eef0f5;display:flex;align-items:center;gap:6px}
.dbg-status .dot{width:8px;height:8px;border-radius:50%;display:inline-block}
.dbg-status .dot.running{background:#16a34a}
.dbg-status .dot.paused{background:#d97706}
.dbg-status .dot.stopped{background:#9ca3af}
.dbg-status .dot.disabled{background:#d1d5db}

.dbg-controls{padding:6px 10px;display:flex;gap:4px;flex-wrap:wrap;border-bottom:1px solid #eef0f5}
.dbg-controls button{background:#f1f5f9;border:1px solid #d0d7e3;border-radius:4px;padding:3px 8px;font-size:11px;cursor:pointer;color:#334}
.dbg-controls button:hover{background:#e2e8f0}

.dbg-tabs{display:flex;border-bottom:1px solid #eef0f5;flex-shrink:0}
.dbg-tab{flex:1;padding:6px 4px;text-align:center;font-size:11px;cursor:pointer;color:#888;border-bottom:2px solid transparent;background:none;border-top:none;border-left:none;border-right:none}
.dbg-tab:hover{color:#1a4a80;background:#f8fafc}
.dbg-tab.active{color:#1a4a80;border-bottom-color:#1a4a80;font-weight:600}

.dbg-content{flex:1;overflow-y:auto;padding:8px 10px;font-size:12px;color:#334}
.dbg-var-row{display:flex;padding:2px 4px;border-bottom:1px solid #e2e6ed;font-family:'Cascadia Code','Fira Code',monospace;font-size:11px}
.dbg-var-row:last-child{border-bottom:none}
.dbg-var-row:nth-child(even){background:#f8fafc}
.dbg-var-name{width:35%;color:#1a4a80;font-weight:500;padding-right:8px;border-right:1px solid #e2e6ed;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.dbg-var-val{width:45%;color:#334;padding:0 8px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;border-right:1px solid #e2e6ed}
.dbg-var-type{width:20%;color:#94a3b8;font-size:10px;padding-left:8px}
.dbg-stack-row{padding:4px 0;border-bottom:1px solid #f7f8fb;font-size:11px}
.dbg-stack-row .proc{color:#1a4a80;font-weight:600}
.dbg-stack-row .loc{color:#64748b;font-size:10px}
.dbg-bp-row{display:flex;align-items:center;gap:6px;padding:3px 0;border-bottom:1px solid #f7f8fb;font-size:11px}
.dbg-bp-row .bp-file{color:#1a4a80;font-weight:500}
.dbg-bp-row .bp-line{color:#64748b}

.dbg-console-input{display:flex;gap:4px;padding:6px 10px;border-top:1px solid #eef0f5;flex-shrink:0}
.dbg-console-input input{flex:1;padding:4px 8px;border:1px solid #d0d7e3;border-radius:4px;font-size:12px;font-family:'Cascadia Code','Fira Code',monospace}
.dbg-console-input input:focus{border-color:#1a4a80;outline:none}
.dbg-console-input button{background:#1a4a80;color:#fff;border:none;padding:4px 10px;border-radius:4px;font-size:11px;cursor:pointer}
.dbg-console-out{flex:1;overflow-y:auto;padding:6px 10px;font-family:'Cascadia Code','Fira Code',monospace;font-size:11px;line-height:1.5}
.dbg-console-line{padding:1px 0}
.dbg-console-line.dbg-err{color:#ef4444}
.dbg-console-line.dbg-ok{color:#a3e635}
.dbg-empty{color:#94a3b8;font-style:italic;padding:10px 0;text-align:center;font-size:12px}
.dbg-val-clickable{cursor:pointer}
.dbg-val-clickable:hover{text-decoration:underline;color:#1a4a80}

/* ── Debug Monaco decorations ────────────────────── */
.dbg-bp-glyph{background:#ef4444;border-radius:50%;margin-left:3px;width:12px!important;height:12px!important}
.dbg-current-line-bg{background:rgba(217,119,6,.2)!important;border-left:3px solid #d97706}
.dbg-current-line-glyph{background:#d97706;border-radius:50%;margin-left:3px;width:12px!important;height:12px!important}

/* ── Admin modal overlay ────────────────────── */
.cfg-modal-overlay{position:fixed;inset:0;background:rgba(0,0,0,.45);z-index:10000;display:none;align-items:center;justify-content:center;padding:24px}
.cfg-modal-overlay.active{display:flex}
.cfg-modal-box{background:#fff;border-radius:10px;width:100%;max-width:900px;max-height:calc(100vh - 48px);display:flex;flex-direction:column;box-shadow:0 8px 32px rgba(0,0,0,.25)}
.cfg-modal-hd{display:flex;align-items:center;justify-content:space-between;padding:10px 16px;border-bottom:1px solid #e2e8f0;background:#f8fafc;border-radius:10px 10px 0 0;flex-shrink:0}
.cfg-modal-hd h3{margin:0;font-size:14px;color:#1e293b}
.cfg-modal-close{background:none;border:none;font-size:20px;cursor:pointer;color:#64748b;padding:0 4px;line-height:1}
.cfg-modal-close:hover{color:#1e293b}
.cfg-modal-body{flex:1;overflow:hidden;position:relative}
.cfg-modal-body iframe{width:100%;height:100%;border:none}
.cfg-modal-loading{position:absolute;inset:0;display:flex;align-items:center;justify-content:center;color:#64748b;font-size:13px}
</style>
{{end}}`

// ── Head / foot ───────────────────────────────────────────────────────────────

const cfgHead = `{{define "cfg-head"}}<!DOCTYPE html>
<html lang="ru">
<head>
<meta charset="utf-8">
<script>
// Самохостинг Monaco: web-воркер грузится из встроенного /vendor/monaco/
// (тот же origin), иначе AMD-воркер не знает baseUrl и падает.
window.MonacoEnvironment = { getWorkerUrl: function () {
  return 'data:text/javascript;charset=utf-8,' + encodeURIComponent(
    "self.MonacoEnvironment={baseUrl:'" + location.origin + "/vendor/monaco/'};" +
    "importScripts('" + location.origin + "/vendor/monaco/vs/base/worker/workerMain.js');");
}};
</script>
<script src="/vendor/monaco/vs/loader.js" onerror="window._monacoLoadErr='loader.js failed'"></script>
<script>{{.InlineJSYaml}}</script>
<title>{{t $.Lang "Конфигуратор"}} — {{if .AppName}}{{.AppName}}{{else}}{{.Base.Name}}{{end}}</title>
{{template "css" .}}
</head>
<body>
<div class="topbar">
  <div class="cfg-menu-wrap">
    <button class="cfg-menu-btn" onclick="cfgMenuToggle()">{{t $.Lang "Меню"}} &#9662;</button>
    <div class="cfg-menu-dropdown" id="cfg-menu">
      <a href="#" onclick="cfgAdmin('about');return false">{{t $.Lang "О программе"}}</a>
      <a href="#" onclick="cfgAdmin('users');return false">{{t $.Lang "Пользователи"}}</a>
      <a href="#" onclick="cfgAdmin('roles');return false">{{t $.Lang "Роли и права"}}</a>
      <a href="#" onclick="cfgAdmin('sessions');return false">{{t $.Lang "Активные пользователи"}}</a>
      <a href="#" onclick="cfgAdmin('audit');return false">{{t $.Lang "Журнал регистрации"}}</a>
      <a href="#" onclick="cfgAdmin('settings');return false">{{t $.Lang "Параметры базы"}}</a>
      <a href="/bases/{{.Base.ID}}/configurator/logout" style="color:#c00;border-top:1px solid #e5e7eb;margin-top:2px">🚪 {{t $.Lang "Выйти"}}</a>
    </div>
  </div>
  <a href="/?sel={{.Base.ID}}">← {{t $.Lang "Лаунчер"}}</a>
  <h1>{{t $.Lang "Конфигуратор"}} — {{if .AppName}}{{.AppName}}{{else}}{{.Base.Name}}{{end}}</h1>
  <span style="font-size:11px;color:#7aa8d8">{{.DSNMasked}} · :{{.Base.Port}} · {{t $.Lang "платформа"}} {{.PlatformVer}}</span>
  <button id="cfg-save-topbar" onclick="cfgSaveActive()" title="{{t $.Lang "Сохранить (Ctrl+S)"}}" class="cfg-save-topbar">&#128190; {{t $.Lang "Сохранить"}}</button>
  <button onclick="launchEnterprise()" title="{{t $.Lang "Запустить предприятие"}}" class="run-enterprise-btn"><svg viewBox="0 0 24 24" fill="#333"><polygon points="6,3 20,12 6,21"/></svg></button>
  <button id="dbg-toggle" class="dbg-topbar-btn" onclick="dbgToggle()">&#128027; {{t $.Lang "Отладка: ВЫКЛ"}}</button>
  <span id="monaco-status" style="font-size:9px;color:#94a3b8">Monaco:...</span>
</div>
<div class="tabs">
  <a class="tab {{if eq .Tab "tree"}}active{{end}}" href="/bases/{{.Base.ID}}/configurator?tab=tree">🌳 {{t $.Lang "Дерево"}}</a>
  <a class="tab {{if eq .Tab "convert"}}active{{end}}" href="/bases/{{.Base.ID}}/configurator?tab=convert">🔄 {{t $.Lang "Импорт конфигурации"}}</a>
  <a class="tab {{if eq .Tab "files"}}active{{end}}" href="/bases/{{.Base.ID}}/configurator?tab=files">📁 {{t $.Lang "Файлы"}}</a>
  <a class="tab {{if eq .Tab "backup"}}active{{end}}" href="/bases/{{.Base.ID}}/configurator?tab=backup">💾 {{t $.Lang "Бэкапы"}}</a>
</div>
<div class="cfg-body">
{{if .Error}}<div class="err-box">{{.Error}}</div>{{end}}
{{if and .FieldsSaved (ne .FieldsSavedEntity "panel-backup")}}<div class="success-box">{{t $.Lang "✓ Типы полей для"}} «{{.FieldsSavedEntity}}» {{t $.Lang "сохранены. Перезапустите базу, чтобы изменения вступили в силу."}}</div>{{end}}
<div id="dbg-wrapper" style="display:flex;flex:1;overflow:hidden">
{{end}}`

const cfgAdminOverlay = `
<div id="admin-overlay" style="display:none;position:fixed;top:0;left:0;right:0;bottom:0;background:rgba(0,0,0,.4);z-index:9999;align-items:center;justify-content:center;padding:20px" onclick="if(event.target===this)this.style.display='none'"></div>
`

const cfgFoot = `{{define "cfg-foot"}}
<!-- debug panel (right sidebar) -->
<div class="dbg-panel" id="dbg-panel" style="display:none">
  <div class="dbg-status" id="dbg-status"><span class="dot disabled"></span> {{t $.Lang "Отладка: ВЫКЛ"}}</div>
  <div class="dbg-controls" id="dbg-controls" style="display:none">
    <button onclick="dbgContinue()">&#9654; {{t $.Lang "Продолжить"}}</button>
    <button onclick="dbgStep('over')" style="font-weight:600">&#10145; {{t $.Lang "Шаг (F10)"}}</button>
    <button onclick="dbgStep('into')">&#11015; {{t $.Lang "Шаг с заходом (F11)"}}</button>
    <button onclick="dbgStep('out')">&#11014; {{t $.Lang "Шаг с выходом"}}</button>
    <button onclick="dbgStop()">&#9209; {{t $.Lang "Стоп"}}</button>
  </div>
  <div class="dbg-tabs">
    <button class="dbg-tab active" onclick="dbgTab('vars')">{{t $.Lang "Переменные"}}</button>
    <button class="dbg-tab" onclick="dbgTab('watch')">{{t $.Lang "Табло"}}</button>
    <button class="dbg-tab" onclick="dbgTab('bp')">{{t $.Lang "Точки ост."}}</button>
    <button class="dbg-tab" onclick="dbgTab('stack')">{{t $.Lang "Стек"}}</button>
    <button class="dbg-tab" onclick="dbgTab('console')">{{t $.Lang "Консоль"}}</button>
  </div>
  <div id="dbg-vars" class="dbg-content"><div class="dbg-empty">{{t $.Lang "Включите отладку для просмотра переменных"}}</div></div>
  <div id="dbg-watch" class="dbg-content" style="display:none;flex-direction:column;padding:0">
    <div style="padding:6px 10px;border-bottom:1px solid #eef0f5;display:flex;gap:4px;flex-shrink:0">
      <input id="dbg-watch-add" type="text" placeholder="Выражение..." onkeydown="if(event.key==='Enter')dbgWatchAdd()" style="flex:1;min-width:0;padding:3px 8px;border:1px solid #d0d7e3;border-radius:4px;font-size:12px;font-family:'Cascadia Code','Fira Code',monospace">
      <button onclick="dbgWatchAdd()" style="background:#1a4a80;color:#fff;border:none;padding:3px 10px;border-radius:4px;font-size:11px;cursor:pointer">+</button>
    </div>
    <div id="dbg-watch-debug" style="padding:2px 10px;font-size:9px;color:#f97316;background:#fffbeb;flex-shrink:0"></div>
    <div id="dbg-watch-list" style="flex:1;overflow-y:auto;padding:4px 10px;min-height:40px"></div>
  </div>
  <div id="dbg-bp" class="dbg-content" style="display:none">
    <div style="padding:6px 0;border-bottom:1px solid #eef;display:flex;gap:4px;flex-shrink:0">
      <input id="dbg-bp-file" type="text" placeholder="{{t $.Lang "Файл"}} (post-...)" style="flex:2;min-width:0;padding:3px 6px;border:1px solid #d0d7e3;border-radius:4px;font-size:11px">
      <input id="dbg-bp-line" type="number" placeholder="{{t $.Lang "Стр"}}" style="width:50px;padding:3px 6px;border:1px solid #d0d7e3;border-radius:4px;font-size:11px">
      <button onclick="dbgManualBP()" style="background:#1a4a80;color:#fff;border:none;padding:3px 8px;border-radius:4px;font-size:10px;cursor:pointer">+</button>
    </div>
    <div id="dbg-bp-list"><div class="dbg-empty">{{t $.Lang "Нет точек останова"}}</div></div>
  </div>
  <div id="dbg-stack" class="dbg-content" style="display:none"><div class="dbg-empty">{{t $.Lang "Стек вызовов пуст"}}</div></div>
  <div id="dbg-console" class="dbg-content" style="display:none;flex-direction:column;padding:0">
    <div id="dbg-diag" style="background:#1e1e2e;color:#a5b4fc;padding:6px 10px;font-size:10px;font-family:'Cascadia Code','Fira Code',monospace;border-bottom:1px solid #333;max-height:120px;overflow-y:auto"></div>
    <div id="dbg-console-out" class="dbg-console-out" style="background:#1e1e2e;color:#cdd6f4"></div>
    <div class="dbg-console-input">
      <input id="dbg-expr" type="text" placeholder="{{t $.Lang "Выражение DSL"}}..." onkeydown="if(event.key==='Enter')dbgEval()">
      <button onclick="dbgEval()">{{t $.Lang "Выполнить"}}</button>
    </div>
  </div>
</div>
</div>{{/* dbg-wrapper */}}
</div>{{/* cfg-body */}}

<!-- Debug value inspector modal -->
<div class="cfg-modal-overlay" id="dbg-val-modal" onclick="if(event.target===this)dbgValModalClose()">
  <div class="cfg-modal-box" style="max-width:780px">
    <div class="cfg-modal-hd">
      <h3 id="dbg-val-modal-title">{{t $.Lang "Значение"}}</h3>
      <div style="display:flex;gap:8px;align-items:center">
        <button onclick="dbgValModalCopy()" style="background:#1a4a80;color:#fff;border:none;padding:4px 12px;border-radius:4px;font-size:12px;cursor:pointer">{{t $.Lang "Копировать"}}</button>
        <button class="cfg-modal-close" onclick="dbgValModalClose()">&times;</button>
      </div>
    </div>
    <div class="cfg-modal-body" style="padding:0">
      <textarea id="dbg-val-modal-text" readonly spellcheck="false" style="display:block;width:100%;height:62vh;border:none;resize:none;padding:12px 16px;font-family:'Cascadia Code','Fira Code',monospace;font-size:12px;line-height:1.5;color:#1e293b;box-sizing:border-box;outline:none;white-space:pre-wrap;word-break:break-word"></textarea>
    </div>
  </div>
</div>

<!-- Admin modal -->
<div class="cfg-modal-overlay" id="cfg-modal" onclick="if(event.target===this)cfgModalClose()">
  <div class="cfg-modal-box">
    <div class="cfg-modal-hd">
      <h3 id="cfg-modal-title">—</h3>
      <button class="cfg-modal-close" onclick="cfgModalClose()">&times;</button>
    </div>
    <div class="cfg-modal-body">
      <div class="cfg-modal-loading" id="cfg-modal-loading">{{t $.Lang "Загрузка..."}}</div>
      <iframe id="cfg-modal-iframe" onload="document.getElementById('cfg-modal-loading').style.display='none'"></iframe>
    </div>
  </div>
</div>

<!-- Query builder modal -->
<div class="qb-overlay" id="qb-overlay">
<div class="qb-modal">
  <div class="qb-modal-hd">
    <h2>{{t $.Lang "Конструктор запроса"}}</h2>
    <div style="display:flex;gap:6px;align-items:center">
      <select id="qb-mode" style="font-size:12px;border:1px solid #c8d0de;border-radius:4px;padding:4px 6px;background:#fff">
        <option value="dsl">{{t $.Lang "Полный код"}}</option>
        <option value="query">{{t $.Lang "Только запрос"}}</option>
      </select>
      <button id="qb-insert" style="background:#1a4a80;color:#fff;border:none;padding:6px 16px;border-radius:4px;cursor:pointer;font-size:13px;font-weight:600">{{t $.Lang "Вставить"}}</button>
      <button id="qb-close" style="background:#e8ecf2;color:#333;border:1px solid #c8d0de;padding:6px 14px;border-radius:4px;cursor:pointer;font-size:13px">{{t $.Lang "Закрыть"}}</button>
    </div>
  </div>
  <div class="qb-modal-bd">
    <div class="qb-grid">
      <!-- LEFT -->
      <div>
        <div class="qb-card">
          <h3>{{t $.Lang "Источник данных"}}</h3>
          <select id="mqb-src" onchange="mqbSetSrc(this.value)" style="width:100%;margin-bottom:6px"><option value="">{{t $.Lang "— выбрать —"}}</option></select>
          <div style="display:flex;align-items:center;gap:6px;margin-bottom:4px">
            <span style="font-size:12px;color:#64748b;flex-shrink:0;width:68px">{{t $.Lang "Псевдоним"}}:</span>
            <input id="mqb-alias" type="text" placeholder="{{t $.Lang "напр. Т"}}" oninput="mqbRebuild()" style="width:100px;font-size:12px;border:1px solid #e2e8f0;border-radius:4px;padding:2px 5px">
          </div>
          <div id="mqb-vtp" style="display:none;margin-top:4px">
            <label style="font-size:12px;color:#64748b">{{t $.Lang "Параметры ВТ"}}</label>
            <input id="mqb-vtpv" type="text" style="width:100%;margin-top:2px" placeholder="&amp;НаДату" oninput="mqbGen()">
          </div>
        </div>
        <div class="qb-card">
          <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:6px">
            <h3 style="margin:0">{{t $.Lang "Соединения"}}</h3>
            <button onclick="mqbAddJoin()" style="background:#dbeafe;color:#1d4ed8;border:none;padding:2px 8px;font-size:12px;border-radius:4px;cursor:pointer">+ JOIN</button>
          </div>
          <div id="mqb-joins"><p style="font-size:12px;color:#94a3b8;margin:0" id="mqb-joins-hint">{{t $.Lang "Нет"}}</p></div>
        </div>
        <div class="qb-card">
          <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:6px">
            <h3 style="margin:0">{{t $.Lang "Поля"}}</h3>
            <div style="display:flex;gap:3px">
              <button onclick="mqbAll(true)" style="background:#e2e8f0;color:#475569;border:none;padding:2px 6px;font-size:11px;border-radius:3px;cursor:pointer">{{t $.Lang "Все"}}</button>
              <button onclick="mqbAll(false)" style="background:#e2e8f0;color:#475569;border:none;padding:2px 6px;font-size:11px;border-radius:3px;cursor:pointer">{{t $.Lang "Сброс"}}</button>
            </div>
          </div>
          <div class="qb-fl" id="mqb-fields"></div>
        </div>
        <div class="qb-card">
          <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:6px">
            <h3 style="margin:0">{{t $.Lang "Условия (ГДЕ)"}}</h3>
            <button onclick="mqbAddCond()" style="background:#dbeafe;color:#1d4ed8;border:none;padding:2px 8px;font-size:12px;border-radius:4px;cursor:pointer">+</button>
          </div>
          <div id="mqb-conds"></div>
        </div>
        <div class="qb-card">
          <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:6px">
            <h3 style="margin:0">{{t $.Lang "Сортировка"}}</h3>
            <button onclick="mqbAddOrd()" style="background:#dbeafe;color:#1d4ed8;border:none;padding:2px 8px;font-size:12px;border-radius:4px;cursor:pointer">+</button>
          </div>
          <div id="mqb-ords"></div>
        </div>
      </div>
      <!-- RIGHT -->
      <div>
        <div class="qb-card">
          <h3>{{t $.Lang "DSL-фрагмент"}}</h3>
          <textarea id="mqb-dsl" rows="18" readonly style="width:100%;font-family:monospace;font-size:12px;border:1px solid #e2e8f0;border-radius:4px;padding:8px;background:#fff;resize:vertical"></textarea>
        </div>
        <div class="qb-card">
          <h3>{{t $.Lang "Текст запроса"}}</h3>
          <textarea id="mqb-qry" rows="10" readonly style="width:100%;font-family:monospace;font-size:12px;border:1px solid #e2e8f0;border-radius:4px;padding:8px;background:#fff;resize:vertical"></textarea>
        </div>
      </div>
    </div>
  </div>
</div>
</div>
<script>
// ── New object form ────────────────────────────────────────────
var _cfgNewTitles = {catalog:'Новый справочник', document:'Новый документ', register:'Новый регистр', inforeg:'Новый регистр сведений', accountreg:'Новый регистр бухгалтерии', enum:'Новое перечисление', subsystem:'Новая подсистема', widget:'Новый виджет', module:'Новый общий модуль', processor:'Новая обработка'};
function cfgNewObj(kind) {
  if (kind === 'printform') { cfgNewPrintFormShow(); return; }
  var f = document.getElementById('cfg-new-form');
  document.getElementById('cfg-new-title').textContent = _cfgNewTitles[kind] || 'Новый объект';
  document.getElementById('cfg-new-kind-inp').value = kind;
  document.getElementById('cfg-new-name').value = '';
  f.style.display = 'block';
  document.getElementById('cfg-new-form-pf').style.display = 'none';
  document.getElementById('cfg-new-name').focus();
}
function cfgHideNew() {
  document.getElementById('cfg-new-form').style.display = 'none';
  document.getElementById('cfg-new-form-pf').style.display = 'none';
}
// obToggleLayout переключает пейны редактора раскладки: «Авто» (галочки) и
// «По рядам» (drag-конструктор).
function obToggleLayout(sel) {
  var form = sel.closest('form'); if (!form) return;
  var auto = form.querySelector('.ob-auto'), rows = form.querySelector('.ob-rows-pane');
  if (auto) auto.style.display = (sel.value === 'rows') ? 'none' : 'block';
  if (rows) rows.style.display = (sel.value === 'rows') ? 'block' : 'none';
}
// obHomeInit инициализирует drag-конструктор рядов для одной формы редактора.
function obHomeInit(form) {
  var key = form.getAttribute('data-home-key'); if (!key) return;
  var rowsWrap = form.querySelector('.ob-rows');
  var pool = form.querySelector('.ob-pool');
  var hidden = form.querySelector('input[name="home_rows"]');
  if (!rowsWrap || !pool || !hidden) return;
  var data = (window.__homeData && window.__homeData[key]) || {rows: []};
  var widgets = window.__homeWidgets || [];
  var titleByName = {}; widgets.forEach(function (w) { titleByName[w.name] = w.title || w.name; });

  function chip(name) {
    var c = document.createElement('span');
    c.className = 'ob-chip'; c.setAttribute('draggable', 'true'); c.dataset.name = name;
    c.textContent = titleByName[name] || name;
    c.addEventListener('dragstart', function () { window.__obDrag = c; setTimeout(function(){ c.classList.add('dragging'); }, 0); });
    c.addEventListener('dragend', function () { c.classList.remove('dragging'); window.__obDrag = null; });
    return c;
  }
  function afterElement(zone, x) {
    var els = Array.prototype.slice.call(zone.querySelectorAll('.ob-chip:not(.dragging)'));
    for (var i = 0; i < els.length; i++) {
      var r = els[i].getBoundingClientRect();
      if (x < r.left + r.width / 2) return els[i];
    }
    return null;
  }
  function bindZone(zone) {
    zone.addEventListener('dragover', function (e) {
      e.preventDefault();
      var dr = window.__obDrag; if (!dr) return;
      var after = afterElement(zone, e.clientX);
      if (after == null) zone.appendChild(dr); else zone.insertBefore(dr, after);
    });
  }
  function makeRow(names) {
    var row = document.createElement('div'); row.className = 'ob-row';
    var zone = document.createElement('div'); zone.className = 'ob-zone';
    (names || []).forEach(function (n) { zone.appendChild(chip(n)); });
    bindZone(zone);
    var del = document.createElement('button');
    del.type = 'button'; del.className = 'ob-row-del'; del.textContent = '✕'; del.title = 'Удалить ряд';
    del.addEventListener('click', function () {
      Array.prototype.slice.call(zone.children).forEach(function (c) { pool.appendChild(c); });
      row.remove();
    });
    row.appendChild(zone); row.appendChild(del);
    return row;
  }

  bindZone(pool);
  rowsWrap.innerHTML = '';
  var used = {};
  (data.rows || []).forEach(function (names) {
    rowsWrap.appendChild(makeRow(names));
    (names || []).forEach(function (n) { used[n] = 1; });
  });
  pool.innerHTML = '';
  widgets.forEach(function (w) { if (!used[w.name]) pool.appendChild(chip(w.name)); });

  var addBtn = form.querySelector('.ob-add-row');
  if (addBtn) addBtn.addEventListener('click', function () { rowsWrap.appendChild(makeRow([])); });

  form.addEventListener('submit', function () {
    var sel = form.querySelector('select[name="home_layout"]');
    if (!sel || sel.value !== 'rows') return; // в «Авто» сохраняются галочки
    var out = [];
    Array.prototype.slice.call(rowsWrap.querySelectorAll('.ob-zone')).forEach(function (zone) {
      var names = Array.prototype.slice.call(zone.querySelectorAll('.ob-chip')).map(function (c) { return c.dataset.name; });
      if (names.length) out.push(names);
    });
    hidden.value = JSON.stringify(out);
  });
}
document.addEventListener('DOMContentLoaded', function () {
  Array.prototype.slice.call(document.querySelectorAll('form[data-home-key]')).forEach(obHomeInit);
});
function cfgNewPrintFormShow() {
  document.getElementById('cfg-new-form').style.display = 'none';
  document.getElementById('cfg-new-form-pf').style.display = 'block';
  document.getElementById('cfg-new-pf-name').value = '';
  document.getElementById('cfg-new-pf-name').focus();
}

// ── Folder picker ──────────────────────────────────────────────
function pickDir(inputId, title) {
  var btn = event.target;
  var cur = document.getElementById(inputId).value || '';
  btn.disabled = true;
  btn.textContent = '...';
  fetch('/browse-dir?title=' + encodeURIComponent(title) + '&initial_path=' + encodeURIComponent(cur))
    .then(function(r){ return r.json(); })
    .then(function(d){
      if (d.path) document.getElementById(inputId).value = d.path;
    })
    .finally(function(){ btn.disabled = false; btn.textContent = '\u{1F4C1}'; });
}

// ── Logo upload helpers ──────────────────────────────────────────
function previewLogo(input) {
  if (input.files && input.files[0]) {
    var reader = new FileReader();
    reader.onload = function(e) {
      var img = document.getElementById('logo-preview');
      img.src = e.target.result;
      img.style.display = '';
    };
    reader.readAsDataURL(input.files[0]);
  }
}
function removeLogo() {
  var img = document.getElementById('logo-preview');
  if (img) { img.src = ''; img.style.display = 'none'; }
  document.getElementById('logo-remove').value = '1';
  var fileInput = document.querySelector('input[name="app_logo_file"]');
  if (fileInput) fileInput.value = '';
}

// ── Reference / enum picker toggle ───────────────────────────────
// Один и тот же select-target используется и для reference, и для enum:
// при смене типа JS меняет options между списком сущностей и списком
// перечислений. Сохраняем выбранное значение если оно есть в новой
// группе options.
var _cfgEntityNames = [{{range $i, $ := $.AllEntityNames}}{{if $i}},{{end}}'{{.}}'{{end}}];
var _cfgEnumNames = [{{range $i, $ := $.AllEnumNames}}{{if $i}},{{end}}'{{.}}'{{end}}];
function cfgToggleRef(sel, refId) {
  var r = document.getElementById(refId);
  if (!r) return;
  if (sel.value === 'reference' || sel.value === 'enum') {
    r.style.display = '';
    var src = sel.value === 'enum' ? _cfgEnumNames : _cfgEntityNames;
    var cur = r.value;
    var keep = false;
    var html = '<option value="">{{t $.Lang "— выбрать —"}}</option>';
    for (var i = 0; i < src.length; i++) {
      var n = src[i];
      var sel2 = (n === cur) ? ' selected' : '';
      if (n === cur) keep = true;
      html += '<option value="' + n + '"' + sel2 + '>' + n + '</option>';
    }
    r.innerHTML = html;
    if (!keep) r.value = '';
  } else {
    r.style.display = 'none';
  }
}
// cfgToggleNum показывает поля «Длина, Точность» только для типа «число».
function cfgToggleNum(sel, numId) {
  var n = document.getElementById(numId);
  if (!n) return;
  n.style.display = (sel.value === 'number') ? '' : 'none';
}
var _cfgNewFieldIdx = 0;
function cfgAddField(tblId, prefix, entityName) {
  _cfgNewFieldIdx++;
  var tbl = document.getElementById(tblId);
  if (!tbl) return;
  var refId = 'cfr-'+entityName+'-nf'+_cfgNewFieldIdx;
  var numId = 'cfn-'+entityName+'-nf'+_cfgNewFieldIdx;
  var tr = document.createElement('tr');
  tr.innerHTML = '<td><input name="'+prefix+'.'+_cfgNewFieldIdx+'.name" style="width:100%;padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px" placeholder="ИмяПоля"></td>'
    +'<td><select name="'+prefix+'.'+_cfgNewFieldIdx+'.type" onchange="cfgToggleRef(this,\''+refId+'\');cfgToggleNum(this,\''+numId+'\')">'
    +'<option value="string">{{t $.Lang "строка"}}</option><option value="number">{{t $.Lang "число"}}</option><option value="date">{{t $.Lang "дата"}}</option><option value="bool">{{t $.Lang "булево"}}</option><option value="reference">{{t $.Lang "ссылка →"}}</option><option value="enum">{{t $.Lang "перечисление →"}}</option>'
    +'</select>'
    +' <span id="'+numId+'" style="display:none" title="{{t $.Lang "Длина, Точность"}}">'
    +'<input type="number" min="1" name="'+prefix+'.'+_cfgNewFieldIdx+'.length" placeholder="дл" style="width:46px;padding:2px 3px;border:1px solid #ccd0d8;border-radius:3px;font-size:11px">'
    +' , <input type="number" min="0" name="'+prefix+'.'+_cfgNewFieldIdx+'.scale" placeholder="точн" style="width:46px;padding:2px 3px;border:1px solid #ccd0d8;border-radius:3px;font-size:11px">'
    +'</span></td>'
    +'<td><select name="'+prefix+'.'+_cfgNewFieldIdx+'.ref" id="'+refId+'" style="display:none">'
    +'<option value="">{{t $.Lang "— выбрать —"}}</option>'
    +'</select></td>';
  tbl.appendChild(tr);
  tr.querySelector('input').focus();
}
var _cfgNewTpIdx = 0;
function cfgAddTP(btn, entityName) {
  _cfgNewTpIdx++;
  var tpName = prompt('Имя табличной части:');
  if (!tpName) return;
  var wrapper = document.createElement('details');
  wrapper.open = true;
  var prefix = 'new_tp.'+_cfgNewTpIdx;
  var tblId = 'ft-'+entityName+'-ntp'+_cfgNewTpIdx;
  wrapper.innerHTML = '<summary class="section-hd" style="cursor:pointer">📋 '+tpName+' (0)</summary>'
    +'<input type="hidden" name="new_tp_name" value="'+tpName+'">'
    +'<input type="hidden" name="'+prefix+'.idx" value="'+_cfgNewTpIdx+'">'
    +'<div class="tp-block"><table class="fields-tbl" id="'+tblId+'">'
    +'<tr><th>{{t $.Lang "Поле"}}</th><th>{{t $.Lang "Тип"}}</th><th style="min-width:150px">{{t $.Lang "Объект"}}</th></tr>'
    +'</table>'
    +'<button type="button" onclick="cfgAddField(\''+tblId+'\',\'new_tp.'+_cfgNewTpIdx+'.field\',\''+entityName+'\')" style="font-size:11px;color:#1a4a80;background:none;border:1px dashed #c0c8d8;padding:2px 8px;border-radius:3px;cursor:pointer;margin:4px 0">+ {{t $.Lang "Добавить поле"}}</button>'
    +'</div>';
  btn.parentNode.insertBefore(wrapper, btn);
  cfgAddField(tblId, 'new_tp.'+_cfgNewTpIdx+'.field', entityName);
}
// ── Predefined items row ──────────────────────────────────────────
var _cfgPreRowIdx = 10000;
function cfgAddPreRow(tblId, fieldCount) {
  _cfgPreRowIdx++;
  var tbl = document.getElementById(tblId);
  if (!tbl) { tbl = document.querySelector('[id="'+tblId+'"]'); }
  if (!tbl) return;
  var idx = _cfgPreRowIdx;
  // read column headers to get field names
  var headers = tbl.querySelectorAll('th');
  var tr = document.createElement('tr');
  var html = '<td><input type="text" name="pre.'+idx+'.name" placeholder="ИмяЭлемента" style="width:100%;font-size:12px;padding:2px 4px;border:1px solid #dde;border-radius:3px"></td>';
  for (var i = 1; i < headers.length; i++) {
    var fn = headers[i].textContent.trim();
    html += '<td><input type="text" name="pre.'+idx+'.field.'+fn+'" style="width:100%;font-size:12px;padding:2px 4px;border:1px solid #dde;border-radius:3px"></td>';
  }
  tr.innerHTML = html;
  tbl.appendChild(tr);
  tr.querySelector('input').focus();
}
// ── AccountReg resource row ───────────────────────────────────────
var _cfgARResIdx = 0;
function cfgAddARField(tblId) {
  _cfgARResIdx++;
  var tbl = document.getElementById(tblId);
  if (!tbl) return;
  tbl.style.display = '';
  var idx = tbl.querySelectorAll('tr').length - 1;
  var tr = document.createElement('tr');
  tr.innerHTML = '<td><input type="text" name="res.'+idx+'.name" placeholder="ИмяРесурса" style="width:100%;font-size:12px;padding:2px 4px;border:1px solid #dde;border-radius:3px"></td>'
    +'<td><select name="res.'+idx+'.type"><option value="number">{{t $.Lang "число"}}</option><option value="string">{{t $.Lang "строка"}}</option><option value="bool">{{t $.Lang "булево"}}</option></select></td>';
  tbl.appendChild(tr);
  tr.querySelector('input').focus();
}
// ── Click-to-edit module (Monaco with textarea fallback) ─────────
var monacoEditors = {};
window._monacoReady = false;
window.onerror = function(msg, url, ln, col, err) {
  var d = document.getElementById('dbg-diag');
  if (d) d.innerHTML = '<div style="color:#ef4444;font-size:10px;background:#1e1e2e;padding:4px">JS ERROR: ' + msg + ' (line ' + ln + ')</div>' + d.innerHTML;
  return false;
};

function startEdit(name) {
  var pre = document.getElementById('pre-'+name);
  var ta  = document.getElementById('ta-'+name);
  if (!pre || !ta) return;
  if (window._monacoReady && typeof monaco !== 'undefined') {
    // Monaco path: create editor inside the code-wrap container
    var wrap = pre.parentNode;
    if (!wrap || wrap.querySelector('.monaco-target')) return; // already active
    var lang = 'onebase-dsl';
    if (name.indexOf('rep-') === 0) lang = 'onebase-query';
    if (name.indexOf('pf-') === 0) lang = 'yaml';
    ta.value = pre.textContent;
    pre.style.display = 'none';
    var div = document.createElement('div');
    div.className = 'monaco-target';
    div._editorId = name;
    wrap.appendChild(div);
    var langId = (lang === 'yaml') ? 'yaml' : 'onebase-dsl';
    var editor = monaco.editor.create(div, {
      value: ta.value,
      language: langId,
      theme: 'onebase-dark',
      minimap: { enabled: false },
      fontSize: 12,
      lineNumbers: 'on',
      scrollBeyondLastLine: false,
      wordWrap: 'on',
      tabSize: 2,
      glyphMargin: true,
      automaticLayout: true,
      contextmenu: false // используем своё контекстное меню (см. document-level handler)
    });
    editor._fileId = name;
    monacoEditors[name] = editor;
    // Запоминаем последнее непустое выделение: правый клик в Monaco
    // сбрасывает selection раньше, чем открывается конструктор запроса,
    // поэтому в openQBModalMonaco текущее выделение уже пустое.
    editor._lastSelText = '';
    editor.onDidChangeCursorSelection(function(ev) {
      var t = editor.getModel().getValueInRange(ev.selection);
      if (t && t.trim()) editor._lastSelText = t;
    });
    // Override F10/F11 inside Monaco so debugger shortcuts work even when editor has focus.
    // Monaco intercepts these via its internal keybinding manager before the DOM event
    // reaches our document-level capture handler.
    editor.addCommand(monaco.KeyCode.F10, function() { dbgStep('over'); });
    editor.addCommand(monaco.KeyCode.F11, function() { dbgStep('into'); });
    editor.addCommand(monaco.KeyMod.Shift | monaco.KeyCode.F11, function() { dbgStep('out'); });
    // Gutter click for breakpoint toggle — setTimeout decouples from Monaco internals
    editor.onMouseDown(function(e) {
      try {
        var tgt = e.target;
        var isGutter = tgt && (tgt.type === monaco.editor.MouseTargetType.GUTTER_GLYPH_MARGIN
          || tgt.type === monaco.editor.MouseTargetType.GUTTER_LINE_NUMBERS
          || tgt.type === monaco.editor.MouseTargetType.GUTTER_LINE_DECORATIONS);
        if (!isGutter) return;
        var line = tgt.position ? tgt.position.lineNumber : (tgt.range ? tgt.range.startLineNumber : 0);
        if (line) {
          var n = name, l = line;
          setTimeout(function(){ dbgToggleBreakpoint(n, l); }, 0);
        }
      } catch(err) { /* ignore Monaco internal errors */ }
    });
  } else {
    // Fallback: original textarea pattern
    if (ta.style.display !== 'none' && ta.style.display !== '') return;
    ta.value = pre.textContent;
    pre.style.display = 'none';
    ta.style.display = 'block';
    ta.focus();
  }
}

function endEdit(name) {
  var pre = document.getElementById('pre-'+name);
  var ta  = document.getElementById('ta-'+name);
  // Sync Monaco content back to textarea if present
  if (monacoEditors[name]) {
    ta.value = monacoEditors[name].getValue();
    monacoEditors[name].dispose();
    delete monacoEditors[name];
    var wrap = pre.parentNode;
    var mDiv = wrap.querySelector('.monaco-target');
    if (mDiv) mDiv.remove();
  }
  if (pre && ta) {
    pre.innerHTML = hl(ta.value);
    pre.style.display = '';
    ta.style.display = 'none';
  }
}
// ── Syntax check ──────────────────────────────────────────────────
// runCheck reads code from a textarea (id "ta-<key>") and posts it to the
// configurator check endpoint, then renders the result in the sibling
// .check-result element with id "check-<key>". The kind argument selects the
// validator: dsl | widget | home_page | entity.
function runCheck(kind, key, name) {
  var ta = document.getElementById('ta-' + key);
  var result = document.getElementById('check-' + key);
  if (!ta || !result) return;
  // Sync Monaco editor content back to textarea if present
  if (typeof monacoEditors !== 'undefined' && monacoEditors[key]) {
    ta.value = monacoEditors[key].getValue();
  }
  // URLSearchParams sets Content-Type to application/x-www-form-urlencoded,
  // which Go's r.ParseForm() can decode. FormData would force multipart and
  // require r.ParseMultipartForm() on the server.
  var body = new URLSearchParams();
  body.set('kind', kind);
  body.set('source', ta.value);
  if (name) body.set('name', name);
  result.className = 'check-result check-pending';
  result.textContent = '⏳ Проверка... (' + ta.value.length + ' симв.)';
  fetch('/bases/' + _dbgBase + '/configurator/check', {
    method: 'POST',
    headers: {'Content-Type': 'application/x-www-form-urlencoded; charset=UTF-8'},
    body: body.toString()
  })
    .then(function(r){ return r.json(); })
    .then(function(d){
      if (d.error) {
        result.className = 'check-result check-err';
        result.textContent = '⚠ ' + d.error;
        return;
      }
      if (d.ok) {
        result.className = 'check-result check-ok';
        result.textContent = '✓ Синтаксис ОК';
      } else {
        result.className = 'check-result check-err';
        var lines = (d.issues || []).map(function(i){
          var pos = (i.line ? ' (стр. ' + i.line + ')' : '');
          return '• ' + i.message + pos;
        });
        result.innerHTML = '<b>Найдено ошибок: ' + d.total + '</b><br>' + lines.join('<br>');
      }
    })
    .catch(function(e){
      result.className = 'check-result check-err';
      result.textContent = '⚠ ' + e.message;
    });
}

function runCheckAll() {
  var btn = document.getElementById('btn-check-all');
  var panel = document.getElementById('check-all-panel');
  var body = document.getElementById('check-all-body');
  btn.disabled = true;
  btn.textContent = 'Проверка...';
  body.innerHTML = '<div style="padding:10px;color:#888">⏳ Идёт проверка конфигурации...</div>';
  panel.style.display = 'flex';
  fetch('/bases/' + _dbgBase + '/configurator/check-all', {method:'POST'})
    .then(function(r){ return r.json(); })
    .then(function(d){
      btn.disabled = false;
      btn.textContent = 'Проверить конфигурацию';
      if (d.error) {
        body.innerHTML = '<div class="check-row check-err">⚠ ' + d.error + '</div>';
        return;
      }
      if (d.ok) {
        body.innerHTML = '<div class="check-row check-ok"><b>✓ Конфигурация корректна</b><br>Ошибок не найдено.</div>';
        return;
      }
      var html = '<div class="check-row check-err" style="font-weight:600">Найдено ошибок: ' + d.total + '</div>';
      (d.issues || []).forEach(function(i){
        html += '<div class="check-row">';
        if (i.kind || i.object) {
          html += '<div style="font-size:11px;color:#888;text-transform:uppercase;letter-spacing:.04em">' +
                  (i.kind || '') + (i.object ? ' · ' + i.object : '') + '</div>';
        }
        html += '<div style="color:#c00">' + escapeHtml(i.message) + '</div>';
        if (i.file) {
          html += '<div style="font-size:10px;color:#aaa;font-family:Consolas,monospace">' + i.file +
                  (i.line ? ':' + i.line : '') + '</div>';
        }
        html += '</div>';
      });
      body.innerHTML = html;
    })
    .catch(function(e){
      btn.disabled = false;
      btn.textContent = 'Проверить конфигурацию';
      body.innerHTML = '<div class="check-row check-err">⚠ ' + e.message + '</div>';
    });
}

function closeCheckAll() {
  document.getElementById('check-all-panel').style.display = 'none';
}

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, function(c){
    return {'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c];
  });
}

// ── Form field reorder ──────────────────────────────────────────
function moveUp(btn){var row=btn.closest('.form-field-row'),prev=row.previousElementSibling;if(prev&&prev.classList.contains('form-field-row'))row.parentNode.insertBefore(row,prev);}
function moveDown(btn){var row=btn.closest('.form-field-row'),next=row.nextElementSibling;if(next&&next.classList.contains('form-field-row'))row.parentNode.insertBefore(next,row);}
// ── Form tabs (outside module-editor-wrap) ────────────────────
function formTab(el,showId,hideId){
  var tabs=el.parentNode;
  tabs.querySelectorAll('.module-tab').forEach(function(t){t.classList.remove('active')});
  el.classList.add('active');
  document.getElementById(showId).classList.add('active');
  document.getElementById(hideId).classList.remove('active');
}
// ── Panel selection ────────────────────────────────────────────
function toggleSidebar() {
  var sb = document.getElementById('cfg-sidebar');
  var btn = document.getElementById('sidebar-toggle');
  sb.classList.toggle('collapsed');
  btn.classList.toggle('collapsed');
  btn.textContent = sb.classList.contains('collapsed') ? '▶' : '◀';
}
function selItem(el) {
  document.querySelectorAll('.cfg-item').forEach(function(e){e.classList.remove('sel')});
  document.querySelectorAll('.cfg-panel').forEach(function(e){e.classList.remove('active')});
  el.classList.add('sel');
  var panel = document.getElementById(el.dataset.id);
  if (panel) panel.classList.add('active');
}
// Context menu for tree items
document.addEventListener('contextmenu', function(ev) {
  var item = ev.target.closest('.cfg-item');
  if (!item) return;
  var did = item.dataset.id || '';
  if (did.indexOf('e-') !== 0 && did.indexOf('r-') !== 0 && did.indexOf('ir-') !== 0 && did.indexOf('ar-') !== 0 &&
      did.indexOf('en-') !== 0 && did.indexOf('rep-') !== 0 && did.indexOf('mod-') !== 0 &&
      did.indexOf('proc-') !== 0 && did.indexOf('pf-') !== 0 && did.indexOf('sub-') !== 0) return;
  ev.preventDefault();
  selItem(item);
  var existing = document.getElementById('cfg-ctx-menu');
  if (existing) existing.remove();
  var menu = document.createElement('div');
  menu.id = 'cfg-ctx-menu';
  menu.style.cssText = 'position:fixed;left:'+ev.clientX+'px;top:'+ev.clientY+'px;background:#fff;border-radius:8px;box-shadow:0 4px 16px rgba(0,0,0,.2);padding:4px 0;z-index:9999;min-width:150px';
  var dname = did.replace(/^[a-z]+-/, '');
  var delBtn = document.createElement('div');
  delBtn.textContent = 'Удалить «' + dname + '»';
  delBtn.style.cssText = 'padding:8px 14px;cursor:pointer;font-size:13px;color:#dc2626';
  delBtn.onmouseover = function(){this.style.background='#fef2f2'};
  delBtn.onmouseout = function(){this.style.background=''};
  delBtn.onclick = function(){
    menu.remove();
    if (!confirm('Удалить «' + dname + '»? Это действие необратимо!')) return;
    var form = document.createElement('form');
    form.method = 'POST';
    form.action = '/bases/' + _dbgBase + '/configurator/entity-delete';
    var inp = document.createElement('input'); inp.type='hidden'; inp.name='entity'; inp.value=dname;
    form.appendChild(inp);
    document.body.appendChild(form);
    form.submit();
  };
  menu.appendChild(delBtn);
  document.body.appendChild(menu);
  setTimeout(function(){ document.addEventListener('click', function rm(){ menu.remove(); document.removeEventListener('click',rm); }); }, 10);
});
function cfgSelectPanel(id) {
  var el = document.querySelector('[data-id="' + id + '"]');
  if (el) selItem(el);
}
// ── AJAX-сохранение форм + единая кнопка «Сохранить» в шапке ────────────────
function cfgToast(msg, isError) {
  var t = document.getElementById('cfg-toast');
  if (!t) { t = document.createElement('div'); t.id = 'cfg-toast'; document.body.appendChild(t); }
  t.textContent = msg;
  t.style.cssText = 'position:fixed;bottom:22px;left:50%;transform:translateX(-50%);z-index:10001;padding:10px 18px;border-radius:6px;font-size:13px;box-shadow:0 4px 16px rgba(0,0,0,.25);color:#fff;max-width:70vw;background:' + (isError ? '#dc2626' : '#16a34a');
  t.style.display = 'block';
  clearTimeout(t._h);
  t._h = setTimeout(function(){ t.style.display = 'none'; }, isError ? 6000 : 2500);
}

// Является ли форма редактированием объекта (её сохраняем через AJAX).
// Формы создания/удаления меняют дерево и идут обычным сабмитом (перезагрузка).
function cfgIsAjaxForm(form) {
  if (!form || form.tagName !== 'FORM') return false;
  if (!form.closest('.cfg-panel')) return false;
  var a = form.getAttribute('action') || '';
  if (/\/(widget-delete|entity-delete|new|new-printform)$/.test(a)) return false;
  return a.indexOf('/configurator/') >= 0;
}

function cfgAjaxSubmit(form) {
  var saveBtn = document.getElementById('cfg-save-topbar');
  if (saveBtn) { saveBtn.disabled = true; }
  var submitBtns = form.querySelectorAll('button[type="submit"],input[type="submit"]');
  submitBtns.forEach(function(b){ b.disabled = true; });
  var done = function(){
    if (saveBtn) saveBtn.disabled = false;
    submitBtns.forEach(function(b){ b.disabled = false; });
  };
  fetch(form.getAttribute('action'), {
    method: 'POST',
    headers: { 'X-Onebase-Ajax': '1' },
    body: new FormData(form)
  })
    .then(function(r){ return r.json().catch(function(){ return { ok:false, error:'Некорректный ответ сервера' }; }); })
    .then(function(d){
      done();
      if (d && d.ok) {
        cfgToast(d.message || 'Сохранено', false);
        cfgMarkDirtyStar(d.running);
      } else {
        cfgToast((d && d.error) || 'Ошибка сохранения', true);
      }
    })
    .catch(function(err){ done(); cfgToast('Ошибка: ' + err.message, true); });
}

// Сохранить объект, открытый в правой панели (единая кнопка в шапке, Ctrl+S).
function cfgSaveActive() {
  var panel = document.querySelector('.cfg-panel.active');
  if (!panel) { cfgToast('Нет открытого объекта для сохранения', true); return; }
  var target = null;
  panel.querySelectorAll('form').forEach(function(f){
    if (!target && cfgIsAjaxForm(f)) target = f;
  });
  if (!target) { cfgToast('В этом разделе нечего сохранять', true); return; }
  if (typeof target.requestSubmit === 'function') target.requestSubmit();
  else target.submit();
}

// После сохранения файла конфигурации, если база запущена, помечаем дерево
// звёздочкой «требуется перезапуск» — как при серверном рендере ConfigDirty.
function cfgMarkDirtyStar(running) {
  if (!running) return;
  var title = '{{t $.Lang "Конфигурация на диске изменилась с момента запуска базы. Перезапустите базу, чтобы изменения применились."}}';
  var targets = [];
  var grp = document.querySelector('#cfg-sidebar .cfg-group');
  if (grp) targets.push(grp);
  var app = document.querySelector('.cfg-item[data-id="panel-app"]');
  if (app) targets.push(app);
  targets.forEach(function(el){
    if (el.querySelector('.cfg-dirty')) return;
    var s = document.createElement('span');
    s.className = 'cfg-dirty';
    s.title = title;
    s.textContent = '*';
    el.appendChild(s);
  });
}

// Глобальный перехват сабмита форм редактирования объектов.
document.addEventListener('submit', function(e) {
  var form = e.target;
  if (!cfgIsAjaxForm(form)) return;
  e.preventDefault();
  cfgAjaxSubmit(form);
}, false);

// ── Перемещение объектов метаданных мышью (drag-and-drop, как в 1С) ─────────
var _treeDrag = null;
var _groupDrag = null;
function cfgGid(det) { return (det && (det.dataset.group || det.dataset.gid)) || ''; }
function initTreeDnd() {
  // объекты внутри групп — перетаскиваемы (порядок объектов)
  document.querySelectorAll('#cfg-sidebar details[data-group]').forEach(function(d) {
    d.querySelectorAll(':scope > .cfg-item').forEach(function(it) { it.setAttribute('draggable', 'true'); });
  });
  // сами группы — перетаскиваемы за заголовок (порядок групп)
  document.querySelectorAll('#cfg-sidebar details.cfg-tree').forEach(function(d) {
    var sum = d.querySelector(':scope > summary');
    if (sum) sum.setAttribute('draggable', 'true');
  });
}
// Применить сохранённый порядок групп при загрузке (клиентская перестановка).
function applyGroupOrder() {
  if (!window._treeGroupOrder || !_treeGroupOrder.length) return;
  var sb = document.getElementById('cfg-sidebar');
  if (!sb) return;
  var groups = Array.prototype.slice.call(sb.querySelectorAll(':scope > details.cfg-tree'));
  if (groups.length < 2) return;
  var byId = {};
  groups.forEach(function(d) { var id = cfgGid(d); if (id) byId[id] = d; });
  var ordered = [];
  _treeGroupOrder.forEach(function(id) { if (byId[id]) { ordered.push(byId[id]); delete byId[id]; } });
  groups.forEach(function(d) { var id = cfgGid(d); if (byId[id]) ordered.push(d); });
  var stop = groups[groups.length - 1].nextSibling;
  ordered.forEach(function(d) { sb.insertBefore(d, stop); });
}
document.addEventListener('dragstart', function(e) {
  // перетаскивание группы — старт на заголовке
  var sum = e.target.closest ? e.target.closest('summary.cfg-group-hd') : null;
  if (sum && sum.parentElement && sum.parentElement.matches('details.cfg-tree')) {
    _groupDrag = sum.parentElement; _treeDrag = null;
    _groupDrag.style.opacity = '0.5';
    if (e.dataTransfer) { e.dataTransfer.effectAllowed = 'move'; try { e.dataTransfer.setData('text/plain', cfgGid(_groupDrag)); } catch (_) {} }
    return;
  }
  // перетаскивание объекта внутри группы
  var it = e.target.closest ? e.target.closest('.cfg-item') : null;
  if (!it || !it.parentElement || !it.parentElement.matches('details[data-group]')) return;
  _treeDrag = it; _groupDrag = null;
  it.style.opacity = '0.4';
  if (e.dataTransfer) { e.dataTransfer.effectAllowed = 'move'; try { e.dataTransfer.setData('text/plain', it.dataset.id || ''); } catch (_) {} }
});
// Сохраняем порядок в dragend, а не в drop: dragend срабатывает всегда после
// перетаскивания (даже если отпустили мимо валидной цели/в пустой области), —
// это устраняет случай, когда «перетащил в конец, но не сохранилось».
document.addEventListener('dragend', function() {
  if (_treeDrag) {
    var parent = _treeDrag.parentElement;
    if (parent && parent.matches('details[data-group]')) {
      var names = [];
      parent.querySelectorAll(':scope > .cfg-item').forEach(function(it) {
        names.push((it.dataset.id || '').replace(/^[a-z]+-/, ''));
      });
      cfgSaveOrder(parent.dataset.group, names);
    }
    _treeDrag.style.opacity = ''; _treeDrag = null;
  }
  if (_groupDrag) {
    var sb = _groupDrag.parentElement;
    if (sb) {
      var ids = [];
      sb.querySelectorAll(':scope > details.cfg-tree').forEach(function(d) { var id = cfgGid(d); if (id) ids.push(id); });
      cfgSaveOrder('groups', ids);
    }
    _groupDrag.style.opacity = ''; _groupDrag = null;
  }
});
document.addEventListener('dragover', function(e) {
  if (_groupDrag) {
    var os = e.target.closest ? e.target.closest('summary.cfg-group-hd') : null;
    var det = os ? os.parentElement : null;
    if (!det || det === _groupDrag || !det.matches('details.cfg-tree') || det.parentElement !== _groupDrag.parentElement) return;
    e.preventDefault();
    if (e.dataTransfer) e.dataTransfer.dropEffect = 'move';
    var rg = det.getBoundingClientRect();
    var beforeG = (e.clientY - rg.top) < rg.height / 2;
    det.parentElement.insertBefore(_groupDrag, beforeG ? det : det.nextSibling);
    return;
  }
  if (!_treeDrag) return;
  var det = _treeDrag.parentElement;
  var it = e.target.closest ? e.target.closest('.cfg-item') : null;
  if (it && it !== _treeDrag && it.parentElement === det) {
    e.preventDefault();
    if (e.dataTransfer) e.dataTransfer.dropEffect = 'move';
    var r = it.getBoundingClientRect();
    var before = (e.clientY - r.top) < r.height / 2;
    det.insertBefore(_treeDrag, before ? it : it.nextSibling);
    return;
  }
  // Над пустой областью группы (ниже последнего элемента) — разрешаем дроп и
  // двигаем элемент в конец, иначе перемещение «на последнее место» не работает:
  // без preventDefault здесь событие drop не сработало бы.
  if (e.target.closest && e.target.closest('summary')) return; // над заголовком — игнор
  var inDet = e.target.closest ? e.target.closest('details[data-group]') : null;
  if (inDet === det) {
    e.preventDefault();
    if (e.dataTransfer) e.dataTransfer.dropEffect = 'move';
    if (det.lastElementChild !== _treeDrag) det.appendChild(_treeDrag);
  }
});
// drop только подавляет действие браузера по умолчанию; само сохранение — в
// dragend (срабатывает надёжнее). Так избегаем и двойного POST.
document.addEventListener('drop', function(e) {
  if (_treeDrag || _groupDrag) e.preventDefault();
});
function cfgSaveOrder(group, names) {
  var fd = new FormData();
  fd.append('group', group);
  names.forEach(function(n) { fd.append('name', n); });
  fetch('/bases/' + _dbgBase + '/configurator/reorder', { method: 'POST', headers: { 'X-Onebase-Ajax': '1' }, body: fd })
    .then(function(r){ return r.json().catch(function(){ return { ok:false }; }); })
    .then(function(d){ if (!d || !d.ok) cfgToast((d && d.error) || 'Не удалось сохранить порядок', true); else cfgToast('Порядок сохранён', false); })
    .catch(function(err){ cfgToast('Ошибка: ' + err.message, true); });
}
function initTree() { applyGroupOrder(); initTreeDnd(); }
initTree();
document.addEventListener('DOMContentLoaded', initTree);

// ── Поиск/фильтр по дереву метаданных (как в 1С) ───────────────────────────
function filterTree(q) {
  q = (q || '').trim().toLowerCase();
  var sidebar = document.getElementById('cfg-sidebar');
  if (!sidebar) return;
  sidebar.querySelectorAll('.cfg-item').forEach(function(it) {
    var txt = (it.textContent || '').toLowerCase();
    it.style.display = (!q || txt.indexOf(q) >= 0) ? '' : 'none';
  });
  sidebar.querySelectorAll('details.cfg-tree').forEach(function(d) {
    var items = d.querySelectorAll('.cfg-item');
    var visible = 0;
    items.forEach(function(it){ if (it.style.display !== 'none') visible++; });
    d.style.display = (!q || visible > 0) ? '' : 'none';
    if (q && visible > 0) d.open = true;
  });
}
document.addEventListener('keydown', function(e) {
  if ((e.ctrlKey || e.metaKey) && (e.key === 's' || e.key === 'S' || e.key === 'ы' || e.key === 'Ы')) {
    e.preventDefault(); cfgSaveActive(); return;
  }
  if ((e.ctrlKey || e.metaKey) && (e.key === 'f' || e.key === 'F' || e.key === 'а' || e.key === 'А')) {
    var s = document.getElementById('cfg-tree-search');
    if (s) { e.preventDefault(); s.focus(); s.select(); }
  } else if (e.key === 'Escape') {
    var s2 = document.getElementById('cfg-tree-search');
    if (s2 && document.activeElement === s2 && s2.value) { s2.value = ''; filterTree(''); }
  }
});
(function(){
  var directId='{{.SelectedTreeID}}';
  var saved='{{.FieldsSavedEntity}}'?'{{.FieldsSavedEntity}}':'{{.ModuleSavedEntity}}';
  var el=null;
  if(directId)el=document.querySelector('[data-id="'+directId+'"]');
  if(!el&&saved&&saved!=='')['e-','r-','ir-','ar-','en-','cn-','rep-','mod-','proc-','pf-','dpf-','mkt-','sub-','panel-app'].forEach(function(p){if(!el)el=document.querySelector('[data-id="'+p+saved+'"]');});
  if(el)selItem(el);else{var f=document.querySelector('.cfg-item');if(f)selItem(f);}
})();

// ── Module tabs ────────────────────────────────────────────────
function modTab(el, panelId) {
  var wrap = el.closest('.module-editor-wrap');
  wrap.querySelectorAll('.module-tab').forEach(function(t){t.classList.remove('active')});
  wrap.querySelectorAll('.module-pane').forEach(function(p){p.classList.remove('active')});
  el.classList.add('active');
  document.getElementById(panelId).classList.add('active');
}

// ── Layout Editor ─────────────────────────────────────────────────
var _led={};
function initLayoutEditor(n){
  var ta=document.getElementById('ta-mkt-'+n);
  var ved=document.getElementById('veditor-'+n);
  if(!ta){if(ved)ved.innerHTML='<p style="color:red">ta not found: '+n+'</p>';return;}
  if(!window.jsyaml){if(ved)ved.innerHTML='<p style="color:red">js-yaml not loaded! [v5]</p>';return;}
  var d=null;
  try{d=jsyaml.load(ta.value);}catch(e){}
  if(!d)d={areas:{}};
  _led[n]={data:d,sel:null,init:true};
  if(Object.keys(d.areas||{}).length>0){renderLayoutEditor(n);}
}
function _ldCellStyle(c,extra){
  var st='padding:4px 8px;min-width:40px;';
  // border
  var bc=c.borderColor||'#999';
  var b=c.border||'';
  if(b==='none')st+='border:none;';
  else if(b==='thick')st+='border:2px solid '+bc+';';
  else st+='border:1px solid '+bc+';';
  if(c.bold)st+='font-weight:bold;';
  if(c.italic)st+='font-style:italic;';
  if(c.fontSize)st+='font-size:'+c.fontSize+'pt;';
  if(c.fontFamily)st+='font-family:'+c.fontFamily+';';
  if(c.backColor)st+='background-color:'+c.backColor+';';
  if(c.textColor)st+='color:'+c.textColor+';';
  if(c.align)st+='text-align:'+c.align+';';
  if(c.valign==='middle')st+='vertical-align:middle;';
  else if(c.valign==='top')st+='vertical-align:top;';
  else if(c.valign==='bottom')st+='vertical-align:bottom;';
  return st+(extra||'');
}
function _ldColgroup(d){
  var cols=d.columns||[];
  if(!cols.length)return '';
  var h='<colgroup>';
  for(var i=0;i<cols.length;i++){
    h+=cols[i]&&cols[i].width?'<col style="width:'+esc(cols[i].width)+'">':'<col>';
  }
  return h+'</colgroup>';
}
// noYamlSync=true prevents overwriting textarea (used when only selection changes)
function renderLayoutEditor(n,noYamlSync){
  var s=_led[n];if(!s)return;
  if(!noYamlSync&&window.jsyaml&&!s.init){
    var y=jsyaml.dump(s.data,{lineWidth:-1,quotingType:'"'});
    var ta=document.getElementById('ta-mkt-'+n);
    if(ta)ta.value=y;
  }
  if(s.init)s.init=false;
  var d=s.data,areas=d.areas||{},h='<div style="font-family:Arial,sans-serif;font-size:12px">';
  var aNames=Object.keys(areas);
  var cg=_ldColgroup(d);
  for(var ai=0;ai<aNames.length;ai++){
    var an=aNames[ai],ar=areas[an];
    h+='<div style="margin-bottom:16px">';
    h+='<div style="display:flex;align-items:center;gap:6px;margin-bottom:4px">';
    h+='<span style="font-weight:bold;color:#4a9">'+esc(an)+'</span>';
    h+='<button type="button" style="font-size:10px;padding:1px 6px;border:1px solid #ccc;border-radius:3px;cursor:pointer" onclick="addLayoutRow(\''+n+"','"+esc(an)+"')\">+ строка</button>";
    h+='<button type="button" style="font-size:10px;padding:1px 6px;border:1px solid #fcc;border-radius:3px;cursor:pointer;color:#c33" onclick="delLayoutArea(\''+n+"','"+esc(an)+"')\">✕</button>";
    h+='</div>';
    h+='<table style="border-collapse:collapse">'+cg;
    var rows=ar.rows||[];
    for(var ri=0;ri<rows.length;ri++){
      var rowStyle=rows[ri].height?' style="height:'+esc(rows[ri].height)+'"':'';
      h+='<tr'+rowStyle+'>';
      var cells=rows[ri].cells||[];
      for(var ci=0;ci<cells.length;ci++){
        var c=cells[ci];
        var isSel=s.sel&&s.sel.area===an&&s.sel.row===ri&&s.sel.col===ci;
        var extra='cursor:pointer;';
        if(isSel)extra+='outline:2px solid #1a73e8;outline-offset:-2px;';
        var st=_ldCellStyle(c,extra);
        var at='';
        if(c.colspan&&c.colspan>1)at+=' colspan="'+c.colspan+'"';
        if(c.rowspan&&c.rowspan>1)at+=' rowspan="'+c.rowspan+'"';
        var txt=c.text?esc(c.text):'';
        if(c.parameter)txt='<span style="color:#888">['+esc(c.parameter)+']</span>';
        if(!txt)txt='&nbsp;';
        h+='<td style="'+st+'"'+at+' onclick="selectCell(\''+n+"','"+esc(an)+"',"+ri+','+ci+')">'+txt+'</td>';
      }
      h+='<td style="border:none;padding:2px"><button type="button" style="font-size:10px;color:#c33;border:none;cursor:pointer;background:transparent" onclick="delLayoutRow(\''+n+"','"+esc(an)+"',"+ri+')\">✕</button></td>';
      h+='</tr>';
    }
    h+='</table></div>';
  }
  h+='</div>';
  var ved=document.getElementById('veditor-'+n);
  if(ved)ved.innerHTML=h;
  renderPreviewOnly(n);
  syncProps(n);
}
function renderPreviewOnly(n){
  var pv=document.getElementById('vpreview-'+n);
  if(!pv)return;
  var s=_led[n];if(!s){pv.innerHTML='';return;}
  var d=s.data,areas=d.areas||{},h='<div style="font-family:Arial,sans-serif;font-size:12px">';
  var cg=_ldColgroup(d);
  var aNames=Object.keys(areas);
  for(var ai=0;ai<aNames.length;ai++){
    var an=aNames[ai],ar=areas[an];
    h+='<div style="margin-bottom:16px"><div style="font-weight:bold;color:#4a9;margin-bottom:4px">'+esc(an)+'</div>';
    h+='<table style="border-collapse:collapse">'+cg;
    var rows=ar.rows||[];
    for(var ri=0;ri<rows.length;ri++){
      var rowStyle=rows[ri].height?' style="height:'+esc(rows[ri].height)+'"':'';
      h+='<tr'+rowStyle+'>';
      var cells=rows[ri].cells||[];
      for(var ci=0;ci<cells.length;ci++){
        var c=cells[ci];
        var st=_ldCellStyle(c,'');
        var at='';
        if(c.colspan&&c.colspan>1)at+=' colspan="'+c.colspan+'"';
        if(c.rowspan&&c.rowspan>1)at+=' rowspan="'+c.rowspan+'"';
        var txt=c.text?esc(c.text):'';
        if(c.parameter)txt='<span style="color:#888">['+esc(c.parameter)+']</span>';
        if(!txt)txt='&nbsp;';
        h+='<td style="'+st+'"'+at+'>'+txt+'</td>';
      }
      h+='</tr>';
    }
    h+='</table></div>';
  }
  h+='</div>';
  pv.innerHTML=h;
}
function selectCell(n,a,r,c){
  var s=_led[n];if(!s)return;
  s.sel={area:a,row:r,col:c};
  renderLayoutEditor(n,true); // don't reformat YAML on cell selection
}
function _setVal(id,v){var el=document.getElementById(id);if(el)el.value=v;}
function _setChk(id,v){var el=document.getElementById(id);if(el)el.checked=v;}
function syncProps(n){
  var s=_led[n];if(!s)return;
  var pp=document.getElementById('vprops-'+n);
  if(!pp)return;
  if(!s.sel){pp.style.display='none';return;}
  var d=s.data,ar=(d.areas||{})[s.sel.area];
  if(!ar||!ar.rows||!ar.rows[s.sel.row]){pp.style.display='none';return;}
  var c=ar.rows[s.sel.row].cells[s.sel.col]||{};
  pp.style.display='block';
  pp.scrollIntoView({behavior:'smooth',block:'nearest'});
  _setVal('vp-text-'+n,c.text||'');
  _setVal('vp-param-'+n,c.parameter||'');
  _setChk('vp-bold-'+n,!!c.bold);
  _setChk('vp-italic-'+n,!!c.italic);
  _setVal('vp-align-'+n,c.align||'');
  _setVal('vp-valign-'+n,c.valign||'');
  _setVal('vp-bg-'+n,c.backColor||'#ffffff');
  _setVal('vp-fg-'+n,c.textColor||'#000000');
  _setVal('vp-ff-'+n,c.fontFamily||'');
  _setVal('vp-fs-'+n,c.fontSize||'');
  _setVal('vp-border-'+n,c.border||'');
  _setVal('vp-bc-'+n,c.borderColor||'#cccccc');
  _setVal('vp-colspan-'+n,c.colspan||1);
  _setVal('vp-rowspan-'+n,c.rowspan||1);
}
function updateCellProp(n,prop,val){
  var s=_led[n];if(!s||!s.sel)return;
  var d=s.data,ar=(d.areas||{})[s.sel.area];
  if(!ar||!ar.rows||!ar.rows[s.sel.row])return;
  var ci=s.sel.col;
  if(!ar.rows[s.sel.row].cells[ci])ar.rows[s.sel.row].cells[ci]={};
  var c=ar.rows[s.sel.row].cells[ci];
  var isSpan=(prop==='colspan'||prop==='rowspan');
  if(val===''||val===0||val===false||(typeof val==='number'&&isNaN(val))||(isSpan&&val<=1)){
    delete c[prop];
  }else{
    c[prop]=val;
  }
  renderLayoutEditor(n);
}
function applyYaml(n){
  var ta=document.getElementById('ta-mkt-'+n);
  if(!ta)return;
  var d=null;
  try{d=jsyaml.load(ta.value);}catch(e){return;}
  if(!d||typeof d!=='object')return; // invalid YAML — keep current state
  if(!d.areas)d.areas={};
  if(!_led[n])_led[n]={data:d,sel:null};
  else _led[n].data=d;
  // re-render designer without syncing YAML back (avoid cursor jump)
  _led[n].init=true;
  renderLayoutEditor(n);
}
// Debounced YAML sync — fires while user is typing in the textarea
var _yamlTimers={};
function scheduleYamlSync(n){
  if(_yamlTimers[n])clearTimeout(_yamlTimers[n]);
  _yamlTimers[n]=setTimeout(function(){applyYaml(n);},400);
}
function saveLayoutEditor(n){
  // Flush debounced YAML input timer first.
  if(_yamlTimers[n]){clearTimeout(_yamlTimers[n]);delete _yamlTimers[n];}
  // Sync in-memory state → textarea (in case visual editor made changes without syncing).
  if(window.jsyaml&&_led[n]){
    var y=jsyaml.dump(_led[n].data,{lineWidth:-1,quotingType:'"'});
    var ta=document.getElementById('ta-mkt-'+n);
    if(ta)ta.value=y;
  }
  return true;
}
function addLayoutArea(n){
  var name=prompt('Имя новой области:');
  if(!name)return;
  var s=_led[n];
  if(!s){
    // init if not yet initialized
    var ta=document.getElementById('ta-mkt-'+n);
    if(!ta||!window.jsyaml)return;
    var d=null;try{d=jsyaml.load(ta.value);}catch(e){}
    if(!d)d={areas:{}};
    s={data:d,sel:null};_led[n]=s;
  }
  if(!s.data.areas)s.data.areas={};
  s.data.areas[name]={rows:[{cells:[{text:'Ячейка'}]}]};
  renderLayoutEditor(n);
}
function delLayoutArea(n,a){
  if(!confirm('Удалить область '+a+'?'))return;
  var s=_led[n];if(!s)return;
  delete s.data.areas[a];
  s.sel=null;
  renderLayoutEditor(n);
}
function addLayoutRow(n,a){
  var s=_led[n];if(!s)return;
  var ar=(s.data.areas||{})[a];if(!ar)return;
  if(!ar.rows)ar.rows=[];
  var maxCols=1;
  for(var i=0;i<ar.rows.length;i++){if(ar.rows[i].cells.length>maxCols)maxCols=ar.rows[i].cells.length;}
  var cells=[];for(var j=0;j<maxCols;j++)cells.push({});
  ar.rows.push({cells:cells});
  renderLayoutEditor(n);
}
function delLayoutRow(n,a,ri){
  var s=_led[n];if(!s)return;
  var ar=(s.data.areas||{})[a];if(!ar||!ar.rows)return;
  ar.rows.splice(ri,1);
  s.sel=null;
  renderLayoutEditor(n);
}
// ── Toolbar operations ─────────────────────────────────────────────
// ldSelectTab kept for backward compat; split-view has no tabs.
function _ldSel(n){
  var s=_led[n];if(!s||!s.sel)return null;
  var ar=(s.data.areas||{})[s.sel.area];
  if(!ar||!ar.rows||!ar.rows[s.sel.row])return null;
  return {s:s,ar:ar,row:ar.rows[s.sel.row],ri:s.sel.row,ci:s.sel.col,area:s.sel.area};
}
function _ldFirstArea(n){
  var s=_led[n];if(!s)return null;
  var keys=Object.keys(s.data.areas||{});
  return keys.length?keys[0]:null;
}
function ldAddRow(n){
  var s=_led[n];if(!s)return;
  var area=s.sel?s.sel.area:_ldFirstArea(n);
  if(!area){alert('Сначала добавьте область');return;}
  addLayoutRow(n,area);
}
function ldDelRow(n){
  var sel=_ldSel(n);
  if(!sel){alert('Выделите ячейку в строке, которую нужно удалить');return;}
  if(!confirm('Удалить строку?'))return;
  delLayoutRow(n,sel.area,sel.ri);
}
function ldAddColumn(n){
  var s=_led[n];if(!s)return;
  var area=s.sel?s.sel.area:_ldFirstArea(n);
  if(!area){alert('Сначала добавьте область');return;}
  var ar=s.data.areas[area];if(!ar||!ar.rows)return;
  for(var i=0;i<ar.rows.length;i++){
    if(!ar.rows[i].cells)ar.rows[i].cells=[];
    ar.rows[i].cells.push({});
  }
  // also extend columns array (column-level widths) if defined
  if(s.data.columns){s.data.columns.push({});}
  renderLayoutEditor(n);
}
function ldDelColumn(n){
  var sel=_ldSel(n);
  if(!sel){alert('Выделите ячейку в колонке, которую нужно удалить');return;}
  if(!confirm('Удалить колонку?'))return;
  var s=sel.s,ar=sel.ar,ci=sel.ci;
  for(var i=0;i<ar.rows.length;i++){
    var cs=ar.rows[i].cells||[];
    if(ci<cs.length)cs.splice(ci,1);
  }
  if(s.data.columns&&ci<s.data.columns.length){s.data.columns.splice(ci,1);}
  s.sel=null;
  renderLayoutEditor(n);
}
function ldMerge(n){
  var sel=_ldSel(n);
  if(!sel){alert('Выделите ячейку, которую нужно объединить с правой соседкой');return;}
  var row=sel.row,ci=sel.ci;
  if(ci+1>=row.cells.length){alert('Нет ячейки справа для объединения');return;}
  var c=row.cells[ci];
  var span=(c.colspan&&c.colspan>1)?c.colspan:1;
  c.colspan=span+1;
  row.cells.splice(ci+1,1);
  renderLayoutEditor(n);
}
function ldSplit(n){
  var sel=_ldSel(n);
  if(!sel){alert('Выделите объединённую ячейку');return;}
  var c=sel.row.cells[sel.ci];
  if(!c.colspan||c.colspan<=1){alert('Ячейка не объединена');return;}
  var span=c.colspan;
  delete c.colspan;
  // insert (span-1) empty cells to the right
  for(var i=0;i<span-1;i++){
    sel.row.cells.splice(sel.ci+1+i,0,{});
  }
  renderLayoutEditor(n);
}
// Init layout editors on load
function initAllLayoutEditors(){
  console.log('[layout] initAllLayoutEditors called, jsyaml=', !!window.jsyaml, 'found', document.querySelectorAll('[id^="ta-mkt-"]').length, 'editors');
  var tas=document.querySelectorAll('[id^="ta-mkt-"]');
  for(var i=0;i<tas.length;i++){
    var n=tas[i].id.replace('ta-mkt-','');
    (function(nn){initLayoutEditor(nn);})(n);
  }
}
// js-yaml is embedded inline so always ready; wait for DOM
if(document.readyState==='loading'){
  document.addEventListener('DOMContentLoaded',initAllLayoutEditors);
}else{
  initAllLayoutEditors();
}

// ── HTML escape (shared) ────────────────────────────────────────
function esc(s){return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;');}

// ── Syntax highlight ───────────────────────────────────────────
(function(){
var KW=['Процедура','КонецПроцедуры','Функция','КонецФункции',
  'Если','Тогда','ИначеЕсли','Иначе','КонецЕсли',
  'Для','Каждого','Из','Цикл','КонецЦикла','Пока','КонецПока',
  'Возврат','Прервать','Продолжить','Истина','Ложь','Неопределено','Новый',
  'И','ИЛИ','НЕ','Не',
  'Procedure','EndProcedure','Function','EndFunction',
  'If','Then','ElseIf','Else','EndIf',
  'For','Each','In','Do','EndDo','While','EndWhile',
  'Return','Break','Continue','True','False','Undefined','New',
  'And','Or','Not','Var'];
var FN=['Error','Ошибка','Сообщить','Формат','ФорматСтроки','СтрЗаменить'];
var SP=['this','Движения','Параметры'];

function hl(code){
  var r='',i=0,n=code.length;
  while(i<n){
    if(code[i]==='/' && code[i+1]==='/'){
      var e=code.indexOf('\n',i);if(e<0)e=n;
      r+='<span class="hl-cmt">'+esc(code.slice(i,e))+'</span>';i=e;continue;
    }
    if(code[i]==='"'){
      var j=i+1;while(j<n && code[j]!=='"')j++;
      r+='<span class="hl-str">'+esc(code.slice(i,j+1))+'</span>';i=j+1;continue;
    }
    if(/[0-9]/.test(code[i])){
      var j=i;while(j<n && /[0-9.]/.test(code[j]))j++;
      r+='<span class="hl-num">'+esc(code.slice(i,j))+'</span>';i=j;continue;
    }
    if(/[а-яёА-ЯЁa-zA-Z_]/.test(code[i])){
      var j=i;while(j<n && /[а-яёА-ЯЁa-zA-Z0-9_]/.test(code[j]))j++;
      var w=code.slice(i,j);
      if(KW.indexOf(w)>=0)r+='<span class="hl-kw">'+esc(w)+'</span>';
      else if(FN.indexOf(w)>=0)r+='<span class="hl-fn">'+esc(w)+'</span>';
      else if(SP.indexOf(w)>=0)r+='<span class="hl-sp">'+esc(w)+'</span>';
      else r+=esc(w);
      i=j;continue;
    }
    r+=esc(code[i]);i++;
  }
  return r;
}
document.querySelectorAll('pre.os-code').forEach(function(el){
  el.innerHTML=hl(el.textContent);
});
})();

// ── Monaco Editor initialization ────────────────────────────────
(function(){
if (typeof require === 'undefined') { window._monacoReady = false; document.getElementById('monaco-status').textContent='Monaco:FAIL(no require)'; return; }
require.config({ paths: { 'vs': '/vendor/monaco/vs' }});
require(['vs/editor/editor.main'], function() {
  // Register OneBase DSL language
  monaco.languages.register({ id: 'onebase-dsl' });
  monaco.languages.setMonarchTokensProvider('onebase-dsl', {
    keywords: [
      'Процедура','КонецПроцедуры','Функция','КонецФункции',
      'Если','Тогда','ИначеЕсли','Иначе','КонецЕсли',
      'Для','Каждого','Из','Цикл','КонецЦикла','Пока','КонецПока',
      'Возврат','Прервать','Продолжить','Истина','Ложь','Неопределено','Новый',
      'И','ИЛИ','НЕ','Не','Перем',
      'Procedure','EndProcedure','Function','EndFunction',
      'If','Then','ElseIf','Else','EndIf',
      'For','Each','In','Do','EndDo','While','EndWhile',
      'Return','Break','Continue','True','False','Undefined','New',
      'And','Or','Not','Var'
    ],
    builtins: [
      'Error','Ошибка','Сообщить','Формат','ФорматСтроки','СтрЗаменить',
      'Запрос','Результат','Выполнить','УстановитьПараметр','Текст',
      'ВЫБРАТЬ','ИЗ','ГДЕ','УПОРЯДОЧИТЬ','ПО','СГРУППИРОВАТЬ',
      'ЛЕВОЕ','ПРАВОЕ','ВНУТРЕННЕЕ','ПОЛНОЕ','СОЕДИНЕНИЕ',
      'КАК','ВОЗР','УБЫВ','СУММА','КОЛИЧЕСТВО','МИНИМУМ','МАКСИМУМ','СРЕДНЕЕ'
    ],
    special: ['this','ЭтотОбъект','Движения','Параметры'],
    tokenizer: {
      root: [
        [/#.*$/, 'comment'],
        ["\/\/.*$", 'comment'],
        [/"/, 'string', '@string'],
        [/\d+(\.\d+)?/, 'number'],
        [/[a-zA-Z_А-яЁё][a-zA-Z0-9_А-яЁё]*/, {
          cases: {
            '@keywords': 'keyword',
            '@builtins': 'type',
            '@special': 'variable.predefined',
            '@default': 'identifier'
          }
        }]
      ],
      string: [
        [/[^"]+/, 'string'],
        [/"/, 'string', '@pop']
      ]
    }
  });
  // Auto-completion
  monaco.languages.registerCompletionItemProvider('onebase-dsl', {
    provideCompletionItems: function(model, position) {
      var word = model.getWordUntilPosition(position);
      var range = { startLineNumber: position.lineNumber, endLineNumber: position.lineNumber, startColumn: word.startColumn, endColumn: word.endColumn };
      var kwSuggestions = [
        'Процедура','КонецПроцедуры','Функция','КонецФункции',
        'Если','Тогда','ИначеЕсли','Иначе','КонецЕсли',
        'Для','Каждого','Из','Цикл','КонецЦикла','Пока','КонецПока',
        'Возврат','Новый','Истина','Ложь','Неопределено',
        'Procedure','EndProcedure','Function','EndFunction'
      ].map(function(k) {
        return { label: k, kind: monaco.languages.CompletionItemKind.Keyword, insertText: k, range: range };
      });
      return { suggestions: kwSuggestions };
    }
  });
  // Dark theme matching existing #1e1e2e style
  monaco.editor.defineTheme('onebase-dark', {
    base: 'vs-dark',
    inherit: true,
    rules: [
      { token: 'keyword', foreground: 'c792ea', fontStyle: 'bold' },
      { token: 'type', foreground: '82aaff' },
      { token: 'variable.predefined', foreground: 'ff5370', fontStyle: 'bold' },
      { token: 'string', foreground: 'c3e88d' },
      { token: 'number', foreground: 'f78c6c' },
      { token: 'comment', foreground: '546e7a', fontStyle: 'italic' }
    ],
    colors: {
      'editor.background': '#1e1e2e',
      'editor.foreground': '#cdd6f4',
      'editor.lineHighlightBackground': '#2a2a3e',
      'editorLineNumber.foreground': '#6c7086',
      'editorLineNumber.activeForeground': '#cdd6f4',
      'editor.selectionBackground': '#45475a',
      'editorCursor.foreground': '#f5e0dc'
    }
  });
  window._monacoReady = true;

  // Auto-open Monaco for visible code blocks
  document.querySelectorAll('pre.os-code').forEach(function(pre) {
    var name = pre.id.replace('pre-','');
    if (name) startEdit(name);
  });
  var ms = document.getElementById('monaco-status');
  if (ms) { ms.textContent = 'Monaco:OK(' + Object.keys(monacoEditors).length + ' ed)'; ms.style.color = '#16a34a'; }
});
})();

// ── Form submit: sync Monaco -> textarea ─────────────────────────
document.querySelectorAll('form').forEach(function(form) {
  form.addEventListener('submit', function() {
    form.querySelectorAll('.code-wrap').forEach(function(wrap) {
      var editorDiv = wrap.querySelector('.monaco-target');
      if (editorDiv && editorDiv._editorId && monacoEditors[editorDiv._editorId]) {
        var ta = wrap.querySelector('textarea.os-edit');
        if (ta) ta.value = monacoEditors[editorDiv._editorId].getValue();
      }
    });
  });
});

// ── Report params ──────────────────────────────────────────────
function repReindex(tableId) {
  var tbl = document.getElementById(tableId);
  if (!tbl) return;
  var rows = tbl.querySelectorAll('tbody tr, tr:not(:first-child)');
  // skip header row (first tr), iterate data rows
  var dataRows = Array.from(tbl.querySelectorAll('tr')).filter(function(r){ return r.querySelector('input[type=text]'); });
  dataRows.forEach(function(tr, i) {
    tr.querySelectorAll('input,select').forEach(function(el) {
      el.name = el.name.replace(/param\.\d+\./, 'param.' + i + '.');
    });
    var btn = tr.querySelector('button[type=button]');
    if (btn) btn.setAttribute('onclick', 'this.closest(\'tr\').remove();repReindex(\'' + tableId + '\')');
  });
}
function repAddParam(tableId) {
  var tbl = document.getElementById(tableId);
  if (!tbl) return;
  var dataRows = Array.from(tbl.querySelectorAll('tr')).filter(function(r){ return r.querySelector('input[type=text]'); });
  var i = dataRows.length;
  var tr = document.createElement('tr');
  tr.innerHTML = '<td><input type="text" name="param.' + i + '.name" value="" style="width:100%;padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px" placeholder="ИмяПараметра"></td>'
    + '<td><select name="param.' + i + '.type" style="padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px">'
    + '<option value="string">{{t $.Lang "строка"}}</option><option value="date">{{t $.Lang "дата"}}</option><option value="number">{{t $.Lang "число"}}</option><option value="select">{{t $.Lang "список"}}</option>'
    {{range $.AllEntityNames}}+'<option value="reference:{{.}}">ссылка: {{.}}</option>'{{end}}
    + '</select></td>'
    + '<td><input type="text" name="param.' + i + '.label" value="" style="width:100%;padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px" placeholder="Заголовок"></td>'
    + '<td><button type="button" style="background:none;border:none;color:#c00;cursor:pointer;font-size:14px" onclick="this.closest(\'tr\').remove();repReindex(\'' + tableId + '\')">✕</button></td>';
  tbl.appendChild(tr);
  tr.querySelector('input[type=text]').focus();
}

// ── Editor context menu — наше меню для textarea, pre и Monaco ──
// Monaco создан с contextmenu:false, поэтому его внутренний обработчик не глотает событие.
(function(){
var _cTA=null,_cMonacoName=null,_cM=null,_cSelText='';
document.addEventListener('contextmenu',function(e){
  var ta=e.target.closest('.os-edit');
  var pre=e.target.closest('pre.os-code');
  var mon=e.target.closest('.monaco-target');
  if(!ta&&!pre&&!mon){hideC();return;}
  e.preventDefault();
  _cMonacoName=null; _cTA=null; _cSelText='';
  // Выделение фиксируем СРАЗУ, в момент ПКМ: Monaco сбрасывает его на
  // mousedown, а startEdit переключает pre→textarea (выделение было в pre).
  if(mon){
    _cMonacoName=mon._editorId||null;
    var ed=_cMonacoName?monacoEditors[_cMonacoName]:null;
    if(ed){
      var mt=ed.getModel().getValueInRange(ed.getSelection());
      _cSelText=(mt&&mt.trim())?mt:(ed._lastSelText||'');
    }
  } else if(pre&&!ta){
    // выделение сейчас в <pre> — забираем DOM-selection ДО startEdit
    _cSelText=(window.getSelection?String(window.getSelection()):'')||'';
    var nm=pre.id.replace('pre-','');startEdit(nm);ta=document.getElementById('ta-'+nm);
    _cTA=ta;
  } else {
    _cTA=ta;
    if(ta)_cSelText=ta.value.substring(ta.selectionStart,ta.selectionEnd);
  }
  showC(e.clientX,e.clientY);
});
function showC(x,y){
  if(!_cM){_cM=document.createElement('div');_cM.className='cfg-ctx-menu';
    _cM.innerHTML='<div class="cfg-ctx-item" onclick="cfgOpenQB()">🔍 Конструктор запроса</div>';
    document.body.appendChild(_cM);}
  _cM.style.display='block';
  _cM.style.left=Math.min(x,window.innerWidth-240)+'px';
  _cM.style.top=Math.min(y,window.innerHeight-50)+'px';
}
function hideC(){if(_cM)_cM.style.display='none';}
document.addEventListener('click',hideC);
window.cfgOpenQB=function(){
  hideC();
  if(_cMonacoName && monacoEditors[_cMonacoName]) openQBModalMonaco(_cMonacoName,_cSelText);
  else if(_cTA) openQBModal(_cTA,_cSelText);
};
})();

// ── Inline query builder ──────────────────────────────────
var _mqbTA=null,_mqbMonacoId=null,_mqbSchema=null,_mqbSrcMap={};
var _mqbCurFields=[],_mqbSel={},_mqbJoins=[];
(function(){
_mqbSchema={{.QBSchema}};
if(!_mqbSchema)_mqbSchema=[];
_mqbSchema.forEach(function(s){_mqbSrcMap[s.id]=s;});
// populate select
var sel=document.getElementById('mqb-src');
var groups={};
_mqbSchema.forEach(function(s){if(!groups[s.group])groups[s.group]=[];groups[s.group].push(s);});
Object.keys(groups).forEach(function(g){
  var og=document.createElement('optgroup');og.label=g;
  groups[g].forEach(function(s){var o=document.createElement('option');o.value=s.id;o.textContent=s.label;og.appendChild(o);});
  sel.appendChild(og);
});
document.getElementById('qb-close').onclick=function(){document.getElementById('qb-overlay').classList.remove('active');};
document.getElementById('qb-insert').onclick=function(){
  var mode=document.getElementById('qb-mode').value;
  var txt=mode==='query'?document.getElementById('mqb-qry').value:document.getElementById('mqb-dsl').value;
  if(!txt)return;
  if(_mqbMonacoId && monacoEditors[_mqbMonacoId]){
    var editor=monacoEditors[_mqbMonacoId];
    var sel=editor.getSelection();
    editor.executeEdits('qb',[{range:sel,text:txt}]);
    _mqbMonacoId=null;
  } else if(_mqbTA){
    var ta=_mqbTA,s=ta.selectionStart,en=ta.selectionEnd;
    ta.value=ta.value.substring(0,s)+txt+ta.value.substring(en);
    var nm=ta.id.replace('ta-',''),pre=document.getElementById('pre-'+nm);
    if(pre)pre.innerHTML=hl(ta.value);
    ta.selectionStart=ta.selectionEnd=s+txt.length;
    ta.focus();
  }
  _mqbTA=null;
  document.getElementById('qb-overlay').classList.remove('active');
};
// close on overlay click
document.getElementById('qb-overlay').addEventListener('click',function(e){if(e.target===this)this.classList.remove('active');});
})();

function openQBModal(ta,presetSel){
  _mqbTA=ta;
  // reset state
  _mqbSel={};_mqbJoins=[];_mqbCurFields=[];
  document.getElementById('mqb-src').value='';
  document.getElementById('mqb-alias').value='';
  document.getElementById('mqb-vtp').style.display='none';
  document.getElementById('mqb-joins').innerHTML='<p style="font-size:12px;color:#94a3b8;margin:0" id="mqb-joins-hint">{{t $.Lang "Нет"}}</p>';
  document.getElementById('mqb-conds').innerHTML='';
  document.getElementById('mqb-ords').innerHTML='';
  document.getElementById('mqb-fields').innerHTML='';
  document.getElementById('mqb-dsl').value='';
  document.getElementById('mqb-qry').value='';
  // parse selected text — приоритет у выделения, снятого в момент ПКМ
  var sel=(presetSel!=null&&String(presetSel).trim())
    ?String(presetSel).trim()
    :ta.value.substring(ta.selectionStart,ta.selectionEnd).trim();
  if(sel){
    var q=qbExtractQuery(sel);
    if(q){document.getElementById('mqb-qry').value=q;qbParseToFields(q);}
  }
  document.getElementById('qb-overlay').classList.add('active');
}
function openQBModalMonaco(editorId,presetSel){
  var editor = monacoEditors[editorId];
  if (!editor) return;
  _mqbTA = null;
  _mqbMonacoId = editorId;
  _mqbSel={};_mqbJoins=[];_mqbCurFields=[];
  document.getElementById('mqb-src').value='';
  document.getElementById('mqb-alias').value='';
  document.getElementById('mqb-vtp').style.display='none';
  document.getElementById('mqb-joins').innerHTML='<p style="font-size:12px;color:#94a3b8;margin:0" id="mqb-joins-hint">{{t $.Lang "Нет"}}</p>';
  document.getElementById('mqb-conds').innerHTML='';
  document.getElementById('mqb-ords').innerHTML='';
  document.getElementById('mqb-fields').innerHTML='';
  document.getElementById('mqb-dsl').value='';
  document.getElementById('mqb-qry').value='';
  // Приоритет — выделение, снятое в момент ПКМ; затем текущее; затем
  // последнее запомненное (правый клик мог сбросить selection).
  var sel = (presetSel!=null&&String(presetSel).trim())?String(presetSel).trim():'';
  if(!sel) sel = editor.getModel().getValueInRange(editor.getSelection()).trim();
  if(!sel && editor._lastSelText) sel = editor._lastSelText.trim();
  if(sel){
    var q=qbExtractQuery(sel);
    if(q){document.getElementById('mqb-qry').value=q;qbParseToFields(q);}
  }
  document.getElementById('qb-overlay').classList.add('active');
}
function qbExtractQuery(text){
  // 1С-style: Запрос.Текст = "ВЫБРАТЬ ... |..."
  var m=text.match(/Текст\s*=\s*\n?\s*"([\s\S]*?)"/i);
  if(m)return m[1].replace(/\n\s*\|/g,'\n').trim();
  // прямой текст запроса. \b в JS не учитывает кириллицу (\w = [A-Za-z0-9_]),
  // поэтому границу слова проверяем явным символьным классом.
  var sm=text.match(/(^|[^A-Za-zА-Яа-яЁё0-9_])ВЫБРАТЬ(?=[^A-Za-zА-Яа-яЁё0-9_]|$)/i);
  if(sm){
    var i=sm.index+sm[1].length;
    return text.substring(i).replace(/\n\s*\|/g,'\n').replace(/^"|"$/g,'').trim();
  }
  return null;
}

// Ищет в _mqbSchema источник по совпадению label-prefix (до '('), регистронезависимо.
function qbFindSource(name){
  var key=String(name||'').split('(')[0].trim().toLowerCase();
  var found=null;
  _mqbSchema.forEach(function(s){
    if(s.label.split('(')[0].toLowerCase()===key)found=s;
  });
  return found;
}

// Разбивает строку по разделителю, игнорируя содержимое скобок.
// \w в JS НЕ включает кириллицу — поэтому проверка границ слов сделана через qbIsWord.
function qbIsWord(ch){return /[A-Za-zА-Яа-яЁё0-9_]/.test(ch);}
function qbSplitTopLevel(s,sep){
  var out=[],depth=0,buf='';
  var sepU=sep.toUpperCase();
  for(var i=0;i<s.length;i++){
    var ch=s[i];
    if(ch==='(')depth++;
    else if(ch===')')depth--;
    if(depth===0 && s.substring(i,i+sep.length).toUpperCase()===sepU &&
       (i===0||!qbIsWord(s[i-1])) && (i+sep.length===s.length||!qbIsWord(s[i+sep.length]))){
      out.push(buf); buf=''; i+=sep.length-1; continue;
    }
    buf+=ch;
  }
  if(buf.length)out.push(buf);
  return out;
}

function qbParseToFields(q){
  try { return qbParseToFieldsImpl(q); }
  catch(err){ if(window.console)console.warn('[QB] parse failed:',err,'\nquery:',q); }
}

function qbParseToFieldsImpl(q){
  q=q.replace(/\r/g,'').trim();

  // Разбить запрос на секции по ключевым словам верхнего уровня.
  var kwRe=/(^|[^\wА-Яа-яЁё])(ВЫБРАТЬ|ИЗ|ГДЕ|СГРУППИРОВАТЬ\s+ПО|УПОРЯДОЧИТЬ\s+ПО)(?=[^\wА-Яа-яЁё]|$)/gi;
  var marks=[],mk;
  while((mk=kwRe.exec(q))){
    marks.push({k:mk[2].toUpperCase().replace(/\s+/g,' '),i:mk.index+mk[1].length,l:mk[2].length});
  }
  function section(name){
    for(var i=0;i<marks.length;i++){
      if(marks[i].k===name){
        var st=marks[i].i+marks[i].l;
        var en=(i+1<marks.length)?marks[i+1].i:q.length;
        return q.substring(st,en).trim();
      }
    }
    return '';
  }
  var selSec=section('ВЫБРАТЬ');
  var fromSec=section('ИЗ');
  var whereSec=section('ГДЕ');
  var orderSec=section('УПОРЯДОЧИТЬ ПО');
  if(!fromSec){if(window.console)console.warn('[QB] no ИЗ section in:',q);return;}

  // FROM: <source>[(...)] [КАК <alias>] [JOIN ...]
  var joinRe=/(^|[^\wА-Яа-яЁё])(ЛЕВОЕ|ПРАВОЕ|ВНУТРЕННЕЕ|ПОЛНОЕ)\s+СОЕДИНЕНИЕ\b/i;
  var firstJ=fromSec.search(joinRe);
  var mainFrom=(firstJ>=0?fromSec.substring(0,firstJ):fromSec).trim();
  var rest=(firstJ>=0?fromSec.substring(firstJ):'').trim();
  var mm=mainFrom.match(/^(\S+(?:\([^)]*\))?)\s*(?:КАК\s+(\S+))?/i);
  if(!mm){if(window.console)console.warn('[QB] cannot parse FROM:',mainFrom);return;}
  var src=qbFindSource(mm[1]);
  if(!src){if(window.console)console.warn('[QB] unknown source:',mm[1]);return;}

  // 1) Выбираем источник (mqbSetSrc сбросит alias, vt и поля без префикса)
  document.getElementById('mqb-src').value=src.id;
  mqbSetSrc(src.id);
  // 2) Алиас и параметр виртуальной таблицы
  if(mm[2])document.getElementById('mqb-alias').value=mm[2];
  var vtm=mm[1].match(/\(([^)]*)\)/);
  if(vtm && src.vtParam){document.getElementById('mqb-vtpv').value=vtm[1];}

  // 3) JOINs — добавляем все, заполняем поля
  if(rest){
    var jRe=/(?:^|[^\wА-Яа-яЁё])(ЛЕВОЕ|ПРАВОЕ|ВНУТРЕННЕЕ|ПОЛНОЕ)\s+СОЕДИНЕНИЕ\s+(\S+(?:\([^)]*\))?)\s+КАК\s+(\S+)\s+ПО\s+([\s\S]*?)(?=(?:^|[^\wА-Яа-яЁё])(?:ЛЕВОЕ|ПРАВОЕ|ВНУТРЕННЕЕ|ПОЛНОЕ)\s+СОЕДИНЕНИЕ\b|$)/gi;
    var jm;
    while((jm=jRe.exec(rest))){
      mqbAddJoin();
      var lastJ=_mqbJoins[_mqbJoins.length-1];
      lastJ.typeSel.value=jm[1].toUpperCase();
      var jSrc=qbFindSource(jm[2]);
      if(jSrc)lastJ.srcSel.value=jSrc.id;
      lastJ.aliasInp.value=jm[3];
      lastJ.onInp.value=jm[4].trim();
    }
  }

  // 4) Пересобрать список полей с учётом алиаса и JOINов
  mqbRebuild();

  // 5) SELECT — заменить автозаполненный _mqbSel выделением из запроса
  if(selSec && selSec!=='*'){
    var newSel={};
    qbSplitTopLevel(selSec,',').forEach(function(fld){
      fld=fld.trim(); if(!fld||fld==='*')return;
      var agg='',alias='',name=fld;
      var aggM=fld.match(/^(СУММА|КОЛИЧЕСТВО|МИНИМУМ|МАКСИМУМ|СРЕДНЕЕ)\s*\(([\s\S]+)\)\s*(?:КАК\s+(\S+))?\s*$/i);
      if(aggM){agg=aggM[1].toUpperCase();name=aggM[2].trim();if(aggM[3])alias=aggM[3];}
      else{
        var aliasM=fld.match(/^([\s\S]+?)\s+КАК\s+(\S+)\s*$/i);
        if(aliasM){name=aliasM[1].trim();alias=aliasM[2];}
      }
      newSel[name]={alias:alias,agg:agg};
    });
    if(Object.keys(newSel).length){_mqbSel=newSel; mqbRenderFields();}
  }

  // 6) WHERE
  if(whereSec){
    qbSplitTopLevel(whereSec,'И').forEach(function(c){
      c=c.trim(); if(!c)return;
      mqbAddCond();
      var rows=document.querySelectorAll('#mqb-conds > div');
      var row=rows[rows.length-1];
      var sels=row.querySelectorAll('select');
      var inp=row.querySelector('input[type=text]');
      var em=c.match(/^([\s\S]+?)\s+(НЕ\s+ЕСТЬ\s+ПУСТО|ЕСТЬ\s+ПУСТО)\s*$/i);
      if(em){
        sels[0].value=em[1].trim();
        sels[1].value=em[2].toUpperCase().replace(/\s+/g,' ');
        inp.style.display='none';
        return;
      }
      var im=c.match(/^([\s\S]+?)\s+В\s*\(([\s\S]+)\)\s*$/i);
      if(im){sels[0].value=im[1].trim();sels[1].value='В';inp.value=im[2].trim();return;}
      var pm=c.match(/^([\s\S]+?)\s+ПОДОБНО\s+([\s\S]+)$/i);
      if(pm){sels[0].value=pm[1].trim();sels[1].value='ПОДОБНО';inp.value=pm[2].trim();return;}
      var om=c.match(/^([\s\S]+?)\s*(<>|>=|<=|=|>|<)\s*([\s\S]+)$/);
      if(om){sels[0].value=om[1].trim();sels[1].value=om[2];inp.value=om[3].trim();}
    });
  }

  // 7) ORDER BY
  if(orderSec){
    qbSplitTopLevel(orderSec,',').forEach(function(o){
      o=o.trim(); if(!o)return;
      mqbAddOrd();
      var rows=document.querySelectorAll('#mqb-ords > div');
      var row=rows[rows.length-1];
      var sels=row.querySelectorAll('select');
      var ubM=o.match(/^(.+?)\s+УБЫВ\s*$/i);
      if(ubM){sels[0].value=ubM[1].trim();sels[1].value='УБЫВ';}
      else{sels[0].value=o.replace(/\s+ВОЗР\s*$/i,'').trim();sels[1].value='ВОЗР';}
    });
  }

  mqbGen();
}

function mqbSetSrc(id){
  var src=_mqbSrcMap[id];_mqbSel={};_mqbJoins=[];
  document.getElementById('mqb-conds').innerHTML='';
  document.getElementById('mqb-ords').innerHTML='';
  document.getElementById('mqb-joins').innerHTML='<p style="font-size:12px;color:#94a3b8;margin:0" id="mqb-joins-hint">{{t $.Lang "Нет"}}</p>';
  document.getElementById('mqb-alias').value='';
  var vtp=document.getElementById('mqb-vtp');
  if(src&&src.vtParam){vtp.style.display='';document.getElementById('mqb-vtpv').value=src.vtParam;}
  else{vtp.style.display='none';}
  mqbRebuild();
}

function mqbRebuild(){
  var srcId=document.getElementById('mqb-src').value;
  var mainSrc=_mqbSrcMap[srcId];
  var mainAlias=document.getElementById('mqb-alias').value.trim();
  var all=[];
  if(mainSrc){
    mainSrc.fields.forEach(function(f){
      var n=mainAlias?mainAlias+'.'+f.name:f.name;
      all.push({name:n,label:n,type:f.type});
    });
    if(mainAlias)all.push({name:mainAlias+'.Ссылка',label:mainAlias+'.Ссылка (id)',type:'ref'});
  }
  _mqbJoins.forEach(function(j){
    var src=_mqbSrcMap[j.srcSel.value],alias=j.aliasInp.value.trim();
    if(!src||!alias)return;
    src.fields.forEach(function(f){all.push({name:alias+'.'+f.name,label:alias+'.'+f.name,type:f.type});});
    all.push({name:alias+'.Ссылка',label:alias+'.Ссылка (id)',type:'ref'});
  });
  var ns={};all.forEach(function(f){ns[f.name]=true;});
  var nw={};Object.keys(_mqbSel).forEach(function(k){if(ns[k])nw[k]=_mqbSel[k];});
  if(!Object.keys(nw).length&&mainSrc){
    all.forEach(function(f){if(!f.name.endsWith('.Ссылка')&&f.name!=='Ссылка')nw[f.name]={alias:'',agg:''};});
  }
  _mqbSel=nw;_mqbCurFields=all;mqbRenderFields();mqbGen();
}

function mqbRenderFields(){
  var div=document.getElementById('mqb-fields');div.innerHTML='';
  if(!_mqbCurFields.length){div.innerHTML='<p style="font-size:12px;color:#94a3b8">Выберите источник</p>';return;}
  var lastP=null;
  _mqbCurFields.forEach(function(f){
    var di=f.name.indexOf('.'),pf=di>=0?f.name.substring(0,di):'';
    if(pf&&pf!==lastP){lastP=pf;
      var sep=document.createElement('div');sep.style.cssText='font-size:11px;font-weight:600;color:#64748b;margin:4px 0 2px;border-top:1px solid #f1f5f9;padding-top:4px';
      sep.textContent=pf;div.appendChild(sep);
    }
    var row=document.createElement('div');row.style.cssText='display:flex;align-items:center;gap:5px;margin-bottom:2px;font-size:12px';
    var chk=document.createElement('input');chk.type='checkbox';chk.checked=!!_mqbSel[f.name];chk.dataset.field=f.name;
    chk.onchange=function(){if(chk.checked)_mqbSel[f.name]={alias:'',agg:''};else delete _mqbSel[f.name];mqbGen();};
    var lbl=document.createElement('label');lbl.textContent=di>=0?f.name.substring(di+1):f.name;lbl.title=f.name;
    lbl.style.cssText='flex:1;cursor:pointer;overflow:hidden;text-overflow:ellipsis;white-space:nowrap';lbl.onclick=function(){chk.click();};
    var agg=document.createElement('select');agg.style.cssText='font-size:11px;padding:1px 2px;border:1px solid #e2e8f0;border-radius:3px;width:82px';
    ['','СУММА','КОЛИЧЕСТВО','МИНИМУМ','МАКСИМУМ','СРЕДНЕЕ'].forEach(function(a){var o=document.createElement('option');o.value=a;o.textContent=a||'—';agg.appendChild(o);});
    if(_mqbSel[f.name])agg.value=_mqbSel[f.name].agg||'';
    agg.onchange=function(){if(_mqbSel[f.name])_mqbSel[f.name].agg=agg.value;mqbGen();};
    var al=document.createElement('input');al.type='text';al.placeholder='КАК';al.style.cssText='font-size:11px;width:60px;padding:1px 3px;border:1px solid #e2e8f0;border-radius:3px';
    if(_mqbSel[f.name])al.value=_mqbSel[f.name].alias||'';
    al.oninput=function(){if(_mqbSel[f.name])_mqbSel[f.name].alias=al.value.trim();mqbGen();};
    row.appendChild(chk);row.appendChild(lbl);row.appendChild(agg);row.appendChild(al);div.appendChild(row);
  });
}

function mqbAll(v){
  _mqbCurFields.forEach(function(f){
    if(f.name.endsWith('.Ссылка')||f.name==='Ссылка')return;
    if(v)_mqbSel[f.name]={alias:'',agg:''};else delete _mqbSel[f.name];
  });mqbRenderFields();mqbGen();
}

function mqbAddJoin(){
  var mainA=document.getElementById('mqb-alias');
  if(!mainA.value.trim()){var ms=_mqbSrcMap[document.getElementById('mqb-src').value];
    if(ms){var p=ms.label.split('.');mainA.value=p.length>=2?p[1].replace(/\(.*$/,''):p[0];}}
  var hint=document.getElementById('mqb-joins-hint');if(hint)hint.remove();
  var jid=Date.now(),div=document.createElement('div');
  div.style.cssText='border:1px solid #e2e8f0;border-radius:5px;padding:6px;margin-bottom:6px;background:#fff';
  var r1=document.createElement('div');r1.style.cssText='display:flex;gap:4px;align-items:center;margin-bottom:4px;flex-wrap:wrap';
  var ts=document.createElement('select');ts.style.cssText='width:110px;font-size:12px;border:1px solid #e2e8f0;border-radius:3px;padding:1px 3px';
  [['ЛЕВОЕ','⬅ ЛЕВОЕ'],['ВНУТРЕННЕЕ','✕ ВНУТРЕННЕЕ'],['ПРАВОЕ','➡ ПРАВОЕ'],['ПОЛНОЕ','⟺ ПОЛНОЕ']].forEach(function(x){var o=document.createElement('option');o.value=x[0];o.textContent=x[1];ts.appendChild(o);});
  var ss=document.createElement('select');ss.style.cssText='flex:1;min-width:120px;font-size:12px;border:1px solid #e2e8f0;border-radius:3px;padding:1px 3px';
  var o0=document.createElement('option');o0.value='';o0.textContent='— источник —';ss.appendChild(o0);
  var jg={};_mqbSchema.forEach(function(s){if(!jg[s.group])jg[s.group]=[];jg[s.group].push(s);});
  Object.keys(jg).forEach(function(g){var og=document.createElement('optgroup');og.label=g;jg[g].forEach(function(s){var o=document.createElement('option');o.value=s.id;o.textContent=s.label;og.appendChild(o);});ss.appendChild(og);});
  var ai=document.createElement('input');ai.type='text';ai.placeholder='Псевдоним';ai.style.cssText='width:80px;font-size:12px;border:1px solid #e2e8f0;border-radius:3px;padding:1px 3px';
  var del=document.createElement('button');del.type='button';del.textContent='×';
  del.style.cssText='background:none;border:none;color:#ef4444;cursor:pointer;font-size:16px;line-height:1;padding:0 2px';
  del.onclick=function(){div.remove();_mqbJoins=_mqbJoins.filter(function(j){return j.id!==jid;});
    if(!_mqbJoins.length)document.getElementById('mqb-joins').innerHTML='<p style="font-size:12px;color:#94a3b8;margin:0" id="mqb-joins-hint">{{t $.Lang "Нет"}}</p>';
    mqbRebuild();};
  r1.appendChild(ts);r1.appendChild(ss);r1.appendChild(ai);r1.appendChild(del);
  var r2=document.createElement('div');r2.style.cssText='display:flex;gap:4px;align-items:center';
  var onL=document.createElement('span');onL.textContent='ПО:';onL.style.cssText='font-size:12px;font-weight:600;color:#475569;width:24px';
  var onI=document.createElement('input');onI.type='text';onI.placeholder='Пс1.Поле = Пс2.Ссылка';
  onI.style.cssText='flex:1;font-size:12px;border:1px solid #e2e8f0;border-radius:3px;padding:1px 4px';
  r2.appendChild(onL);r2.appendChild(onI);div.appendChild(r1);div.appendChild(r2);
  document.getElementById('mqb-joins').appendChild(div);
  var jd={id:jid,el:div,typeSel:ts,srcSel:ss,aliasInp:ai,onInp:onI};_mqbJoins.push(jd);
  ss.onchange=function(){var src=_mqbSrcMap[ss.value];if(src&&!ai.value.trim()){var p=src.label.split('.');ai.value=p.length>=2?p[1].replace(/\(.*$/,''):p[0];}mqbRebuild();};
  ts.onchange=function(){mqbGen();};ai.oninput=function(){mqbRebuild();};onI.oninput=function(){mqbGen();};
  mqbRebuild();
}

function mqbAddCond(){
  var div=document.createElement('div');div.style.cssText='display:flex;gap:3px;margin-bottom:4px;align-items:center;flex-wrap:wrap';
  var fs=document.createElement('select');fs.style.cssText='flex:1;min-width:80px;font-size:12px;border:1px solid #e2e8f0;border-radius:3px;padding:1px 3px';
  _mqbCurFields.forEach(function(f){var o=document.createElement('option');o.value=f.name;o.textContent=f.name;fs.appendChild(o);});
  var ops=document.createElement('select');ops.style.cssText='width:90px;font-size:12px;border:1px solid #e2e8f0;border-radius:3px;padding:1px 3px';
  ['=','<>','>','<','>=','<=','ЕСТЬ ПУСТО','НЕ ЕСТЬ ПУСТО','ПОДОБНО','В'].forEach(function(op){var o=document.createElement('option');o.value=op;o.textContent=op;ops.appendChild(o);});
  var vi=document.createElement('input');vi.type='text';vi.placeholder='&Параметр';vi.style.cssText='flex:1;min-width:60px;font-size:12px;border:1px solid #e2e8f0;border-radius:3px;padding:1px 4px';
  ops.onchange=function(){var nv=ops.value==='ЕСТЬ ПУСТО'||ops.value==='НЕ ЕСТЬ ПУСТО';vi.style.display=nv?'none':'';mqbGen();};
  fs.onchange=vi.oninput=function(){mqbGen();};
  var del=document.createElement('button');del.type='button';del.textContent='×';del.style.cssText='background:none;border:none;color:#ef4444;cursor:pointer;font-size:14px;line-height:1';
  del.onclick=function(){div.remove();mqbGen();};
  div.appendChild(fs);div.appendChild(ops);div.appendChild(vi);div.appendChild(del);
  document.getElementById('mqb-conds').appendChild(div);mqbGen();
}

function mqbAddOrd(){
  var div=document.createElement('div');div.style.cssText='display:flex;gap:3px;margin-bottom:4px;align-items:center';
  var fs=document.createElement('select');fs.style.cssText='flex:1;font-size:12px;border:1px solid #e2e8f0;border-radius:3px;padding:1px 3px';
  _mqbCurFields.forEach(function(f){var o=document.createElement('option');o.value=f.name;o.textContent=f.name;fs.appendChild(o);});
  var ds=document.createElement('select');ds.style.cssText='width:70px;font-size:12px;border:1px solid #e2e8f0;border-radius:3px;padding:1px 3px';
  [['ВОЗР','↑ ВОЗР'],['УБЫВ','↓ УБЫВ']].forEach(function(x){var o=document.createElement('option');o.value=x[0];o.textContent=x[1];ds.appendChild(o);});
  var del=document.createElement('button');del.type='button';del.textContent='×';del.style.cssText='background:none;border:none;color:#ef4444;cursor:pointer;font-size:14px;line-height:1';
  del.onclick=function(){div.remove();mqbGen();};
  fs.onchange=ds.onchange=function(){mqbGen();};
  div.appendChild(fs);div.appendChild(ds);div.appendChild(del);
  document.getElementById('mqb-ords').appendChild(div);mqbGen();
}

function mqbGen(){
  var srcId=document.getElementById('mqb-src').value,src=_mqbSrcMap[srcId];
  if(!src){document.getElementById('mqb-qry').value='';document.getElementById('mqb-dsl').value='';return;}
  var mainAlias=document.getElementById('mqb-alias').value.trim();
  var activeJ=_mqbJoins.filter(function(j){return!!j.srcSel.value&&!!j.aliasInp.value.trim();});
  var hasJ=activeJ.length>0,selP=[],hasAgg=false,grpF=[];
  _mqbCurFields.forEach(function(f){
    var info=_mqbSel[f.name];if(!info)return;var expr=f.name;
    if(info.agg){expr=info.agg+'('+f.name+')';hasAgg=true;}else{grpF.push(f.name);}
    if(info.alias)expr+=' КАК '+info.alias;selP.push('  '+expr);
  });
  if(!selP.length)selP=['  *'];
  var from=src.label;
  if(src.vtParam){var vv=document.getElementById('mqb-vtpv').value.trim()||src.vtParam;from=from.replace(/\(.*?\)/,'('+vv+')');}
  if(mainAlias||hasJ)from+=' КАК '+(mainAlias||'Т');
  activeJ.forEach(function(j){
    var jSrc=_mqbSrcMap[j.srcSel.value],jA=j.aliasInp.value.trim(),jL=jSrc.label;
    var onC=j.onInp.value.trim();
    from+='\n  '+j.typeSel.value+' СОЕДИНЕНИЕ '+jL+' КАК '+jA;
    from+='\n  ПО '+(onC||'/* условие */');
  });
  var wP=[],params={};
  document.getElementById('mqb-conds').querySelectorAll('div').forEach(function(row){
    var sels=row.querySelectorAll('select'),inp=row.querySelector('input[type=text]');
    if(!sels[0])return;var field=sels[0].value,op=sels[1]?sels[1].value:'=',val=(inp&&inp.style.display!=='none')?inp.value.trim():'';
    if(op==='ЕСТЬ ПУСТО'||op==='НЕ ЕСТЬ ПУСТО'){wP.push(field+' '+op);}
    else if(val){var m=val.match(/&[А-Яа-яёЁA-Za-z_]\w*/g);if(m)m.forEach(function(p){params[p]=true;});
      wP.push(op==='В'?field+' В ('+val+')':field+' '+op+' '+val);}
  });
  activeJ.forEach(function(j){var m=j.onInp.value.match(/&[А-Яа-яёЁA-Za-z_]\w*/g);if(m)m.forEach(function(p){params[p]=true;});});
  var oP=[];
  document.getElementById('mqb-ords').querySelectorAll('div').forEach(function(row){
    var sels=row.querySelectorAll('select');if(!sels[0])return;var f=sels[0].value,d=sels[1]?sels[1].value:'ВОЗР';
    oP.push(d==='УБЫВ'?f+' УБЫВ':f);
  });
  var q='ВЫБРАТЬ\n'+selP.join(',\n')+'\nИЗ '+from;
  if(wP.length)q+='\nГДЕ '+wP.join('\n  И ');
  if(hasAgg&&grpF.length)q+='\nСГРУППИРОВАТЬ ПО '+grpF.join(', ');
  if(oP.length)q+='\nУПОРЯДОЧИТЬ ПО '+oP.join(', ');
  document.getElementById('mqb-qry').value=q;
  var pL=Object.keys(params);
  var qL=q.split('\n'),strLit='"'+qL[0];
  for(var i=1;i<qL.length;i++)strLit+='\n|'+qL[i];strLit+='"';
  var dsl='Запрос = Новый Запрос;\nЗапрос.Текст =\n  '+strLit+';\n';
  pL.forEach(function(p){dsl+='Запрос.УстановитьПараметр("'+p.slice(1)+'", '+p+');\n';});
  dsl+='Результат = Запрос.Выполнить();\n\nДля Каждого Строка Из Результат Цикл\n';
  var ff=_mqbCurFields.find(function(f){return!!_mqbSel[f.name]&&!f.name.endsWith('.Ссылка')&&f.name!=='Ссылка';});
  if(ff){var fn=(_mqbSel[ff.name]&&_mqbSel[ff.name].alias)||ff.name.replace(/.*\./,'');dsl+='  Сообщить(Строка.'+fn+');\n';}
  dsl+='КонецЦикла;';
  document.getElementById('mqb-dsl').value=dsl;
}

// ── Debug panel ──────────────────────────────────────────────────
var _dbgBase = '{{.Base.ID}}'; // base ID for debug proxy
var _basePort = {{.Base.Port}};
var _sessionToken = '{{.SessionToken}}';
var _treeGroupOrder = [{{range $i, $g := .GroupOrder}}{{if $i}},{{end}}'{{$g}}'{{end}}]; // пользовательский порядок групп дерева
var _dbgEnabled = false;
var _dbgPollTimer = null;
var _dbgPollCount = 0;
var _dbgBreakpoints = {}; // { editorId: { line: true } }
var _lastVarsKey = '';
var _lastStackHtml = '';
var _lastDiagHtml = '';
var _dbgCurrentLineDecos = {}; // { editorId: decorationIds }

// ── Backup progress helper ────────────────────────────────────────
function cfgBackupStart(form, label) {
  var btn = form.querySelector('[type=submit]');
  if (btn) { btn.disabled = true; btn.textContent = label || '⏳ Выполняется...'; }
  // Show page-level overlay so user can't click anything else
  var ov = document.getElementById('_backup-overlay');
  if (!ov) {
    ov = document.createElement('div');
    ov.id = '_backup-overlay';
    ov.style.cssText = 'position:fixed;top:0;left:0;right:0;bottom:0;background:rgba(0,0,0,.3);z-index:8000;display:flex;align-items:center;justify-content:center';
    ov.innerHTML = '<div style="background:#fff;border-radius:10px;padding:28px 40px;text-align:center;box-shadow:0 8px 32px rgba(0,0,0,.18);font-size:15px;color:#1e293b">' + (label||'⏳ Выполняется...') + '<br><span style="font-size:12px;color:#64748b;margin-top:6px;display:block">Пожалуйста, подождите…</span></div>';
    document.body.appendChild(ov);
  }
}

// ── Configurator admin panels ────────────────────────────────────
function cfgAdmin(name) {
  document.getElementById('cfg-menu').classList.remove('open');
  // Use global overlay instead of panel-admin
  var overlay = document.getElementById('admin-overlay');
  if(!overlay)return;
  overlay.style.display='flex';
  overlay.innerHTML = '<div style="background:#fff;border-radius:8px;box-shadow:0 8px 32px rgba(0,0,0,.2);width:90%;max-width:800px;max-height:85vh;overflow-y:auto;position:relative"><div style="padding:20px;text-align:center;color:#888">{{t $.Lang "Загрузка..."}}</div></div>';
  fetch('/bases/' + _dbgBase + '/configurator/admin/' + name)
    .then(function(r){ return r.text(); })
    .then(function(html){
      overlay.innerHTML = '<div style="background:#fff;border-radius:8px;box-shadow:0 8px 32px rgba(0,0,0,.2);width:90%;max-width:800px;max-height:85vh;overflow-y:auto;position:relative">'
        +'<div style="position:sticky;top:0;background:#fff;padding:8px 16px;border-bottom:1px solid #e2e8f0;display:flex;justify-content:space-between;align-items:center"><span style="font-weight:600;font-size:13px">Администрирование</span><button onclick="document.getElementById(\'admin-overlay\').style.display=\'none\'" style="background:none;border:none;font-size:20px;cursor:pointer;color:#666">×</button></div>'
        +'<div style="padding:16px">'+html+'</div></div>';
      // Scripts inside innerHTML don't execute — re-run them manually
      overlay.querySelectorAll('script').forEach(function(s){
        var ns = document.createElement('script');
        ns.textContent = s.textContent;
        document.body.appendChild(ns);
        document.body.removeChild(ns);
      });
    });
}

function cfgMenuToggle() {
  var m = document.getElementById('cfg-menu');
  m.classList.toggle('open');
}
function cfgModalClose() {
  var modal = document.getElementById('cfg-modal');
  modal.classList.remove('active');
  var iframe = document.getElementById('cfg-modal-iframe');
  iframe.src = 'about:blank';
}
// close menu on outside click
document.addEventListener('click', function(e) {
  var wrap = e.target.closest('.cfg-menu-wrap');
  if (!wrap) {
    var m = document.getElementById('cfg-menu');
    if (m) m.classList.remove('open');
  }
});

function _enterpriseURL() {
  return _sessionToken
    ? 'http://localhost:' + _basePort + '/ui?_tk=' + encodeURIComponent(_sessionToken)
    : 'http://localhost:' + _basePort + '/ui';
}

// Запускает (restart=false) или перезапускает (restart=true) базу и открывает
// пользовательский режим. Дожидается готовности сервера перед открытием окна.
function _doLaunch(restart) {
  var btn = document.querySelector('.run-enterprise-btn');
  if (btn) btn.style.background = '#a3a3a3';
  var url = _enterpriseURL();
  var endpoint = restart
    ? '/bases/' + _dbgBase + '/configurator/restart'
    : '/bases/' + _dbgBase + '/start';
  fetch(endpoint, {method:'POST'})
    .then(function(r){ return r.json().catch(function(){ return {}; }); })
    .then(function(d){
      if (btn) btn.style.background = '';
      if (d && d.error) { alert('Не удалось запустить базу: ' + d.error); return; }
      window.open(url, '_blank');
    })
    .catch(function(){
      if (btn) btn.style.background = '';
      window.open(url, '_blank');
    });
}

// Точка входа жёлтой ▶: проверяет, отстала ли БД от конфигурации (как F5 в 1С),
// и при необходимости предлагает обновить структуру БД перед запуском.
function launchEnterprise() {
  fetch('/bases/' + _dbgBase + '/configurator/launch-state')
    .then(function(r){ return r.json(); })
    .then(function(st){
      if (st && st.configChanged) {
        _showLaunchDialog(!!(st && st.running));
      } else {
        _doLaunch(false);
      }
    })
    .catch(function(){ _doLaunch(false); });
}

function _closeLaunchDialog() {
  var ov = document.getElementById('launch-dialog-overlay');
  if (ov) ov.remove();
}

function _showLaunchDialog(running) {
  _closeLaunchDialog();
  var rv = running ? 'true' : 'false';
  var ov = document.createElement('div');
  ov.id = 'launch-dialog-overlay';
  ov.style.cssText = 'position:fixed;top:0;left:0;right:0;bottom:0;background:rgba(0,0,0,.4);display:flex;align-items:center;justify-content:center;z-index:10000';
  var box = document.createElement('div');
  box.className = 'cfg-modal-box';
  box.style.maxWidth = '470px';
  box.innerHTML =
      '<div class="cfg-modal-hd"><span style="font-weight:600;font-size:13px">Запуск предприятия</span>'
    + '<button onclick="_closeLaunchDialog()" style="background:none;border:none;font-size:20px;cursor:pointer;color:#666">×</button></div>'
    + '<div style="padding:16px 18px;font-size:13px;color:#334;line-height:1.5">'
    + 'Конфигурация изменена с момента последнего обновления базы данных.<br>Обновить структуру БД и запустить?'
    + '<div id="launch-dialog-msg" style="display:none;margin-top:10px;font-size:11px;padding:8px;border-radius:4px;max-height:140px;overflow-y:auto;white-space:pre-wrap"></div>'
    + '</div>'
    + '<div style="display:flex;gap:8px;justify-content:flex-end;padding:12px 18px;border-top:1px solid #e2e8f0;flex-wrap:wrap">'
    + '<button id="ld-cancel" onclick="_closeLaunchDialog()" style="padding:7px 14px;background:#fff;border:1px solid #cbd5e1;border-radius:4px;cursor:pointer;font-size:12px">Отмена</button>'
    + '<button id="ld-nomigrate" onclick="_launchNoMigrate(' + rv + ')" style="padding:7px 14px;background:#fff;border:1px solid #1a4a80;color:#1a4a80;border-radius:4px;cursor:pointer;font-size:12px">Запустить без обновления</button>'
    + '<button id="ld-migrate" onclick="_launchWithMigrate(' + rv + ')" style="padding:7px 14px;background:#1a4a80;color:#fff;border:none;border-radius:4px;cursor:pointer;font-size:12px">Обновить БД и запустить</button>'
    + '</div>';
  ov.appendChild(box);
  document.body.appendChild(ov);
}

function _launchNoMigrate(running) {
  _closeLaunchDialog();
  _doLaunch(running);
}

function _launchWithMigrate(running) {
  var msg = document.getElementById('launch-dialog-msg');
  var mb = document.getElementById('ld-migrate');
  var nb = document.getElementById('ld-nomigrate');
  if (mb) { mb.disabled = true; mb.textContent = 'Обновление БД...'; }
  if (nb) nb.disabled = true;
  fetch('/bases/' + _dbgBase + '/configurator/migrate', {method:'POST'})
    .then(function(r){ return r.json(); })
    .then(function(d){
      if (d && d.error) {
        if (mb) { mb.disabled = false; mb.textContent = 'Обновить БД и запустить'; }
        if (nb) nb.disabled = false;
        if (msg) { msg.style.display='block'; msg.style.background='#fff0f0'; msg.style.color='#c00'; msg.textContent = d.error + (d.output ? '\n' + d.output : ''); }
        return;
      }
      _closeLaunchDialog();
      _doLaunch(running);
    })
    .catch(function(e){
      if (mb) { mb.disabled = false; mb.textContent = 'Обновить БД и запустить'; }
      if (nb) nb.disabled = false;
      if (msg) { msg.style.display='block'; msg.style.background='#fff0f0'; msg.style.color='#c00'; msg.textContent = 'Ошибка: ' + e.message; }
    });
}

function runMigrate() {
  var btn = document.getElementById('btn-migrate');
  var result = document.getElementById('migrate-result');
  btn.disabled = true;
  btn.textContent = 'Обновление...';
  result.style.display = 'none';
  fetch('/bases/' + _dbgBase + '/configurator/migrate', {method:'POST'})
    .then(function(r){ return r.json(); })
    .then(function(d){
      btn.disabled = false;
      btn.textContent = 'Обновить БД';
      result.style.display = 'block';
      if (d.error) {
        result.style.background = '#fff0f0';
        result.style.color = '#c00';
        result.textContent = d.error;
      } else {
        result.style.background = '#f0fdf4';
        result.style.color = '#15803d';
        result.textContent = d.output || 'Обновление завершено';
      }
    })
    .catch(function(e){
      btn.disabled = false;
      btn.textContent = 'Обновить БД';
      result.style.display = 'block';
      result.style.background = '#fff0f0';
      result.style.color = '#c00';
      result.textContent = 'Ошибка: ' + e.message;
    });
}

function dbgToggle() {
  var btn = document.getElementById('dbg-toggle');
  var panel = document.getElementById('dbg-panel');
  if (!_dbgEnabled) {
    fetch('/bases/' + _dbgBase + '/debug/enable', {method:'POST'})
      .then(function(r){
        if (!r.ok) return r.json().then(function(d){ throw new Error(d.error || ('HTTP ' + r.status)); });
        return r.json();
      })
      .then(function(d){
        if (d.status !== 'enabled') { throw new Error(d.error || 'Unexpected response'); }
        _dbgEnabled = true;
        btn.innerHTML = '&#128027; Отладка: ВКЛ';
        btn.className = 'dbg-topbar-btn dbg-on';
        panel.style.display = 'flex';
        dbgUpdateStatus('running', 'init');
        var diagEl = document.getElementById('dbg-diag');
        if (diagEl) diagEl.innerHTML = '<div style="color:#22c55e;font-size:10px">Enable OK: dbg=' + (d.dbg_ptr||'?') + ' sess=' + (d.sess_ptr||'?') + '</div>';
        dbgStartPoll();
      })
      .catch(function(e){ alert('Ошибка включения отладки: ' + e.message); });
  } else {
    fetch('/bases/' + _dbgBase + '/debug/disable', {method:'POST'})
      .then(function(r){return r.json()})
      .then(function(d){
        _dbgEnabled = false;
        btn.innerHTML = '&#128027; Отладка: ВЫКЛ';
        btn.className = 'dbg-topbar-btn';
        panel.style.display = 'none';
        dbgStopPoll();
        dbgClearLineHighlight();
        dbgUpdateStatus('disabled', 'off');
      })
      .catch(function(e){ console.error('dbg disable error', e); });
  }
}

function dbgStartPoll() {
  dbgStopPoll();
  _dbgPollTimer = setInterval(dbgPoll, 500);
}
function dbgStopPoll() {
  if (_dbgPollTimer) { clearInterval(_dbgPollTimer); _dbgPollTimer = null; }
}

function dbgPoll() {
  fetch('/bases/' + _dbgBase + '/debug/status?_=' + Date.now())
    .then(function(r){return r.json()})
    .then(function(snap){
      _dbgPollCount++;
      if (snap.state === 'disabled') {
        dbgUpdateStatus('disabled', snap.state);
        return;
      }
      var st = snap.state; // "running" | "paused" | "stopped"

      // show/hide controls
      var ctrl = document.getElementById('dbg-controls');
      ctrl.style.display = (st === 'paused' || st === 'running') ? 'flex' : 'none';

      // if paused with location, highlight current line in Monaco — BEFORE dbgUpdateStatus
      if (st === 'paused' && snap.location) {
        var locFile = snap.location.file;
        var locLine = snap.location.line;
        dbgShowLocation(locFile, locLine);
        dbgWatchEvalAll();
      } else if (st === 'stopped' || st === 'disabled') {
        dbgClearLineHighlight();
      }

      // NOW update status (reads _dbgHighlightLog and _dbgPauseReason)
      _dbgPauseReason = snap.pause_reason || '';
      dbgUpdateStatus(st, snap.state);
      // update button color
      var btn = document.getElementById('dbg-toggle');
      if (st === 'paused') btn.className = 'dbg-topbar-btn dbg-paused';
      else if (st === 'running') btn.className = 'dbg-topbar-btn dbg-on';

      // variables — only update DOM if changed
      if (snap.variables && snap.variables.length) {
        var varsKey = snap.variables.map(function(v){return v.name+'='+v.value;}).join('|');
        if (varsKey !== _lastVarsKey) {
          _lastVarsKey = varsKey;
          var h = '';
          snap.variables.forEach(function(v){
            h += '<div class="dbg-var-row"><span class="dbg-var-name">' + esc(v.name) + '</span>'
              + '<span class="dbg-var-val dbg-val-clickable" title="Открыть значение" onclick="dbgInspectValue(\'' + esc(v.name).replace(/'/g,"\\'") + '\')">' + esc(v.value) + '</span><span class="dbg-var-type">' + esc(v.type) + '</span></div>';
          });
          document.getElementById('dbg-vars').innerHTML = h;
        }
      } else if (st !== 'paused') {
        if (_lastVarsKey !== '') {
          _lastVarsKey = '';
          document.getElementById('dbg-vars').innerHTML = '<div class="dbg-empty">Нет переменных</div>';
        }
      }

      // stack — only update DOM if changed
      if (snap.stack && snap.stack.length) {
        var h = '';
        snap.stack.forEach(function(f){
          h += '<div class="dbg-stack-row"><span class="proc">' + esc(f.procedure) + '</span> '
            + '<span class="loc">' + esc(f.module) + ':' + f.line + '</span></div>';
        });
        if (h !== _lastStackHtml) { _lastStackHtml = h; document.getElementById('dbg-stack').innerHTML = h; }
      }

      // breakpoints — always render from local state
      dbgRenderBPList();

      // diagnostics — show only detailed check messages in console tab
      var diagEl = document.getElementById('dbg-diag');
      if (diagEl && snap.state !== 'disabled') {
        var dh = '';
        if (snap.diag_messages && snap.diag_messages.length) {
          var msgs = snap.diag_messages.slice(-12);
          msgs.forEach(function(m){ dh += '<div class="dbg-console-line">' + esc(m) + '</div>'; });
        }
        if (dh !== _lastDiagHtml) { _lastDiagHtml = dh; diagEl.innerHTML = dh; }
      } else if (diagEl && snap.state === 'disabled') {
        if (_lastDiagHtml !== '__disabled__') { _lastDiagHtml = '__disabled__'; diagEl.innerHTML = '<div style="color:#9ca3af;font-size:10px">Debug disabled</div>'; }
      }
    })
    .catch(function(e){
      var diagEl = document.getElementById('dbg-diag');
      if (diagEl) { diagEl.innerHTML = '<div style="color:#ef4444;font-size:10px">Poll error: ' + esc(e.message) + '</div>' + diagEl.innerHTML; }
    });
}

var _dbgHighlightLog = '';
var _dbgPauseReason = '';
var _lastStatusHtml = '';
function dbgUpdateStatus(st, rawState) {
  var el = document.getElementById('dbg-status');
  var labels = {disabled:'Отладка: ВЫКЛ', running:'Отладка: выполнение...', paused:'Отладка: пауза', stopped:'Отладка: останов'};
  var dot = '<span class="dot ' + st + '"></span> ' + (labels[st]||st);
  if (_dbgPauseReason && st === 'paused') {
    dot += ' <span style="font-size:10px;color:#94a3b8">(' + (_dbgPauseReason === 'breakpoint' ? 'точка останова' : 'шаг') + ')</span>';
  }
  var html = _dbgHighlightLog ? (dot + ' <span style="font-size:9px;color:#f97316;user-select:text">' + _dbgHighlightLog + '</span>') : dot;
  // Avoid rewriting innerHTML on every poll — it clears any text the user is selecting.
  if (html === _lastStatusHtml) return;
  _lastStatusHtml = html;
  el.innerHTML = html;
}

function dbgContinue() {
  dbgClearLineHighlight();
  fetch('/bases/' + _dbgBase + '/debug/continue', {method:'POST'}).then(function(){
    setTimeout(dbgPoll, 100);
    setTimeout(dbgPoll, 500);
  }).catch(function(e){console.error(e);});
}
function dbgStep(mode) {
  dbgClearLineHighlight();
  fetch('/bases/' + _dbgBase + '/debug/step', {method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({mode:mode})}).then(function(){
    // Poll repeatedly until we see "paused" or timeout (2s)
    var tries = 0;
    function pollUntilPaused() {
      if (tries > 12) { dbgWatchDebug('step timeout'); return; }
      tries++;
      setTimeout(function(){
        fetch('/bases/' + _dbgBase + '/debug/status?_=' + Date.now())
          .then(function(r){return r.json()})
          .then(function(snap){
            dbgWatchDebug('step poll #' + tries + ' state=' + snap.state + ' reason=' + (snap.pause_reason||'?') + ' loc=' + (snap.location ? snap.location.file + ':' + snap.location.line : 'none'));
            if (snap.state === 'paused' && snap.location) {
              dbgPoll(); // run full poll to update everything
            } else {
              pollUntilPaused();
            }
          })
          .catch(function(e){ dbgWatchDebug('step poll err: ' + e.message); pollUntilPaused(); });
      }, 200);
    }
    pollUntilPaused();
  }).catch(function(e){console.error(e);});
}
document.addEventListener('keydown', function(e) {
  if (e.target.tagName === 'INPUT' || e.target.tagName === 'TEXTAREA') return;
  if (e.key === 'F10') { e.preventDefault(); e.stopPropagation(); dbgStep('over'); }
  else if (e.key === 'F11' && !e.shiftKey) { e.preventDefault(); e.stopPropagation(); dbgStep('into'); }
  else if (e.key === 'F11' && e.shiftKey) { e.preventDefault(); e.stopPropagation(); dbgStep('out'); }
}, true);
function dbgStop() {
  fetch('/bases/' + _dbgBase + '/debug/stop', {method:'POST'})
    .then(function(){
      var btn = document.getElementById('dbg-toggle');
      _dbgEnabled = false;
      btn.innerHTML = '&#128027; Отладка: ВЫКЛ';
      btn.className = 'dbg-topbar-btn';
      dbgStopPoll();
      dbgClearLineHighlight();
      dbgUpdateStatus('disabled', 'stop');
    }).catch(function(e){console.error(e);});
}

function dbgEval() {
  var inp = document.getElementById('dbg-expr');
  var expr = inp.value.trim();
  if (!expr) return;
  var out = document.getElementById('dbg-console-out');
  out.innerHTML += '<div class="dbg-console-line">&gt; ' + esc(expr) + '</div>';
  inp.value = '';
  fetch('/bases/' + _dbgBase + '/debug/evaluate', {method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({expr:expr})})
    .then(function(r){return r.json()})
    .then(function(res){
      if (res.is_error) {
        out.innerHTML += '<div class="dbg-console-line dbg-err">' + esc(res.error) + '</div>';
      } else {
        var val = res.value !== null && res.value !== undefined ? String(res.value) : 'Неопределено';
        out.innerHTML += '<div class="dbg-console-line dbg-ok">= ' + esc(val) + ' (' + esc(res.type||'') + ')</div>';
      }
      out.scrollTop = out.scrollHeight;
    })
    .catch(function(e){
      out.innerHTML += '<div class="dbg-console-line dbg-err">Ошибка: ' + esc(e.message) + '</div>';
      out.scrollTop = out.scrollHeight;
    });
}

function dbgTab(name) {
  var tabs = ['vars','watch','bp','stack','console'];
  tabs.forEach(function(t){
    var el = document.getElementById('dbg-' + t);
    if (t === name) {
      el.style.display = 'flex';
      el.style.flexDirection = 'column';
    } else {
      el.style.display = 'none';
    }
  });
  document.querySelectorAll('.dbg-tab').forEach(function(btn, i){
    btn.classList.toggle('active', tabs[i] === name);
  });
}

// ── Watch (Табло v4) ──────────────────────────────────────────
var _dbgWatchList = []; // [{name: 'Перем', id: 'w0'}]
var _dbgWatchId = 0;

function dbgWatchDebug(msg) {
  var d = document.getElementById('dbg-watch-debug');
  if (d) d.textContent = msg;
}

function dbgWatchAdd() {
  var inp = document.getElementById('dbg-watch-add');
  if (!inp) { dbgWatchDebug('ERROR: input not found'); return; }
  var name = inp.value.trim();
  if (!name) return;
  inp.value = '';
  _dbgWatchId++;
  var wid = 'w' + _dbgWatchId;
  _dbgWatchList.push({name: name, id: wid});
  dbgWatchDebug('added "' + name + '" id=' + wid + ' total=' + _dbgWatchList.length);
  dbgWatchRender();
  dbgWatchEvalAll();
}

function dbgWatchRemove(id) {
  _dbgWatchList = _dbgWatchList.filter(function(w){ return w.id !== id; });
  dbgWatchRender();
}

function dbgWatchRender() {
  var el = document.getElementById('dbg-watch-list');
  if (!el) { dbgWatchDebug('ERROR: list element not found'); return; }
  if (!_dbgWatchList.length) {
    el.innerHTML = '<div style="color:#94a3b8;padding:8px 0;text-align:center;font-size:12px">Добавьте выражение</div>';
    dbgWatchDebug('empty list');
    return;
  }
  var h = '';
  _dbgWatchList.forEach(function(w){
    h += '<div class="dbg-var-row">'
      + '<span class="dbg-var-name">' + esc(w.name) + '</span>'
      + ' <span style="color:#ef4444;cursor:pointer;font-size:9px" onclick="dbgWatchRemove(\'' + w.id + '\')">&#10005;</span>'
      + ' <span class="dbg-var-val dbg-val-clickable" title="Открыть значение" onclick="dbgWatchOpen(\'' + w.id + '\')" id="wv-' + w.id + '">...</span>'
      + ' <span class="dbg-var-type" id="wt-' + w.id + '"></span>'
      + '</div>';
  });
  el.innerHTML = h;
  dbgWatchDebug('rendered ' + _dbgWatchList.length + ' items');
}

function dbgValToStr(v) {
  if (v === null || v === undefined) return 'Неопределено';
  if (typeof v === 'object') { try { return JSON.stringify(v, null, 2); } catch(e) { return String(v); } }
  return String(v);
}

var _dbgWatchGen = 0;
function dbgWatchEvalAll() {
  if (!_dbgWatchList.length) return;
  _dbgWatchGen++;
  var gen = _dbgWatchGen;
  _dbgWatchList.forEach(function(w){
    var valEl = document.getElementById('wv-' + w.id);
    if (!valEl) return;
    if (!_dbgEnabled) { valEl.textContent = '(отладка выкл)'; return; }
    fetch('/bases/' + _dbgBase + '/debug/evaluate', {method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({expr:w.name})})
      .then(function(r){return r.json()})
      .then(function(res){
        if (gen !== _dbgWatchGen) return; // a newer evaluation round started — drop stale result
        var v = document.getElementById('wv-' + w.id);
        var t = document.getElementById('wt-' + w.id);
        if (!v) return;
        if (res.is_error) {
          // Usually a transient "interpreter not paused" window between steps —
          // keep the last good value, just dim it instead of flickering to an error.
          v.style.opacity = '0.4';
          return;
        }
        var txt = dbgValToStr(res.value);
        w._full = txt;
        w._type = res.type || '';
        var disp = txt.length > 300 ? txt.substring(0, 300) + '…' : txt;
        if (v.textContent !== disp) v.textContent = disp;
        v.style.opacity = '';
        v.style.color = '';
        v.title = (txt.length > 60) ? 'Открыть значение полностью' : 'Открыть значение';
        var tp = res.type || '';
        if (t && t.textContent !== tp) t.textContent = tp;
      })
      .catch(function(e){
        if (gen !== _dbgWatchGen) return;
        var v = document.getElementById('wv-' + w.id);
        if (v) v.style.opacity = '0.4';
      });
  });
}

function dbgWatchOpen(id) {
  var w = null;
  for (var i = 0; i < _dbgWatchList.length; i++) { if (_dbgWatchList[i].id === id) { w = _dbgWatchList[i]; break; } }
  if (!w) return;
  if (w._full !== undefined) dbgShowBigValue(w.name, w._type || '', w._full);
  else dbgInspectValue(w.name);
}

// Inspect a variable/expression: re-evaluate it (full, untruncated) and show in a modal.
function dbgInspectValue(expr) {
  if (!_dbgEnabled) { dbgShowBigValue(expr, '', '(отладка выключена)'); return; }
  fetch('/bases/' + _dbgBase + '/debug/evaluate', {method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({expr:expr})})
    .then(function(r){return r.json()})
    .then(function(res){
      if (res.is_error) { dbgShowBigValue(expr, 'Ошибка', String(res.error)); return; }
      dbgShowBigValue(expr, res.type || '', dbgValToStr(res.value));
    })
    .catch(function(e){ dbgShowBigValue(expr, 'Ошибка', String(e.message)); });
}

function dbgShowBigValue(name, type, value) {
  var t = document.getElementById('dbg-val-modal-title');
  if (t) t.textContent = String(name) + (type ? '  ·  ' + type : '');
  var ta = document.getElementById('dbg-val-modal-text');
  if (ta) { ta.value = (value === null || value === undefined) ? '' : String(value); ta.scrollTop = 0; }
  var m = document.getElementById('dbg-val-modal');
  if (m) m.classList.add('active');
}
function dbgValModalClose() {
  var m = document.getElementById('dbg-val-modal');
  if (m) m.classList.remove('active');
}
function dbgValModalCopy() {
  var ta = document.getElementById('dbg-val-modal-text');
  if (!ta) return;
  ta.focus(); ta.select();
  try { document.execCommand('copy'); } catch(e){}
  if (navigator.clipboard) { try { navigator.clipboard.writeText(ta.value); } catch(e){} }
}

function dbgHighlightLine(file, line) {
  dbgClearLineHighlight();
  var ed = dbgFindEditor(file);
  if (ed) dbgApplyHighlight(ed, line);
}

function dbgClearLineHighlight() {
  Object.keys(_dbgCurrentLineDecos).forEach(function(id){
    var ed = monacoEditors[id];
    if (ed) ed.deltaDecorations(_dbgCurrentLineDecos[id], []);
  });
  _dbgCurrentLineDecos = {};
}

function dbgShowLocation(file, line) {
  dbgClearLineHighlight();
  var edKeys = Object.keys(monacoEditors);
  var ed = dbgFindEditor(file);
  if (ed) {
    dbgApplyHighlight(ed, line);
    _dbgHighlightLog = file + ':' + line + ' OK ed=' + ed._fileId;
    return;
  }
  _dbgHighlightLog = 'NO_ED "' + file + '" keys=[' + edKeys.join(',') + ']';
  // 2. Editor not created — activate the module pane and create editor
  var modId = dbgResolveModulePane(file);
  if (modId) {
    var pane = document.getElementById(modId);
    if (pane && !pane.classList.contains('active')) {
      var wrap = pane.closest('.module-editor-wrap');
      if (wrap) {
        wrap.querySelectorAll('.module-tab').forEach(function(t){t.classList.remove('active')});
        wrap.querySelectorAll('.module-pane').forEach(function(p){p.classList.remove('active')});
      }
      pane.classList.add('active');
      if (wrap) {
        wrap.querySelectorAll('.module-tab').forEach(function(t){
          if (t.getAttribute('onclick') && t.getAttribute('onclick').indexOf(modId) !== -1) {
            t.classList.add('active');
          }
        });
      }
    }
    var pre = pane ? pane.querySelector('pre.os-code') : null;
    if (pre) {
      var editorName = pre.id.replace('pre-', '');
      startEdit(editorName);
      setTimeout(function(){
        var ed2 = monacoEditors[editorName];
        if (ed2) {
          dbgApplyHighlight(ed2, line);
          _dbgHighlightLog = 'LATE ' + editorName + ':' + line;
        } else {
          _dbgHighlightLog = 'NO_ED2 ' + editorName;
        }
      }, 300);
    } else {
      _dbgHighlightLog += ' noPre modId=' + modId;
    }
  }
}

function dbgApplyHighlight(ed, line) {
  var deco = ed.deltaDecorations([], [{
    range: new monaco.Range(line, 1, line, 1),
    options: {
      isWholeLine: true,
      className: 'dbg-current-line-bg',
      glyphMarginClassName: 'dbg-current-line-glyph',
      overviewRuler: { color: '#d97706', position: monaco.editor.OverviewRulerLane.Full }
    }
  }]);
  _dbgCurrentLineDecos[ed._fileId] = deco;
  ed.revealLineInCenter(line);
}

// dbgResolveModulePane maps a normalized file ID like "post-ПоступлениеТоваров"
// to the DOM pane ID like "mp-post-ПоступлениеТоваров"
// Uses case-insensitive DOM lookup since normalizeFilePath lowercases.
function dbgResolveModulePane(file) {
  var prefixes = ['post-', 'mod-', 'proc-', 'rep-', 'pf-'];
  var candidate = null;
  for (var i = 0; i < prefixes.length; i++) {
    if (file.toLowerCase().indexOf(prefixes[i]) === 0) {
      candidate = 'mp-' + file;
      break;
    }
  }
  if (!candidate) candidate = 'mp-obj-' + file;
  // Try exact match first, then case-insensitive search
  if (document.getElementById(candidate)) return candidate;
  var allPanes = document.querySelectorAll('[id^="mp-"]');
  var cLow = candidate.toLowerCase();
  for (var j = 0; j < allPanes.length; j++) {
    if (allPanes[j].id.toLowerCase() === cLow) return allPanes[j].id;
  }
  return candidate;
}

function dbgFindEditor(file) {
  // file from server is normalizeFilePath'd (lowercased + capitalizeFirst)
  // Monaco editor ids preserve original casing, so we need case-insensitive lookup
  var fileLow = file.toLowerCase();
  var keys = Object.keys(monacoEditors);
  for (var i = 0; i < keys.length; i++) {
    if (keys[i].toLowerCase() === fileLow) return monacoEditors[keys[i]];
  }
  // Fallback: try prefixed variants
  var prefixes = ['post-', 'mod-', 'proc-', 'rep-', 'pf-'];
  for (var p = 0; p < prefixes.length; p++) {
    var candidate = prefixes[p] + fileLow;
    for (var j = 0; j < keys.length; j++) {
      if (keys[j].toLowerCase() === candidate) return monacoEditors[keys[j]];
    }
  }
  return null;
}

function dbgFindTabId(file) {
  // Same candidate logic but searches tab data-file-id attributes
  var candidates = [file, 'post-' + file, 'mod-' + file, 'proc-' + file, 'rep-' + file];
  for (var i = 0; i < candidates.length; i++) {
    var tab = document.querySelector('[data-file-id="' + candidates[i] + '"]');
    if (tab) return candidates[i];
  }
  // Try matching by suffix (server may send full path, tabs have short IDs)
  var tabs = document.querySelectorAll('[data-file-id]');
  for (var t = 0; t < tabs.length; t++) {
    var tid = tabs[t].getAttribute('data-file-id');
    if (file.indexOf(tid) !== -1 || tid.indexOf(file) !== -1) return tid;
  }
  return null;
}

function dbgToggleBreakpoint(editorId, line) {
  var diagEl = document.getElementById('dbg-diag');
  if(diagEl) diagEl.innerHTML += '<div style="color:#fbbf24;font-size:10px">BP click: ' + esc(editorId) + ':' + line + ' hasEd=' + !!monacoEditors[editorId] + ' enabled=' + _dbgEnabled + '</div>';
  if (!monacoEditors[editorId]) return;
  var ed = monacoEditors[editorId];
  if (!_dbgBreakpoints[editorId]) _dbgBreakpoints[editorId] = {};
  var has = _dbgBreakpoints[editorId][line];
  if (has) {
    delete _dbgBreakpoints[editorId][line];
  } else {
    _dbgBreakpoints[editorId][line] = true;
  }
  try { dbgRenderBreakpoints(editorId); } catch(e) { console.error('renderBP', e); }
  try { dbgRenderBPList(); } catch(e) { console.error('renderBPList', e); }
  // sync with server
  var diagEl = document.getElementById('dbg-diag');
  if (_dbgEnabled) {
    if(diagEl) diagEl.innerHTML += '<div style="color:#60a5fa;font-size:10px">BP send: ' + esc(editorId) + ':' + line + '</div>';
    fetch('/bases/' + _dbgBase + '/debug/breakpoint', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({file: editorId, line: line, action: 'toggle'})
    }).then(function(r){
      if (!r.ok) { if(diagEl) diagEl.innerHTML += '<div style="color:#ef4444;font-size:10px">BP error: HTTP ' + r.status + '</div>'; return r.text(); }
      return r.json();
    }).then(function(d){
      if(d && d.id) {
        if(diagEl) diagEl.innerHTML += '<div style="color:#16a34a;font-size:10px">BP OK: ' + esc(d.file) + ':' + d.line + ' count=' + (d.bp_count||0) + '</div>';
      }
      else if(d && d.status) { if(diagEl) diagEl.innerHTML += '<div style="color:#fbbf24;font-size:10px">BP: ' + d.status + '</div>'; }
      else if(d && d.error) { if(diagEl) diagEl.innerHTML += '<div style="color:#ef4444;font-size:10px">BP error: ' + d.error + '</div>'; }
    }).catch(function(e){
      if(diagEl) diagEl.innerHTML += '<div style="color:#ef4444;font-size:10px">BP fetch error: ' + e.message + '</div>';
    });
  } else {
    if(diagEl) diagEl.innerHTML += '<div style="color:#fbbf24;font-size:10px">BP saved locally (debug not enabled)</div>';
  }
}

function dbgManualBP() {
  var fileInp = document.getElementById('dbg-bp-file');
  var lineInp = document.getElementById('dbg-bp-line');
  if (!fileInp || !lineInp) return;
  var file = fileInp.value.trim();
  var line = parseInt(lineInp.value);
  if (!file || !line) { dbgWatchDebug('Укажите файл и строку'); return; }
  // Save locally
  if (!_dbgBreakpoints[file]) _dbgBreakpoints[file] = {};
  _dbgBreakpoints[file][line] = true;
  // Also set in Monaco if available
  if (monacoEditors[file]) dbgRenderBreakpoints(file);
  dbgRenderBPList();
  // Send to server
  var diagEl = document.getElementById('dbg-diag');
  if (_dbgEnabled) {
    if(diagEl) diagEl.innerHTML += '<div style="color:#60a5fa;font-size:10px">Manual BP: ' + esc(file) + ':' + line + '</div>';
    fetch('/bases/' + _dbgBase + '/debug/breakpoint', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({file: file, line: line, action: 'set'})
    }).then(function(r){ return r.json(); }).then(function(d){
      if(d && d.id) {
        if(diagEl) diagEl.innerHTML += '<div style="color:#16a34a;font-size:10px">BP OK: ' + esc(d.file) + ':' + d.line + ' count=' + (d.bp_count||0) + '</div>';
      } else if(d && d.error) {
        if(diagEl) diagEl.innerHTML += '<div style="color:#ef4444;font-size:10px">BP err: ' + d.error + '</div>';
      }
    }).catch(function(e){
      if(diagEl) diagEl.innerHTML += '<div style="color:#ef4444;font-size:10px">BP fetch err: ' + e.message + '</div>';
    });
  } else {
    dbgWatchDebug('BP saved locally (debug not enabled)');
  }
  fileInp.value = '';
  lineInp.value = '';
}

function dbgRenderBPList() {
  var el = document.getElementById('dbg-bp-list');
  if (!el) return;
  var keys = Object.keys(_dbgBreakpoints);
  var h = '';
  keys.forEach(function(file){
    var lines = Object.keys(_dbgBreakpoints[file]);
    lines.forEach(function(ln){
      h += '<div class="dbg-bp-row" style="cursor:pointer" onclick="dbgGoToBP(\'' + esc(file).replace(/'/g,"\\'") + '\',' + ln + ')">'
        + '<span style="color:#ef4444">&#9679;</span>'
        + '<span class="bp-file">' + esc(file) + '</span>'
        + '<span class="bp-line">:' + ln + '</span></div>';
    });
  });
  if (!h) h = '<div class="dbg-empty">Нет точек останова</div>';
  el.innerHTML = h;
}
function dbgGoToBP(file, line) {
  // Open the editor tab if available
  var tab = document.querySelector('[data-file-id="' + file + '"]');
  if (tab) { selItem(tab); tab.scrollIntoView(); }
  // Activate Monaco and scroll to line
  var pre = document.getElementById('pre-' + file);
  if (pre && !pre.querySelector('.monaco-target')) { startEdit(file); }
  setTimeout(function(){
    var ed = monacoEditors[file];
    if (ed) { ed.revealLineInCenter(line); ed.setPosition({lineNumber:line,column:1}); ed.focus(); }
  }, 200);
}

function dbgRenderBreakpoints(editorId) {
  var ed = monacoEditors[editorId];
  if (!ed) return;
  var bps = _dbgBreakpoints[editorId] || {};
  var decos = [];
  Object.keys(bps).forEach(function(ln){
    decos.push({
      range: new monaco.Range(parseInt(ln), 1, parseInt(ln), 1),
      options: {
        isWholeLine: false,
        glyphMarginClassName: 'dbg-bp-glyph',
        glyphMarginHoverMessage: {value: 'Точка останова: строка ' + ln},
        stickiness: monaco.editor.TrackedRangeStickiness.NeverGrowsWhenTypingAtEdges
      }
    });
  });
  ed._dbgBpDecos = ed.deltaDecorations(ed._dbgBpDecos || [], decos);
}
</script>
<script>
document.querySelectorAll('details.cfg-tree').forEach(function(d){
  var a=d.querySelector('.tree-toggle');if(!a)return;
  function u(){a.textContent=d.open?'▾':'▸'}
  d.addEventListener('toggle',u);u();
});
</script>
</body></html>
{{end}}`

// ── Main dispatcher ───────────────────────────────────────────────────────────

const cfgMain = `{{define "cfg-main"}}
{{template "cfg-head" .}}
{{if eq .Tab "tree"}}{{template "tab-tree" .}}{{end}}
{{if eq .Tab "convert"}}{{template "tab-convert" .}}{{end}}
{{if eq .Tab "files"}}{{template "tab-files" .}}{{end}}
{{if eq .Tab "backup"}}{{template "tab-backup" .}}{{end}}
{{template "cfg-foot" .}}
<div id="admin-overlay" style="display:none;position:fixed;top:0;left:0;right:0;bottom:0;background:rgba(0,0,0,.4);z-index:9999;align-items:center;justify-content:center;padding:20px" onclick="if(event.target===this)this.style.display='none'"></div>
{{end}}`

// ── Tree tab ──────────────────────────────────────────────────────────────────

const cfgTabTree = `{{define "tab-tree"}}
<div class="cfg-split">

{{/* ── Left panel ── */}}
<div class="cfg-left" id="cfg-sidebar">
<button class="sidebar-toggle" id="sidebar-toggle" onclick="toggleSidebar()" title="{{t $.Lang "Свернуть дерево"}}">◀</button>
  <div style="padding:4px 8px 6px 8px">
    <input id="cfg-tree-search" type="search" placeholder="{{t $.Lang "Поиск метаданных…"}}" autocomplete="off" oninput="filterTree(this.value)"
      style="width:100%;box-sizing:border-box;padding:5px 8px;border:1px solid #ccd0d8;border-radius:4px;font-size:12px;background:#fff;color:#333">
  </div>
  <div class="cfg-group">{{t $.Lang "Конфигурация"}}{{if .ConfigDirty}}<span class="cfg-dirty" title="{{t $.Lang "Конфигурация на диске изменилась с момента запуска базы. Перезапустите базу, чтобы изменения применились."}}">*</span>{{end}}</div>
  <div class="cfg-item" data-id="panel-app" onclick="selItem(this)">
    <span class="ic">⚙</span>{{if .AppName}}{{.AppName}}{{else}}{{t $.Lang "Без названия"}}{{end}}{{if .ConfigDirty}}<span class="cfg-dirty" title="{{t $.Lang "Конфигурация на диске изменилась с момента запуска базы. Перезапустите базу, чтобы изменения применились."}}">*</span>{{end}}
  </div>
  <div class="cfg-item" data-id="home-page" onclick="selItem(this)">
    <span class="ic">🏠</span>Главная страница
  </div>

  <details open class="cfg-tree" data-group="modules"><summary class="cfg-group cfg-group-hd"><span class="tree-toggle">▾</span><span>{{t $.Lang "Общие модули"}}</span><span class="cfg-add-btn" onclick="event.stopPropagation();cfgNewObj('module')" title="{{t $.Lang "Добавить общий модуль"}}">+</span></summary>
  {{range .Modules}}
  <div class="cfg-item" data-id="mod-{{.Name}}" onclick="selItem(this)">
    <span class="ic">📦</span>{{.Name}}
  </div>
  {{end}}
  </details>

  <details open class="cfg-tree" data-group="subsystems"><summary class="cfg-group cfg-group-hd"><span class="tree-toggle">▾</span><span>{{t $.Lang "Подсистемы"}}</span><span class="cfg-add-btn" onclick="event.stopPropagation();cfgNewObj('subsystem')" title="{{t $.Lang "Добавить подсистему"}}">+</span></summary>
  {{range .Subsystems}}
  <div class="cfg-item" data-id="sub-{{.Name}}" onclick="selItem(this)">
    <span class="ic">🗂</span>{{.Title}}
  </div>
  {{end}}
  </details>

  <details open class="cfg-tree" data-group="catalogs"><summary class="cfg-group cfg-group-hd"><span class="tree-toggle">▾</span><span>{{t $.Lang "Справочники"}}</span><span class="cfg-add-btn" onclick="event.stopPropagation();cfgNewObj('catalog')" title="{{t $.Lang "Добавить справочник"}}">+</span></summary>
  {{range .Catalogs}}
  <div class="cfg-item" data-id="e-{{.Name}}" onclick="selItem(this)">
    <span class="ic">📕</span>{{.Name}}
  </div>
  {{end}}
  </details>

  <details open class="cfg-tree" data-group="documents"><summary class="cfg-group cfg-group-hd"><span class="tree-toggle">▾</span><span>{{t $.Lang "Документы"}}</span><span class="cfg-add-btn" onclick="event.stopPropagation();cfgNewObj('document')" title="{{t $.Lang "Добавить документ"}}">+</span></summary>
  {{range .Docs}}
  <div class="cfg-item" data-id="e-{{.Name}}" onclick="selItem(this)">
    <span class="ic">📄</span>{{.Name}}{{if .Posting}}<span class="bp">✓</span>{{end}}
  </div>
  {{end}}
  </details>

  <details open class="cfg-tree" data-group="registers"><summary class="cfg-group cfg-group-hd"><span class="tree-toggle">▾</span><span>{{t $.Lang "Регистры"}}</span><span class="cfg-add-btn" onclick="event.stopPropagation();cfgNewObj('register')" title="{{t $.Lang "Добавить регистр"}}">+</span></summary>
  {{range .Registers}}
  <div class="cfg-item" data-id="r-{{.Name}}" onclick="selItem(this)">
    <span class="ic">📊</span>{{.Name}}
  </div>
  {{end}}
  </details>

  <details open class="cfg-tree" data-group="inforegisters"><summary class="cfg-group cfg-group-hd"><span class="tree-toggle">▾</span><span>{{t $.Lang "Регистры сведений"}}</span><span class="cfg-add-btn" onclick="event.stopPropagation();cfgNewObj('inforeg')" title="{{t $.Lang "Добавить регистр сведений"}}">+</span></summary>
  {{range .InfoRegisters}}
  <div class="cfg-item" data-id="ir-{{.Name}}" onclick="selItem(this)">
    <span class="ic">{{if .Periodic}}⏱{{else}}📋{{end}}</span>{{.Name}}
  </div>
  {{end}}
  </details>

  <details open class="cfg-tree" data-group="accountregisters"><summary class="cfg-group cfg-group-hd"><span class="tree-toggle">▾</span><span>{{t $.Lang "Регистры бухгалтерии"}}</span><span class="cfg-add-btn" onclick="event.stopPropagation();cfgNewObj('accountreg')" title="{{t $.Lang "Добавить регистр бухгалтерии"}}">+</span></summary>
  {{range .AccountRegisters}}
  <div class="cfg-item" data-id="ar-{{.Name}}" onclick="selItem(this)">
    <span class="ic">⚖</span>{{if .Title}}{{.Title}}{{else}}{{.Name}}{{end}}
  </div>
  {{end}}
  </details>

  <details open class="cfg-tree" data-group="enums"><summary class="cfg-group cfg-group-hd"><span class="tree-toggle">▾</span><span>{{t $.Lang "Перечисления"}}</span><span class="cfg-add-btn" onclick="event.stopPropagation();cfgNewObj('enum')" title="{{t $.Lang "Добавить перечисление"}}">+</span></summary>
  {{range .Enums}}
  <div class="cfg-item" data-id="en-{{.Name}}" onclick="selItem(this)">
    <span class="ic">🔢</span>{{.Name}}
  </div>
  {{end}}
  </details>

  <details open class="cfg-tree" data-group="constants"><summary class="cfg-group cfg-group-hd"><span class="tree-toggle">▾</span><span>{{t $.Lang "Константы"}}</span></summary>
  {{range .Constants}}
  <div class="cfg-item" data-id="cn-{{.Name}}" onclick="selItem(this)">
    <span class="ic">🔒</span>{{if .Label}}{{.Label}}{{else}}{{.Name}}{{end}}
  </div>
  {{end}}
  </details>

  <details open class="cfg-tree" data-group="reports"><summary class="cfg-group cfg-group-hd"><span class="tree-toggle">▾</span><span>{{t $.Lang "Отчёты"}}</span></summary>
  {{range .Reports}}
  <div class="cfg-item" data-id="rep-{{.Name}}" onclick="selItem(this)">
    <span class="ic">📈</span>{{if .Title}}{{.Title}}{{else}}{{.Name}}{{end}}
  </div>
  {{end}}
  </details>

  <details open class="cfg-tree" data-group="processors"><summary class="cfg-group cfg-group-hd"><span class="tree-toggle">▾</span><span>{{t $.Lang "Обработки"}}</span><span class="cfg-add-btn" onclick="event.stopPropagation();cfgNewObj('processor')" title="{{t $.Lang "Добавить обработку"}}">+</span></summary>
  {{range .Processors}}
  <div class="cfg-item" data-id="proc-{{.Name}}" onclick="selItem(this)">
    <span class="ic">⚙</span>{{if .Title}}{{.Title}}{{else}}{{.Name}}{{end}}
  </div>
  {{end}}
  </details>

  <details open class="cfg-tree" data-gid="printforms"><summary class="cfg-group cfg-group-hd"><span class="tree-toggle">▾</span><span>{{t $.Lang "Печатные формы"}}</span><span class="cfg-add-btn" onclick="event.stopPropagation();cfgNewObj('printform')" title="{{t $.Lang "Добавить печатную форму"}}">+</span></summary>
  {{range .PrintForms}}
  <div class="cfg-item{{if .Shadowed}} cfg-item-shadowed{{end}}" data-id="pf-{{.Name}}" onclick="selItem(this)"{{if .Shadowed}} title="Эту YAML-форму перебивает одноимённая .os — в runtime используется DSL-вариант (см. замечание #10)"{{end}}>
    <span class="ic">🖨</span>{{if .Shadowed}}<span style="color:#d97706" title="Перебивается .os">⚠️ </span>{{end}}{{.Name}}<span style="color:#aaa;font-size:10px;margin-left:4px">→{{.Document}}{{if .Shadowed}} (скрыта .os){{end}}</span>
  </div>
  {{end}}
  {{range .DSLPrintForms}}
  <div class="cfg-item" data-id="dpf-{{.Name}}" onclick="selItem(this)"{{if .Overrides}} title="Перебивает одноимённую YAML-форму у этого документа"{{end}}>
    <span class="ic">📋</span>{{.Name}}<span style="color:#aaa;font-size:10px;margin-left:4px">→{{.Document}} (DSL{{if .Overrides}}, перебивает YAML{{end}})</span>
  </div>
  {{if .HasLayout}}
  <div class="cfg-item cfg-sub" data-id="mkt-{{.Name}}" onclick="selItem(this)" style="padding-left:32px">
    <span class="ic" style="font-size:12px">&#x1F4D0;</span>{{t $.Lang "Макет"}} {{.Name}}
  </div>
  {{end}}
  {{end}}
  </details>

  <details open class="cfg-tree" data-gid="managedforms">
    <summary class="cfg-group cfg-group-hd">
      <span class="tree-toggle">▾</span><span><a href="/bases/{{.Base.ID}}/configurator/forms" style="color:inherit;text-decoration:none" title="{{t $.Lang "Все управляемые формы"}}">◇ {{t $.Lang "Управляемые формы"}}</a></span>
    </summary>
    {{range .ManagedForms}}
    <div class="cfg-item">
      <a href="/bases/{{$.Base.ID}}/configurator/forms/edit?entity={{.Entity}}&name={{.Name}}" style="color:inherit;text-decoration:none;display:block">
        <span class="ic">◇</span>{{.Entity}} · {{formLabel .Name}}<span style="color:#aaa;font-size:10px;margin-left:4px">{{.Kind}}</span>
      </a>
    </div>
    {{end}}
  </details>

  <details open class="cfg-tree" data-group="widgets"><summary class="cfg-group cfg-group-hd"><span class="tree-toggle">▾</span><span>{{t $.Lang "Виджеты"}}</span><span class="cfg-add-btn" onclick="event.stopPropagation();cfgNewObj('widget')" title="{{t $.Lang "Добавить виджет"}}">+</span></summary>
  {{range .Widgets}}
  <div class="cfg-item" data-id="wdg-{{.Name}}" onclick="selItem(this)">
    <span class="ic">🧩</span>{{if .Title}}{{.Title}}{{else}}{{.Name}}{{end}}<span style="color:#aaa;font-size:10px;margin-left:4px">[{{.Type}}]</span>
  </div>
  {{end}}
  </details>

  <div id="cfg-new-form" class="cfg-new-form" style="display:none">
    <div id="cfg-new-title" style="font-size:11px;font-weight:700;color:#555;margin-bottom:6px;text-transform:uppercase;letter-spacing:.3px"></div>
    <form method="POST" action="/bases/{{$.Base.ID}}/configurator/new">
      <input type="hidden" name="kind" id="cfg-new-kind-inp" value="">
      <input type="text" name="name" id="cfg-new-name" placeholder="{{t $.Lang "Имя объекта"}}" autocomplete="off">
      <div class="row">
        <button type="submit" class="btn-create">{{t $.Lang "Создать"}}</button>
        <button type="button" class="btn-cancel" onclick="cfgHideNew()">✕</button>
      </div>
    </form>
  </div>
  <div id="cfg-new-form-pf" class="cfg-new-form" style="display:none">
    <div style="font-size:11px;font-weight:700;color:#555;margin-bottom:6px;text-transform:uppercase;letter-spacing:.3px">{{t $.Lang "Новая печатная форма"}}</div>
    <form method="POST" action="/bases/{{$.Base.ID}}/configurator/new-printform">
      <input type="text" name="name" id="cfg-new-pf-name" placeholder="{{t $.Lang "Имя формы"}} (напр. СчётНаОплату)" autocomplete="off">
      <select name="document" style="width:100%;padding:5px 6px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px;margin-bottom:6px">
        <option value="">{{t $.Lang "— документ/справочник —"}}</option>
        {{range $.AllEntityNames}}<option value="{{.}}">{{.}}</option>{{end}}
      </select>
      <div class="row">
        <button type="submit" class="btn-create">{{t $.Lang "Создать"}}</button>
        <button type="button" class="btn-cancel" onclick="cfgHideNew()">✕</button>
      </div>
    </form>
  </div>
  <div style="margin-top:12px;padding:8px 12px;border-top:1px solid #d8dde8">
    <button onclick="runCheckAll()" id="btn-check-all" style="width:100%;padding:7px 10px;background:#fff;border:1px solid #1a4a80;color:#1a4a80;border-radius:4px;cursor:pointer;font-size:12px;margin-bottom:6px">{{t $.Lang "Проверить конфигурацию"}}</button>
    <button onclick="runMigrate()" id="btn-migrate" style="width:100%;padding:7px 10px;background:#1a4a80;color:#fff;border:none;border-radius:4px;cursor:pointer;font-size:12px">{{t $.Lang "Обновить БД"}}</button>
    <div id="migrate-result" style="display:none;margin-top:6px;font-size:11px;padding:6px;border-radius:3px;max-height:120px;overflow-y:auto"></div>
  </div>
</div>

<div id="check-all-panel">
  <header>
    <span>{{t $.Lang "Проверка конфигурации"}}</span>
    <button type="button" onclick="closeCheckAll()" title="{{t $.Lang "Закрыть"}}">✕</button>
  </header>
  <div id="check-all-body"></div>
</div>

{{/* ── Right panel ── */}}
<div class="cfg-right">

  {{/* Admin panel (loaded via AJAX) */}}
  <div class="cfg-panel" id="panel-admin" style="overflow-y:auto"></div>

  {{/* App config */}}
  <div class="cfg-panel" id="panel-app">
    <div class="panel-title">⚙ {{t $.Lang "Конфигурация"}}</div>
    <div class="panel-kind">{{t $.Lang "Общие параметры приложения"}}</div>
    <form method="POST" action="/bases/{{.Base.ID}}/configurator/app" enctype="multipart/form-data" style="margin-top:12px">
      <div class="fg">
        <label>{{t $.Lang "Название конфигурации"}}</label>
        <input type="text" name="app_name" value="{{.AppName}}" placeholder="{{t $.Lang "Моя конфигурация"}}" autofocus>
        <div class="hint">{{t $.Lang "Отображается в заголовке окна и навигации пользовательского режима"}}</div>
      </div>
      <div class="fg" style="margin-top:10px">
        <label>{{t $.Lang "Версия"}}</label>
        <input type="text" name="app_version" value="{{.AppVersion}}" placeholder="1.0">
      </div>
      <div class="fg" style="margin-top:10px">
        <label>{{t $.Lang "Язык интерфейса"}}</label>
        <select name="app_lang" style="padding:6px 8px;border:1px solid #d0d7e3;border-radius:4px;font-size:13px">
          <option value="">{{t $.Lang "По умолчанию (русский)"}}</option>
          {{range .AvailableLangs}}<option value="{{.Code}}"{{selIf $.AppLang .Code}}>{{.Native}}</option>{{end}}
        </select>
        <div class="hint">{{t $.Lang "Язык по умолчанию для пользователей этой базы"}}</div>
      </div>
      <div class="fg" style="margin-top:10px">
        <label>{{t $.Lang "Логотип"}}</label>
        <div style="display:flex;align-items:center;gap:12px;margin-bottom:6px">
          <img id="logo-preview" src="{{if .AppLogo}}/bases/{{.Base.ID}}/configurator/logo{{end}}" style="max-height:48px;max-width:120px;border:1px solid #e2e8f0;border-radius:4px;padding:2px;{{if not .AppLogo}}display:none{{end}}">
          <div>
            <label class="btn-save" style="cursor:pointer;display:inline-block;padding:4px 12px;font-size:12px">
              Загрузить
              <input type="file" name="app_logo_file" accept="image/*" style="display:none" onchange="previewLogo(this)">
            </label>
            {{if .AppLogo}}<button type="button" onclick="removeLogo()" style="margin-left:6px;padding:4px 8px;font-size:12px;background:none;border:1px solid #e2e8f0;border-radius:4px;cursor:pointer;color:#ef4444">{{t $.Lang "Удалить"}}</button>{{end}}
          </div>
        </div>
        <input type="hidden" name="app_logo_existing" value="{{.AppLogo}}">
        <input type="hidden" name="app_logo_remove" id="logo-remove" value="0">
        <div class="hint">{{t $.Lang "PNG, SVG, JPG — не более 2 МБ"}}</div>
      </div>
      <div class="module-save-row" style="margin-top:12px">
        <button class="btn-save" type="submit">{{t $.Lang "Сохранить"}}</button>
        {{if and .FieldsSaved (eq .FieldsSavedEntity "__app__")}}<span class="save-ok">{{t $.Lang "✓ Сохранено — перезапустите базу"}}</span>{{end}}
      </div>
    </form>
  </div>

  {{if not (or .Catalogs .Docs .Registers .InfoRegisters .Enums .Constants .Reports)}}
  <div style="color:#aaa;padding:60px 20px;text-align:center">
    <div style="font-size:36px;margin-bottom:10px">📭</div>
    <div>{{t $.Lang "Используйте «+» слева для добавления объектов конфигурации."}}</div>
  </div>
  {{end}}

  {{/* Catalogs */}}
  {{range .Catalogs}}
  <div class="cfg-panel" id="e-{{.Name}}">
    <div class="panel-title">📄 {{.Name}}</div>
    <div class="panel-kind">{{t $.Lang "Справочник"}}</div>
    {{template "entity-detail" (dict "Entity" . "BaseID" $.Base.ID "ConfigSource" $.Base.ConfigSource "ModuleSaved" $.ModuleSaved "ModuleSavedEntity" $.ModuleSavedEntity "AllEntityNames" $.AllEntityNames "AllEnumNames" $.AllEnumNames "FieldsSaved" $.FieldsSaved "FieldsSavedEntity" $.FieldsSavedEntity "ManagedForms" $.ManagedForms "Lang" $.Lang)}}
  </div>
  {{end}}

  {{/* Documents */}}
  {{range .Docs}}
  <div class="cfg-panel" id="e-{{.Name}}">
    <div class="panel-title">
      📃 {{.Name}}
      {{if .Posting}}<span style="background:#dbeafe;color:#1d4ed8;font-size:11px;font-weight:600;padding:2px 8px;border-radius:10px">{{t $.Lang "проводится"}}</span>{{end}}
    </div>
    <div class="panel-kind">{{t $.Lang "Документ"}}</div>
    {{template "entity-detail" (dict "Entity" . "BaseID" $.Base.ID "ConfigSource" $.Base.ConfigSource "ModuleSaved" $.ModuleSaved "ModuleSavedEntity" $.ModuleSavedEntity "AllEntityNames" $.AllEntityNames "AllEnumNames" $.AllEnumNames "FieldsSaved" $.FieldsSaved "FieldsSavedEntity" $.FieldsSavedEntity "ManagedForms" $.ManagedForms "Lang" $.Lang)}}
  </div>
  {{end}}

  {{/* Registers */}}
  {{range .Registers}}
  <div class="cfg-panel" id="r-{{.Name}}">
    <div class="panel-title">📊 {{.Name}}</div>
    <div class="panel-kind">{{t $.Lang "Регистр накопления"}}</div>
    {{template "register-detail" (dict "Register" . "BaseID" $.Base.ID "AllEntityNames" $.AllEntityNames "FieldsSaved" $.FieldsSaved "FieldsSavedEntity" $.FieldsSavedEntity "Lang" $.Lang)}}
  </div>
  {{end}}

  {{/* InfoRegisters */}}
  {{range .InfoRegisters}}
  {{$ir := .}}
  <div class="cfg-panel" id="ir-{{.Name}}">
    <div class="panel-title">{{if .Periodic}}⏱{{else}}📋{{end}} {{.Name}}</div>
    <div class="panel-kind">{{t $.Lang "Регистр сведений"}}</div>
    <form method="POST" action="/bases/{{$.Base.ID}}/configurator/inforeg-fields">
    <input type="hidden" name="inforeg" value="{{.Name}}">
    <div style="margin:10px 0 12px">
      <label style="display:flex;align-items:center;gap:8px;font-size:13px;cursor:pointer">
        <input type="radio" name="periodic" value="true" {{if .Periodic}}checked{{end}}> {{t $.Lang "Периодический (ключ включает период)"}}
      </label>
      <label style="display:flex;align-items:center;gap:8px;font-size:13px;cursor:pointer;margin-top:4px">
        <input type="radio" name="periodic" value="false" {{if not .Periodic}}checked{{end}}> {{t $.Lang "Непериодический"}}
      </label>
    </div>
    {{$allEntities := $.AllEntityNames}}
    {{if .Dimensions}}
    <details open><summary class="section-hd" style="cursor:pointer">{{t $.Lang "Измерения"}} ({{len .Dimensions}})</summary>
    <table class="fields-tbl" id="ir-dim-{{.Name}}">
    <tr><th>{{t $.Lang "Поле"}}</th><th>{{t $.Lang "Тип"}}</th><th style="min-width:150px">{{t $.Lang "Объект"}}</th></tr>
    {{range $i, $f := .Dimensions}}
    <input type="hidden" name="dim.{{$i}}.name" value="{{$f.Name}}">
    <tr>
      <td>{{$f.Name}}</td>
      <td>
        <select name="dim.{{$i}}.type" onchange="cfgToggleRef(this,'irdr-{{$ir.Name}}-{{$i}}')">
          <option value="string"    {{if eq $f.Type "string"}}selected{{end}}>{{t $.Lang "строка"}}</option>
          <option value="number"    {{if eq $f.Type "number"}}selected{{end}}>{{t $.Lang "число"}}</option>
          <option value="date"      {{if eq $f.Type "date"}}selected{{end}}>{{t $.Lang "дата"}}</option>
          <option value="bool"      {{if eq $f.Type "bool"}}selected{{end}}>{{t $.Lang "булево"}}</option>
          <option value="reference" {{if eq $f.Type "reference"}}selected{{end}}>{{t $.Lang "ссылка →"}}</option>
        </select>
      </td>
      <td>
        <select name="dim.{{$i}}.ref" id="irdr-{{$ir.Name}}-{{$i}}"{{if ne $f.Type "reference"}} style="display:none"{{end}}>
          <option value="">{{t $.Lang "— выбрать —"}}</option>
          {{range $allEntities}}<option value="{{.}}"{{if eq . $f.RefEntity}} selected{{end}}>{{.}}</option>{{end}}
        </select>
      </td>
    </tr>
    {{end}}
    </table>
    <button type="button" onclick="cfgAddField('ir-dim-{{.Name}}','new_dim','')" style="font-size:11px;color:#1a4a80;background:none;border:1px dashed #c0c8d8;padding:2px 8px;border-radius:3px;cursor:pointer;margin:4px 0">+ {{t $.Lang "Добавить измерение"}}</button>
    </details>
    {{end}}
    {{if .Resources}}
    <details open><summary class="section-hd" style="cursor:pointer;margin-top:8px">{{t $.Lang "Ресурсы"}} ({{len .Resources}})</summary>
    <table class="fields-tbl" id="ir-res-{{.Name}}">
    <tr><th>{{t $.Lang "Поле"}}</th><th>{{t $.Lang "Тип"}}</th><th style="min-width:150px">{{t $.Lang "Объект"}}</th></tr>
    {{range $i, $f := .Resources}}
    <input type="hidden" name="res.{{$i}}.name" value="{{$f.Name}}">
    <tr>
      <td>{{$f.Name}}</td>
      <td>
        <select name="res.{{$i}}.type" onchange="cfgToggleRef(this,'irrr-{{$ir.Name}}-{{$i}}')">
          <option value="string"    {{if eq $f.Type "string"}}selected{{end}}>{{t $.Lang "строка"}}</option>
          <option value="number"    {{if eq $f.Type "number"}}selected{{end}}>{{t $.Lang "число"}}</option>
          <option value="date"      {{if eq $f.Type "date"}}selected{{end}}>{{t $.Lang "дата"}}</option>
          <option value="bool"      {{if eq $f.Type "bool"}}selected{{end}}>{{t $.Lang "булево"}}</option>
          <option value="reference" {{if eq $f.Type "reference"}}selected{{end}}>{{t $.Lang "ссылка →"}}</option>
        </select>
      </td>
      <td>
        <select name="res.{{$i}}.ref" id="irrr-{{$ir.Name}}-{{$i}}"{{if ne $f.Type "reference"}} style="display:none"{{end}}>
          <option value="">{{t $.Lang "— выбрать —"}}</option>
          {{range $allEntities}}<option value="{{.}}"{{if eq . $f.RefEntity}} selected{{end}}>{{.}}</option>{{end}}
        </select>
      </td>
    </tr>
    {{end}}
    </table>
    <button type="button" onclick="cfgAddField('ir-res-{{.Name}}','new_res','')" style="font-size:11px;color:#1a4a80;background:none;border:1px dashed #c0c8d8;padding:2px 8px;border-radius:3px;cursor:pointer;margin:4px 0">+ {{t $.Lang "Добавить ресурс"}}</button>
    </details>
    {{end}}
    <div class="module-save-row" style="margin-bottom:14px;margin-top:10px">
      <button class="btn-save" type="submit">{{t $.Lang "Сохранить"}}</button>
      {{if and $.FieldsSaved (eq $.FieldsSavedEntity .Name)}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
    </div>
    </form>
  </div>
  {{end}}

  {{/* AccountRegisters */}}
  {{range .AccountRegisters}}
  {{$ar := .}}
  <div class="cfg-panel" id="ar-{{.Name}}">
    <div class="panel-title">⚖ {{if .Title}}{{.Title}}{{else}}{{.Name}}{{end}}</div>
    <div class="panel-kind">{{t $.Lang "Регистр бухгалтерии"}}</div>
    <form method="POST" action="/bases/{{$.Base.ID}}/configurator/account-register">
    <input type="hidden" name="accountreg" value="{{.Name}}">
    <div class="fg" style="margin-bottom:10px">
      <label>{{t $.Lang "Заголовок"}}</label>
      <input type="text" name="title" value="{{.Title}}" placeholder="{{t $.Lang "Отображаемое имя"}}">
    </div>
    <div class="fg" style="margin-bottom:12px">
      <label>{{t $.Lang "План счетов (имя объекта)"}}</label>
      <input type="text" name="accounts" value="{{.Accounts}}" placeholder="{{t $.Lang "ПланСчетов"}}">
    </div>
    {{$allEntities := $.AllEntityNames}}
    {{if .Resources}}
    <details open><summary class="section-hd" style="cursor:pointer">{{t $.Lang "Ресурсы"}} ({{len .Resources}})</summary>
    <table class="fields-tbl" id="ar-res-{{.Name}}">
    <tr><th>{{t $.Lang "Поле"}}</th><th>{{t $.Lang "Тип"}}</th></tr>
    {{range $i, $f := .Resources}}
    <input type="hidden" name="res.{{$i}}.name" value="{{$f.Name}}">
    <tr>
      <td>{{$f.Name}}</td>
      <td>
        <select name="res.{{$i}}.type">
          <option value="number" {{if eq $f.Type "number"}}selected{{end}}>{{t $.Lang "число"}}</option>
          <option value="string" {{if eq $f.Type "string"}}selected{{end}}>{{t $.Lang "строка"}}</option>
          <option value="bool"   {{if eq $f.Type "bool"}}selected{{end}}>{{t $.Lang "булево"}}</option>
        </select>
      </td>
    </tr>
    {{end}}
    </table>
    <button type="button" onclick="cfgAddARField('ar-res-{{.Name}}')" style="font-size:11px;color:#1a4a80;background:none;border:1px dashed #c0c8d8;padding:2px 8px;border-radius:3px;cursor:pointer;margin:4px 0">+ {{t $.Lang "Добавить ресурс"}}</button>
    </details>
    {{else}}
    <div id="ar-res-{{.Name}}-wrap">
    <table class="fields-tbl" id="ar-res-{{.Name}}" style="display:none"><tr><th>{{t $.Lang "Поле"}}</th><th>{{t $.Lang "Тип"}}</th></tr></table>
    </div>
    <button type="button" onclick="cfgAddARField('ar-res-{{.Name}}')" style="font-size:11px;color:#1a4a80;background:none;border:1px dashed #c0c8d8;padding:2px 8px;border-radius:3px;cursor:pointer;margin:4px 0">+ {{t $.Lang "Добавить ресурс"}}</button>
    {{end}}
    <div class="module-save-row" style="margin-bottom:14px;margin-top:10px">
      <button class="btn-save" type="submit">{{t $.Lang "Сохранить"}}</button>
      {{if and $.FieldsSaved (eq $.FieldsSavedEntity .Name)}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
    </div>
    </form>
  </div>
  {{end}}

  {{/* Enums */}}
  {{range .Enums}}
  <div class="cfg-panel" id="en-{{.Name}}">
    <div class="panel-title">🔢 {{.Name}}</div>
    <div class="panel-kind">{{t $.Lang "Перечисление"}}</div>
    <div class="section-hd">{{t $.Lang "Значения"}} <span class="edit-hint">({{t $.Lang "каждое значение — отдельная строка"}})</span></div>
    <form method="POST" action="/bases/{{$.Base.ID}}/configurator/enum">
      <input type="hidden" name="enum_name" value="{{.Name}}">
      <textarea name="values" rows="8" style="width:100%;font-size:13px;padding:6px 8px;border:1px solid #cbd5e1;border-radius:4px;resize:vertical;font-family:inherit">{{range .Values}}{{.}}&#10;{{end}}</textarea>
      <div class="module-save-row">
        <button class="btn-save" type="submit">{{t $.Lang "Сохранить"}}</button>
        {{if and $.FieldsSaved (eq $.FieldsSavedEntity .Name)}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
      </div>
    </form>
  </div>
  {{end}}

  {{/* Constants */}}
  {{range .Constants}}
  {{$cn := .}}
  <div class="cfg-panel" id="cn-{{.Name}}">
    <div class="panel-title">🔒 {{if .Label}}{{.Label}}{{else}}{{.Name}}{{end}}</div>
    <div class="panel-kind">{{t $.Lang "Константа"}} · <span class="{{fieldTypeClass .Type}}">{{fieldTypeLabel .Type .RefEntity}}</span></div>
    <form method="POST" action="/bases/{{$.Base.ID}}/configurator/constant" style="margin-top:12px">
      <input type="hidden" name="const_name" value="{{.Name}}">
      <div class="fg">
        <label>{{t $.Lang "Заголовок"}}</label>
        <input type="text" name="label" value="{{.Label}}" placeholder="{{t $.Lang "Отображаемое имя"}}">
      </div>
      <div class="fg" style="margin-top:8px">
        <label>{{t $.Lang "Тип"}}</label>
        <select name="type" onchange="cfgToggleRef(this,'cnref-{{.Name}}')">
          <option value="string" {{if eq .Type "string"}}selected{{end}}>{{t $.Lang "Строка"}}</option>
          <option value="number" {{if eq .Type "number"}}selected{{end}}>{{t $.Lang "Число"}}</option>
          <option value="date" {{if eq .Type "date"}}selected{{end}}>{{t $.Lang "Дата"}}</option>
          <option value="boolean" {{if eq .Type "boolean"}}selected{{end}}>{{t $.Lang "Булево"}}</option>
          <option value="reference" {{if eq .Type "reference"}}selected{{end}}>{{t $.Lang "Ссылка"}}</option>
        </select>
      </div>
      <div id="cnref-{{.Name}}" class="fg" style="margin-top:8px;{{if ne .Type "reference"}}display:none{{end}}">
        <label>{{t $.Lang "Объект"}}</label>
        <select name="ref">
          <option value="">{{t $.Lang "— выбрать —"}}</option>
          {{range $.AllEntityNames}}<option value="{{.}}" {{if eq . $cn.RefEntity}}selected{{end}}>{{.}}</option>{{end}}
        </select>
      </div>
      <div class="fg" style="margin-top:8px">
        <label>{{t $.Lang "По умолчанию"}}</label>
        <input type="text" name="default" value="{{.Default}}" placeholder="{{t $.Lang "Значение по умолчанию"}}">
      </div>
      <div class="module-save-row">
        <button class="btn-save" type="submit">{{t $.Lang "Сохранить"}}</button>
        {{if and $.FieldsSaved (eq $.FieldsSavedEntity .Name)}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
      </div>
    </form>
  </div>
  {{end}}

  {{/* Reports */}}
  {{range .Reports}}
  {{$rn := .Name}}
  <div class="cfg-panel" id="rep-{{.Name}}">
    <div class="panel-title">📈 {{if .Title}}{{.Title}}{{else}}{{.Name}}{{end}}</div>
    <div class="panel-kind">{{t $.Lang "Отчёт"}}</div>
    <form method="POST" action="/bases/{{$.Base.ID}}/configurator/report">
      <input type="hidden" name="report_name" value="{{.Name}}">
      <div class="fg" style="margin-top:8px">
        <label>{{t $.Lang "Заголовок"}}</label>
        <input type="text" name="title" value="{{.Title}}" placeholder="{{t $.Lang "Название отчёта"}}">
      </div>
      <div class="section-hd" style="margin-top:12px">
        Параметры
        <button type="button" class="cfg-add-btn" style="font-size:14px;margin-left:8px" onclick="repAddParam('params-{{$rn}}')">+</button>
      </div>
      <table class="fields-tbl" id="params-{{$rn}}">
        <tr><th>{{t $.Lang "Имя"}} (&amp;{{t $.Lang "Параметр"}})</th><th>{{t $.Lang "Тип"}}</th><th>{{t $.Lang "Заголовок"}}</th><th></th></tr>
        {{range $i, $p := .Params}}
        <tr>
          <td><input type="text" name="param.{{$i}}.name" value="{{$p.Name}}" style="width:100%;padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px"></td>
          <td>
            <select name="param.{{$i}}.type" style="padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px">
              <option value="string" {{if eq $p.Type "string"}}selected{{end}}>{{t $.Lang "строка"}}</option>
              <option value="date"   {{if eq $p.Type "date"}}selected{{end}}>{{t $.Lang "дата"}}</option>
              <option value="number" {{if eq $p.Type "number"}}selected{{end}}>{{t $.Lang "число"}}</option>
              <option value="select" {{if eq $p.Type "select"}}selected{{end}}>{{t $.Lang "список"}}</option>
              {{range $.AllEntityNames}}<option value="reference:{{.}}" {{if eq $p.Type (print "reference:" .)}}selected{{end}}>ссылка: {{.}}</option>
              {{end}}
            </select>
          </td>
          <td><input type="text" name="param.{{$i}}.label" value="{{$p.Label}}" placeholder="{{$p.Name}}" style="width:100%;padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px"></td>
          <td><button type="button" style="background:none;border:none;color:#c00;cursor:pointer;font-size:14px" onclick="this.closest('tr').remove();repReindex('params-{{$rn}}')">✕</button></td>
        </tr>
        {{end}}
      </table>
      <div class="section-hd" style="margin-top:12px">{{t $.Lang "Запрос"}}</div>
      <div class="code-wrap" title="{{t $.Lang "Кликните для редактирования"}}">
        <pre class="os-code clickable-code" id="pre-rep-{{.Name}}"
             onclick="startEdit('rep-{{.Name}}')">{{if .Query}}{{.Query}}{{else}}ВЫБРАТЬ&#10;  *&#10;ИЗ РегистрНакопления.ИмяРегистра{{end}}</pre>
        <textarea class="os-edit" id="ta-rep-{{.Name}}" name="query"
                  style="display:none"
                  onblur="endEdit('rep-{{.Name}}')">{{.Query}}</textarea>
      </div>
      <div class="fg" style="margin-top:12px">
        <label>{{t $.Lang "Процедура диаграммы"}} (chart_proc)</label>
        <input type="text" name="chart_proc" value="{{.ChartProc}}" placeholder="СформироватьДиаграмму">
      </div>
      <div class="section-hd" style="margin-top:8px">{{t $.Lang "Код диаграммы"}} (.rep.os) <span class="edit-hint">({{t $.Lang "кликните для редактирования"}})</span></div>
      <div class="code-wrap">
        <pre class="os-code clickable-code" id="pre-repchart-{{.Name}}"
             onclick="startEdit('repchart-{{.Name}}')">{{.ChartSource}}</pre>
        <textarea class="os-edit" id="ta-repchart-{{.Name}}" name="chart_source"
                  style="display:none"
                  onblur="endEdit('repchart-{{.Name}}')">{{.ChartSource}}</textarea>
      </div>
      <div class="module-save-row">
        <button class="btn-save" type="submit">{{t $.Lang "Сохранить"}}</button>
        <button type="button" class="btn-check" onclick="runCheck('dsl','repchart-{{.Name}}','{{.Name}}')">{{t $.Lang "Проверить"}}</button>
        <span class="check-result" id="check-repchart-{{.Name}}"></span>
        {{if and $.FieldsSaved (eq $.FieldsSavedEntity .Name)}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
      </div>
    </form>
  </div>
  {{end}}

  {{/* Modules */}}
  {{range .Modules}}
  {{$mn := .Name}}
  <div class="cfg-panel" id="mod-{{.Name}}">
    <div class="panel-title">📦 {{.Name}}</div>
    <div class="panel-kind">{{t $.Lang "Общий модуль"}}</div>
    <form method="POST" action="/bases/{{$.Base.ID}}/configurator/common-module">
      <input type="hidden" name="module_name" value="{{.Name}}">
      <div class="section-hd">Исходный код <span class="edit-hint">({{t $.Lang "кликните для редактирования"}})</span></div>
      <div class="module-editor-wrap">
        <div class="code-wrap">
          <pre class="os-code" id="pre-mod-{{$mn}}" onclick="startEdit('mod-{{$mn}}')">{{if .Source}}{{.Source}}{{else}}Функция ИмяФункции(Параметр)&#10;    Возврат Параметр&#10;КонецФункции{{end}}</pre>
          <textarea class="os-edit" id="ta-mod-{{$mn}}" name="source"
                    style="display:none"
                    onblur="endEdit('mod-{{$mn}}')">{{.Source}}</textarea>
        </div>
      </div>
      <div class="module-save-row">
        <button class="btn-save" type="submit">{{t $.Lang "Сохранить"}}</button>
        <button type="button" class="btn-check" onclick="runCheck('dsl','mod-{{$mn}}','{{$mn}}')">{{t $.Lang "Проверить"}}</button>
        <span class="check-result" id="check-mod-{{$mn}}"></span>
        {{if and $.ModuleSaved (eq $.ModuleSavedEntity .Name)}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
      </div>
    </form>
  </div>
  {{end}}

  {{/* Processors */}}
  {{range .Processors}}
  {{$pn := .Name}}
  <div class="cfg-panel" id="proc-{{.Name}}">
    <div class="panel-title">⚙ {{if .Title}}{{.Title}}{{else}}{{.Name}}{{end}}</div>
    <div class="panel-kind">{{t $.Lang "Обработка"}}</div>
    <form method="POST" action="/bases/{{$.Base.ID}}/configurator/processor">
      <input type="hidden" name="processor_name" value="{{.Name}}">
      <div class="fg" style="margin-top:8px">
        <label>{{t $.Lang "Заголовок"}}</label>
        <input type="text" name="title" value="{{.Title}}" placeholder="{{t $.Lang "Название обработки"}}">
      </div>
      <div class="section-hd" style="margin-top:12px">
        Параметры
        <button type="button" class="cfg-add-btn" style="font-size:14px;margin-left:8px" onclick="repAddParam('pparams-{{$pn}}')">+</button>
      </div>
      <table class="fields-tbl" id="pparams-{{$pn}}">
        <tr><th>{{t $.Lang "Имя"}} (&amp;{{t $.Lang "Параметры"}}.*)</th><th>{{t $.Lang "Тип"}}</th><th>{{t $.Lang "Заголовок"}}</th><th></th></tr>
        {{range $i, $p := .Params}}
        <tr>
          <td><input type="text" name="param.{{$i}}.name" value="{{$p.Name}}" style="width:100%;padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px"></td>
          <td>
            <select name="param.{{$i}}.type" style="padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px">
              <option value="string" {{if eq $p.Type "string"}}selected{{end}}>{{t $.Lang "строка"}}</option>
              <option value="date"   {{if eq $p.Type "date"}}selected{{end}}>{{t $.Lang "дата"}}</option>
              <option value="number" {{if eq $p.Type "number"}}selected{{end}}>{{t $.Lang "число"}}</option>
            </select>
          </td>
          <td><input type="text" name="param.{{$i}}.label" value="{{$p.Label}}" placeholder="{{$p.Name}}" style="width:100%;padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px"></td>
          <td><button type="button" style="background:none;border:none;color:#c00;cursor:pointer;font-size:14px" onclick="this.closest('tr').remove();repReindex('pparams-{{$pn}}')">✕</button></td>
        </tr>
        {{end}}
      </table>
      <details open><summary class="section-hd" style="cursor:pointer;margin-top:12px">{{t $.Lang "Исходный код"}} ({{t $.Lang "Процедура Выполнить()"}}) <span class="edit-hint">({{t $.Lang "кликните для редактирования"}})</span></summary>
      <div class="code-wrap">
        <pre class="os-code" id="pre-proc-{{$pn}}" onclick="startEdit('proc-{{$pn}}')">{{if .Source}}{{.Source}}{{else}}Процедура Выполнить()&#10;    Сообщить("Привет!")&#10;КонецПроцедуры{{end}}</pre>
        <textarea class="os-edit" id="ta-proc-{{$pn}}" name="source"
                  style="display:none"
                  onblur="endEdit('proc-{{$pn}}')">{{.Source}}</textarea>
      </div>
      <div class="module-save-row">
        <button class="btn-save" type="submit">{{t $.Lang "Сохранить"}}</button>
        <button type="button" class="btn-check" onclick="runCheck('dsl','proc-{{$pn}}','{{$pn}}')">{{t $.Lang "Проверить"}}</button>
        <span class="check-result" id="check-proc-{{$pn}}"></span>
        {{if and $.FieldsSaved (eq $.FieldsSavedEntity .Name)}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
      </div>
      </details>
    </form>
    {{/* Управляемые формы обработки */}}
		{{$procForms := filterFormsByEntity $.ManagedForms .Name}}
    <div class="section-hd" style="margin-top:18px">â {{t $.Lang "Управляемая форма"}}</div>
    <div style="background:#f8fafc;border:1px dashed #c8d4f0;border-radius:6px;padding:12px 14px;font-size:12px;color:#475569;line-height:1.5">
      {{if $procForms}}
      <table style="width:100%;border-collapse:collapse;margin:8px 0;font-size:12px">
        <thead><tr style="background:#fff;border-bottom:1px solid #e2e8f0">
          <th style="text-align:left;padding:4px 8px">{{t $.Lang "Имя"}}</th>
          <th style="text-align:left;padding:4px 8px">{{t $.Lang "Тип"}}</th>
          <th style="text-align:left;padding:4px 8px">{{t $.Lang "Модуль"}}</th>
          <th></th>
        </tr></thead>
        <tbody>
		{{range $procForms}}
        <tr style="border-bottom:1px solid #eef0f5">
          <td style="padding:6px 8px">â {{formLabel .Name}}</td>
          <td style="padding:6px 8px">{{if .Kind}}{{.Kind}}{{else}}â{{end}}</td>
          <td style="padding:6px 8px">{{if .HasOS}}{{t $.Lang "есть"}}{{else}}â{{end}}</td>
          <td style="text-align:right;padding:6px 8px">
            <a href="/bases/{{$.Base.ID}}/configurator/forms/edit?entity={{.Entity}}&name={{.Name}}"
               style="display:inline-block;padding:3px 10px;background:#1a4a80;color:#fff;text-decoration:none;border-radius:4px;font-size:11px">
              {{t $.Lang "Редактировать"}}
            </a>
          </td>
        </tr>
        {{end}}
        </tbody>
      </table>
      {{else}}
      <p style="margin:0 0 10px">{{t $.Lang "У обработки"}} <b>{{.Name}}</b> {{t $.Lang "нет управляемых форм."}}</p>
      {{end}}
      <div style="margin-top:10px;display:flex;gap:6px;flex-wrap:wrap;align-items:center">
		<a href="/bases/{{$.Base.ID}}/configurator/forms/edit?entity={{.Name}}&name=ФормаОбъекта"
           style="display:inline-block;padding:5px 12px;background:#16a34a;color:#fff;text-decoration:none;border-radius:4px;font-size:12px">
          + {{t $.Lang "Форма объекта"}}
        </a>
        <a href="/bases/{{$.Base.ID}}/configurator/forms"
           style="display:inline-block;padding:5px 12px;background:#e2e8f0;color:#334155;text-decoration:none;border-radius:4px;font-size:12px">
          {{t $.Lang "Все формы"}} / {{t $.Lang "Импорт из 1С"}}
        </a>
      </div>
    </div>
  </div>
  {{end}}

  {{/* Print forms */}}
  {{range .PrintForms}}
  <div class="cfg-panel" id="pf-{{.Name}}">
    <div class="panel-title">🖨 {{.Name}}</div>
    <div class="panel-kind">{{t $.Lang "Печатная форма"}} · {{t $.Lang "документ"}}: {{.Document}}</div>
    <form method="POST" action="/bases/{{$.Base.ID}}/configurator/printform">
      <input type="hidden" name="printform_filename" value="{{.FileName}}">
      <div class="section-hd">YAML-описание <span class="edit-hint">({{t $.Lang "кликните для редактирования"}})</span></div>
      <div class="code-wrap">
        <pre class="os-code" id="pre-pf-{{.Name}}" onclick="startEdit('pf-{{.Name}}')">{{.Source}}</pre>
        <textarea class="os-edit" id="ta-pf-{{.Name}}" name="source"
                  style="display:none"
                  onblur="endEdit('pf-{{.Name}}')">{{.Source}}</textarea>
      </div>
      <div class="module-save-row">
        <button class="btn-save" type="submit">{{t $.Lang "Сохранить"}}</button>
        {{if and $.FieldsSaved (eq $.FieldsSavedEntity .Name)}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
      </div>
    </form>
  </div>
  {{end}}

  {{/* DSL Print forms (.os) */}}
  {{range .DSLPrintForms}}
  <div class="cfg-panel" id="dpf-{{.Name}}">
    <div class="panel-title">📋 {{.Name}}</div>
    <div class="panel-kind">{{t $.Lang "DSL печатная форма"}} · {{t $.Lang "документ"}}: {{.Document}}</div>
    <form method="POST" action="/bases/{{$.Base.ID}}/configurator/printform">
      <input type="hidden" name="printform_filename" value="{{.FileName}}">
      <input type="hidden" name="printform_dsl" value="1">
      {{if .HasLayout}}<div class="section-hd" style="color:#4a9">{{t $.Lang "Макет привязан"}} (<code>{{.Name}}.layout.yaml</code>)</div>{{end}}
      <div class="section-hd">{{t $.Lang "Код формы"}} (.os) <span class="edit-hint">({{t $.Lang "кликните для редактирования"}})</span></div>
      <div class="code-wrap">
        <pre class="os-code" id="pre-dpf-{{.Name}}" onclick="startEdit('dpf-{{.Name}}')">{{.Source}}</pre>
        <textarea class="os-edit" id="ta-dpf-{{.Name}}" name="source"
                  style="display:none"
                  onblur="endEdit('dpf-{{.Name}}')">{{.Source}}</textarea>
      </div>
      <div class="module-save-row">
        <button class="btn-save" type="submit">{{t $.Lang "Сохранить"}}</button>
        <button type="button" class="btn-check" onclick="runCheck('dsl','dpf-{{.Name}}','{{.Name}}')">{{t $.Lang "Проверить"}}</button>
        <span class="check-result" id="check-dpf-{{.Name}}"></span>
        {{if and $.FieldsSaved (eq $.FieldsSavedEntity .Name)}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
      </div>
    </form>
  </div>
  {{end}}

  {{/* Layout panels */}}
  {{range .DSLPrintForms}}
  {{if .HasLayout}}
  <div class="cfg-panel" id="mkt-{{.Name}}">
    <div class="panel-title">&#x1F4D0; {{t $.Lang "Макет"}}: {{.Name}}</div>
    <form method="POST" action="/bases/{{$.Base.ID}}/configurator/layout" onsubmit="return saveLayoutEditor('{{.Name}}')">
      <input type="hidden" name="layout_name" value="{{.Name}}">

      {{/* Toolbar: structural operations */}}
      <div style="display:flex;gap:6px;margin:4px 0 8px;flex-wrap:wrap;align-items:center">
        <button type="button" class="btn-save" onclick="addLayoutArea('{{.Name}}')" style="font-size:12px;padding:4px 10px">+ {{t $.Lang "Область"}}</button>
        <span style="width:1px;background:#d1d5db;align-self:stretch"></span>
        <button type="button" onclick="ldAddRow('{{.Name}}')"     style="font-size:12px;padding:4px 10px;background:#fff;border:1px solid #cbd5e1;border-radius:4px;cursor:pointer">+ {{t $.Lang "Строка"}}</button>
        <button type="button" onclick="ldAddColumn('{{.Name}}')"  style="font-size:12px;padding:4px 10px;background:#fff;border:1px solid #cbd5e1;border-radius:4px;cursor:pointer">+ {{t $.Lang "Колонка"}}</button>
        <button type="button" onclick="ldDelRow('{{.Name}}')"     style="font-size:12px;padding:4px 10px;background:#fff;border:1px solid #fcc;border-radius:4px;cursor:pointer;color:#c33">{{t $.Lang "Удалить строку"}}</button>
        <button type="button" onclick="ldDelColumn('{{.Name}}')"  style="font-size:12px;padding:4px 10px;background:#fff;border:1px solid #fcc;border-radius:4px;cursor:pointer;color:#c33">{{t $.Lang "Удалить колонку"}}</button>
        <span style="width:1px;background:#d1d5db;align-self:stretch"></span>
        <button type="button" onclick="ldMerge('{{.Name}}')"      style="font-size:12px;padding:4px 10px;background:#fff;border:1px solid #cbd5e1;border-radius:4px;cursor:pointer">{{t $.Lang "Объединить"}} →</button>
        <button type="button" onclick="ldSplit('{{.Name}}')"      style="font-size:12px;padding:4px 10px;background:#fff;border:1px solid #cbd5e1;border-radius:4px;cursor:pointer">{{t $.Lang "Разъединить"}}</button>
      </div>

      {{/* Split view: YAML editor (left) + visual designer (right) */}}
      <div style="display:flex;border:1px solid #d1d5db;border-radius:0 0 6px 6px;overflow:hidden;min-height:340px">
        {{/* Left pane: YAML */}}
        <div style="flex:0 0 42%;border-right:1px solid #d1d5db;display:flex;flex-direction:column;min-width:0">
          <div style="background:#f8fafc;border-bottom:1px solid #e2e8f0;padding:3px 10px;font-size:11px;font-weight:600;color:#64748b;flex-shrink:0;letter-spacing:.03em">YAML</div>
          <textarea id="ta-mkt-{{.Name}}" name="source"
                    style="flex:1;padding:8px;border:none;outline:none;font-family:'Cascadia Code','Consolas',monospace;font-size:11px;resize:none;tab-size:2;background:#fafbfc;min-height:300px;width:100%;box-sizing:border-box;line-height:1.5"
                    oninput="scheduleYamlSync('{{.Name}}')"
                    onblur="applyYaml('{{.Name}}')">{{.LayoutYAML}}</textarea>
        </div>
        {{/* Right pane: Visual designer */}}
        <div style="flex:1;display:flex;flex-direction:column;min-width:0;overflow:hidden">
          <div style="background:#f8fafc;border-bottom:1px solid #e2e8f0;padding:3px 10px;font-size:11px;font-weight:600;color:#64748b;flex-shrink:0;letter-spacing:.03em">{{t $.Lang "Конструктор"}}</div>
          <div id="veditor-{{.Name}}" style="flex:1;padding:8px;overflow:auto;background:#fff">{{if .LayoutPreview}}{{.LayoutPreview}}{{else}}<p style="color:#999;font-size:12px">{{t $.Lang "Нет данных. Нажмите «+ Область» для начала."}}</p>{{end}}</div>
        </div>
      </div>

      {{/* Cell properties panel */}}
      <div id="vprops-{{.Name}}" style="display:none;background:#f0f8ff;border:1px solid #b0d0f0;border-radius:4px;padding:10px;margin-top:8px">
        <div style="font-weight:bold;margin-bottom:6px;font-size:12px">{{t $.Lang "Свойства ячейки"}}</div>
        <div style="display:grid;grid-template-columns:1fr 1fr 1fr;gap:6px;font-size:12px">
          <div><label>{{t $.Lang "Текст"}}</label><br><input id="vp-text-{{.Name}}" style="width:100%;padding:3px" oninput="updateCellProp('{{.Name}}','text',this.value)"></div>
          <div><label>{{t $.Lang "Параметр"}}</label><br><input id="vp-param-{{.Name}}" style="width:100%;padding:3px" oninput="updateCellProp('{{.Name}}','parameter',this.value)"></div>
          <div><label>{{t $.Lang "Шрифт"}}</label><br>
            <select id="vp-ff-{{.Name}}" style="width:100%;padding:3px" onchange="updateCellProp('{{.Name}}','fontFamily',this.value)">
              <option value="">{{t $.Lang "По умолчанию"}}</option>
              <option>Arial</option><option>Times New Roman</option><option>Courier New</option><option>Verdana</option><option>Tahoma</option>
            </select>
          </div>
          <div><label>{{t $.Lang "Размер"}} (pt)</label><br><input type="number" id="vp-fs-{{.Name}}" min="6" max="72" style="width:100%;padding:3px" oninput="updateCellProp('{{.Name}}','fontSize',parseInt(this.value)||0)"></div>
          <div><label><input type="checkbox" id="vp-bold-{{.Name}}" onchange="updateCellProp('{{.Name}}','bold',this.checked)"> {{t $.Lang "Жирный"}}</label>
               &nbsp;<label><input type="checkbox" id="vp-italic-{{.Name}}" onchange="updateCellProp('{{.Name}}','italic',this.checked)"> {{t $.Lang "Курсив"}}</label></div>
          <div></div>
          <div><label>{{t $.Lang "Гор. выравнивание"}}</label><br>
            <select id="vp-align-{{.Name}}" style="width:100%;padding:3px" onchange="updateCellProp('{{.Name}}','align',this.value)">
              <option value="">—</option><option value="left">{{t $.Lang "Лево"}}</option><option value="center">{{t $.Lang "Центр"}}</option><option value="right">{{t $.Lang "Право"}}</option>
            </select>
          </div>
          <div><label>{{t $.Lang "Верт. выравнивание"}}</label><br>
            <select id="vp-valign-{{.Name}}" style="width:100%;padding:3px" onchange="updateCellProp('{{.Name}}','valign',this.value)">
              <option value="">—</option><option value="top">{{t $.Lang "Верх"}}</option><option value="middle">{{t $.Lang "Середина"}}</option><option value="bottom">{{t $.Lang "Низ"}}</option>
            </select>
          </div>
          <div></div>
          <div><label>{{t $.Lang "Фон"}}</label><br><input type="color" id="vp-bg-{{.Name}}" style="width:100%;height:28px" oninput="updateCellProp('{{.Name}}','backColor',this.value)"></div>
          <div><label>{{t $.Lang "Цвет текста"}}</label><br><input type="color" id="vp-fg-{{.Name}}" style="width:100%;height:28px" oninput="updateCellProp('{{.Name}}','textColor',this.value)"></div>
          <div></div>
          <div><label>{{t $.Lang "Границы"}}</label><br>
            <select id="vp-border-{{.Name}}" style="width:100%;padding:3px" onchange="updateCellProp('{{.Name}}','border',this.value)">
              <option value="">{{t $.Lang "По умолчанию"}}</option><option value="none">{{t $.Lang "Нет"}}</option><option value="thin">{{t $.Lang "Тонкая"}}</option><option value="all">{{t $.Lang "Все"}}</option><option value="thick">{{t $.Lang "Толстая"}}</option>
            </select>
          </div>
          <div><label>{{t $.Lang "Цвет границы"}}</label><br><input type="color" id="vp-bc-{{.Name}}" style="width:100%;height:28px" oninput="updateCellProp('{{.Name}}','borderColor',this.value)"></div>
          <div></div>
          <div><label>{{t $.Lang "Объединить"}} (colspan)</label><br><input type="number" id="vp-colspan-{{.Name}}" min="1" max="20" style="width:100%;padding:3px" oninput="updateCellProp('{{.Name}}','colspan',parseInt(this.value)||0)"></div>
          <div><label>{{t $.Lang "Объединить"}} (rowspan)</label><br><input type="number" id="vp-rowspan-{{.Name}}" min="1" max="20" style="width:100%;padding:3px" oninput="updateCellProp('{{.Name}}','rowspan',parseInt(this.value)||0)"></div>
          <div></div>
        </div>
      </div>
      <div class="module-save-row">
        <button class="btn-save" type="submit">{{t $.Lang "Сохранить"}}</button>
      </div>
    </form>
  </div>
  {{end}}
  {{end}}

  {{/* Subsystems */}}
  {{range $sub := .Subsystems}}
  <div class="cfg-panel" id="sub-{{$sub.Name}}">
    <div class="panel-title">🗂 {{$sub.Title}}</div>
    <div class="panel-kind">{{t $.Lang "Подсистема"}}</div>
    <form method="POST" action="/bases/{{$.Base.ID}}/configurator/subsystem" data-home-key="sub-{{$sub.Name}}">
      <input type="hidden" name="subsystem_name" value="{{$sub.Name}}">
      <div class="fg" style="margin-top:12px">
        <label>{{t $.Lang "Заголовок"}}</label>
        <input type="text" name="title" value="{{$sub.Title}}" placeholder="{{t $.Lang "Название подсистемы"}}">
      </div>
      <div class="fg" style="margin-top:8px">
        <label>{{t $.Lang "Иконка"}}</label>
        <input type="text" name="icon" value="{{$sub.Icon}}" placeholder="shopping-cart">
      </div>
      <div class="fg" style="margin-top:8px">
        <label>{{t $.Lang "Порядок"}}</label>
        <input type="number" name="order" value="{{$sub.Order}}" style="width:100px">
      </div>

      <div class="section-hd" style="margin-top:14px">{{t $.Lang "Состав подсистемы"}}</div>
      {{if $.Catalogs}}
      <div style="margin-top:6px"><span style="font-size:11px;font-weight:700;color:#555">{{t $.Lang "Справочники"}}</span></div>
      {{range $e := $.Catalogs}}
      <label style="display:flex;align-items:center;gap:6px;font-size:12px;padding:2px 0;cursor:pointer">
        <input type="checkbox" name="catalogs" value="{{$e.Name}}" {{range $sub.Contents.Catalogs}}{{if eq . $e.Name}}checked{{end}}{{end}}>
        {{$e.Name}}
      </label>
      {{end}}
      {{end}}
      {{if $.Docs}}
      <div style="margin-top:8px"><span style="font-size:11px;font-weight:700;color:#555">{{t $.Lang "Документы"}}</span></div>
      {{range $e := $.Docs}}
      <label style="display:flex;align-items:center;gap:6px;font-size:12px;padding:2px 0;cursor:pointer">
        <input type="checkbox" name="documents" value="{{$e.Name}}" {{range $sub.Contents.Documents}}{{if eq . $e.Name}}checked{{end}}{{end}}>
        {{$e.Name}}
      </label>
      {{end}}
      {{end}}
      {{if $.Registers}}
      <div style="margin-top:8px"><span style="font-size:11px;font-weight:700;color:#555">{{t $.Lang "Регистры накопления"}}</span></div>
      {{range $r := $.Registers}}
      <label style="display:flex;align-items:center;gap:6px;font-size:12px;padding:2px 0;cursor:pointer">
        <input type="checkbox" name="registers" value="{{$r.Name}}" {{range $sub.Contents.Registers}}{{if eq . $r.Name}}checked{{end}}{{end}}>
        {{$r.Name}}
      </label>
      {{end}}
      {{end}}
      {{if $.InfoRegisters}}
      <div style="margin-top:8px"><span style="font-size:11px;font-weight:700;color:#555">{{t $.Lang "Регистры сведений"}}</span></div>
      {{range $r := $.InfoRegisters}}
      <label style="display:flex;align-items:center;gap:6px;font-size:12px;padding:2px 0;cursor:pointer">
        <input type="checkbox" name="inforegs" value="{{$r.Name}}" {{range $sub.Contents.InfoRegs}}{{if eq . $r.Name}}checked{{end}}{{end}}>
        {{$r.Name}}
      </label>
      {{end}}
      {{end}}
      {{if $.Reports}}
      <div style="margin-top:8px"><span style="font-size:11px;font-weight:700;color:#555">{{t $.Lang "Отчёты"}}</span></div>
      {{range $r := $.Reports}}
      <label style="display:flex;align-items:center;gap:6px;font-size:12px;padding:2px 0;cursor:pointer">
        <input type="checkbox" name="reports" value="{{$r.Name}}" {{range $sub.Contents.Reports}}{{if eq . $r.Name}}checked{{end}}{{end}}>
        {{if $r.Title}}{{$r.Title}}{{else}}{{$r.Name}}{{end}}
      </label>
      {{end}}
      {{end}}
      {{if $.Processors}}
      <div style="margin-top:8px"><span style="font-size:11px;font-weight:700;color:#555">{{t $.Lang "Обработки"}}</span></div>
      {{range $p := $.Processors}}
      <label style="display:flex;align-items:center;gap:6px;font-size:12px;padding:2px 0;cursor:pointer">
        <input type="checkbox" name="processors" value="{{$p.Name}}" {{range $sub.Contents.Processors}}{{if eq . $p.Name}}checked{{end}}{{end}}>
        {{if $p.Title}}{{$p.Title}}{{else}}{{$p.Name}}{{end}}
      </label>
      {{end}}
      {{end}}

      <div class="section-hd" style="margin-top:14px">{{t $.Lang "Рабочий стол подсистемы"}}</div>
      {{if $.Widgets}}
      <div class="fg" style="margin:6px 0;display:flex;align-items:center;gap:8px;flex-wrap:wrap">
        <label style="font-size:12px;color:#555">{{t $.Lang "Раскладка"}}</label>
        <select name="home_layout" style="width:180px" onchange="obToggleLayout(this)">
          <option value="auto" {{if ne $sub.HomeLayout "rows"}}selected{{end}}>{{t $.Lang "Авто (по ширине)"}}</option>
          <option value="rows" {{if eq $sub.HomeLayout "rows"}}selected{{end}}>{{t $.Lang "По рядам"}}</option>
        </select>
      </div>
      {{template "home-layout-editor" dict "Widgets" $.Widgets "Selected" $sub.HomeWidgets "Layout" $sub.HomeLayout "Lang" $.Lang "AutoHint" (t $.Lang "Отметьте виджеты для рабочего стола подсистемы")}}
      <script>window.__homeData=window.__homeData||{};window.__homeData[{{js (printf "sub-%s" $sub.Name)}}]={rows:{{js $sub.HomeRows}}};window.__homeWidgets={{js $.WidgetOptions}};</script>
      {{else}}
      <div style="font-size:12px;color:#94a3b8;margin:6px 0">{{t $.Lang "Виджеты не созданы"}}</div>
      {{end}}

      <div class="module-save-row" style="margin-top:14px">
        <button class="btn-save" type="submit">{{t $.Lang "Сохранить"}}</button>
        {{if and $.FieldsSaved (eq $.FieldsSavedEntity $sub.Name)}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
      </div>
    </form>
  </div>
  {{end}}

  {{/* Widgets */}}
  {{range .Widgets}}
  <div class="cfg-panel" id="wdg-{{.Name}}">
    <div class="panel-title">🧩 {{if .Title}}{{.Title}}{{else}}{{.Name}}{{end}}</div>
    <div class="panel-kind">{{t $.Lang "Виджет дашборда"}} · {{t $.Lang "тип"}} <b>{{.Type}}</b></div>
    <form method="POST" action="/bases/{{$.Base.ID}}/configurator/widget">
      <input type="hidden" name="widget_name" value="{{.Name}}">
      <div style="font-size:12px;color:#64748b;margin:6px 0">
        {{t $.Lang "Поля виджета"}}: <code>name</code>, <code>type</code> (kpi/list/chart/actions/recent), <code>title</code>, <code>query</code>, <code>params</code>.
        Шаблоны параметров записывайте как <code>&#123;&#123;today|start_of_month&#125;&#125;</code> или <code>&#123;&#123;today|minus_days:30&#125;&#125;</code>.
      </div>
      <div class="code-wrap">
        <textarea name="yaml" id="ta-wdg-{{.Name}}" style="width:100%;min-height:380px;font-family:Consolas,monospace;font-size:12px;border:1px solid #ccd0d8;border-radius:4px;padding:8px;tab-size:2">{{.YAML}}</textarea>
      </div>
      <div class="module-save-row">
        <button class="btn-save" type="submit">{{t $.Lang "Сохранить"}}</button>
        <button type="button" class="btn-check" onclick="runCheck('widget','wdg-{{.Name}}','{{.Name}}')">{{t $.Lang "Проверить"}}</button>
        <span class="check-result" id="check-wdg-{{.Name}}"></span>
        {{if and $.FieldsSaved (eq $.FieldsSavedEntity .Name)}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
      </div>
    </form>
    <form method="POST" action="/bases/{{$.Base.ID}}/configurator/widget-delete" style="margin-top:8px" onsubmit="return confirm('Удалить виджет {{.Name}}?')">
      <input type="hidden" name="widget_name" value="{{.Name}}">
      <button type="submit" style="background:none;border:1px solid #d8dde8;color:#c00;padding:4px 10px;font-size:11px;border-radius:3px;cursor:pointer">{{t $.Lang "Удалить виджет"}}</button>
    </form>
  </div>
  {{end}}

  {{/* Home page */}}
  <div class="cfg-panel" id="home-page">
    <div class="panel-title">🏠 {{t $.Lang "Главная страница"}}</div>
    <div class="panel-kind">{{t $.Lang "Раскладка стартового дашборда"}} (<code>config/home_page.yaml</code>)</div>
    <form method="POST" action="/bases/{{.Base.ID}}/configurator/home-page" data-home-key="home">
      <div class="fg" style="margin-top:12px">
        <label>{{t $.Lang "Заголовок"}}</label>
        <input type="text" name="home_title" value="{{.GlobalHome.Title}}" placeholder="{{t $.Lang "Главная"}}">
      </div>
      {{if .Widgets}}
      <div class="fg" style="margin:6px 0;display:flex;align-items:center;gap:8px;flex-wrap:wrap">
        <label style="font-size:12px;color:#555">{{t $.Lang "Раскладка"}}</label>
        <select name="home_layout" style="width:180px" onchange="obToggleLayout(this)">
          <option value="auto" {{if ne .GlobalHome.Layout "rows"}}selected{{end}}>{{t $.Lang "Авто (по ширине)"}}</option>
          <option value="rows" {{if eq .GlobalHome.Layout "rows"}}selected{{end}}>{{t $.Lang "По рядам"}}</option>
        </select>
      </div>
      {{template "home-layout-editor" dict "Widgets" .Widgets "Selected" .GlobalHome.Widgets "Layout" .GlobalHome.Layout "Lang" .Lang "AutoHint" (t .Lang "Отметьте виджеты для главной страницы")}}
      <script>window.__homeData=window.__homeData||{};window.__homeData["home"]={rows:{{js .GlobalHome.Rows}}};window.__homeWidgets={{js .WidgetOptions}};</script>
      {{else}}
      <div style="font-size:12px;color:#94a3b8;margin:6px 0">{{t $.Lang "пока ни одного — создайте через «+» в дереве «Виджеты»"}}</div>
      {{end}}
      <div class="module-save-row" style="margin-top:14px">
        <button class="btn-save" type="submit">{{t $.Lang "Сохранить"}}</button>
        {{if and .FieldsSaved (eq .FieldsSavedEntity "home-page")}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
      </div>
    </form>

    <details style="margin-top:16px">
      <summary style="cursor:pointer;font-size:12px;color:#64748b;font-weight:600">{{t $.Lang "Расширенно (YAML)"}}</summary>
      <form method="POST" action="/bases/{{.Base.ID}}/configurator/home-page-yaml" style="margin-top:8px">
        <div style="font-size:12px;color:#64748b;margin:6px 0">
          Поля: <code>title</code>, <code>layout</code> (<code>auto</code> / <code>rows</code> / <code>grid</code>), <code>rows[].widgets</code>, <code>titles</code>.
          Если файл пуст, на главной показываются все зарегистрированные виджеты.
        </div>
        <div class="code-wrap">
          <textarea name="yaml" id="ta-home-page" style="width:100%;min-height:260px;font-family:Consolas,monospace;font-size:12px;border:1px solid #ccd0d8;border-radius:4px;padding:8px;tab-size:2">{{.HomePageYAML}}</textarea>
        </div>
        <div class="module-save-row">
          <button class="btn-save" type="submit">{{t $.Lang "Сохранить"}}</button>
          <button type="button" class="btn-check" onclick="runCheck('home_page','home-page','home_page')">{{t $.Lang "Проверить"}}</button>
          <span class="check-result" id="check-home-page"></span>
        </div>
      </form>
    </details>
  </div>

</div>{{/* cfg-right */}}
</div>{{/* cfg-split */}}
{{end}}

{{/* home-layout-editor — общий редактор раскладки рабочего стола: пейн «Авто»
   (галочки) и пейн «По рядам» (drag-конструктор). Параметры (dict): Widgets,
   Selected (отмеченные имена), Layout ("auto"|"rows"), Lang, AutoHint. */}}
{{define "home-layout-editor"}}
<div class="ob-auto"{{if eq .Layout "rows"}} style="display:none"{{end}}>
  <div style="font-size:12px;color:#64748b;margin:6px 0">{{.AutoHint}}</div>
  {{$sel := .Selected}}
  {{range $w := .Widgets}}
  <label style="display:flex;align-items:center;gap:6px;font-size:12px;padding:2px 0;cursor:pointer">
    <input type="checkbox" name="home_widgets" value="{{$w.Name}}" {{range $sel}}{{if eq . $w.Name}}checked{{end}}{{end}}>
    {{if $w.Title}}{{$w.Title}} <span style="color:#94a3b8">({{$w.Name}})</span>{{else}}{{$w.Name}}{{end}}
  </label>
  {{end}}
</div>
<div class="ob-rows-pane"{{if ne .Layout "rows"}} style="display:none"{{end}}>
  <input type="hidden" name="home_rows">
  <div style="font-size:12px;color:#64748b;margin:6px 0">{{t $.Lang "Перетащите виджеты в ряды. «+ Ряд» добавляет новый ряд."}}</div>
  <div class="ob-rows"></div>
  <button type="button" class="ob-add-row" style="margin:6px 0;background:none;border:1px dashed #c3c9d4;color:#475569;padding:5px 12px;font-size:12px;border-radius:5px;cursor:pointer">+ {{t $.Lang "Ряд"}}</button>
  <div style="font-size:11px;color:#94a3b8;margin:8px 0 4px">{{t $.Lang "Доступные виджеты"}}</div>
  <div class="ob-pool ob-zone"></div>
</div>
{{end}}

{{define "entity-detail"}}
{{$e := .Entity}}
{{$baseID := .BaseID}}
{{$allEntities := .AllEntityNames}}
{{$allEnums := .AllEnumNames}}
{{$fSaved := .FieldsSaved}}
{{$fSavedEnt := .FieldsSavedEntity}}

{{/* Module section — outside the fields form to avoid nested <form> elements */}}
<details open><summary class="section-hd" style="cursor:pointer">{{t $.Lang "Модули"}}</summary>
<div class="module-editor-wrap">
  <div class="module-tabs">
    <div class="module-tab active" onclick="modTab(this,'mp-obj-{{$e.Name}}')">📝 {{t $.Lang "Модуль объекта"}}</div>
    {{if eq $e.Kind "Документ"}}<div class="module-tab" onclick="modTab(this,'mp-post-{{$e.Name}}')">✅ {{t $.Lang "ОбработкаПроведения"}}</div>{{end}}
    <div class="module-tab" onclick="modTab(this,'mp-mgr-{{$e.Name}}')">📋 {{t $.Lang "Модуль менеджера"}}</div>
  </div>

  <div class="module-pane active" id="mp-obj-{{$e.Name}}">
    <form method="POST" action="/bases/{{.BaseID}}/configurator/module">
      <input type="hidden" name="entity" value="{{$e.Name}}">
      <input type="hidden" name="module_type" value="object">
      <div class="code-wrap" title="{{t $.Lang "Кликните для редактирования"}}">
        <pre class="os-code clickable-code" id="pre-{{$e.Name}}"
             onclick="startEdit('{{$e.Name}}')">{{if $e.Source}}{{$e.Source}}{{else}}// Кликните для редактирования&#10;Процедура ПриЗаписи()&#10;&#10;КонецПроцедуры{{end}}</pre>
        <textarea class="os-edit" id="ta-{{$e.Name}}" name="source"
                  style="display:none"
                  onblur="endEdit('{{$e.Name}}')">{{$e.Source}}</textarea>
      </div>
      <div class="module-save-row">
        <button class="btn-save" type="submit">{{t $.Lang "Сохранить"}}</button>
        <button type="button" class="btn-check" onclick="runCheck('dsl','{{$e.Name}}','{{$e.Name}}')">{{t $.Lang "Проверить"}}</button>
        <span class="check-result" id="check-{{$e.Name}}"></span>
        <span class="edit-hint">✎ {{t $.Lang "кликните на код для редактирования"}}</span>
        {{if and $.ModuleSaved (eq $.ModuleSavedEntity $e.Name)}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
      </div>
    </form>
  </div>

  {{if eq $e.Kind "Документ"}}
  <div class="module-pane" id="mp-post-{{$e.Name}}">
    <div style="font-size:11px;color:#64748b;margin-bottom:6px">{{t $.Lang "Процедура"}} <b>{{t $.Lang "ОбработкаПроведения"}}()</b> — {{t $.Lang "вызывается при нажатии «Провести». Активируется флагом"}} <b>{{t $.Lang "Проводится"}}</b> {{t $.Lang "в свойствах документа. Здесь пишите движения регистров."}}</div>
    <form method="POST" action="/bases/{{.BaseID}}/configurator/module">
      <input type="hidden" name="entity" value="{{$e.Name}}">
      <input type="hidden" name="module_type" value="posting">
      <div class="code-wrap" title="{{t $.Lang "Кликните для редактирования"}}">
        <pre class="os-code clickable-code" id="pre-post-{{$e.Name}}"
             onclick="startEdit('post-{{$e.Name}}')">{{if $e.PostingSource}}{{$e.PostingSource}}{{else}}Процедура ОбработкаПроведения()&#10;  // Движения.ИмяРегистра.Очистить()&#10;  // Дв = Движения.ИмяРегистра.Добавить()&#10;  // Дв.ВидДвижения = "Приход"&#10;  // Дв.Номенклатура = Строка.Номенклатура&#10;  // Дв.Количество = Строка.Количество&#10;КонецПроцедуры{{end}}</pre>
        <textarea class="os-edit" id="ta-post-{{$e.Name}}" name="source"
                  style="display:none"
                  onblur="endEdit('post-{{$e.Name}}')">{{$e.PostingSource}}</textarea>
      </div>
      <div class="module-save-row">
        <button class="btn-save" type="submit">{{t $.Lang "Сохранить"}}</button>
        <button type="button" class="btn-check" onclick="runCheck('dsl','post-{{$e.Name}}','{{$e.Name}}-ОбработкаПроведения')">{{t $.Lang "Проверить"}}</button>
        <span class="check-result" id="check-post-{{$e.Name}}"></span>
        <span class="edit-hint">✎ {{t $.Lang "кликните на код для редактирования"}}</span>
        {{if and $.ModuleSaved (eq $.ModuleSavedEntity $e.Name)}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
      </div>
    </form>
  </div>
  {{end}}

  <div class="module-pane" id="mp-mgr-{{$e.Name}}">
    <div style="font-size:11px;color:#64748b;margin-bottom:6px">{{t $.Lang "Экспортные процедуры и функции этого модуля вызываются как"}} <b>{{if eq $e.Kind "Документ"}}{{t $.Lang "Документы"}}{{else}}{{t $.Lang "Справочники"}}{{end}}.{{$e.Name}}.{{t $.Lang "Метод"}}(…)</b> — {{t $.Lang "по аналогии с 1С:Предприятие. Здесь размещают функции уровня типа объекта: печать, поиск, сервисные расчёты."}}</div>
    <form method="POST" action="/bases/{{.BaseID}}/configurator/module">
      <input type="hidden" name="entity" value="{{$e.Name}}">
      <input type="hidden" name="module_type" value="manager">
      <div class="code-wrap" title="{{t $.Lang "Кликните для редактирования"}}">
        <pre class="os-code clickable-code" id="pre-mgr-{{$e.Name}}"
             onclick="startEdit('mgr-{{$e.Name}}')">{{if $e.ManagerSource}}{{$e.ManagerSource}}{{else}}Функция Пример(Параметр)&#10;  Возврат Параметр;&#10;КонецФункции{{end}}</pre>
        <textarea class="os-edit" id="ta-mgr-{{$e.Name}}" name="source"
                  style="display:none"
                  onblur="endEdit('mgr-{{$e.Name}}')">{{$e.ManagerSource}}</textarea>
      </div>
      <div class="module-save-row">
        <button class="btn-save" type="submit">{{t $.Lang "Сохранить"}}</button>
        <button type="button" class="btn-check" onclick="runCheck('dsl','mgr-{{$e.Name}}','{{$e.Name}}-Менеджер')">{{t $.Lang "Проверить"}}</button>
        <span class="check-result" id="check-mgr-{{$e.Name}}"></span>
        <span class="edit-hint">✎ {{t $.Lang "кликните на код для редактирования"}}</span>
        {{if and $.ModuleSaved (eq $.ModuleSavedEntity $e.Name)}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
      </div>
    </form>
  </div>
</div>

</details>

<form method="POST" action="/bases/{{$baseID}}/configurator/fields">
<input type="hidden" name="entity" value="{{$e.Name}}">
<input type="hidden" name="entity_kind" value="{{$e.Kind}}">
{{range $e.TableParts}}<input type="hidden" name="tp_names" value="{{.Name}}">{{end}}

{{if eq $e.Kind "Документ"}}
<div class="section-hd">{{t $.Lang "Свойства"}}</div>
<div style="margin-bottom:10px">
  <label style="display:flex;align-items:center;gap:8px;font-size:13px;cursor:pointer">
    <input type="checkbox" name="posting" value="true" {{if $e.Posting}}checked{{end}}>
    <span>{{t $.Lang "Проводится — поддержка кнопки «Провести» и обработки проведения"}}</span>
  </label>
</div>
{{end}}

{{/* Ввод на основании (Plan 38): доступен и для документов, и для
     справочников. Маркер based_on_present=1 нужен, чтобы POST-handler мог
     отличить «секция вообще не пришла» от «все чекбоксы сняты» — без него
     based_on невозможно было бы очистить через UI. */}}
<details {{if $e.BasedOn}}open{{end}} style="margin-bottom:10px">
<summary class="section-hd" style="cursor:pointer">{{t $.Lang "Ввод на основании"}}{{if $e.BasedOn}} ({{len $e.BasedOn}}){{end}}</summary>
<input type="hidden" name="based_on_present" value="1">
<div style="font-size:12px;color:#475569;margin:6px 0">{{t $.Lang "Объекты, на основании которых можно вводить эту сущность. При создании появится кнопка «Ввести на основании ▾» в форме источника."}}</div>
<div style="display:flex;flex-wrap:wrap;gap:6px 14px;font-size:13px">
  {{range $allEntities}}{{if ne . $e.Name}}{{$entName := .}}
  <label style="display:flex;align-items:center;gap:5px;cursor:pointer">
    <input type="checkbox" name="based_on" value="{{$entName}}"
      {{range $b := $e.BasedOn}}{{if eq $b $entName}}checked{{end}}{{end}}>
    <span>{{$entName}}</span>
  </label>
  {{end}}{{end}}
</div>
{{if $e.Receivers}}
<div style="margin-top:10px;padding-top:8px;border-top:1px dashed #e2e8f0">
  <div style="font-size:12px;color:#475569;margin-bottom:4px">{{t $.Lang "На основании этой сущности вводятся:"}}</div>
  <div style="font-size:13px">{{range $i, $r := $e.Receivers}}{{if $i}}, {{end}}<code>{{$r}}</code>{{end}}</div>
  <div style="font-size:11px;color:#94a3b8;margin-top:4px">{{t $.Lang "Это обратный список: исходное based_on хранится у этих сущностей. Чтобы изменить — откройте соответствующий объект."}}</div>
</div>
{{end}}
</details>

{{if eq $e.Kind "Справочник"}}
<div class="section-hd">Свойства</div>
<div style="margin-bottom:10px">
  <label style="display:flex;align-items:center;gap:8px;font-size:13px;cursor:pointer">
    <input type="checkbox" name="hierarchical" value="true" {{if $e.Hierarchical}}checked{{end}}>
    <span>Иерархический — поддержка групп (папок) и режима «Дерево / Список»</span>
  </label>
  <div style="color:#94a3b8;font-size:11px;margin-left:24px;margin-top:2px">
    После включения требуется миграция БД: появятся колонки <code>is_folder</code> и <code>parent_id</code>.
  </div>
</div>
{{end}}

{{if $e.Fields}}
<details open><summary class="section-hd" style="cursor:pointer">{{t $.Lang "Реквизиты"}} ({{len $e.Fields}})</summary>
<table class="fields-tbl" id="ft-{{$e.Name}}">
<tr><th>{{t $.Lang "Поле"}}</th><th>{{t $.Lang "Тип"}}</th><th style="min-width:150px">{{t $.Lang "Объект"}}</th><th title="{{t $.Lang "Кнопка «+ Создать» в picker'е для ссылочного поля. По умолчанию включена для шапки документа."}}">{{t $.Lang "+ в picker'е"}}</th></tr>
{{range $i, $f := $e.Fields}}
<input type="hidden" name="field.{{$i}}.name" value="{{$f.Name}}">
<tr>
  <td>{{$f.Name}}</td>
  <td>
    <select name="field.{{$i}}.type" onchange="cfgToggleRef(this,'cfr-{{$e.Name}}-f{{$i}}');cfgToggleNum(this,'cfn-{{$e.Name}}-f{{$i}}')">
      <option value="string"    {{if eq $f.Type "string"}}selected{{end}}>{{t $.Lang "строка"}}</option>
      <option value="number"    {{if eq $f.Type "number"}}selected{{end}}>{{t $.Lang "число"}}</option>
      <option value="date"      {{if eq $f.Type "date"}}selected{{end}}>{{t $.Lang "дата"}}</option>
      <option value="bool"      {{if eq $f.Type "bool"}}selected{{end}}>{{t $.Lang "булево"}}</option>
      <option value="reference" {{if eq $f.Type "reference"}}selected{{end}}>{{t $.Lang "ссылка →"}}</option>
      <option value="enum"      {{if eq $f.Type "enum"}}selected{{end}}>{{t $.Lang "перечисление →"}}</option>
    </select>
    <span id="cfn-{{$e.Name}}-f{{$i}}"{{if ne $f.Type "number"}} style="display:none"{{end}} title="{{t $.Lang "Длина, Точность"}}">
      <input type="number" min="1" name="field.{{$i}}.length" value="{{if $f.Length}}{{$f.Length}}{{end}}" placeholder="дл" style="width:46px;padding:2px 3px;border:1px solid #ccd0d8;border-radius:3px;font-size:11px">
      , <input type="number" min="0" name="field.{{$i}}.scale" value="{{if $f.Length}}{{$f.Scale}}{{end}}" placeholder="точн" style="width:46px;padding:2px 3px;border:1px solid #ccd0d8;border-radius:3px;font-size:11px">
    </span>
  </td>
  <td>
    <select name="field.{{$i}}.ref" id="cfr-{{$e.Name}}-f{{$i}}"{{if and (ne $f.Type "reference") (ne $f.Type "enum")}} style="display:none"{{end}}>
      <option value="">{{t $.Lang "— выбрать —"}}</option>
      {{if eq $f.Type "enum"}}
        {{range $allEnums}}<option value="{{.}}"{{if eq . $f.EnumName}} selected{{end}}>{{.}}</option>{{end}}
      {{else}}
        {{range $allEntities}}<option value="{{.}}"{{if eq . $f.RefEntity}} selected{{end}}>{{.}}</option>{{end}}
      {{end}}
    </select>
  </td>
  <td style="text-align:center">
    {{if eq $f.Type "reference"}}
    <input type="hidden" name="field.{{$i}}.inline_present" value="1">
    <input type="checkbox" name="field.{{$i}}.inline_allow" value="1"{{if $f.InlineAllowChecked false}} checked{{end}} title="{{t $.Lang "Показывать «+ Создать» в picker'е"}}">
    {{end}}
  </td>
</tr>
{{end}}
</table>
<button type="button" onclick="cfgAddField('ft-{{$e.Name}}','new_field',{{$e.Name}})" style="font-size:11px;color:#1a4a80;background:none;border:1px dashed #c0c8d8;padding:2px 8px;border-radius:3px;cursor:pointer;margin:4px 0">+ {{t $.Lang "Добавить поле"}}</button>
</details>
{{end}}

{{range $j, $tp := $e.TableParts}}
<details open><summary class="section-hd" style="cursor:pointer">📋 {{$tp.Name}} ({{len $tp.Fields}})</summary>
<div class="tp-block">
<table class="fields-tbl" id="ft-{{$e.Name}}-tp{{$j}}">
<tr><th>{{t $.Lang "Поле"}}</th><th>{{t $.Lang "Тип"}}</th><th style="min-width:150px">{{t $.Lang "Объект"}}</th><th title="{{t $.Lang "Кнопка «+ Создать» в picker'е. В ТЧ по умолчанию выключена."}}">{{t $.Lang "+ в picker'е"}}</th></tr>
{{range $i, $f := $tp.Fields}}
<input type="hidden" name="tp.{{$tp.Name}}.field.{{$i}}.name" value="{{$f.Name}}">
<tr>
  <td>{{$f.Name}}</td>
  <td>
    <select name="tp.{{$tp.Name}}.field.{{$i}}.type" onchange="cfgToggleRef(this,'cfr-{{$e.Name}}-tp{{$j}}f{{$i}}');cfgToggleNum(this,'cfn-{{$e.Name}}-tp{{$j}}f{{$i}}')">
      <option value="string"    {{if eq $f.Type "string"}}selected{{end}}>{{t $.Lang "строка"}}</option>
      <option value="number"    {{if eq $f.Type "number"}}selected{{end}}>{{t $.Lang "число"}}</option>
      <option value="date"      {{if eq $f.Type "date"}}selected{{end}}>{{t $.Lang "дата"}}</option>
      <option value="bool"      {{if eq $f.Type "bool"}}selected{{end}}>{{t $.Lang "булево"}}</option>
      <option value="reference" {{if eq $f.Type "reference"}}selected{{end}}>{{t $.Lang "ссылка →"}}</option>
      <option value="enum"      {{if eq $f.Type "enum"}}selected{{end}}>{{t $.Lang "перечисление →"}}</option>
    </select>
    <span id="cfn-{{$e.Name}}-tp{{$j}}f{{$i}}"{{if ne $f.Type "number"}} style="display:none"{{end}} title="{{t $.Lang "Длина, Точность"}}">
      <input type="number" min="1" name="tp.{{$tp.Name}}.field.{{$i}}.length" value="{{if $f.Length}}{{$f.Length}}{{end}}" placeholder="дл" style="width:46px;padding:2px 3px;border:1px solid #ccd0d8;border-radius:3px;font-size:11px">
      , <input type="number" min="0" name="tp.{{$tp.Name}}.field.{{$i}}.scale" value="{{if $f.Length}}{{$f.Scale}}{{end}}" placeholder="точн" style="width:46px;padding:2px 3px;border:1px solid #ccd0d8;border-radius:3px;font-size:11px">
    </span>
  </td>
  <td>
    <select name="tp.{{$tp.Name}}.field.{{$i}}.ref" id="cfr-{{$e.Name}}-tp{{$j}}f{{$i}}"{{if and (ne $f.Type "reference") (ne $f.Type "enum")}} style="display:none"{{end}}>
      <option value="">{{t $.Lang "— выбрать —"}}</option>
      {{if eq $f.Type "enum"}}
        {{range $allEnums}}<option value="{{.}}"{{if eq . $f.EnumName}} selected{{end}}>{{.}}</option>{{end}}
      {{else}}
        {{range $allEntities}}<option value="{{.}}"{{if eq . $f.RefEntity}} selected{{end}}>{{.}}</option>{{end}}
      {{end}}
    </select>
  </td>
  <td style="text-align:center">
    {{if eq $f.Type "reference"}}
    <input type="hidden" name="tp.{{$tp.Name}}.field.{{$i}}.inline_present" value="1">
    <input type="checkbox" name="tp.{{$tp.Name}}.field.{{$i}}.inline_allow" value="1"{{if $f.InlineAllowChecked true}} checked{{end}} title="{{t $.Lang "Показывать «+ Создать» в picker'е"}}">
    {{end}}
  </td>
</tr>
{{end}}
</table>
<button type="button" onclick="cfgAddField('ft-{{$e.Name}}-tp{{$j}}','new_tp.{{$tp.Name}}.field',{{$e.Name}})" style="font-size:11px;color:#1a4a80;background:none;border:1px dashed #c0c8d8;padding:2px 8px;border-radius:3px;cursor:pointer;margin:4px 0">+ {{t $.Lang "Добавить поле"}}</button>
</div>
</details>
{{end}}

<button type="button" onclick="cfgAddTP(this,'{{$e.Name}}')" style="font-size:11px;color:#1a4a80;background:none;border:1px dashed #c0c8d8;padding:2px 8px;border-radius:3px;cursor:pointer;margin:4px 0">+ {{t $.Lang "Добавить табличную часть"}}</button>

<div class="module-save-row" style="margin-bottom:14px">
  <button class="btn-save" type="submit">{{t $.Lang "Сохранить типы полей"}}</button>
  {{if and $fSaved (eq $fSavedEnt $e.Name)}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
</div>
</form>

{{/* Predefined items — only for catalogs */}}
{{if eq $e.Kind "Справочник"}}
<details style="margin-top:18px"><summary class="section-hd" style="cursor:pointer">{{t $.Lang "Предопределённые элементы"}} ({{len $e.Predefined}})</summary>
<form method="POST" action="/bases/{{$baseID}}/configurator/predefined">
<input type="hidden" name="entity" value="{{$e.Name}}">
{{range $e.Fields}}<input type="hidden" name="pre_field_names" value="{{.Name}}">{{end}}
<div style="font-size:11px;color:#64748b;margin-bottom:8px">Элементы, которые всегда присутствуют в справочнике. Имя — программный идентификатор (без пробелов).</div>
<table class="fields-tbl" id="pre-tbl-{{$e.Name}}">
<tr><th>{{t $.Lang "Имя"}}</th>{{range $e.Fields}}<th>{{.Name}}</th>{{end}}</tr>
{{range $i, $pd := $e.Predefined}}
<tr>
  <td><input type="text" name="pre.{{$i}}.name" value="{{$pd.Name}}" style="width:100%;font-size:12px;padding:2px 4px;border:1px solid #dde;border-radius:3px"></td>
  {{range $e.Fields}}<td><input type="text" name="pre.{{$i}}.field.{{.Name}}" value="{{index $pd.Fields .Name}}" style="width:100%;font-size:12px;padding:2px 4px;border:1px solid #dde;border-radius:3px"></td>{{end}}
</tr>
{{end}}
</table>
<button type="button" onclick="cfgAddPreRow('pre-tbl-{{$e.Name}}',{{len $e.Fields}})" style="font-size:11px;color:#1a4a80;background:none;border:1px dashed #c0c8d8;padding:2px 8px;border-radius:3px;cursor:pointer;margin:6px 0">+ {{t $.Lang "Добавить элемент"}}</button>
<div class="module-save-row" style="margin-bottom:8px;margin-top:6px">
  <button class="btn-save" type="submit">{{t $.Lang "Сохранить предопределённые"}}</button>
  {{if and $fSaved (eq $fSavedEnt $e.Name)}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
</div>
</form>
</details>
{{end}}

{{/* Linked print forms */}}
{{if $e.LinkedPrintForms}}
<div class="section-hd" style="margin-top:18px">{{t $.Lang "Печатные формы"}}</div>
<div style="display:flex;flex-wrap:wrap;gap:8px;margin-bottom:8px">
  {{range $e.LinkedPrintForms}}
  <a href="#" onclick="cfgSelectPanel('pf-{{.Name}}');return false"
     style="display:inline-flex;align-items:center;gap:5px;padding:5px 12px;background:#f0f4ff;border:1px solid #c8d4f0;border-radius:4px;font-size:12px;color:#1a4a80;text-decoration:none">
    🖨 {{.Name}}
  </a>
  {{end}}
</div>
{{end}}

{{/* Forms section */}}
<div class="section-hd" style="margin-top:18px">{{t $.Lang "Формы"}}</div>
<form method="POST" action="/bases/{{$baseID}}/configurator/form">
<input type="hidden" name="entity" value="{{$e.Name}}">

<div class="module-tabs" style="margin-top:8px">
  <div class="module-tab active" onclick="formTab(this,'fl-{{$e.Name}}','fe-{{$e.Name}}')">📋 {{t $.Lang "Форма списка"}}</div>
  <div class="module-tab" onclick="formTab(this,'fe-{{$e.Name}}','fl-{{$e.Name}}')">📄 {{t $.Lang "Форма элемента"}}</div>
</div>

{{/* List form fields */}}
<div class="module-pane active" id="fl-{{$e.Name}}" style="padding:10px 0">
<p style="font-size:11px;color:#64748b;margin-bottom:8px">{{t $.Lang "Выберите поля, отображаемые в списке. Порядок строк = порядок колонок."}}</p>
<div id="fl-sort-{{$e.Name}}">
{{range $i, $f := $e.Fields}}
<div class="form-field-row" style="display:flex;align-items:center;gap:6px;padding:3px 0;font-size:12px">
  <input type="hidden" name="lf.{{$i}}.name" value="{{$f.Name}}">
  <label style="display:flex;align-items:center;gap:5px;cursor:pointer;flex:1">
    <input type="checkbox" name="lf.{{$i}}.vis" value="1" {{if not $f.FormListHidden}}checked{{end}}>
    <span style="color:#1a4a80">{{$f.Name}}</span>
    <span class="ft {{fieldTypeClass $f.Type}}" style="font-size:11px">{{fieldTypeLabel $f.Type $f.RefEntity}}</span>
  </label>
  <button type="button" onclick="moveUp(this)" style="background:none;border:1px solid #e2e8f0;border-radius:3px;padding:1px 6px;cursor:pointer;font-size:11px">↑</button>
  <button type="button" onclick="moveDown(this)" style="background:none;border:1px solid #e2e8f0;border-radius:3px;padding:1px 6px;cursor:pointer;font-size:11px">↓</button>
</div>
{{end}}
</div>
</div>

{{/* Element form fields */}}
<div class="module-pane" id="fe-{{$e.Name}}" style="padding:10px 0">
<p style="font-size:11px;color:#64748b;margin-bottom:8px">{{t $.Lang "Выберите поля, отображаемые в форме элемента."}}</p>
<div id="fe-sort-{{$e.Name}}">
{{range $i, $f := $e.Fields}}
<div class="form-field-row" style="display:flex;align-items:center;gap:6px;padding:3px 0;font-size:12px">
  <input type="hidden" name="ef.{{$i}}.name" value="{{$f.Name}}">
  <label style="display:flex;align-items:center;gap:5px;cursor:pointer;flex:1">
    <input type="checkbox" name="ef.{{$i}}.vis" value="1" {{if not $f.FormItemHidden}}checked{{end}}>
    <span style="color:#1a4a80">{{$f.Name}}</span>
    <span class="ft {{fieldTypeClass $f.Type}}" style="font-size:11px">{{fieldTypeLabel $f.Type $f.RefEntity}}</span>
  </label>
</div>
{{end}}
{{range $j, $tp := $e.TableParts}}
<div style="font-size:11px;font-weight:600;color:#7c3aed;margin:8px 0 2px;padding-left:2px">📋 {{$tp.Name}} ({{t $.Lang "табличная часть"}})</div>
{{range $i, $f := $tp.Fields}}
<div class="form-field-row" style="display:flex;align-items:center;gap:6px;padding:3px 0 3px 16px;font-size:12px">
  <input type="hidden" name="ef.tp{{$j}}.{{$i}}.name" value="tp.{{$tp.Name}}.{{$f.Name}}">
  <label style="display:flex;align-items:center;gap:5px;cursor:pointer;flex:1">
    <input type="checkbox" name="ef.tp{{$j}}.{{$i}}.vis" value="1" checked>
    <span style="color:#1a4a80">{{$f.Name}}</span>
    <span class="ft {{fieldTypeClass $f.Type}}" style="font-size:11px">{{fieldTypeLabel $f.Type $f.RefEntity}}</span>
  </label>
</div>
{{end}}
{{end}}
</div>
</div>

<div class="module-save-row">
  <button class="btn-save" type="submit">{{t $.Lang "Сохранить формы"}}</button>
  {{if and $fSaved (eq $fSavedEnt $e.Name)}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
</div>
</form>

{{/* ── Управляемые формы (план 37, этап 4) ────────────────────────────── */}}
<div class="section-hd" style="margin-top:18px">◇ Управляемая форма</div>
<div style="background:#f8fafc;border:1px dashed #c8d4f0;border-radius:6px;padding:12px 14px;font-size:12px;color:#475569;line-height:1.5">
  <p style="margin:0 0 8px">
    Управляемая форма — декларативное описание UI в YAML, переопределяющее
    авто-форму выше. Поддерживает группы, страницы-закладки, реквизиты формы,
    события и обработчики на DSL OneBase. Подробнее: <a href="https://github.com/ivanarama/onebase/blob/main/docs/forms.md" target="_blank" style="color:#1a4a80">docs/forms.md</a>.
  </p>
  {{$mine := filterFormsByEntity $.ManagedForms $e.Name}}
  {{if $mine}}
  <table style="width:100%;border-collapse:collapse;margin:8px 0;font-size:12px">
    <thead><tr style="background:#fff;border-bottom:1px solid #e2e8f0">
      <th style="text-align:left;padding:4px 8px">Имя</th>
      <th style="text-align:left;padding:4px 8px">Тип</th>
      <th style="text-align:left;padding:4px 8px">Модуль</th>
      <th></th>
    </tr></thead>
    <tbody>
    {{range $mine}}
    <tr style="border-bottom:1px solid #eef0f5">
      <td style="padding:6px 8px">◇ {{formLabel .Name}}</td>
      <td style="padding:6px 8px">{{if .Kind}}{{.Kind}}{{else}}—{{end}}</td>
      <td style="padding:6px 8px">{{if .HasOS}}есть{{else}}—{{end}}</td>
      <td style="text-align:right;padding:6px 8px">
        <a href="/bases/{{$baseID}}/configurator/forms/edit?entity={{.Entity}}&name={{.Name}}"
           style="display:inline-block;padding:3px 10px;background:#1a4a80;color:#fff;text-decoration:none;border-radius:4px;font-size:11px">
          Редактировать
        </a>
      </td>
    </tr>
    {{end}}
    </tbody>
  </table>
  <p style="margin:4px 0 0;color:#64748b;font-size:11px">
    В пользовательском режиме карточка <b>{{$e.Name}}</b> будет рендериться по
    YAML с маркером ◇ managed, а не авто-форме выше.
  </p>
  {{else}}
  <p style="margin:0 0 10px">У сущности <b>{{$e.Name}}</b> нет управляемых форм. Авто-форма выше используется по умолчанию.</p>
  {{end}}
  <div style="margin-top:10px;display:flex;gap:6px;flex-wrap:wrap;align-items:center">
    <a href="/bases/{{$baseID}}/configurator/forms/edit?entity={{$e.Name}}&name=ФормаОбъекта"
       style="display:inline-block;padding:5px 12px;background:#16a34a;color:#fff;text-decoration:none;border-radius:4px;font-size:12px">
      + Форма объекта
    </a>
    <a href="/bases/{{$baseID}}/configurator/forms/edit?entity={{$e.Name}}&name=ФормаСписка"
       style="display:inline-block;padding:5px 12px;background:#16a34a;color:#fff;text-decoration:none;border-radius:4px;font-size:12px">
      + Форма списка
    </a>
    <a href="/bases/{{$baseID}}/configurator/forms"
       style="display:inline-block;padding:5px 12px;background:#e2e8f0;color:#334155;text-decoration:none;border-radius:4px;font-size:12px">
      Все формы / Импорт из 1С
    </a>
  </div>
</div>
{{end}}`

// ── Register detail (editable) ────────────────────────────────────────────────

const cfgRegDetail = `{{define "register-detail"}}
{{$rg := .Register}}
{{$baseID := .BaseID}}
{{$allEntities := .AllEntityNames}}
{{$fSaved := .FieldsSaved}}
{{$fSavedEnt := .FieldsSavedEntity}}

<form method="POST" action="/bases/{{$baseID}}/configurator/register-fields">
<input type="hidden" name="register" value="{{$rg.Name}}">

{{if $rg.Dimensions}}
<div class="section-hd">{{t $.Lang "Измерения"}}</div>
<table class="fields-tbl">
<tr><th>{{t $.Lang "Поле"}}</th><th>{{t $.Lang "Тип"}}</th><th style="min-width:150px">{{t $.Lang "Объект"}}</th></tr>
{{range $i, $f := $rg.Dimensions}}
<input type="hidden" name="dim.{{$i}}.name" value="{{$f.Name}}">
<tr>
  <td>{{$f.Name}}</td>
  <td>
    <select name="dim.{{$i}}.type" onchange="cfgToggleRef(this,'cfr-{{$rg.Name}}-d{{$i}}')">
      <option value="string"    {{if eq $f.Type "string"}}selected{{end}}>{{t $.Lang "строка"}}</option>
      <option value="number"    {{if eq $f.Type "number"}}selected{{end}}>{{t $.Lang "число"}}</option>
      <option value="date"      {{if eq $f.Type "date"}}selected{{end}}>{{t $.Lang "дата"}}</option>
      <option value="bool"      {{if eq $f.Type "bool"}}selected{{end}}>{{t $.Lang "булево"}}</option>
      <option value="reference" {{if eq $f.Type "reference"}}selected{{end}}>{{t $.Lang "ссылка →"}}</option>
    </select>
  </td>
  <td>
    <select name="dim.{{$i}}.ref" id="cfr-{{$rg.Name}}-d{{$i}}"{{if ne $f.Type "reference"}} style="display:none"{{end}}>
      <option value="">{{t $.Lang "— выбрать —"}}</option>
      {{range $allEntities}}<option value="{{.}}"{{if eq . $f.RefEntity}} selected{{end}}>{{.}}</option>{{end}}
    </select>
  </td>
</tr>
{{end}}
</table>
{{end}}

{{if $rg.Resources}}
<div class="section-hd">{{t $.Lang "Ресурсы"}}</div>
<table class="fields-tbl">
<tr><th>{{t $.Lang "Поле"}}</th><th>{{t $.Lang "Тип"}}</th><th style="min-width:150px">{{t $.Lang "Объект"}}</th></tr>
{{range $i, $f := $rg.Resources}}
<input type="hidden" name="res.{{$i}}.name" value="{{$f.Name}}">
<tr>
  <td>{{$f.Name}}</td>
  <td>
    <select name="res.{{$i}}.type" onchange="cfgToggleRef(this,'cfr-{{$rg.Name}}-r{{$i}}')">
      <option value="string"    {{if eq $f.Type "string"}}selected{{end}}>{{t $.Lang "строка"}}</option>
      <option value="number"    {{if eq $f.Type "number"}}selected{{end}}>{{t $.Lang "число"}}</option>
      <option value="date"      {{if eq $f.Type "date"}}selected{{end}}>{{t $.Lang "дата"}}</option>
      <option value="bool"      {{if eq $f.Type "bool"}}selected{{end}}>{{t $.Lang "булево"}}</option>
      <option value="reference" {{if eq $f.Type "reference"}}selected{{end}}>{{t $.Lang "ссылка →"}}</option>
    </select>
  </td>
  <td>
    <select name="res.{{$i}}.ref" id="cfr-{{$rg.Name}}-r{{$i}}"{{if ne $f.Type "reference"}} style="display:none"{{end}}>
      <option value="">{{t $.Lang "— выбрать —"}}</option>
      {{range $allEntities}}<option value="{{.}}"{{if eq . $f.RefEntity}} selected{{end}}>{{.}}</option>{{end}}
    </select>
  </td>
</tr>
{{end}}
</table>
{{end}}

{{if $rg.Attributes}}
<div class="section-hd">{{t $.Lang "Реквизиты"}}</div>
<table class="fields-tbl">
<tr><th>{{t $.Lang "Поле"}}</th><th>{{t $.Lang "Тип"}}</th><th style="min-width:150px">{{t $.Lang "Объект"}}</th></tr>
{{range $i, $f := $rg.Attributes}}
<input type="hidden" name="attr.{{$i}}.name" value="{{$f.Name}}">
<tr>
  <td>{{$f.Name}}</td>
  <td>
    <select name="attr.{{$i}}.type" onchange="cfgToggleRef(this,'cfr-{{$rg.Name}}-a{{$i}}')">
      <option value="string"    {{if eq $f.Type "string"}}selected{{end}}>{{t $.Lang "строка"}}</option>
      <option value="number"    {{if eq $f.Type "number"}}selected{{end}}>{{t $.Lang "число"}}</option>
      <option value="date"      {{if eq $f.Type "date"}}selected{{end}}>{{t $.Lang "дата"}}</option>
      <option value="bool"      {{if eq $f.Type "bool"}}selected{{end}}>{{t $.Lang "булево"}}</option>
      <option value="reference" {{if eq $f.Type "reference"}}selected{{end}}>{{t $.Lang "ссылка →"}}</option>
    </select>
  </td>
  <td>
    <select name="attr.{{$i}}.ref" id="cfr-{{$rg.Name}}-a{{$i}}"{{if ne $f.Type "reference"}} style="display:none"{{end}}>
      <option value="">{{t $.Lang "— выбрать —"}}</option>
      {{range $allEntities}}<option value="{{.}}"{{if eq . $f.RefEntity}} selected{{end}}>{{.}}</option>{{end}}
    </select>
  </td>
</tr>
{{end}}
</table>
{{end}}

<div class="module-save-row" style="margin-bottom:14px">
  <button class="btn-save" type="submit">{{t $.Lang "Сохранить типы полей"}}</button>
  {{if and $fSaved (eq $fSavedEnt $rg.Name)}}<span class="save-ok">{{t $.Lang "✓ Сохранено"}}</span>{{end}}
</div>
</form>
{{end}}`

// ── Converter tab ─────────────────────────────────────────────────────────────

const cfgTabConvert = `{{define "tab-convert"}}
<div class="pad">
<div class="convert-form">
  <h3>🔄 {{t $.Lang "Импорт конфигурации из выгрузки 1С:Предприятие 8.3"}}</h3>
  <form method="POST" action="/bases/{{.Base.ID}}/configurator/convert">
    <div class="fg">
      <label>{{t $.Lang "Путь к папке XML-выгрузки"}}</label>
      <div class="input-browse">
        <input type="text" id="convert-src-dir" name="src_dir" value="{{.ConvertSrcDir}}"
               placeholder="C:\Users\...\export\МояКонфигурация" autofocus>
        <button type="button" class="btn-browse" onclick="pickDir('convert-src-dir','{{t $.Lang "Выберите папку XML-выгрузки"}}')">📁</button>
      </div>
      <div class="hint">{{t $.Lang "Выгрузка делается в конфигураторе 1С:Предприятие: Конфигурация → Выгрузить конфигурацию в файлы"}}</div>
    </div>
    <div class="form-btns">
      <button class="btn-primary" type="submit" name="apply" value="0">{{t $.Lang "Просмотр"}}</button>
      <button class="btn-secondary" type="submit" name="apply" value="1">{{t $.Lang "Конвертировать и применить"}}</button>
    </div>
  </form>
</div>
{{if .ConvertApplied}}<div class="applied">{{t $.Lang "✓ Конфигурация применена к базе"}}</div>{{end}}
{{if .ConvertResult}}
<div class="convert-result">
  <h3>{{t $.Lang "Результат"}}</h3>
  <pre class="convert-out">{{.ConvertResult}}</pre>
</div>
{{end}}
</div>
{{end}}`

// ── Files tab ─────────────────────────────────────────────────────────────────

const cfgTabFiles = `{{define "tab-files"}}
<div class="pad">
<div class="files-grid">
  <div class="file-card">
    <h3>📤 {{t $.Lang "Выгрузить конфигурацию"}}</h3>
    <p>{{t $.Lang "Экспортирует файлы в"}}<br><code>~/.onebase/workspace/{{.Base.ID}}/</code><br>и открывает папку.</p>
    {{if eq .Base.ConfigSource "database"}}
    <form method="POST" action="/bases/{{.Base.ID}}/config/export">
      <button class="btn-primary" type="submit">{{t $.Lang "Выгрузить"}}</button>
    </form>
    {{else}}
    <p style="color:#888;font-size:12px">{{t $.Lang "Файловый режим — файлы в:"}}<br><code>{{.Base.Path}}</code></p>
    {{end}}
  </div>
  <div class="file-card">
    <h3>📥 {{t $.Lang "Загрузить конфигурацию"}}</h3>
    <p>{{t $.Lang "Загружает файлы из папки в базу данных и применяет миграцию."}}</p>
    {{if eq .Base.ConfigSource "database"}}
    <form method="POST" action="/bases/{{.Base.ID}}/config/import">
      <div class="fg">
        <label>{{t $.Lang "Путь к папке"}}</label>
        <input type="text" name="path" placeholder="~/.onebase/workspace/{{.Base.ID}}">
      </div>
      <button class="btn-primary" type="submit">{{t $.Lang "Загрузить"}}</button>
    </form>
    {{else}}
    <p style="color:#888;font-size:12px">{{t $.Lang "Редактируйте файлы напрямую. Сервер перезагружает конфигурацию автоматически."}}</p>
    {{end}}
  </div>
</div>
</div>
{{end}}`

const cfgTabBackup = `{{define "tab-backup"}}
<div class="pad">
  <h2 style="margin:0 0 4px;font-size:18px">💾 {{t $.Lang "Бэкапы"}}</h2>
  <p style="font-size:12px;color:#64748b;margin:0 0 16px">{{t $.Lang "Резервное копирование и восстановление базы данных"}}</p>
  {{if .BackupMessage}}<div class="success-box">{{.BackupMessage}}</div>{{end}}
  <div style="font-size:12px;color:#64748b;margin-bottom:12px;padding:6px 10px;background:#f8fafc;border:1px solid #e2e8f0;border-radius:4px">
    {{t $.Lang "Папка бэкапов"}}: <code style="background:#e2e8f0;padding:1px 4px;border-radius:3px">{{.BackupDir}}</code>
  </div>
  <form method="POST" action="/bases/{{.Base.ID}}/configurator/backup/create" style="margin-bottom:16px" onsubmit="cfgBackupStart(this,'⏳ Создаю бэкап...')">
    <button class="btn-save" type="submit">{{t $.Lang "Создать бэкап сейчас"}}</button>
  </form>
  <form method="POST" action="/bases/{{.Base.ID}}/configurator/backup/upload" enctype="multipart/form-data" style="margin-bottom:16px;display:flex;align-items:center;gap:8px" onsubmit="cfgBackupStart(this,'⏳ Загружаю...')">
    <input type="file" name="backup_file" accept=".sql.gz,.sql" required style="font-size:12px">
    <button class="btn-save" type="submit">{{t $.Lang "Загрузить файл бэкапа"}}</button>
  </form>
  <h3 style="font-size:13px;margin:0 0 8px;color:#374151">{{t $.Lang "Файлы бэкапов"}}</h3>
  <table class="fields-tbl">
  <tr><th>{{t $.Lang "Файл"}}</th><th>{{t $.Lang "Размер"}}</th><th>{{t $.Lang "Дата"}}</th><th></th></tr>
  {{range .BackupFiles}}
  <tr>
    <td style="font-size:12px">{{.Name}}</td>
    <td style="font-size:12px;color:#64748b">{{.Size}}</td>
    <td style="font-size:12px;color:#64748b">{{.Date}}</td>
    <td style="white-space:nowrap">
      <a href="/bases/{{$.Base.ID}}/configurator/backup/{{.Name}}/download" style="font-size:11px;color:#1a4a80;text-decoration:none">{{t $.Lang "Скачать"}}</a>
      <form method="POST" action="/bases/{{$.Base.ID}}/configurator/backup/{{.Name}}/restore" style="display:inline" onsubmit="if(!confirm('Восстановить {{.Name}}? Текущие данные будут заменены!'))return false;cfgBackupStart(this,'⏳ Восстановление...')">
        <button type="submit" style="font-size:11px;color:#16a34a;background:none;border:none;cursor:pointer;padding:0 4px">{{t $.Lang "Восстановить"}}</button>
      </form>
      <form method="POST" action="/bases/{{$.Base.ID}}/configurator/backup/{{.Name}}/delete" style="display:inline" onsubmit="return confirm('Удалить {{.Name}}?')">
        <button type="submit" style="font-size:11px;color:#dc2626;background:none;border:none;cursor:pointer;padding:0 4px">{{t $.Lang "Удалить"}}</button>
      </form>
    </td>
  </tr>
  {{else}}
  <tr><td colspan="4" style="color:#94a3b8;font-size:12px;padding:8px">{{t $.Lang "Нет бэкапов"}}</td></tr>
  {{end}}
  </table>
  <details style="margin-top:20px"><summary style="font-size:13px;font-weight:600;color:#374151;cursor:pointer;margin-bottom:8px">{{t $.Lang "Настройки автобэкапа"}}</summary>
  <form method="POST" action="/bases/{{.Base.ID}}/configurator/backup/settings">
    <div style="margin-bottom:8px">
      <label style="display:flex;align-items:center;gap:8px;font-size:13px;cursor:pointer">
        <input type="checkbox" name="backup_enabled" {{if .BackupSettings.Enabled}}checked{{end}}>
        {{t $.Lang "Включить автобэкап"}}
      </label>
    </div>
    <div style="margin-bottom:8px">
      <label style="font-size:12px;color:#64748b;display:block;margin-bottom:4px">{{t $.Lang "Расписание"}} (cron)</label>
      <input type="text" name="backup_schedule" value="{{.BackupSettings.Schedule}}" placeholder="0 2 * * *" style="width:200px;padding:4px 8px;border:1px solid #e2e8f0;border-radius:4px;font-size:13px">
    </div>
    <div style="margin-bottom:8px">
      <label style="font-size:12px;color:#64748b;display:block;margin-bottom:4px">{{t $.Lang "Хранить последних"}}</label>
      <input type="number" name="backup_keep" value="{{.BackupSettings.KeepLast}}" placeholder="7" min="1" max="100" style="width:80px;padding:4px 8px;border:1px solid #e2e8f0;border-radius:4px;font-size:13px">
    </div>
    <div style="margin-bottom:8px">
      <label style="font-size:12px;color:#64748b;display:block;margin-bottom:4px">{{t $.Lang "Директория"}} ({{t $.Lang "пусто = по умолчанию"}})</label>
      <input type="text" name="backup_dir" value="{{.BackupSettings.Directory}}" placeholder="{{.BackupDir}}" style="width:100%;max-width:500px;padding:6px 10px;border:1px solid #d1d5db;border-radius:4px;font-size:13px;background:#fff">
    </div>
    <button class="btn-save" type="submit">{{t $.Lang "Сохранить настройки"}}</button>
  </form>
  </details>
  <details style="margin-top:20px"><summary style="font-size:13px;font-weight:600;color:#374151;cursor:pointer;margin-bottom:8px">{{t $.Lang "Полная выгрузка (база + конфигурация)"}}</summary>
  <p style="font-size:12px;color:#64748b;margin:0 0 12px">{{t $.Lang "Выгрузка базы данных и конфигурации в один файл (.obz). Позволяет полностью перенести базу на другой сервер."}}</p>
  <div style="display:flex;gap:12px;align-items:flex-start;flex-wrap:wrap">
    <form method="GET" action="/bases/{{.Base.ID}}/configurator/backup/full-export" style="display:flex;flex-direction:column;gap:6px">
      <label style="display:flex;gap:6px;align-items:center;font-size:12px;color:#374151">
        <input type="checkbox" name="compatible" value="true" checked>
        <span>{{t $.Lang "Совместимый формат (PostgreSQL ↔ SQLite)"}}</span>
      </label>
      <div style="font-size:11px;color:#64748b;margin-left:22px">{{t $.Lang "Без галки — быстрый бинарный дамп, только для той же СУБД"}}</div>
      <button class="btn-save" type="submit" style="width:fit-content">{{t $.Lang "Выгрузить всё в .obz"}}</button>
    </form>
    <form method="POST" action="/bases/{{.Base.ID}}/configurator/backup/full-import" enctype="multipart/form-data" style="display:flex;align-items:center;gap:8px" onsubmit="if(!confirm('Восстановить из .obz файла? Все текущие данные будут заменены!'))return false;cfgBackupStart(this,'⏳ Восстановление из .obz...')">
      <input type="file" name="obz_file" accept=".obz" required style="font-size:12px">
      <button class="btn-save" type="submit" style="background:#dc2626">{{t $.Lang "Загрузить из .obz"}}</button>
    </form>
  </div>
  </details>
  <details style="margin-top:12px"><summary style="font-size:13px;font-weight:600;color:#374151;cursor:pointer;margin-bottom:8px">{{t $.Lang "Перенос конфигурации (только метаданные)"}}</summary>
  <p style="font-size:12px;color:#64748b;margin:0 0 12px">{{t $.Lang "Экспортируйте конфигурацию в ZIP для переноса на другой сервер или импортируйте из архива."}}</p>
  <div style="display:flex;gap:12px;align-items:flex-start;flex-wrap:wrap">
    <a href="/bases/{{.Base.ID}}/configurator/config/export-zip" class="btn-save" style="text-decoration:none;display:inline-block">{{t $.Lang "Экспорт в ZIP"}}</a>
    <form method="POST" action="/bases/{{.Base.ID}}/configurator/config/import-zip" enctype="multipart/form-data" style="display:flex;align-items:center;gap:8px">
      <input type="file" name="config_zip" accept=".zip" required style="font-size:12px">
      <button class="btn-save" type="submit">{{t $.Lang "Импорт из ZIP"}}</button>
    </form>
  </div>
  </details>
</div>
{{end}}`
