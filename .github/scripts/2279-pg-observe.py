"""One read-only full PostgreSQL UI diagnostic; no formal PR acceptance."""
from collections import Counter
import hashlib, json, os, pathlib, platform, shutil, stat, subprocess, time

ROOT = pathlib.Path.cwd()
SOURCE = ROOT/'candidate'
OUT = ROOT/'proof'
OUT.mkdir(mode=0o700, exist_ok=True)
MANIFEST_BYTES = (ROOT/'harness/.github/scripts/2279-manifest.json').read_bytes()
MANIFEST = json.loads(MANIFEST_BYTES)
COMMANDS = []
RESULT = {'status':'running','commit':MANIFEST['commit'],'tree':MANIFEST['tree'],
          'manifest_sha256':hashlib.sha256(MANIFEST_BYTES).hexdigest(),
          'platform':platform.platform(), 'environment':{
              'CGO_ENABLED':os.environ.get('CGO_ENABLED'),
              'TEST_DATABASE_URL':'dedicated PostgreSQL 16 service; credentials omitted',
              'ONEBASE_BROWSER_DIAGNOSTICS_DIR':str(OUT/'browser-logs')}}
os.environ['ONEBASE_BROWSER_DIAGNOSTICS_DIR'] = str(OUT/'browser-logs')

def save():
    (OUT/'commands.json').write_text(json.dumps(COMMANDS,indent=2),encoding='utf-8')
    (OUT/'result.json').write_text(json.dumps(RESULT,ensure_ascii=False,indent=2),encoding='utf-8')

def command(name, argv, timeout=600, require_success=True):
    stdout, stderr = OUT/(name+'.stdout'), OUT/(name+'.stderr')
    start = time.time()
    receipt = {'name':name,'argv':argv,'cwd':str(SOURCE),'started_at':start}
    COMMANDS.append(receipt)
    save()
    try:
        with stdout.open('wb') as out, stderr.open('wb') as err:
            process = subprocess.Popen(argv,cwd=SOURCE,stdout=out,stderr=err,start_new_session=True)
            try:
                code = process.wait(timeout=timeout)
            except subprocess.TimeoutExpired:
                import signal
                os.killpg(process.pid,signal.SIGTERM)
                try:
                    process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    os.killpg(process.pid,signal.SIGKILL)
                    process.wait()
                receipt['timed_out'] = True
                code = 124
        receipt.update(exit=code,elapsed=time.time()-start,
                       stdout_sha256=hashlib.sha256(stdout.read_bytes()).hexdigest(),
                       stderr_sha256=hashlib.sha256(stderr.read_bytes()).hexdigest())
        print(name,'exit',code,'seconds',round(receipt['elapsed'],2),flush=True)
        if code and require_success:
            raise RuntimeError(name+' failed; full raw logs retained')
        return code, stdout.read_text(encoding='utf-8')
    finally:
        save()

def verify(label):
    assert command(label+'-head',['git','rev-parse','HEAD'])[1].strip()==MANIFEST['commit']
    assert command(label+'-tree',['git','rev-parse','HEAD^{tree}'])[1].strip()==MANIFEST['tree']
    raw = subprocess.check_output(['git','ls-files','--stage','-z'],cwd=SOURCE)
    actual = {}
    for entry in raw.rstrip(b'\0').split(b'\0'):
        meta,path=entry.split(b'\t',1)
        mode,oid,stage=meta.decode().split()
        assert stage=='0'
        actual[path.decode('utf-8')]=(mode,oid)
    expected={f['path']:(f['mode'],f['oid']) for f in MANIFEST['files']}
    assert actual==expected and len(actual)==3725
    for f in MANIFEST['files']:
        path=SOURCE/f['path']
        assert not path.is_symlink()
        assert hashlib.sha256(path.read_bytes()).hexdigest()==f['sha256'],f['path']
        assert bool(path.stat().st_mode&stat.S_IXUSR)==(f['mode']=='100755'),f['path']
    assert not command(label+'-dirty',['git','diff','--name-only','HEAD'])[1].strip()
    return {'commit':MANIFEST['commit'],'tree':MANIFEST['tree'],'files':len(actual)}

