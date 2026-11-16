(function () {
 'use strict';
 if (window.__obRecursiveNav) return;
 window.__obRecursiveNav = true;
 function summary(folder) { return folder.querySelector(':scope > summary'); }
 function reflect(folder) { var s=summary(folder); if(s)s.setAttribute('aria-expanded',String(folder.open)); }
 function persist(folder) { reflect(folder);try {localStorage.setItem(folder.dataset.navKey,folder.open?'1':'0');}catch(e){} }
 function ancestors(link) { for(var p=link.parentElement;p;p=p.parentElement)if(p.matches('details.navtree')){p.open=true;reflect(p);} }
 function active(root) {
  var current=new URL(location.href);
  root.querySelectorAll('a[href]').forEach(function(link){
   var target=new URL(link.href);
   if(target.pathname===current.pathname && target.searchParams.get('subsystem')===current.searchParams.get('subsystem'))ancestors(link);
  });
 }
 function init() {
  var root=document.getElementById('ob-nav');if(!root)return;
  root.querySelectorAll('details.navtree').forEach(function(folder){
   if(folder.dataset.treeInitialized)return;
   folder.dataset.treeInitialized='1';
   try{var saved=localStorage.getItem(folder.dataset.navKey);if(saved==='0'||saved==='1')folder.open=saved==='1';}catch(e){}
   reflect(folder);
   folder.addEventListener('toggle',function(){reflect(folder);});
   var s=summary(folder);
   s.addEventListener('click',function(){setTimeout(function(){persist(folder);},0);});
   s.addEventListener('keydown',function(e){
    if(e.key==='ArrowRight'){e.preventDefault();if(!folder.open){folder.open=true;persist(folder);}else{var child=folder.querySelector(':scope > a, :scope > details > summary');if(child)child.focus();}}
    if(e.key==='ArrowLeft'){e.preventDefault();if(folder.open){folder.open=false;persist(folder);}else{var parent=folder.parentElement.closest('details.navtree');if(parent)summary(parent).focus();}}
   });
  });
  if(!root.dataset.treeActiveInitialized){root.dataset.treeActiveInitialized='1';active(root);root.addEventListener('click',function(e){var link=e.target.closest('a[href]');if(link)ancestors(link);});}
 }
 if(document.readyState==='loading')document.addEventListener('DOMContentLoaded',init);else init();
 new MutationObserver(function(records){if(records.some(function(r){return Array.from(r.addedNodes).some(function(n){return n.nodeType===1&&(n.matches('#ob-nav, details.navtree')||n.querySelector('#ob-nav, details.navtree'));});}))init();}).observe(document.documentElement,{childList:true,subtree:true});
 var style=document.createElement('style');style.textContent='#ob-nav .navtree{margin:0;padding:0;max-width:none;background:transparent;border-radius:0;box-shadow:none;color:#cbd5e1}#ob-nav .navtree>summary{cursor:pointer;padding:8px 10px;font-size:13px;font-weight:600;color:#cbd5e1;overflow-wrap:anywhere}#ob-nav .navtree>summary:hover{color:#fff;background:#334155}#ob-nav .navtree .navtree{margin-left:12px}#ob-nav .navtree>a{padding-left:18px}#ob-nav .navtree>summary:focus-visible{outline:2px solid #7dd3fc;outline-offset:-2px}';document.head.appendChild(style);
})();
