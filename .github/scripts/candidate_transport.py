"""Local, data-only candidate transport. No network, checkout, commit or execution."""
import argparse,base64,difflib,hashlib,json,os,re,shutil,stat,subprocess,sys,threading,zlib
from pathlib import Path

ALLOWED=('CHANGELOG.md','go.sum','internal/i18n/locales/en.json')
MAX_WIRE=60000
MAX_RAW=2*1024*1024
MAX_BLOB=32*1024*1024
MAX_TOTAL=256*1024*1024
MAX_FILES=10000
GIT=shutil.which('git')
CONTEXT_KEYS={'source','main','base','owner_contract_sha256','proof_epoch','task_id','started_at','operator_sha','repository','operator_ref','workflow','nonce','purpose'}

def need(value,message):
    if not value:raise ValueError(message)
def digest(data):return hashlib.sha256(data).hexdigest()
def canonical(value):return json.dumps(value,ensure_ascii=True,sort_keys=True,separators=(',',':')).encode('ascii')
def pairs(items):
    result={}
    for key,value in items:
        need(key not in result,'duplicate JSON key');result[key]=value
    return result
def load(raw):return json.loads(raw,object_pairs_hook=pairs)
def read(path,limit=MAX_RAW):
    p=Path(path);need(p.stat().st_size<=limit,'input exceeds bound');return p.read_bytes()
def save(path,data):
    with Path(path).open('xb')as f:f.write(data)
def oid(value):need(isinstance(value,str)and re.fullmatch('[0-9a-f]{40}',value),'invalid exact Git SHA');return value
def sha(value):need(isinstance(value,str)and re.fullmatch('[0-9a-f]{64}',value),'invalid SHA256');return value
def valid_path(path):
    need(isinstance(path,str)and 0<len(path.encode('utf8'))<=512,'invalid path length')
    need(not any(ord(x)<32 or ord(x)==127 for x in path)and '\\'not in path and ':'not in path,'unsafe path')
    parts=path.split('/')
    reserved={'con','prn','aux','nul',*(f'com{i}'for i in range(1,10)),*(f'lpt{i}'for i in range(1,10))}
    need(all(x not in ('','.','..')and x.casefold()!='.git'and not x.endswith((' ','.'))and x.split('.')[0].casefold()not in reserved for x in parts),'unsafe path component')
    return path
def context(value):
    need(isinstance(value,dict)and set(value)==CONTEXT_KEYS,'unknown or missing context fields')
    for key in ('source','main','base','operator_sha'):oid(value[key])
    for key in ('owner_contract_sha256','proof_epoch'):sha(value[key])
    for key in ('task_id','started_at','workflow','nonce'):
        need(isinstance(value[key],str)and 1<=len(value[key])<=160 and all(32<=ord(x)<127 for x in value[key]),'invalid context '+key)
    need(re.fullmatch('[0-9a-f]{32,64}',value['nonce']),'invalid nonce')
    need(value['workflow']=='.github/workflows/ci.yml','unexpected operator workflow')
    need(value['repository']=='ivanarama/onebase'and value['operator_ref']=='refs/heads/operator/2222-linux-qualification-20261109','unexpected operator repository/ref')
    need(value['purpose']in ('archived-pilot','admitted-candidate'),'unknown purpose')
    return value

