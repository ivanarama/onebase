import json, os, sys
from pathlib import Path
from playwright.sync_api import sync_playwright
base, owner, first, second, call_id = sys.argv[1:]
proof = os.environ.get('ONEBASE_AUDIO_PROOF_DIR')
if proof:
    Path(proof).mkdir(parents=True, exist_ok=True)
with sync_playwright() as p:
    browser = p.chromium.launch(headless=True, executable_path=os.environ.get("ONEBASE_AUDIO_CHROMIUM"))
    try:
        page = browser.new_page(viewport={'width':1100,'height':760})
        page.set_default_timeout(7000)
        requests, events, metadata, errors, geometry, close_events, close_modes = [], [], [], [], [], [], []
        def compact(label):
            m=page.locator('[data-ob-audio]').evaluate('''r=>{
              const b=r.getBoundingClientRect(),a=r.querySelector('audio');
              return {height:b.height,width:b.width,display:getComputedStyle(r).display,
                controls:a.controls,hidden:a.hidden,
                children:Array.from(r.children).filter(e=>e!==a&&e.getBoundingClientRect().width>0).map(e=>{
                  const q=e.getBoundingClientRect();return {tag:e.tagName,y:q.y-b.y,h:q.height};}),
                seek:r.querySelector('[data-ob-audio-seek]').getBoundingClientRect().width};}''')
            assert 28<=m['height']<=30.5 and m['width']<=600.5,m
            assert not m['controls'] and m['hidden'] and m['display']=='inline-flex',m
            assert 39.5<=m['seek']<=120.5,m
            assert all(-0.5<=c['y'] and c['y']+c['h']<=m['height']+0.5 for c in m['children']),m
            geometry.append({'label':label,**m})
        page.on('request', lambda r: requests.append(r.url))
        page.on('pageerror', lambda e: errors.append(str(e)))
        def response(r):
            if r.request.method == 'POST' and '/form-event' in r.url:
                events.append(r.json())
            if r.request.method == 'POST' and '/form-close-intent' in r.url:
                close_events.append(r.json())
                close_modes.append(r.request.headers.get('x-onebase-close-mode'))
            if r.url.endswith('/attachments'):
                metadata.append(r.json())
        page.on('response', response)
        page.goto(base+'/ui/catalog/Calls/'+call_id, wait_until='domcontentloaded')
        player = page.locator('[data-ob-audio]')
        media = page.locator('[data-ob-audio-media]')
        assert player.count() == 1
        assert media.get_attribute('preload') == 'none' and media.get_attribute('src') is None
        assert not any('/attachments' in u for u in requests)
        compact('managed-initial')
        assert page.evaluate('window._obFormDirty') is False
        if proof: page.screenshot(path=str(Path(proof)/'01-managed-before-play.png'))
        media.evaluate('(a)=>a.muted=true')
        page.locator('[data-ob-audio-play]').click()
        page.wait_for_function('()=>document.querySelector("audio").currentTime>0.05')
        assert media.get_attribute('src') == '/ui/attachments/'+first+'/download'
        assert page.locator('[data-ob-audio-caption]').inner_text() == 'Call A'
        assert len(events)==1, events
        assert events[-1].get('dirty') is False and page.evaluate('window._obFormDirty') is False
        page.locator('[data-ob-audio-play]').click()
        assert media.evaluate('(a)=>a.paused')
        page.locator('[data-ob-audio-play]').click()
        page.wait_for_function('()=>!document.querySelector("audio").paused')
        assert len(events)==1
        # Capture the actual native rejection while returning the SAME Promise.
        media.evaluate('''a=>{const native=a.play;a._playErrors=[];a.play=function(...args){
          const p=Reflect.apply(native,this,args);p.catch(e=>a._playErrors.push(e.name));return p;};}''')
        page.locator('[data-ob-audio-stop]').click()
        page.evaluate('''()=>{document.querySelector('[data-ob-audio-play]').click();document.querySelector('audio').pause();}''')
        page.wait_for_function('()=>document.querySelector("audio")._playErrors.includes("AbortError")')
        assert page.locator('[data-ob-audio-status]').inner_text()==''
        compact('native-early-pause')
        page.locator('[data-ob-audio-play]').click()
        page.wait_for_function('()=>!document.querySelector("audio").paused')
        page.locator('[data-ob-audio-stop]').click()
        assert media.get_attribute('src') is None and media.evaluate('(a)=>a.paused')
        page.locator('[data-ob-audio-play]').click()
        page.wait_for_function('()=>!document.querySelector("audio").paused')
        assert len(events)==1
        page.locator('[data-ob-audio-play]').click()
        assert media.evaluate('(a)=>a.paused')
        dirty_before=page.evaluate('window._obFormDirty')
        page.evaluate('''()=>{window._audioNativeChanges=[];['input','change'].forEach(t=>document.addEventListener(t,e=>{
          if(e.target.closest('[data-ob-audio]'))window._audioNativeChanges.push(t);},true));}''')
        page.locator('[data-ob-audio-seek]').press('Home')
        page.locator('[data-ob-audio-seek]').press('ArrowRight')
        seek=page.locator('[data-ob-audio-seek]').bounding_box()
        page.mouse.click(seek['x']+seek['width']*0.7,seek['y']+seek['height']/2)
        page.locator('[data-ob-audio-volume]').press('Home')
        page.locator('[data-ob-audio-volume]').press('ArrowRight')
        assert abs(media.evaluate('(a)=>a.volume')-0.05)<0.01
        assert media.evaluate('(a)=>a.currentTime')>=0.7
        assert page.evaluate('window._audioNativeChanges')==[]
        assert page.evaluate('window._obFormDirty')==dirty_before
        assert dirty_before is False
        compact('keyboard-pointer-seek-volume-no-dirty')
        if proof: page.screenshot(path=str(Path(proof)/'02-managed-clean-controls.png'))
        page.locator('[data-ob-audio-stop]').click()
        # Click the actual standard Close control. No explicit discard mode,
        # no dirty setter, no patched lifecycle: clean state must choose the
        # existing discard intent and navigate without any save prompt.
        page.locator('button[data-ob-form-close=""]').click()
        page.wait_for_url(base+'/ui/catalog/calls')
        assert len(close_events)==1 and close_modes==['discard'],close_events
        assert close_events[0].get('ok') and close_events[0].get('close',{}).get('allowed'),close_events
        assert close_events[0].get('dirty') is False,close_events
        assert not close_events[0].get('savedId') and not close_events[0].get('version'),close_events
        assert not page.locator('#ob-managed-close-confirm').count()
        page.goto(base+'/ui/catalog/Calls/'+call_id,wait_until='domcontentloaded')
        assert page.evaluate('window._obFormDirty') is False
        player=page.locator('[data-ob-audio]')
        media=page.locator('[data-ob-audio-media]')
        media.evaluate('(a)=>a.muted=true')
        # A genuine trusted input must survive a fresh server Play, not only a
        # cached media resume. Keep that edited page open throughout the test.
        page.locator('input[name="Name"]').fill('Own real unsaved edit')
        page.locator('input[name="Name"]').press('Tab')
        assert page.evaluate('window._obFormDirty') is True
        # SelectB is deliberately an ordinary command changing Choice as well
        # as playback carriers. Its dirty semantics are outside AudioPlay's
        # narrow exception, so exercise it only after the genuine native edit.
        page.locator('[data-ob-fire-click="SelectB"]').click()
        page.wait_for_function('()=>document.querySelector("[data-ob-audio-caption]").textContent==="Call B"')
        assert media.get_attribute('src') is None
        page.locator('[data-ob-audio-play]').click()
        page.wait_for_function('()=>document.querySelector("audio").currentTime>0.05')
        assert media.get_attribute('src') == '/ui/attachments/'+second+'/download'
        assert len(events)==3 and all(e.get('ok') for e in events), events
        assert events[-1].get('dirty') is True and page.evaluate('window._obFormDirty') is True
        assert page.locator('input[name="Name"]').input_value()=='Own real unsaved edit'
        # The very same Close control must protect a genuine native edit.
        page.locator('button[data-ob-form-close=""]').click()
        page.locator('#ob-managed-close-confirm').wait_for(state='visible')
        assert len(close_events)==1
        page.locator('[data-ob-close-choice="cancel"]').click()
        assert page.url==base+'/ui/catalog/Calls/'+call_id
        assert page.evaluate('window._obFormDirty') is True
        assert page.locator('input[name="Name"]').input_value()=='Own real unsaved edit'
        assert len(close_events)==1
        dirty_page=page
        page=browser.new_page(viewport={'width':1100,'height':760})
        page.set_default_timeout(7000)
        page.on('request',lambda r:requests.append(r.url))
        page.on('pageerror',lambda e:errors.append(str(e)))
        page.on('response',response)
        requests.clear()
        page.goto(base+'/ui/catalog/Recordings/'+owner,wait_until='domcontentloaded')
        assert page.locator('[data-ob-audio]').count()==1
        compact('auto-audio-only')
        assert not page.locator('[data-ob-attachments],#att-upload-form').count()
        assert not any('/attachments' in u for u in requests), requests
        media=page.locator('[data-ob-audio-media]')
        assert media.get_attribute('src') is None
        media.evaluate('(a)=>a.muted=true')
        page.locator('[data-ob-audio-play]').click()
        page.wait_for_function('()=>document.querySelector("audio").currentTime>0.05')
        assert len(metadata)==1 and len(metadata[0])>=1
        attachment=media.get_attribute('src')
        partial=page.request.get(base+attachment,headers={'Range':'bytes=0-43'})
        assert partial.status==206 and partial.headers['content-range'].startswith('bytes 0-43/')
        page.locator('[data-ob-audio-stop]').click()
        page.locator('[data-ob-audio-play]').click()
        page.wait_for_function('()=>!document.querySelector("audio").paused')
        assert len(metadata)==1 and media.get_attribute('src')==attachment
        if proof: page.screenshot(path=str(Path(proof)/'03-auto-audio-only.png'))
        requests.clear()
        page.goto(base+'/ui/catalog/Ordinary/new',wait_until='domcontentloaded')
        assert page.locator('[data-ob-audio]').count()==0
        assert not any('/static/audio.js' in u for u in requests)
        assert not errors,errors
        assert dirty_page.evaluate('window._obFormDirty') is True
        assert dirty_page.locator('input[name="Name"]').input_value()=='Own real unsaved edit'
        result={'public_http_browser':'PASS','geometry':geometry,'play_preparations':2,'metadata_lists':1,'range':206,'silent_wav':True,'native_early_pause':'AbortError/status empty','seek_volume_native_input_change_events':[],'initial_dirty':False,'after_clean_server_Play_dirty':False,'dirty_unchanged':True,'real_native_edit_after_fresh_server_Play_dirty':True,'real_native_edit_retained':True,'Save_invoked':False,'clean_standard_Close':'allowed/discard/noSave/noPrompt','real_edit_standard_Close':'prompt/Cancel/noSave/editRetained','ordinary_audio_asset_requests':0,'js_errors':errors}
        if proof: (Path(proof)/'receipt.json').write_text(json.dumps(result,ensure_ascii=False,indent=2),encoding='utf-8')
        print(json.dumps(result))
    except Exception:
        print(json.dumps({'errors':errors,'events':events,'requests':requests,'player':page.locator('[data-ob-audio]').evaluate_all('(xs)=>xs.map(x=>x.outerHTML)'), 'media':page.locator('audio').evaluate_all('(xs)=>xs.map(a=>({src:a.getAttribute("src"),paused:a.paused,error:a.error&&a.error.message,ready:a.readyState,time:a.currentTime}))')},ensure_ascii=True),flush=True)
        raise
    finally:
        browser.close()
