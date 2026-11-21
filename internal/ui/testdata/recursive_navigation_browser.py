"""Optional Playwright acceptance against the Go HTTP fixture, never shared bases."""
import json, os, pathlib
from playwright.sync_api import sync_playwright

origin=os.environ['OB_RECURSIVE_MENU_URL']
out=pathlib.Path(os.environ['OB_RECURSIVE_MENU_ARTIFACTS']);out.mkdir(parents=True,exist_ok=True)
checks={};errors=[]
def require(name,ok):
 checks[name]=bool(ok)
 if not ok:raise AssertionError(name)
def folder(page,id):return page.locator('details[data-nav-id="cfg:'+id+'"]')
def read(page,key):return page.evaluate('(key)=>localStorage.getItem(key)',key)
with sync_playwright() as driver:
 browser=driver.chromium.launch(executable_path=os.environ['OB_BROWSER_CHROME'],headless=True)
 context=browser.new_context(viewport={'width':1280,'height':900})
 context.route('**/*',lambda route:route.continue_() if route.request.url.startswith(origin+'/') else route.abort())
 def login(token):context.add_cookies([{'name':'onebase_session','value':token,'url':origin}])
 login(os.environ['OB_RECURSIVE_MENU_ALICE'])
 page=context.new_page();page.on('pageerror',lambda e:errors.append(str(e)))
 try:
  page.goto(origin+'/ui/?subsystem=School',wait_until='domcontentloaded')
  page.locator('[data-tree-initialized="1"]').first.wait_for()
  require('folder_uses_sidebar_background',folder(page,'reports').evaluate('(x)=>getComputedStyle(x).backgroundColor')=='rgba(0, 0, 0, 0)')
  require('three_folders_before_command',page.locator('details[data-nav-id="cfg:reports"] > details[data-nav-id="cfg:masters"] > details[data-nav-id="cfg:held"] > a[data-nav-id="cfg:full"]').count()==1)
  ids=folder(page,'reports').locator(':scope > a, :scope > details').evaluate_all('(xs)=>xs.map(x=>x.dataset.navId)')
  require('mixed_order_and_role_projection',ids==['cfg:direct','cfg:masters','cfg:last'])
  require('acl_prunes_empty_branch',page.locator('[data-nav-id="cfg:empty"], [data-nav-id="cfg:forbidden"]').count()==0)
  require('direct_forbidden403',context.request.get(origin+'/ui/catalog/Private?subsystem=School').status==403)
  held=folder(page,'held');masters=folder(page,'masters');key=held.get_attribute('data-nav-key');master_key=masters.get_attribute('data-nav-key')
  held.locator(':scope > summary').click();page.wait_for_function('(key)=>localStorage.getItem(key)==="0"',arg=key)
  require('mouse_collapse_and_aria',not held.evaluate('(x)=>x.open') and held.locator(':scope > summary').get_attribute('aria-expanded')=='false')
  page.reload(wait_until='domcontentloaded');page.locator('[data-tree-initialized="1"]').first.wait_for()
  require('collapse_survives_reload',not folder(page,'held').evaluate('(x)=>x.open'))
  summary=folder(page,'held').locator(':scope > summary');summary.focus();summary.press('Enter');page.wait_for_function('(key)=>localStorage.getItem(key)==="1"',arg=key)
  require('enter_expands_with_aria',folder(page,'held').evaluate('(x)=>x.open') and summary.get_attribute('aria-expanded')=='true')
  summary.press('ArrowLeft');page.wait_for_function('(key)=>localStorage.getItem(key)==="0"',arg=key)
  summary.press('ArrowRight');page.wait_for_function('(key)=>localStorage.getItem(key)==="1"',arg=key)
  require('arrow_keys_persist',folder(page,'held').evaluate('(x)=>x.open'))
  # Close another branch and then navigate directly into the held command.
  other=folder(page,'other');other_key=other.get_attribute('data-nav-key');other.locator(':scope > summary').click();page.wait_for_function('(key)=>localStorage.getItem(key)==="0"',arg=other_key)
  masters.locator(':scope > summary').click();page.wait_for_function('(key)=>localStorage.getItem(key)==="0"',arg=master_key)
  page.goto(origin+'/ui/catalog/Classes?subsystem=School&extra=filter',wait_until='domcontentloaded');page.locator('[data-tree-initialized="1"]').first.wait_for()
  require('active_ancestors_expand_without_overwriting_preference',folder(page,'masters').evaluate('(x)=>x.open') and folder(page,'held').evaluate('(x)=>x.open') and read(page,master_key)=='0')
  require('active_ancestors_leave_sibling_preferences_unchanged',not folder(page,'other').evaluate('(x)=>x.open') and read(page,other_key)=='0')
  page.screenshot(path=str(out/'01-active-nested-menu.png'),full_page=True)
  # A second login in the same browser must not inherit the first user's collapse key.
  login(os.environ['OB_RECURSIVE_MENU_BOB']);page.goto(origin+'/ui/?subsystem=School',wait_until='domcontentloaded');page.locator('[data-tree-initialized="1"]').first.wait_for()
  require('user_isolation_in_same_browser',folder(page,'masters').get_attribute('data-nav-key')!=master_key and folder(page,'masters').evaluate('(x)=>x.open'))
  login(os.environ['OB_RECURSIVE_MENU_ALICE']);page.goto(origin+'/ui/settings/navigation?subsystem=School',wait_until='domcontentloaded')
  row=page.locator('.navigation-row').filter(has=page.locator('[data-node-id="cfg:full"]'))
  require('editor_lists_nested_command',row.count()==1)
  row.locator('[data-action="out"]').click()
  # Save uses the real production controller and HTTP handler.
  page.locator('#navigation-save button[type="submit"]').click();page.wait_for_url('**/ui/settings/navigation?saved=1&subsystem=School')
  data=page.locator('#navigation-editor-data').evaluate('(x)=>JSON.parse(x.textContent)')
  nodes=data['desired']['sections'][0]['nodes'][1]['nodes']
  require('editor_move_round_trip',[n['id'] for n in nodes]==['cfg:held','cfg:full'])
  require('editor_keeps_hidden_roles_private','cfg:chief' not in json.dumps(data))
  page.screenshot(path=str(out/'02-personal-recursive-editor.png'),full_page=True)
  require('javascript_errors_zero',not errors)
 finally:
  (out/'receipt.json').write_text(json.dumps({'checks':checks,'errors':errors,'browser':browser.version,'own_sqlite':True,'shared_changes':False},indent=2),encoding='utf-8')
  context.close();browser.close()
print(json.dumps(checks))