class Objects:
    def __init__(self,source,out):
        need(GIT,'git unavailable');source=Path(source).resolve();need(source.is_dir(),'object directory unavailable')
        need('\n'not in str(source)and '\r'not in str(source),'unsafe object directory')
        self.root=Path(out);self.root.mkdir(mode=0o700)
        self.repo=self.root/'objects-repo';(self.repo/'objects/info').mkdir(parents=True);(self.repo/'objects/pack').mkdir();(self.repo/'refs').mkdir()
        save(self.repo/'HEAD',b'ref: refs/heads/unborn\n')
        save(self.repo/'config',b'[core]\nrepositoryformatversion = 0\nbare = true\nhooksPath = /dev/null\n[protocol]\nallow = never\n')
        save(self.repo/'objects/info/alternates',(source.as_posix()+'\n').encode('utf8'))
        self.env={k:v for k,v in os.environ.items()if k.upper()in {'PATH','SYSTEMROOT','WINDIR','TEMP','TMP','LANG','LC_ALL'}}
        self.env.update(GIT_CONFIG_NOSYSTEM='1',GIT_CONFIG_GLOBAL=os.devnull,GIT_ATTR_NOSYSTEM='1',GIT_NO_REPLACE_OBJECTS='1',GIT_NO_LAZY_FETCH='1',GIT_TERMINAL_PROMPT='0',GIT_CONFIG_COUNT='0',GIT_OPTIONAL_LOCKS='0',LC_ALL='C')
        self.commands=[]
    def command(self,*args,data=None,ok=(0,)):
        argv=[GIT,'--git-dir='+str(self.repo),'-c','core.attributesFile='+os.devnull,*args]
        p=subprocess.run(argv,input=data,capture_output=True,env=self.env,timeout=120)
        self.commands.append({'argv':argv,'exit':p.returncode,'stdout_sha256':digest(p.stdout),'stderr_sha256':digest(p.stderr)})
        need(p.returncode in ok,'fixed Git operation failed: '+args[0]+': '+p.stderr.decode('utf8','replace')[:500])
        return p.stdout,p.returncode
    def git(self,*args,**kwargs):return self.command(*args,**kwargs)[0]
    def tree(self,tree):
        oid(tree);entries={};raw=self.git('ls-tree','-r','-z',tree)
        for row in raw.split(b'\0'):
            if not row:continue
            meta,path=row.split(b'\t',1);mode,kind,blob=meta.decode('ascii').split();path=valid_path(path.decode('utf8'))
            need(kind=='blob'and mode in ('100644','100755'),'symlink/gitlink/unknown mode rejected')
            need(path not in entries,'duplicate path');entries[path]={'path':path,'mode':mode,'blob':oid(blob)}
        need(0<len(entries)<=MAX_FILES,'tree file-count bound')
        folded=[p.casefold()for p in entries];need(len(set(folded))==len(folded),'case-colliding paths')
        return entries
    def manifest(self,tree):
        entries=self.tree(tree);result=[];total=0
        argv=[GIT,'--git-dir='+str(self.repo),'cat-file','--batch']
        p=subprocess.Popen(argv,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,env=self.env)
        timer=threading.Timer(120,p.kill);timer.start()
        try:
            for path in sorted(entries,key=lambda x:x.encode('utf8')):
                record=dict(entries[path]);p.stdin.write((record['blob']+'\n').encode());p.stdin.flush()
                header=p.stdout.readline(200).decode('ascii').strip().split();need(len(header)==3 and header[:2]==[record['blob'],'blob'],'missing blob')
                size=int(header[2]);total+=size;need(0<=size<=MAX_BLOB and total<=MAX_TOTAL,'blob/tree size bound')
                h=hashlib.sha256();g=hashlib.sha1(('blob '+str(size)+'\0').encode());remaining=size
                while remaining:
                    block=p.stdout.read(min(65536,remaining));need(block,'truncated blob');remaining-=len(block);h.update(block);g.update(block)
                need(p.stdout.read(1)==b'\n'and g.hexdigest()==record['blob'],'corrupted blob')
                record.update(bytes=size,sha256=h.hexdigest());result.append(record)
            p.stdin.close();need(p.wait(timeout=20)==0,'blob reader failed')
        finally:
            timer.cancel()
            if p.poll()is None:p.kill();p.wait()
            p.stdout.close();p.stderr.close()
        return {'format':'onebase-candidate-manifest-v1','tree':tree,'entries':result,'total_bytes':total}
    def materialize(self,root,manifest):
        p=subprocess.Popen([GIT,'--git-dir='+str(self.repo),'cat-file','--batch'],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,env=self.env)
        timer=threading.Timer(120,p.kill);timer.start()
        try:
            for record in manifest['entries']:
                p.stdin.write((record['blob']+'\n').encode());p.stdin.flush();header=p.stdout.readline(200).decode('ascii').strip().split()
                need(header==[record['blob'],'blob',str(record['bytes'])],'materialization blob changed')
                target=root.joinpath(*valid_path(record['path']).split('/'));target.parent.mkdir(parents=True,exist_ok=True)
                remaining=record['bytes'];h=hashlib.sha256()
                with target.open('xb')as f:
                    while remaining:
                        block=p.stdout.read(min(65536,remaining));need(block,'truncated materialization');remaining-=len(block);f.write(block);h.update(block)
                need(p.stdout.read(1)==b'\n'and h.hexdigest()==record['sha256'],'materialization bytes changed')
                if os.name=='posix':target.chmod(0o755 if record['mode']=='100755'else 0o644)
            p.stdin.close();need(p.wait(timeout=20)==0,'materialization reader failed')
        finally:
            timer.cancel()
            if p.poll()is None:p.kill();p.wait()
            p.stdout.close();p.stderr.close()
    def blob(self,blob):
        size=int(self.git('cat-file','-s',oid(blob)));need(0<=size<=MAX_BLOB,'blob bound');return self.git('cat-file','blob',blob)
    def merge(self,ctx):
        for key in ('source','main','base'):need(self.git('cat-file','-t',ctx[key]).strip()==b'commit','parent is not commit')
        bases=self.git('merge-base','--all',ctx['source'],ctx['main']).decode().splitlines();need(bases==[ctx['base']],'unknown/multiple merge base')
        raw,code=self.command('merge-tree','--write-tree','--name-only','--no-messages','-z',ctx['source'],ctx['main'],ok=(0,1))
        parts=raw.split(b'\0');tree=oid(parts.pop(0).decode('ascii').strip());conflicts=[valid_path(x.decode('utf8'))for x in parts if x]
        need(len(set(conflicts))==len(conflicts)and set(conflicts)<=set(ALLOWED),'new semantic conflict outside approved paths')
        need((code==1)==bool(conflicts),'unknown merge output')
        return tree,conflicts
    def write_tree(self,entries):
        directories={'':{}}
        for path,record in entries.items():
            components=valid_path(path).split('/');parent=''
            for name in components[:-1]:
                child=(parent+'/'+name).lstrip('/');directories.setdefault(child,{});directories[parent][name]=('040000','tree',child);parent=child
            directories[parent][components[-1]]=(record['mode'],'blob',record['blob'])
        trees={}
        for directory in sorted(directories,key=lambda p:p.count('/')+(1 if p else 0),reverse=True):
            rows=[]
            for name,(mode,kind,value)in sorted(directories[directory].items(),key=lambda x:(x[0]+('/'if x[1][1]=='tree'else '')).encode('utf8')):
                value=trees[value]if kind=='tree'else value
                rows.append((mode+' '+kind+' '+value+'\t'+name).encode('utf8')+b'\0')
            trees[directory]=oid(self.git('mktree','-z',data=b''.join(rows)).decode().strip())
        return trees['']

