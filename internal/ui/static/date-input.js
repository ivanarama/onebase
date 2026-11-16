/* Date masks keep native field identity, canonical values and calendar. */
(function () {
  'use strict';
  var editing = new WeakMap(), nativePicker = new WeakSet(), buttons = new WeakMap();
  var labels = {};
  try { labels = JSON.parse(document.getElementById('ob-date-input-i18n').textContent); } catch (_) {}
  function eligible(el) {
    return el && el.tagName === 'INPUT' && !el.disabled && !el.readOnly &&
      (editing.has(el) || el.type === 'date' || el.type === 'datetime-local');
  }
  function pattern(type) { return type === 'datetime-local' ? '__.__.____ __:__:__' : '__.__.____'; }
  function pad(n, count) { return String(n).padStart(count || 2, '0'); }
  function canonicalDigits(value, type) {
    var m = String(value || '').match(/^(\d{4})-(\d{2})-(\d{2})(?:[ T](\d{2}):(\d{2})(?::(\d{2}))?)?$/);
    return m ? m[3]+m[2]+m[1]+(type === 'datetime-local' ? (m[4]||'00')+(m[5]||'00')+(m[6]||'00') : '') : '';
  }
  function draw(el, state, caret) {
    var text = state.mask.split('');
    state.slots.forEach(function (pos, i) { text[pos] = state.digits[i] || '_'; });
    state.rendered = text.join(''); el.value = state.rendered;
    if (el.setSelectionRange && caret !== undefined) el.setSelectionRange(caret, caret);
  }
  function clearError(el, state) {
    state.badPaste = false; el.setCustomValidity('');
    if (el.removeAttribute) el.removeAttribute('aria-invalid');
  }
  function notify(el, state) {
    clearError(el, state); el.dispatchEvent(new Event('input', {bubbles:true}));
  }
  function buttonVisible(el, show) { var b = buttons.get(el); if (b) b.style.display = show ? '' : 'none'; }
  function calendarButton(el) {
    if (buttons.has(el) || !el.parentNode || !document.createElement || typeof el.showPicker !== 'function') return;
    var parent=el.parentNode, b=document.createElement('button');
    if (window.getComputedStyle(parent).position==='static') parent.style.position='relative';
    b.type = 'button'; b.tabIndex = -1; b.textContent = '▦'; b.title = labels.calendar || 'Open calendar';
    b.setAttribute('aria-label', b.title); b.setAttribute('aria-haspopup', 'dialog');
    b.style.cssText = 'position:absolute;width:24px;height:22px;border:1px solid currentColor;border-radius:4px;background:transparent;color:inherit;cursor:pointer';
    b.addEventListener('pointerdown', function (e) { e.preventDefault(); });
    b.addEventListener('click', function () { openCalendar(el); });
    parent.appendChild(b); buttons.set(el, b);
  }
  function placeCalendar(el) {
    var b=buttons.get(el); if (!b) return;
    var outer=el.parentNode.getBoundingClientRect(), rect=el.getBoundingClientRect();
    b.style.left=(rect.right-outer.left-29)+'px'; b.style.top=(rect.top-outer.top+(rect.height-22)/2)+'px';
    el.style.paddingRight='34px';
  }
  function begin(el) {
    if (editing.has(el)) return editing.get(el);
    nativePicker.delete(el);
    var mask = pattern(el.type), slots = [], raw = canonicalDigits(el.value, el.type);
    for (var i=0; i<mask.length; i++) if (mask[i] === '_') slots.push(i);
    var state = {type:el.type, value:el.value, mask:mask, slots:slots, digits:slots.map(function (_, j) {return raw[j]||'';}),
      placeholder:el.getAttribute ? el.getAttribute('placeholder') : null, inputmode:el.getAttribute ? el.getAttribute('inputmode') : null,
      paddingRight:el.style ? el.style.paddingRight : ''};
    if (state.type==='datetime-local' && el.hasAttribute && !el.hasAttribute('step')) el.setAttribute('step','1');
    editing.set(el, state); el.type = 'text';
    if (el.setAttribute) { el.setAttribute('placeholder', mask); el.setAttribute('inputmode', 'numeric'); }
    calendarButton(el); placeCalendar(el); buttonVisible(el, true); draw(el, state, 0); return state;
  }
  function restoreAttr(el, name, value) {
    if (!el.setAttribute) return;
    if (value === null) el.removeAttribute(name); else el.setAttribute(name, value);
  }
  function finish(el, state, value) {
    clearError(el, state); editing.delete(el); el.type = state.type; el.value = value;
    if (el.style) el.style.paddingRight=state.paddingRight;
    restoreAttr(el, 'placeholder', state.placeholder); restoreAttr(el, 'inputmode', state.inputmode); buttonVisible(el, false);
  }
  function normalized(state) {
    if (!state.digits.some(Boolean)) return '';
    if (state.digits.some(function (d) {return !d;})) return null;
    var raw=state.digits.join(''), day=+raw.slice(0,2), month=+raw.slice(2,4), year=+raw.slice(4,8);
    if (year<1 || year>9999 || month<1 || month>12 || day<1 || day>31) return false;
    var date=new Date(0); date.setHours(12,0,0,0); date.setFullYear(year,month-1,day);
    if (date.getFullYear()!==year || date.getMonth()!==month-1 || date.getDate()!==day) return false;
    var value=pad(year,4)+'-'+pad(month)+'-'+pad(day);
    if (state.type === 'datetime-local') {
      var h=+raw.slice(8,10), m=+raw.slice(10,12), s=+raw.slice(12,14);
      if (h>23 || m>59 || s>59) return false;
      value+='T'+pad(h)+':'+pad(m)+':'+pad(s);
    }
    return value;
  }
  function fail(el, state, incomplete) {
    var message=incomplete ? (state.type==='datetime-local' ? labels.incompleteDatetime : labels.incompleteDate) : labels.invalid;
    el.setCustomValidity(message || (incomplete ? 'Enter the complete date' : 'Invalid date'));
    if (el.setAttribute) el.setAttribute('aria-invalid','true'); return false;
  }
  function commit(el) {
    var state=editing.get(el); if (!state) return true;
    var value=normalized(state);
    if (state.badPaste || value===false) return fail(el,state,false);
    if (value===null || value==='' && el.required) return fail(el,state,true);
    finish(el,state,value);
    if (el.validity && !el.validity.valid) {
      var nativeError=el.validationMessage, again=begin(el); again.value=state.value;
      el.setCustomValidity(nativeError); if (el.setAttribute) el.setAttribute('aria-invalid','true'); return false;
    }
    if (el.value!==state.value) {
      el.dispatchEvent(new Event('input',{bubbles:true})); el.dispatchEvent(new Event('change',{bubbles:true}));
    }
    return true;
  }
  window.obCommitDateInput=commit;
  // Managed form handlers can replace a focused date without leaving an old
  // draft in the mask. Keep the same native control and emit no extra events.
  window.obSetDateInputValue=function (el, value) {
    var state=editing.get(el); if (!state) return false;
    var caret=el.selectionStart||0, nativeValue=value==null ? '' : String(value);
    if (state.type==='date') nativeValue=nativeValue.split(/[T ]/)[0];
    finish(el,state,nativeValue);
    if (document.activeElement===el && !el.disabled && !el.readOnly) {
      state=begin(el); draw(el,state,Math.min(caret,state.mask.length));
    }
    return true;
  };
  function openCalendar(el) {
    var state=editing.get(el);
    if (state) {var value=normalized(state); finish(el,state,typeof value==='string' ? value : state.value);}
    nativePicker.add(el);
    try {el.showPicker();} catch (_) {nativePicker.delete(el); el.focus();}
  }
  function edit(el, text, direction) {
    var state=begin(el), start=el.selectionStart||0, end=el.selectionEnd||0, selected=end>start;
    var index=state.slots.findIndex(function (p) {return p>=start;}); if (index<0) index=state.slots.length;
    if (text && text.length>state.slots.length-index) {state.badPaste=true; fail(el,state,false); return;}
    if (selected) state.slots.forEach(function (p,i) {if (p>=start && p<end) state.digits[i]='';});
    if (!text) {
      if (!selected && direction<0) index--;
      if (index<0) return;
      if (!selected && index<state.slots.length) state.digits[index]='';
    } else {for (var i=0; i<text.length; i++) state.digits[index++]=text[i];}
    draw(el,state,state.slots[index]===undefined ? state.mask.length : state.slots[index]); notify(el,state);
  }
  function pasted(text,type) {
    var raw=String(text).trim();
    if (/^\d{4}-\d{2}-\d{2}(?:[ T]\d{2}:\d{2}(?::\d{2})?)?$/.test(raw)) return canonicalDigits(raw,type);
    if (/^[\d./:\s-]+$/.test(raw)) return raw.replace(/\D/g,'');
    return raw==='' ? '' : null;
  }
  document.addEventListener('focus',function (e) {if (eligible(e.target) && !nativePicker.has(e.target)) begin(e.target);},true);
  document.addEventListener('keydown',function (e) {
    var el=e.target; if (e.isComposing) return;
    if (e.altKey && e.key==='ArrowDown' && eligible(el) && typeof el.showPicker==='function') {e.preventDefault(); e.stopPropagation(); openCalendar(el); return;}
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase()==='v' && eligible(el)) {begin(el); return;}
    if (e.ctrlKey || e.altKey || e.metaKey) return;
    if (el && el.tagName==='INPUT' && !el.disabled && el.readOnly && (editing.has(el) || el.type==='date' || el.type==='datetime-local') && e.key==='Enter') {
      e.preventDefault(); e.stopPropagation();
      if (editing.has(el)) {var locked=editing.get(el); finish(el,locked,locked.value);}
      if (!(typeof window.obEnterNavigateFrom==='function' && window.obEnterNavigateFrom(el))) el.blur(); return;
    }
    if (!eligible(el)) return;
    if (e.key==='Escape' && editing.has(el)) {var old=editing.get(el); finish(el,old,old.value); nativePicker.add(el); e.preventDefault(); e.stopPropagation(); return;}
    if (e.key==='Enter') {
      e.preventDefault(); e.stopPropagation(); if (!commit(el)) {if (el.reportValidity) el.reportValidity(); return;}
      nativePicker.add(el); if (!(typeof window.obEnterNavigateFrom==='function' && window.obEnterNavigateFrom(el))) el.blur(); return;
    }
    if (/^\d$/.test(e.key)) {e.preventDefault(); edit(el,e.key); return;}
    if (editing.has(el) && (e.key==='Backspace' || e.key==='Delete')) {e.preventDefault(); edit(el,'',e.key==='Backspace' ? -1 : 1); return;}
    if (editing.has(el) && e.key.length===1) e.preventDefault();
  },true);
  document.addEventListener('beforeinput',function (e) {
    if (!editing.has(e.target) || e.isComposing) return;
    if (e.inputType==='insertText') {e.preventDefault(); if (/^\d+$/.test(e.data||'')) edit(e.target,e.data);}
    else if (e.inputType==='deleteContentBackward' || e.inputType==='deleteContentForward' || e.inputType==='deleteByCut') {e.preventDefault(); edit(e.target,'',e.inputType==='deleteContentBackward' ? -1 : 1);}
  },true);
  document.addEventListener('paste',function (e) {
    if (!eligible(e.target) || !e.clipboardData) return;
    e.preventDefault(); var state=begin(e.target), digits=pasted(e.clipboardData.getData('text'),state.type);
    if (digits===null) {state.badPaste=true; fail(e.target,state,false); return;}
    if (digits.length===state.slots.length || digits.length===8) e.target.setSelectionRange(0,digits.length===8 ? 10 : state.mask.length);
    edit(e.target,digits,1);
  },true);
  document.addEventListener('input',function (e) {
    var state=editing.get(e.target); if (!state) return;
    if (e.target.value!==state.rendered) {
      var digits=pasted(e.target.value,state.type);
      if (digits===null || digits.length>state.slots.length) {state.badPaste=true; fail(e.target,state,false); return;}
      state.digits=state.slots.map(function (_,i) {return digits[i]||'';}); draw(e.target,state,state.mask.length);
    }
    clearError(e.target,state);
  },true);
  document.addEventListener('blur',function (e) {
    if (eligible(e.target)) commit(e.target);
    else if (editing.has(e.target)) {var locked=editing.get(e.target); finish(e.target,locked,canonicalDigits(e.target.value,locked.type) ? e.target.value : locked.value);}
    nativePicker.delete(e.target);
  },true);
  document.addEventListener('submit',function (e) {
    var form=e.target, invalid; if (!form || !form.querySelectorAll) return;
    Array.from(form.querySelectorAll('input')).forEach(function (el) {if (editing.has(el) && !commit(el) && !invalid) invalid=el;});
    if (!invalid && form.checkValidity && !form.checkValidity()) invalid=form.querySelector('input:invalid');
    if (invalid) {e.preventDefault(); e.stopImmediatePropagation(); if (invalid.focus) invalid.focus(); if (invalid.reportValidity) invalid.reportValidity();}
  },true);
})();
