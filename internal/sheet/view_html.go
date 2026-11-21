package sheet

import (
	"encoding/json"
	"fmt"
	"strings"
)

func (d *Document) viewerScript(sb *strings.Builder, notifyParent bool) {
	groups, _ := json.Marshal(d.ResolvedGroups())
	// Readonly local controls: no network, form inputs or parent dirty events.
	fmt.Fprintf(sb, `<style>@media print{tr[hidden]{display:table-row!important}}.sheet-fold{border:0;background:transparent;cursor:pointer;margin-right:5px}.sheet-fixed{position:sticky;background:white;z-index:2}</style>
<script>(function(){'use strict';var groups=(%s)||[],rows=Array.from(document.querySelectorAll('tr[data-sheet-row]'));
function paint(){rows.forEach(function(row,i){row.hidden=groups.some(function(g){return !g.Expanded&&i>g.HeaderEnd&&i<=g.End;});});}
groups.forEach(function(g){var row=rows[g.Start];if(!row||!row.cells.length||g.End<=g.HeaderEnd)return;var b=document.createElement('button');b.className='sheet-fold';b.type='button';b.setAttribute('aria-label',g.Title||'Группа');function label(){b.textContent=g.Expanded?'−':'+';b.setAttribute('aria-expanded',String(g.Expanded));}b.onclick=function(){g.Expanded=!g.Expanded;label();paint();};label();row.cells[0].prepend(b);});paint();
var top=document.querySelector('.doc-toolbar').getBoundingClientRect().height;rows.forEach(function(row,i){var left=0,col=0;Array.from(row.cells).forEach(function(c){if(i<%d){c.style.position='sticky';c.style.top=top+'px';c.style.zIndex='3';c.style.background=c.style.background||'white';}if(col<%d){c.style.position='sticky';c.style.left=left+'px';c.style.zIndex=i<%d?'4':'2';c.style.background=c.style.background||'white';}left+=c.getBoundingClientRect().width;col+=c.colSpan;});if(i<%d)top+=row.getBoundingClientRect().height;});
if(%t&&window.parent!==window)document.addEventListener('click',function(e){var a=e.target.closest('[data-sheet-drilldown]');if(!a)return;e.preventDefault();window.parent.postMessage({type:'ob-tabular-cell',url:a.href},location.origin);});
})();</script>`, groups, d.FreezeRows, d.FreezeCols, d.FreezeRows, d.FreezeRows, notifyParent)
}