def splices(before,after):
    left=before.splitlines(keepends=True);right=after.splitlines(keepends=True)
    def offsets(lines):
        result=[0]
        for line in lines:result.append(result[-1]+len(line))
        return result
    aoff=offsets(left);boff=offsets(right)
    matcher=difflib.SequenceMatcher(None,left,right,autojunk=False)
    return [[aoff[a],aoff[b],base64.b64encode(after[boff[c]:boff[d]]).decode('ascii')]for op,a,b,c,d in matcher.get_opcodes()if op!='equal']
def apply_splices(before,edits):
    need(isinstance(edits,list)and len(edits)<=10000,'splice count bound');parts=[];cursor=0
    for edit in edits:
        need(isinstance(edit,list)and len(edit)==3,'invalid splice')
        a,b,text=edit;need(type(a)is int and type(b)is int and cursor<=a<=b<=len(before),'overlap/out-of-range splice')
        replacement=base64.b64decode(text,validate=True);parts.extend((before[cursor:a],replacement));cursor=b
    parts.append(before[cursor:]);result=b''.join(parts);need(len(result)<=MAX_BLOB,'resolution bound');return result
def wire(packet,admission):return canonical({'candidate_packet':packet,'candidate_admission':canonical(admission).decode('ascii'),'admission_sha256':digest(canonical(admission))})
def decode(packet):
    need(isinstance(packet,str)and len(packet)<=MAX_WIRE and packet.isascii(),'encoded packet bound')
    compressed=base64.b64decode(packet,validate=True);d=zlib.decompressobj();raw=d.decompress(compressed,MAX_RAW+1)
    need(len(raw)<=MAX_RAW and d.eof and not d.unused_data and not d.unconsumed_tail,'compressed input bound/trailing data')
    value=load(raw);need(canonical(value)==raw,'noncanonical packet');return value
