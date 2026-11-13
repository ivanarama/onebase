import json, os, sys
from pathlib import Path
from playwright.sync_api import sync_playwright
base, owner, first, second = sys.argv[1:]
proof = os.environ.get('ONEBASE_AUDIO_PROOF_DIR')
if proof:
    Path(proof).mkdir(parents=True, exist_ok=True)
with sync_playwright() as p:
    browser = p.chromium.launch(headless=True, executable_path=os.environ.get("ONEBASE_AUDIO_CHROMIUM"))
    try:
        page = browser.new_page(viewport={'width':1100,'height':760})
        page.set_default_timeout(7000)
        requests, events, metadata, errors = [], [], [], []
        page.on('request', lambda r: requests.append(r.url))
        page.on('pageerror', lambda e: errors.append(str(e)))
        def response(r):
            if r.request.method == 'POST' and '/form-event' in r.url:
                events.append(r.json())
            if r.url.endswith('/attachments'):
                metadata.append(r.json())
        page.on('response', response)
        page.goto(base+'/ui/catalog/Calls/new', wait_until='domcontentloaded')
        player = page.locator('[data-ob-audio]')
        media = page.locator('[data-ob-audio-media]')
        assert player.count() == 1
        assert media.get_attribute('preload') == 'none' and media.get_attribute('src') is None
        assert not any('/attachments' in u for u in requests)
        if proof: page.screenshot(path=str(Path(proof)/'01-managed-before-play.png'))
        media.evaluate('(a)=>a.muted=true')
        page.locator('[data-ob-audio-play]').click()
        page.wait_for_function('()=>document.querySelector("audio").currentTime>0.05')
        assert media.get_attribute('src') == '/ui/attachments/'+first+'/download'
        assert page.locator('[data-ob-audio-caption]').inner_text() == 'Call A'
        assert len(events)==1, events
        page.locator('[data-ob-audio-play]').click()
        assert media.evaluate('(a)=>a.paused')
        page.locator('[data-ob-audio-play]').click()
        page.wait_for_function('()=>!document.querySelector("audio").paused')
        assert len(events)==1
        page.locator('[data-ob-audio-stop]').click()
        assert media.get_attribute('src') is None and media.evaluate('(a)=>a.paused')
        page.locator('[data-ob-audio-play]').click()
        page.wait_for_function('()=>!document.querySelector("audio").paused')
        assert len(events)==1
        page.locator('[data-ob-fire-click="SelectB"]').click()
        page.wait_for_function('()=>document.querySelector("[data-ob-audio-caption]").textContent==="Call B"')
        assert media.get_attribute('src') is None
        page.locator('[data-ob-audio-play]').click()
        page.wait_for_function('()=>document.querySelector("audio").currentTime>0.05')
        assert media.get_attribute('src') == '/ui/attachments/'+second+'/download'
        assert len(events)==3 and all(e.get('ok') for e in events), events
        media.evaluate('(a)=>{a.volume=0.4;a.currentTime=0.7}')
        assert abs(media.evaluate('(a)=>a.volume')-0.4)<0.01
        assert media.evaluate('(a)=>a.currentTime')>=0.7
        if proof: page.screenshot(path=str(Path(proof)/'02-managed-after-call-b.png'))
        page.locator('[data-ob-audio-stop]').click()
        requests.clear()
        page.goto(base+'/ui/catalog/Recordings/'+owner,wait_until='domcontentloaded')
        assert page.locator('[data-ob-audio]').count()==1
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
        print(json.dumps({'public_http_browser':'PASS','play_preparations':2,'metadata_lists':1,'range':206,'silent_wav':True,'ordinary_audio_asset_requests':0,'js_errors':errors}))
    except Exception:
        print(json.dumps({'errors':errors,'events':events,'requests':requests,'player':page.locator('[data-ob-audio]').evaluate_all('(xs)=>xs.map(x=>x.outerHTML)'), 'media':page.locator('audio').evaluate_all('(xs)=>xs.map(a=>({src:a.getAttribute("src"),paused:a.paused,error:a.error&&a.error.message,ready:a.readyState,time:a.currentTime}))')},ensure_ascii=True),flush=True)
        raise
    finally:
        browser.close()