def outcomes(name,text,inventory=None):
    events=[json.loads(line) for line in text.splitlines()]
    runs=Counter(e['Test'] for e in events if e['Action']=='run' and 'Test' in e)
    terminal={kind:[e['Test'] for e in events if e['Action']==kind and 'Test' in e]
              for kind in ['pass','fail','skip']}
    tops=[test for test in runs if '/' not in test]
    result={'executed':sum(runs.values()),'passed':len(terminal['pass']),
            'failed':len(terminal['fail']),'skipped':len(terminal['skip']),
            'top_level_executed':len(tops),'failed_tests':terminal['fail'],
            'skipped_tests':terminal['skip'],
            'duplicate_runs':[test for test,n in runs.items() if n!=1],
            'unterminated':list((runs-Counter(sum(terminal.values(),[]))).elements())}
    if inventory is not None:
        result['inventory_total']=len(inventory)
        result['inventory_missing']=sorted(set(inventory)-set(tops))
        result['inventory_extra']=sorted(set(tops)-set(inventory))
    (OUT/(name+'-outcomes.json')).write_text(json.dumps(result,ensure_ascii=False,indent=2),encoding='utf-8')
    return result

try:
    RESULT['before']=verify('before')
    RESULT['go_env']=json.loads(command('go-env',['go','env','-json','GOVERSION','GOOS','GOARCH','CGO_ENABLED','GOFLAGS','GOTOOLCHAIN'])[1])
    assert RESULT['go_env']['GOVERSION']=='go1.26.10'
    assert RESULT['go_env']['GOOS']=='linux' and RESULT['go_env']['GOARCH']=='amd64'
    assert RESULT['go_env']['CGO_ENABLED']=='1'
    RESULT['os_release']=pathlib.Path('/etc/os-release').read_text()
    assert 'VERSION_ID="24.04"' in RESULT['os_release']
    command('node-version',['node','--version'])
    browser=next((shutil.which(n) for n in ['msedge','microsoft-edge','google-chrome','chromium','chromium-browser'] if shutil.which(n)),None)
    assert browser, 'supported existing browser unavailable'
    command('browser-version',[browser,'--version'])
    resolved=pathlib.Path(browser).resolve()
    RESULT['browser']={'executable':browser,'resolved':str(resolved),
                       'sha256':hashlib.sha256(resolved.read_bytes()).hexdigest()}
    native=pathlib.Path('/opt/google/chrome/chrome')
    if native.is_file():
        RESULT['browser']['native_executable']=str(native)
        RESULT['browser']['native_sha256']=hashlib.sha256(native.read_bytes()).hexdigest()
    command('postgres-version',['psql','--version'])
    command('postgres-readiness',['pg_isready','-h','127.0.0.1','-U','onebase','-d','onebase_test'])
    code,text=command('readiness',['go','test','-race','-count=1','-timeout=10m','-json','./internal/ui','-run','^TestMobileLayoutBrowserReadiness$'])
    RESULT['readiness']=outcomes('readiness',text)
    assert RESULT['readiness']['passed']==9 and RESULT['readiness']['failed']==0 and RESULT['readiness']['skipped']==0
    assert not RESULT['readiness']['duplicate_runs'] and not RESULT['readiness']['unterminated']
    _,listing=command('inventory',['go','test','-race','-list','.','./internal/ui'])
    inventory=[s for s in listing.splitlines() if s.startswith(('Test','Example','Fuzz'))]
    assert inventory and len(inventory)==len(set(inventory))
    RESULT['full_ui_started_at']=time.time()
    save()
    code,text=command('full-ui',['go','test','-race','-count=1','-timeout','20m','-json','./internal/ui'],timeout=1740,require_success=False)
    RESULT['full_ui']=outcomes('full-ui',text,inventory)
    RESULT['full_ui_exit']=code
    RESULT['status']='passed' if code==0 else 'failed'
    assert not RESULT['full_ui']['duplicate_runs']
    if code==0:
        assert not RESULT['full_ui']['failed_tests'] and not RESULT['full_ui']['unterminated']
        assert not RESULT['full_ui']['inventory_missing'] and not RESULT['full_ui']['inventory_extra']
    if code:
        raise RuntimeError('the one full PostgreSQL UI diagnostic failed; no retry')
except BaseException as error:
    RESULT['status']='failed'
    RESULT['error']=type(error).__name__+': '+str(error)
    raise
finally:
    try:
        RESULT['after']=verify('after')
    finally:
        save()