def resolutions(store,main,auto,candidate):
    expected=store.tree(auto);actual=store.tree(candidate);main_entries=store.tree(main)
    need(set(expected)==set(actual),'candidate adds/deletes paths beyond auto merge')
    need(all(expected[p]==actual[p]for p in expected if p not in ALLOWED),'candidate changed unapproved path/mode/blob')
    result=[]
    for path in ALLOWED:
        need(path in actual and path in main_entries and actual[path]['mode']==main_entries[path]['mode']=='100644','resolution missing or mode changed')
        before=store.blob(main_entries[path]['blob']);after=store.blob(actual[path]['blob'])
        result.append({'path':path,'main_blob':main_entries[path]['blob'],'main_sha256':digest(before),'blob':actual[path]['blob'],'sha256':digest(after),'mode':'100644','edits':splices(before,after)})
    return result
def reconstruct_tree(store,value):
    need(set(value)=={'format','context','tree','manifest_sha256','resolutions'},'unknown packet fields')
    need(value['format']=='onebase-candidate-packet-v1','unknown packet version');ctx=context(value['context']);oid(value['tree']);sha(value['manifest_sha256'])
    auto,conflicts=store.merge(ctx);entries=store.tree(auto);main=store.tree(ctx['main'])
    records=value['resolutions'];need(isinstance(records,list)and [x.get('path')for x in records]==list(ALLOWED),'unknown/missing/duplicate resolution path')
    for record in records:
        need(set(record)=={'path','main_blob','main_sha256','blob','sha256','mode','edits'},'unknown resolution fields');path=record['path'];valid_path(path)
        need(path in entries and path in main and record['mode']==main[path]['mode']=='100644'and record['main_blob']==main[path]['blob'],'resolution parent/mode drift')
        before=store.blob(record['main_blob']);need(digest(before)==sha(record['main_sha256']),'resolution main bytes drift')
        after=apply_splices(before,record['edits']);need(digest(after)==sha(record['sha256']),'resolution bytes drift')
        blob=store.git('hash-object','-w','--stdin',data=after).decode().strip();need(blob==oid(record['blob']),'resolution Git blob drift');entries[path]={'path':path,'mode':'100644','blob':blob}
    tree=store.write_tree(entries);need(tree==value['tree'],'candidate tree mismatch');manifest=store.manifest(tree)
    need(digest(canonical(manifest))==value['manifest_sha256'],'full manifest mismatch');return manifest,conflicts

