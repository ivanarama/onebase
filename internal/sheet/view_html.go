package sheet

import (
	"encoding/json"
	"fmt"
	"strings"
)

func (d *Document) viewerScript(sb *strings.Builder, notifyParent bool) {
	groups, _ := json.Marshal(d.ResolvedGroups())
	// Readonly local controls: no network, form inputs or parent dirty events.
	fmt.Fprintf(sb, `<style>@media print{tr[hidden]{display:table-row!important}.sheet-fold,.sheet-levels{display:none!important}}.sheet-fold{border:1px solid currentColor;border-radius:2px;background:transparent;cursor:pointer;margin-right:5px;color:inherit;width:1.25em;height:1.25em;padding:0;line-height:1}.sheet-levels{display:inline-flex;gap:4px}.doc-toolbar .sheet-level{padding:4px 8px}.sheet-level[aria-pressed=true]{font-weight:700;outline:2px solid currentColor;outline-offset:-2px}.sheet-fixed{position:sticky;background:white;z-index:2}</style>
<script>(function(){'use strict';var groups=(%s)||[],rows=Array.from(document.querySelectorAll('tr[data-sheet-row]'));
var folds=[],levelButtons=[],toolbar=document.querySelector('.doc-toolbar');
function paint(){rows.forEach(function(row,i){row.hidden=groups.some(function(g){return !g.Expanded&&i>g.HeaderEnd&&i<=g.End;});});folds.forEach(function(f){f.button.textContent=f.group.Expanded?'−':'+';f.button.setAttribute('aria-expanded',String(f.group.Expanded));});}
groups.forEach(function(g){var row=rows[g.Start];if(!row||!row.cells.length||g.End<=g.HeaderEnd)return;var b=document.createElement('button');b.className='sheet-fold';b.type='button';b.setAttribute('aria-label',g.Title||'Группа');folds.push({button:b,group:g});b.onclick=function(){g.Expanded=!g.Expanded;levelButtons.forEach(function(button){button.setAttribute('aria-pressed','false');});paint();};row.cells[0].prepend(b);});
if(groups.length&&toolbar){var min=groups.reduce(function(n,g){return Math.min(n,g.Level);},groups[0].Level),max=groups.reduce(function(n,g){return Math.max(n,g.Level);},min);
var details=rows.some(function(row,i){return groups.some(function(g){return i>g.HeaderEnd&&i<=g.End;})&&!groups.some(function(g){return i>=g.Start&&i<=g.HeaderEnd;});});
var panel=document.createElement('span');panel.className='sheet-levels';panel.setAttribute('role','group');panel.setAttribute('aria-label','Уровни группировки');
for(var n=1;n<=max-min+1+(details?1:0);n++){(function(level){var b=document.createElement('button');b.type='button';b.className='sheet-level';b.textContent=String(level);b.setAttribute('data-sheet-level',String(level));b.setAttribute('aria-pressed','false');b.onclick=function(){groups.forEach(function(g){g.Expanded=g.Level-min+1<level;});levelButtons.forEach(function(button){button.setAttribute('aria-pressed',String(button===b));});paint();};levelButtons.push(b);panel.appendChild(b);})(n);}toolbar.appendChild(panel);}paint();
var top=document.querySelector('.doc-toolbar').getBoundingClientRect().height;rows.forEach(function(row,i){var left=0,col=0;Array.from(row.cells).forEach(function(c){if(i<%d){c.style.position='sticky';c.style.top=top+'px';c.style.zIndex='3';c.style.background=c.style.background||'white';}if(col<%d){c.style.position='sticky';c.style.left=left+'px';c.style.zIndex=i<%d?'4':'2';c.style.background=c.style.background||'white';}left+=c.getBoundingClientRect().width;col+=c.colSpan;});if(i<%d)top+=row.getBoundingClientRect().height;});
if(%t&&window.parent!==window)document.addEventListener('click',function(e){var a=e.target.closest('[data-sheet-drilldown]');if(!a)return;e.preventDefault();window.parent.postMessage({type:'ob-tabular-cell',url:a.href},location.origin);});
})();</script>`, groups, d.FreezeRows, d.FreezeCols, d.FreezeRows, d.FreezeRows, notifyParent)
}
