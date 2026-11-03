"""Read-only exact-candidate acceptance transport; no formal PR authority."""
from pathlib import Path
from collections import Counter
import hashlib,json,os,stat,subprocess,sys,time
ROOT=Path.cwd();source=ROOT/'candidate';out=ROOT/'proof';out.mkdir(exist_ok=True)
manifest=json.loads((ROOT/'harness/.github/scripts/2279-manifest.json').read_text())
commit='b70bff38cf95f3769cc806ea14099c63b79b7d50';tree='35d88895eae7ca8513b5bac4f7595b8acd8c9feb'
commands=[]
def command(name,args,cwd=source,timeout=900):
 start=time.time();p=subprocess.run(args,cwd=cwd,capture_output=True,timeout=timeout)
 (out/(name+'.stdout')).write_bytes(p.stdout);(out/(name+'.stderr')).write_bytes(p.stderr)
 commands.append(dict(name=name,argv=args,cwd=str(cwd),exit=p.returncode,elapsed=time.time()-start,stdout_sha256=hashlib.sha256(p.stdout).hexdigest(),stderr_sha256=hashlib.sha256(p.stderr).hexdigest()))
 (out/'commands.json').write_text(json.dumps(commands,indent=2))
 print(name,'exit',p.returncode,'elapsed',round(time.time()-start,2),flush=True)
 if p.returncode:raise RuntimeError(name+' failed; see retained full logs')
 return p.stdout.decode()
def verify(label):
 assert command(label+'-head',['git','rev-parse','HEAD']).strip()==commit
 assert command(label+'-tree',['git','rev-parse','HEAD^{tree}']).strip()==tree
 entries=command(label+'-index',['git','ls-files','--stage','-z']).rstrip('\0').split('\0')
 expected={f['path']:(f['mode'],f['oid']) for f in manifest['files']};actual={}
 for line in entries:
  mode,oid,rest=line.split(' ',2);stage,path=rest.split('\t',1);assert stage=='0';actual[path]=(mode,oid)
 assert actual==expected and len(actual)==3724,dict(missing=sorted(expected.keys()-actual.keys())[:5],extra=sorted(actual.keys()-expected.keys())[:5],changed=[p for p in expected.keys()&actual.keys() if expected[p]!=actual[p]][:5])
 for f in manifest['files']:
  path=source/f['path'];assert not path.is_symlink()
  assert hashlib.sha256(path.read_bytes()).hexdigest()==f['sha256'],f['path']
  assert bool(path.stat().st_mode & stat.S_IXUSR)==(f['mode']=='100755'),f['path']
 assert not command(label+'-dirty',['git','diff','--name-only','HEAD']).strip()
 return dict(commit=commit,tree=tree,files=len(actual),manifest_sha256=hashlib.sha256((ROOT/'harness/.github/scripts/2279-manifest.json').read_bytes()).hexdigest())
def test_group(label,names):
 pattern='^('+'|'.join(names)+')$'
 listing=command(label+'-inventory',['go','test','-list',pattern,'./internal/ui'])
 selected=[s for s in listing.splitlines() if s.startswith('Test')];assert Counter(selected)==Counter(names),(selected,names)
 text=command(label,['go','test','-race','-count=1','-timeout=10m','-json','./internal/ui','-run',pattern],timeout=1100)
 events=[json.loads(line) for line in text.splitlines()]
 assert not any(e['Action'] in ('fail','skip') for e in events),'failure or skipped required test'
 runs=[e['Test'] for e in events if e['Action']=='run'];passed=[e['Test'] for e in events if e['Action']=='pass' and 'Test' in e]
 assert Counter(runs)==Counter(passed)
 assert Counter(n for n in runs if '/' not in n)==Counter(names)
 if label=='browser':
  for dialect in ['sqlite','postgres']:
   for nested in ['false','true']:assert 'TestEqualHeightGeometry_Browser/'+dialect+'/nested='+nested in passed
 return dict(selected=names,executed=len(runs),passed=len(passed),skipped=0,tests=passed)
result=dict(status='running',source=commit,tree=tree)
try:
 result['before']=verify('before')
 result['environment']=json.loads(command('go-env',['go','env','-json','GOVERSION','GOOS','GOARCH','CGO_ENABLED','GOFLAGS','GOTOOLCHAIN']))
 assert result['environment']['GOVERSION']=='go1.26.10' and result['environment']['CGO_ENABLED']=='1'
 command('node-version',['node','--version']);command('browser-version',['google-chrome','--version'])
 result['browser']=test_group('browser',['TestEqualHeightGeometry_Browser','TestFitWindowMessagePanel_Browser','TestFitWindowNoFormScroll_Browser'])
 result['node']=test_group('node',['TestManagedRequiredInvalidHandlerRevealsHiddenTabs','TestTabsBehaviorInNode','TestManagedFormAttrsBehaviorInNode','TestFormEvent_UnplacedAttributesSurviveOtherEvent'])
 command('mod-verify',['go','mod','verify'])
 command('trade-check',['go','run','./cmd/onebase','check','--project','examples/trade'])
 command('module-list',['go','list','-m','all']);command('module-graph',['go','mod','graph'])
 baseline=ROOT/'baseline';command('baseline-worktree',['git','worktree','add','--detach',str(baseline),manifest['source']])
 before=command('baseline-module-list',['go','list','-m','all'],cwd=baseline)
 command('baseline-module-graph',['go','mod','graph'],cwd=baseline)
 old={s.split()[0]:s for s in before.splitlines()};new={s.split()[0]:s for s in (out/'module-list.stdout').read_text().splitlines()}
 changes={name:(old.get(name),new.get(name)) for name in old.keys()|new.keys() if old.get(name)!=new.get(name)}
 versions={'net':('v0.60.0','v0.62.0'),'crypto':('v0.57.0','v0.58.0'),'sys':('v0.48.0','v0.49.0'),'text':('v0.42.0','v0.43.0'),'sync':('v0.23.0','v0.24.0'),'term':('v0.46.0','v0.47.0'),'tools':('v0.49.0','v0.51.0')}
 assert changes=={'golang.org/x/'+name:('golang.org/x/'+name+' '+a,'golang.org/x/'+name+' '+b) for name,(a,b) in versions.items()},changes
 result['module_changes']=changes
 command('diff-check',['git','diff','--check']);result['after']=verify('after');result['status']='passed'
except BaseException as error:
 result['status']='failed';result['error']=type(error).__name__+': '+str(error);raise
finally:
 (out/'result.json').write_text(json.dumps(result,ensure_ascii=False,indent=2))