def pack(args):
    ctx=context(load(read(args.context)));candidate=oid(args.candidate_tree);store=Objects(args.objects,args.out)
    auto,conflicts=store.merge(ctx);manifest=store.manifest(candidate)
    value={'format':'onebase-candidate-packet-v1','context':ctx,'tree':candidate,'manifest_sha256':digest(canonical(manifest)),'resolutions':resolutions(store,ctx['main'],auto,candidate)}
    raw=canonical(value);need(len(raw)<=MAX_RAW,'raw packet bound');packet=base64.b64encode(zlib.compress(raw,9)).decode('ascii')
    admission={'format':'onebase-candidate-admission-v1','context':ctx,'tree':candidate,'manifest_sha256':value['manifest_sha256'],'packet_sha256':digest(packet.encode('ascii'))}
    dispatch=wire(packet,admission);need(len(dispatch)<=MAX_WIRE,'dispatch inputs exceed conservative bound')
    rebuilt,_=reconstruct_tree(store,decode(packet));need(rebuilt==manifest,'sender reconstruction mismatch')
    save(store.root/'full-manifest.json',canonical(manifest));save(store.root/'admission.json',canonical(admission));save(store.root/'candidate.packet',packet.encode('ascii'));save(store.root/'dispatch-inputs.json',dispatch)
    report={'status':'PACKED_DATA_ONLY','tree':candidate,'manifest_sha256':value['manifest_sha256'],'admission_sha256':digest(canonical(admission)),'packet_sha256':admission['packet_sha256'],'dispatch_ascii_bytes':len(dispatch),'raw_packet_bytes':len(raw),'conflicts':conflicts,'files':len(manifest['entries']),'runtime_or_public_actions':0}
    save(store.root/'receipt.json',canonical(report));save(store.root/'git-commands.json',canonical(store.commands));return report
def verify_files(root,manifest):
    root=Path(root);need(root.is_dir()and not root.is_symlink(),'invalid materialized root');expected={x['path']:x for x in manifest['entries']};seen=set()
    expected_dirs={'/'.join(path.split('/')[:i])for path in expected for i in range(1,len(path.split('/')))}
    for folder,dirs,files in os.walk(root,followlinks=False):
        for name in dirs:
            p=Path(folder)/name;need(not p.is_symlink()and valid_path(p.relative_to(root).as_posix())in expected_dirs,'extra/symlink directory rejected')
        for name in files:
            p=Path(folder)/name;relative=valid_path(p.relative_to(root).as_posix());need(relative in expected and stat.S_ISREG(p.lstat().st_mode),'extra/nonregular file')
            record=expected[relative];data=read(p,MAX_BLOB);need(len(data)==record['bytes']and digest(data)==record['sha256']and hashlib.sha1(('blob '+str(len(data))+'\0').encode()+data).hexdigest()==record['blob'],'materialized blob changed')
            if os.name=='posix':need(stat.S_IMODE(p.stat().st_mode)==(0o755 if record['mode']=='100755'else 0o644),'materialized mode changed')
            seen.add(relative)
    need(seen==set(expected),'materialized path inventory changed');return os.name=='posix'
def validate_manifest(manifest):
    need(isinstance(manifest,dict)and set(manifest)=={'format','tree','entries','total_bytes'}and manifest['format']=='onebase-candidate-manifest-v1','unknown manifest');oid(manifest['tree'])
    entries=manifest['entries'];need(isinstance(entries,list)and 0<len(entries)<=MAX_FILES,'manifest count bound');paths=[];total=0;dirs={'':{}}
    for record in entries:
        need(isinstance(record,dict)and set(record)=={'path','mode','blob','bytes','sha256'},'unknown manifest record')
        path=valid_path(record['path']);paths.append(path);oid(record['blob']);sha(record['sha256']);need(record['mode']in ('100644','100755')and type(record['bytes'])is int and 0<=record['bytes']<=MAX_BLOB,'invalid manifest mode/size');total+=record['bytes']
        parts=path.split('/');parent=''
        for name in parts[:-1]:
            child=(parent+'/'+name).lstrip('/');dirs.setdefault(child,{});need(name not in dirs[parent]or dirs[parent][name]==('40000',child,True),'manifest prefix collision');dirs[parent][name]=('40000',child,True);parent=child
        need(parts[-1]not in dirs[parent],'manifest duplicate/prefix collision');dirs[parent][parts[-1]]=(record['mode'],record['blob'],False)
    need(paths==sorted(paths,key=lambda p:p.encode('utf8'))and len(set(p.casefold()for p in paths))==len(paths),'manifest order/collision')
    need(type(manifest['total_bytes'])is int and total==manifest['total_bytes']and total<=MAX_TOTAL,'manifest aggregate size')
    trees={}
    for directory in sorted(dirs,key=lambda p:p.count('/')+(1 if p else 0),reverse=True):
        raw=b''
        for name,(mode,value,isdir)in sorted(dirs[directory].items(),key=lambda x:(x[0]+('/'if x[1][2]else '')).encode('utf8')):
            raw+=mode.encode()+b' '+name.encode('utf8')+b'\0'+bytes.fromhex(trees[value]if isdir else value)
        trees[directory]=hashlib.sha1(('tree '+str(len(raw))+'\0').encode()+raw).hexdigest()
    need(trees['']==manifest['tree'],'manifest Git tree mismatch');return manifest
def reconstruct(args):
    raw=read(args.admission);need(digest(raw)==sha(args.expected_admission_sha),'external admission hash mismatch');admission=load(raw)
    need(raw==canonical(admission)and set(admission)=={'format','context','tree','manifest_sha256','packet_sha256'}and admission['format']=='onebase-candidate-admission-v1','unknown/noncanonical admission')
    packet=read(args.packet,MAX_WIRE).decode('ascii');need(digest(packet.encode())==sha(admission['packet_sha256']),'packet hash mismatch');value=decode(packet)
    need(all(value[k]==admission[k]for k in ('context','tree','manifest_sha256')),'admission binding mismatch');need(len(wire(packet,admission))<=MAX_WIRE,'full dispatch bound')
    store=Objects(args.objects,args.out);manifest,conflicts=reconstruct_tree(store,value);before=canonical(manifest);save(store.root/'before-manifest.json',before)
    tree=store.root/'tree';tree.mkdir(mode=0o700)
    store.materialize(tree,manifest)
    mode_verified=verify_files(tree,manifest);after=canonical(store.manifest(manifest['tree']));need(before==after,'before/after tree manifest changed');save(store.root/'after-manifest.json',after)
    report={'status':'RECONSTRUCTED_DATA_ONLY','tree':manifest['tree'],'manifest_sha256':digest(before),'before_after_identical':True,'filesystem_mode_verified':mode_verified,'requires_linux_mode_readback':not mode_verified,'product_lint_authorized':False,'admission_sha256':digest(raw),'packet_sha256':admission['packet_sha256'],'conflicts':conflicts,'runtime_or_public_actions':0}
    save(store.root/'receipt.json',canonical(report));save(store.root/'git-commands.json',canonical(store.commands));return report
def verify(args):
    raw=read(args.manifest,8*1024*1024);need(digest(raw)==sha(args.expected_manifest_sha),'external manifest hash mismatch');manifest=load(raw);need(canonical(manifest)==raw,'noncanonical manifest')
    validate_manifest(manifest);mode_verified=verify_files(args.root,manifest)
    return {'verified':True,'filesystem_mode_verified':mode_verified,'product_lint_authorized':False}
def main():
    parser=argparse.ArgumentParser();sub=parser.add_subparsers(dest='action',required=True)
    p=sub.add_parser('pack');p.add_argument('--objects',required=True);p.add_argument('--context',required=True);p.add_argument('--candidate-tree',required=True);p.add_argument('--out',required=True);p.set_defaults(run=pack)
    p=sub.add_parser('reconstruct');p.add_argument('--objects',required=True);p.add_argument('--admission',required=True);p.add_argument('--expected-admission-sha',required=True);p.add_argument('--packet',required=True);p.add_argument('--out',required=True);p.set_defaults(run=reconstruct)
    p=sub.add_parser('verify');p.add_argument('--root',required=True);p.add_argument('--manifest',required=True);p.add_argument('--expected-manifest-sha',required=True);p.set_defaults(run=verify)
    args=parser.parse_args()
    try:print(json.dumps(args.run(args),sort_keys=True))
    except Exception as error:print(str(error),file=sys.stderr);return 1
    return 0
if __name__=='__main__':sys.exit(main())
