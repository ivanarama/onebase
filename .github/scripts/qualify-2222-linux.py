#!/usr/bin/env python3
"""One synthetic environment qualification. Never runs the product linter."""
import argparse
import ctypes
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import struct
import subprocess
import sys
import tarfile
import time
import urllib.request

CONFIG_SHA = '323463bba50bded33ec9bdebe2c3cdd3c1eb33d06b40b8d13d56303ba7b90b22'
REF = 'refs/heads/operator/2222-linux-qualification-20261109'
REPO = 'ivanarama/onebase'
LIMIT = 9 * 1024**3
HOST_CLASS = 16 * 1024**3
UID_FIRST, UID_LAST = 300000, 300499  # Outside ordinary and systemd DynamicUser UID pools.
LINTE = {'govet', 'ineffassign', 'staticcheck', 'bodyclose', 'unused', 'errcheck', 'gosec'}
GO_URL = 'https://go.dev/dl/go1.26.10.linux-amd64.tar.gz'
GO_SUMS = 'https://go.dev/dl/?mode=json&include=all'
LINT_URL = 'https://github.com/golangci/golangci-lint/releases/download/v2.11.4/golangci-lint-2.11.4-linux-amd64.tar.gz'
LINT_SUMS = 'https://github.com/golangci/golangci-lint/releases/download/v2.11.4/golangci-lint-2.11.4-checksums.txt'
PARENT = Path('/var/lib/onebase-2222-qualification')
ENV_FIXED = {'GOMAXPROCS':'2', 'GOMEMLIMIT':'7GiB', 'GOGC':'100',
 'GOTOOLCHAIN':'go1.26.10', 'GOOS':'linux', 'GOARCH':'amd64', 'CGO_ENABLED':'1',
 'GOWORK':'off', 'GOENV':'off', 'GOPROXY':'off', 'GOSUMDB':'off',
 'CC':'/usr/bin/cc', 'LANG':'C.UTF-8', 'TZ':'UTC'}

def need(ok, message):
    if not ok:
        raise RuntimeError(message)

def digest(path):
    h = hashlib.sha256()
    with Path(path).open('rb') as stream:
        for block in iter(lambda:stream.read(1024*1024), b''):
            h.update(block)
    return h.hexdigest()

def write_json(path, value):
    with Path(path).open('x', encoding='utf-8', newline='\n') as stream:
        json.dump(value, stream, ensure_ascii=False, sort_keys=True, indent=2)
        stream.write('\n'); stream.flush(); os.fsync(stream.fileno())

def identity(env):
    value = {k:env.get('GITHUB_'+k.upper(), '') for k in
             ('repository', 'ref', 'sha', 'run_id', 'run_attempt', 'event_name', 'actor')}
    need(value['repository']==REPO and value['ref']==REF, 'operator repository/ref')
    need(value['event_name']=='workflow_dispatch' and value['actor']=='ivanarama', 'dispatch actor/event')
    need(re.fullmatch('[0-9a-f]{40}', value['sha']) is not None, 'operator SHA')
    need(re.fullmatch('[1-9][0-9]{0,19}', value['run_id']) is not None and value['run_attempt']=='1', 'one initial run only')
    return value

def parse_meminfo(text):
    values = {}
    for line in text.splitlines():
        match = re.fullmatch(r'(MemTotal|MemAvailable):\s+(\d+) kB', line)
        if match: values[match[1]] = int(match[2])*1024
    need(set(values)=={'MemTotal','MemAvailable'}, 'usable memory evidence missing')
    return values

def assigned_memory(table):
    """SMBIOS type17 populated-device sizes; never use max-capacity type16."""
    total = count = 0; offset = 0
    while offset < len(table):
        need(offset+4<=len(table), 'truncated SMBIOS header')
        typ, length = table[offset], table[offset+1]
        need(length>=4 and offset+length<=len(table), 'SMBIOS formatted length')
        block = table[offset:offset+length]
        if typ==17:
            need(length>=14, 'SMBIOS type17 size missing')
            size = struct.unpack_from('<H', block, 12)[0]
            need(size!=65535, 'SMBIOS populated size unknown')
            if size==32767:
                need(length>=32, 'SMBIOS extended size missing')
                size = struct.unpack_from('<I', block, 28)[0] & 0x7fffffff
                need(size>0, 'SMBIOS extended size unknown')
                amount = size*1024**2
            else:
                amount = (size & 0x7fff)*(1024 if size & 0x8000 else 1024**2)
            if amount: total+=amount; count+=1
        end = table.find(b'\0\0', offset+length)
        need(end>=0, 'SMBIOS string terminator missing')
        offset=end+2
        if typ==127: break
    need(count>0 and total>0, 'assigned physical memory class UNKNOWN')
    return total, count

def cpu_set(text):
    values=set()
    for item in text.strip().split(','):
        need(re.fullmatch(r'\d+(?:-\d+)?', item) is not None, 'CPU set syntax')
        bounds=[int(x)for x in item.split('-')]
        start,end=(bounds[0],bounds[-1]); need(0<=start<=end<4096, 'CPU bounds')
        values.update(range(start,end+1))
    return sorted(values)

def current_cgroup():
    lines=Path('/proc/self/cgroup').read_text().splitlines()
    need(len(lines)==1 and lines[0].startswith('0::/'), 'unified cgroup v2 required')
    relative=lines[0][3:]
    need('..' not in Path(relative).parts and relative.startswith('/system.slice/ob2222-qual-'), 'own service cgroup identity')
    return relative, Path('/sys/fs/cgroup')/relative.lstrip('/')

def resource_snapshot():
    relative,cg=current_cgroup()
    files=('cpu.max','cpu.max.burst','cpuset.cpus.effective','memory.max','memory.swap.max',
           'memory.current','memory.peak','memory.events','cgroup.procs')
    raw={name:(cg/name).read_text().strip()for name in files}
    events={k:int(v)for k,v in (line.split()for line in raw['memory.events'].splitlines())}
    need(raw['cpu.max']=='200000 100000' and raw['cpu.max.burst']=='0', '2 CPU quota/burst readback')
    need(cpu_set(raw['cpuset.cpus.effective'])==[0,1] and sorted(os.sched_getaffinity(0))==[0,1], '2 CPU placement readback')
    need(raw['memory.max']==str(LIMIT) and raw['memory.swap.max']=='0', '9GiB/swap0 readback')
    # An enclosing cgroup may impose a smaller limit; it is not extra capacity.
    ancestor=cg.parent
    ancestors=[]
    while ancestor.is_relative_to('/sys/fs/cgroup'):
        path=ancestor/'memory.max'
        if path.exists():
            value=path.read_text().strip(); ancestors.append({'path':str(path),'value':value})
            need(value=='max' or int(value)>=LIMIT, 'ancestor memory capacity below9GiB')
        if ancestor==Path('/sys/fs/cgroup'): break
        ancestor=ancestor.parent
    return {'cgroup':relative,'raw':raw,'events':events,'ancestors':ancestors,
            'peak':int(raw['memory.peak']),'pid':os.getpid()}

def command(evidence, name, argv, env, cwd, timeout=120):
    start=time.monotonic()
    try:
        p=subprocess.run(argv,env=env,cwd=cwd,capture_output=True,timeout=timeout)
        stdout,stderr=p.stdout,p.stderr; code=p.returncode
    except subprocess.TimeoutExpired as error:
        stdout,stderr=error.stdout or b'',error.stderr or b''; code=124
    (evidence/(name+'.stdout')).write_bytes(stdout)
    (evidence/(name+'.stderr')).write_bytes(stderr)
    write_json(evidence/(name+'.command.json'),{'argv':argv,'exit':code,'seconds':time.monotonic()-start})
    need(code==0, name+' nonzero/timeout: '+str(code))
    return stdout

def fetch(url, destination, maximum):
    """Fixed HTTPS official URLs only. No retries, tokens or caller URLs."""
    need(url in {GO_URL,GO_SUMS,LINT_URL,LINT_SUMS}, 'nonfixed download URL')
    request=urllib.request.Request(url,headers={'User-Agent':'onebase-2222-qualification'})
    start=time.monotonic(); size=0
    with urllib.request.urlopen(request,timeout=45)as response, destination.open('xb')as stream:
        need(response.geturl().startswith('https://'), 'HTTPS redirect required')
        while True:
            block=response.read(1024*1024)
            if not block: break
            size+=len(block); need(size<=maximum and time.monotonic()-start<180,'download bound')
            stream.write(block)
    return digest(destination)

def unpack(archive, target):
    target.mkdir(); total=0
    with tarfile.open(archive,'r:gz')as tar:
        for item in tar:
            pure=Path(item.name)
            need(not pure.is_absolute() and '..'not in pure.parts and not item.issym() and not item.islnk(), 'unsafe release archive path/link')
            need(item.isdir()or item.isfile(), 'unsupported archive entry')
            total+=item.size; need(total<=2*1024**3,'expanded archive bound')
            dest=target/pure
            if item.isdir(): dest.mkdir(parents=True,exist_ok=True)
            else:
                dest.parent.mkdir(parents=True,exist_ok=True)
                with tar.extractfile(item)as source,dest.open('xb')as output: shutil.copyfileobj(source,output,1024*1024)
                dest.chmod(0o755 if item.mode & 0o111 else 0o644)

def clear_and_drop(uid):
    libc=ctypes.CDLL(None,use_errno=True)
    need(libc.prctl(38,1,0,0,0)==0,'NoNewPrivileges')
    need(libc.prctl(47,4,0,0,0)==0,'ambient capability clear')
    for cap in range(64):
        if libc.prctl(24,cap,0,0,0)!=0:
            need(ctypes.get_errno()==22,'bounding capability drop')
    os.setgroups([]); os.setgid(uid); os.setuid(uid)
    class Header(ctypes.Structure): _fields_=[('version',ctypes.c_uint),('pid',ctypes.c_int)]
    class Data(ctypes.Structure): _fields_=[('effective',ctypes.c_uint),('permitted',ctypes.c_uint),('inheritable',ctypes.c_uint)]
    h=Header(0x20080522,0); data=(Data*2)()
    need(libc.capset(ctypes.byref(h),data)==0,'capability zero')

def child_identity(uid, expected_cgroup):
    status={line.split(':',1)[0]:line.split(':',1)[1].strip()for line in Path('/proc/self/status').read_text().splitlines()if ':'in line}
    caps={k:status[k]for k in ('CapInh','CapPrm','CapEff','CapBnd','CapAmb')}
    need(os.getuid()==os.geteuid()==os.getgid()==uid and not os.getgroups(),'restricted fresh UID/groups')
    need(all(int(x,16)==0 for x in caps.values())and status['NoNewPrivs']=='1','child privilege readback')
    relative,cg=current_cgroup(); need(relative==expected_cgroup,'child cgroup drift')
    denied=[]
    for name in ('cgroup.procs','memory.max','cpu.max'):
        try: fd=os.open(cg/name,os.O_WRONLY)
        except PermissionError: denied.append(name)
        except OSError as error:
            need(error.errno in (13,30),'unexpected cgroup write probe'); denied.append(name)
        else: os.close(fd); raise RuntimeError('child can open cgroup for writing')
    ipc={}
    for name in ('/var/run/docker.sock','/run/docker.sock','/run/containerd/containerd.sock'):
        try:os.stat(name)
        except FileNotFoundError:state='absent'
        except PermissionError:state='inaccessible'
        else:state='present'
        read,write=os.access(name,os.R_OK),os.access(name,os.W_OK)
        need(not read and not write,'child has privileged host IPC permissions')
        ipc[name]={'state':state,'readable':read,'writable':write}
    return {'uid':uid,'gid':os.getgid(),'groups':os.getgroups(),'caps':caps,'nnp':1,
            'cgroup':relative,'write_denied':denied,'host_ipc':ipc,'pid':os.getpid(),'ppid':os.getppid()}

def worker(prefix):
    info=json.loads((prefix/'input/worker.json').read_text())
    work=prefix/'work'; evidence=work/'evidence'; evidence.mkdir()
    result={'identity':child_identity(info['uid'],info['cgroup']), 'env':dict(os.environ)}
    allowed=set(ENV_FIXED)|{'PATH','HOME','TMPDIR','GOCACHE','GOPATH','GOMODCACHE','PYTHONUTF8'}
    need(set(os.environ)<=allowed,'unexpected child environment')
    need(all(os.environ.get(k)==v for k,v in ENV_FIXED.items())and 'GOFLAGS'not in os.environ,'exact child environment')
    go=str(prefix/'tools/go/go/bin/go'); lint=str(prefix/'tools/lint/golangci-lint-2.11.4-linux-amd64/golangci-lint')
    result['go_version']=command(evidence,'go-version',[go,'version'],dict(os.environ),work,15).decode().strip()
    need(result['go_version']=='go version go1.26.10 linux/amd64','exact Go version')
    result['go_env']=json.loads(command(evidence,'go-env',[go,'env','-json','GOHOSTOS','GOHOSTARCH','GOOS','GOARCH','CGO_ENABLED','GOTOOLCHAIN'],dict(os.environ),work,15))
    need(result['go_env']=={'GOHOSTOS':'linux','GOHOSTARCH':'amd64','GOOS':'linux','GOARCH':'amd64','CGO_ENABLED':'1','GOTOOLCHAIN':'go1.26.10'},'native Go/CGO')
    result['lint_version']=command(evidence,'lint-version',[lint,'version','--short'],dict(os.environ),work,15).decode().strip()
    need(result['lint_version']=='2.11.4','exact linter version')
    config=str(prefix/'input/canonical-config.yml')
    linters=json.loads(command(evidence,'configured-linters',[lint,'linters','--json','--config',config],dict(os.environ),work,30))
    enabled=linters.get('Enabled'); need(isinstance(enabled,list),'linters JSON schema')
    names=[x.get('name',x.get('Name'))for x in enabled]
    need(len(names)==7 and set(names)==LINTE,'all seven configured linters')
    result['enabled_linters']=sorted(names)
    module=work/'synthetic'; module.mkdir()
    (module/'go.mod').write_text('module example.invalid/qualification\n\ngo 1.26.10\n',encoding='utf8')
    (module/'main.go').write_text('package main\n/*\nint answer(void) { return 42; }\n*/\nimport "C"\nimport "fmt"\nfunc main() { fmt.Printf("cgo=%d\\n", int(C.answer())) }\n',encoding='utf8')
    source={p.name:digest(p)for p in module.iterdir()}
    command(evidence,'synthetic-cgo-build',[go,'build','-o',str(work/'synthetic-cgo'),'.'],dict(os.environ),module,180)
    result['cgo_stdout']=command(evidence,'synthetic-cgo-execute',[str(work/'synthetic-cgo')],dict(os.environ),module,15).decode()
    need(result['cgo_stdout']=='cgo=42\n','native C compiler/link/execute')
    config_copy=module/'.golangci.yml';shutil.copyfile(config,config_copy)
    need(digest(config_copy)==CONFIG_SHA,'synthetic canonical config')
    inventory=command(evidence,'synthetic-inventory',[go,'list','./...'],dict(os.environ),module,30).decode().splitlines()
    need(inventory==['example.invalid/qualification'],'synthetic inventory')
    result['synthetic_inventory']=inventory
    command(evidence,'synthetic-full-lint',[lint,'run','-v','-j','2','--config','.golangci.yml',
       '--timeout','10m','--cpu-profile-path',str(evidence/'synthetic-lint-cpu.pprof'),'./...'],dict(os.environ),module,660)
    need((evidence/'synthetic-lint-cpu.pprof').stat().st_size>0,'synthetic lint CPU profile missing')
    need(digest(config_copy)==CONFIG_SHA,'synthetic config drift')
    result['synthetic_lint_executed']=True
    need(all(digest(module/name)==h for name,h in source.items()),'synthetic source drift')
    result['synthetic_source']=source
    result['identity_after']=child_identity(info['uid'],info['cgroup'])
    write_json(work/'worker-result.json',result)

def inside(prefix, ident):
    evidence=prefix/'evidence'; evidence.mkdir()
    report={'schema':'onebase-2222-synthetic-qualification-v1','mode':'synthetic-only','status':'FAILED',
            'identity':ident,'product_lint_executed':False,'keeper_lifecycle_qualified':False,'errors':[]}
    start=time.monotonic(); before=None
    try:
        need(os.geteuid()==0 and platform.system()=='Linux'and platform.machine()=='x86_64','native Linux amd64 root setup')
        need(not any(re.search('TOKEN|SECRET|PASSWORD|CREDENTIAL',k)for k in os.environ),'service credential environment')
        parent_status={line.split(':',1)[0]:line.split(':',1)[1].strip()for line in Path('/proc/self/status').read_text().splitlines()if ':'in line}
        allowed_caps=sum(1<<cap for cap in (0,1,6,7,8))
        need(parent_status['NoNewPrivs']=='1'and int(parent_status['CapEff'],16)&~allowed_caps==0,'supervisor minimal caps/NNP')
        parent_caps={k:parent_status[k]for k in ('CapInh','CapPrm','CapEff','CapBnd','CapAmb')}
        need(all(int(x,16)&~allowed_caps==0 for x in parent_caps.values()),'supervisor capability sets')
        report['supervisor']={'uid':os.geteuid(),'nnp':1,'effective_caps':parent_status['CapEff'],'caps':parent_caps,'allowed_caps':allowed_caps}
        before=resource_snapshot(); report['resources_before']=before
        mem_raw=Path('/proc/meminfo').read_text(); (evidence/'meminfo-before.txt').write_text(mem_raw)
        mem=parse_meminfo(mem_raw)
        dmi=Path('/sys/firmware/dmi/tables/DMI').read_bytes(); need(len(dmi)<=2*1024*1024,'DMI bound')
        (evidence/'DMI.bin').write_bytes(dmi)
        physical,count=assigned_memory(dmi)
        report['host']={'sysname':platform.system(),'machine':platform.machine(),'assigned_bytes':physical,
                        'physical_source':'SMBIOS-type17','populated_devices':count,**mem}
        need(physical>=HOST_CLASS,'assigned physical host below16GiB')
        need(mem['MemAvailable']>=LIMIT,'less than9GiB host available at admission')
        config=prefix/'input/canonical-config.yml'
        shutil.copyfile(config,evidence/'canonical-config.yml')
        shutil.copyfile(Path(__file__),evidence/'helper.py')
        need(digest(config)==CONFIG_SHA,'canonical config before')
        downloads=prefix/'downloads'; downloads.mkdir()
        fetch(GO_SUMS,downloads/'go-releases.json',8*1024*1024)
        candidates=[f for r in json.loads((downloads/'go-releases.json').read_bytes())if r.get('version')=='go1.26.10'for f in r['files']if f.get('filename')=='go1.26.10.linux-amd64.tar.gz']
        need(len(candidates)==1 and re.fullmatch('[0-9a-f]{64}',candidates[0]['sha256']),'official Go checksum')
        go_sha=fetch(GO_URL,downloads/'go.tar.gz',512*1024*1024)
        need(go_sha==candidates[0]['sha256'],'Go archive checksum mismatch')
        fetch(LINT_SUMS,downloads/'lint-checksums.txt',2*1024*1024)
        rows=[line.split()for line in (downloads/'lint-checksums.txt').read_text().splitlines()if line.split()and line.split()[-1].lstrip('*')=='golangci-lint-2.11.4-linux-amd64.tar.gz']
        need(len(rows)==1 and len(rows[0])==2 and re.fullmatch('[0-9a-f]{64}',rows[0][0]),'official linter checksum')
        lint_sha=fetch(LINT_URL,downloads/'lint.tar.gz',512*1024*1024)
        need(lint_sha==rows[0][0],'linter archive checksum mismatch')
        report['downloads']={'go':{'url':GO_URL,'checksum_source':GO_SUMS,'archive_sha256':go_sha,'official_sha256':candidates[0]['sha256']},
          'lint':{'url':LINT_URL,'checksum_source':LINT_SUMS,'archive_sha256':lint_sha,'official_sha256':rows[0][0]}}
        shutil.copyfile(downloads/'go-releases.json',evidence/'go-releases.json')
        shutil.copyfile(downloads/'lint-checksums.txt',evidence/'lint-checksums.txt')
        tools=prefix/'tools'; tools.mkdir(); unpack(downloads/'go.tar.gz',tools/'go'); unpack(downloads/'lint.tar.gz',tools/'lint')
        binaries={'go':tools/'go/go/bin/go','lint':tools/'lint/golangci-lint-2.11.4-linux-amd64/golangci-lint'}
        (evidence/'binaries').mkdir()
        for name,binary in binaries.items():shutil.copyfile(binary,evidence/'binaries'/name)
        shutil.copyfile(tools/'go/go/LICENSE',evidence/'binaries/go.LICENSE.txt')
        shutil.copyfile(tools/'lint/golangci-lint-2.11.4-linux-amd64/LICENSE',evidence/'binaries/lint.LICENSE.txt')
        report['source_before']={'config':digest(config),'helper':digest(Path(__file__)),**{k:digest(p)for k,p in binaries.items()}}
        import pwd,grp
        uid=None
        for candidate in range(UID_FIRST,UID_LAST+1):
            try:pwd.getpwuid(candidate)
            except KeyError:
                try:grp.getgrgid(candidate)
                except KeyError:uid=candidate;break
        need(uid is not None,'unused unprivileged uid unavailable')
        # Dedicated new UID must not already own a live process, even without passwd entry.
        for proc in Path('/proc').glob('[0-9]*/status'):
            try:raw=proc.read_text()
            except (FileNotFoundError,PermissionError,ProcessLookupError):continue
            match=re.search(r'^Uid:\s+(\d+)',raw,re.M)
            need(not match or int(match[1])!=uid,'unused UID already has live process')
        work=prefix/'work'; work.mkdir(); os.chown(work,uid,uid)
        for name in ('home','tmp','gocache','gopath','gomodcache'):
            path=work/name;path.mkdir();os.chown(path,uid,uid)
        write_json(prefix/'input/worker.json',{'uid':uid,'cgroup':before['cgroup']})
        env={**ENV_FIXED,'PATH':str(binaries['go'].parent)+':/usr/bin:/bin','PYTHONUTF8':'1',
             **{key:str(work/value)for key,value in {'HOME':'home','TMPDIR':'tmp','GOCACHE':'gocache','GOPATH':'gopath','GOMODCACHE':'gomodcache'}.items()}}
        child=os.fork()
        if child==0:
            try:
                clear_and_drop(uid)
                os.execve('/usr/bin/python3',['/usr/bin/python3','-B','-X','utf8',str(Path(__file__)),'worker',str(prefix)],env)
            except BaseException as error:
                print('restricted child failed: '+str(error),file=sys.stderr,flush=True);os._exit(1)
        _,status=os.waitpid(child,0)
        need(os.waitstatus_to_exitcode(status)==0,'restricted child nonzero')
        report['worker']=json.loads((work/'worker-result.json').read_text())
        report['source_after']={'config':digest(config),'helper':digest(Path(__file__)),**{k:digest(p)for k,p in binaries.items()}}
        need(report['source_before']==report['source_after'],'config/helper/tool drift')
        report['status']='QUALIFIED'
    except BaseException as error:
        report['errors'].append(type(error).__name__+': '+str(error))
    finally:
        try:
            after=resource_snapshot();report['resources_after']=after
            need(before is not None,'initial resources missing')
            need(after['peak']<=LIMIT,'measured cgroup memory peak exceeded9GiB')
            need(all(after['events'].get(k,0)==before['events'].get(k,0)for k in ('max','oom','oom_kill','oom_group_kill')),'memory limit event')
            need([int(x)for x in after['raw']['cgroup.procs'].split()]==[os.getpid()],'validation descendants remain')
        except BaseException as error:report['errors'].append(type(error).__name__+': '+str(error))
        report['seconds']=time.monotonic()-start
        if report['errors']:report['status']='FAILED'
        if (prefix/'work/evidence').exists():shutil.copytree(prefix/'work/evidence',evidence/'worker')
        report['files']={str(p.relative_to(evidence)).replace(os.sep,'/'):digest(p)for p in evidence.rglob('*')if p.is_file()}
        if report['status']=='QUALIFIED':
            try:verify(evidence/'receipt.json',ident['sha'],ident['run_id'],report['source_before']['helper'],data=report)
            except BaseException as error:report['status']='FAILED';report['errors'].append('verifier: '+str(error))
        write_json(evidence/'receipt.json',report)
    return 0 if report['status']=='QUALIFIED' else 1

def unit_snapshot(unit):
    keys=('Id','LoadState','ActiveState','SubState','MainPID','ExecMainPID','ExecMainCode','ExecMainStatus','Result','Description','InvocationID','ControlGroup')
    argv=['/usr/bin/systemctl','show',unit,*['--property='+k for k in keys]]
    p=subprocess.run(argv,capture_output=True,timeout=15)
    value={line.split('=',1)[0]:line.split('=',1)[1]for line in p.stdout.decode('utf8','replace').splitlines()if '='in line}
    return {'argv':argv,'exit':p.returncode,'stdout':p.stdout.decode('utf8','replace'),'stderr':p.stderr.decode('utf8','replace'),'fields':value}

def workload_terminal(snapshot, unit, description):
    v=snapshot['fields']
    need(snapshot['exit']==0 and v.get('Id')==unit and v.get('LoadState')=='loaded'and v.get('Description')==description and re.fullmatch('[0-9a-f]{32}',v.get('InvocationID','')),'unit readback identity UNKNOWN')
    if v.get('MainPID')!='0':return False
    return ((v.get('ActiveState'),v.get('SubState'))in {('active','exited'),('failed','failed'),('inactive','dead')}
            and v.get('ExecMainCode')in {'1','2','3'}and re.fullmatch('[0-9]+',v.get('ExecMainStatus',''))is not None)

def bootstrap():
    need(sys.platform=='linux'and os.geteuid()==0,'bootstrap only root on ephemeral Linux runner')
    ident=identity(os.environ)
    prefix=PARENT/(ident['run_id']+'-'+ident['run_attempt']+'-'+ident['sha'][:12])
    need(not PARENT.is_symlink()and not prefix.exists(),'owned namespace already present or symlink')
    PARENT.mkdir(mode=0o755,exist_ok=True)
    need(PARENT.stat().st_uid==0 and not(PARENT.stat().st_mode&0o022),'trusted prefix parent')
    prefix.mkdir(mode=0o755); (prefix/'input').mkdir()
    script=prefix/'input/qualify-2222-linux.py';shutil.copyfile(__file__,script)
    config=Path(__file__).resolve().parents[2]/'.golangci.yml';shutil.copyfile(config,prefix/'input/canonical-config.yml')
    props=['User=root','Group=root','NoNewPrivileges=yes','CapabilityBoundingSet=CAP_CHOWN CAP_DAC_OVERRIDE CAP_SETUID CAP_SETGID CAP_SETPCAP',
      'ProtectControlGroups=yes','ProtectSystem=strict','ProtectHome=yes','ProtectKernelTunables=yes','ProtectKernelModules=yes',
      'ProtectKernelLogs=yes','PrivateDevices=yes','PrivateTmp=yes','RestrictNamespaces=yes','RestrictSUIDSGID=yes','LockPersonality=yes',
      'ProtectClock=yes','ProtectHostname=yes','SystemCallArchitectures=native','Delegate=no','UMask=0022',
      'CPUAccounting=yes','MemoryAccounting=yes','TasksAccounting=yes','TasksMax=256','AllowedCPUs=0,1',
      'CPUQuota=200%','CPUQuotaPeriodSec=100ms','MemoryMax='+str(LIMIT),'MemorySwapMax=0','RuntimeMaxSec=1080s',
      'TimeoutStopSec=15s','KillMode=control-group','RemainAfterExit=yes','ReadWritePaths='+str(prefix),
      'StandardOutput=append:'+str(prefix/'service.stdout'),'StandardError=append:'+str(prefix/'service.stderr')]
    unit='ob2222-qual-'+ident['run_id']+'-'+ident['run_attempt']+'.service'
    description='OneBase2222 synthetic sha='+ident['sha']+' run='+ident['run_id']+' attempt=1'
    argv=['/usr/bin/systemd-run','--no-ask-password','--expand-environment=no','--service-type=exec',
      '--unit='+unit,'--description='+description]
    argv+=['--property='+x for x in props]
    argv+=['--setenv=PATH=/usr/bin:/bin','--setenv=LANG=C.UTF-8','--setenv=PYTHONUTF8=1']
    argv+=['--setenv=GITHUB_'+k.upper()+'='+v for k,v in ident.items()]
    argv+=['/usr/bin/python3','-B','-X','utf8',str(script),'inside',str(prefix)]
    # Outside the unit this is only fixed host control-plane start/state/wait/stop/copy.
    snapshots=[]; terminal=False; successful=False; code=1; start_output=b''; start_error=b'';start_exit=None;stop_exit=None
    try:
        p=subprocess.run(argv,capture_output=True,timeout=30)
        start_exit=p.returncode
        start_output,start_error=p.stdout,p.stderr
        need(p.returncode==0,'unit start failed/UNKNOWN; no restart')
        deadline=time.monotonic()+1110; invocation=None
        while time.monotonic()<deadline:
            snap=unit_snapshot(unit);snapshots.append(snap)
            done=workload_terminal(snap,unit,description)
            current=snap['fields']['InvocationID'];need(invocation in (None,current),'unit incarnation changed')
            invocation=current
            if done:
                v=snap['fields']
                successful=v['ActiveState']=='active'and v['SubState']=='exited'and v['ExecMainCode']=='1'and v['ExecMainStatus']=='0'and v['Result']=='success'
                # Keep the successful exited unit loaded until its state is captured,
                # then stop only that exact empty, incarnation-bound owned service.
                stopped=subprocess.run(['/usr/bin/systemctl','stop',unit],capture_output=True,timeout=30)
                stop_exit=stopped.returncode
                need(stopped.returncode==0,'own unit stop outcome UNKNOWN')
                after=unit_snapshot(unit);snapshots.append(after);v2=after['fields']
                need((after['exit']==0 and v2.get('ActiveState')=='inactive'and v2.get('MainPID')=='0')or v2.get('LoadState')=='not-found','own unit terminal cleanup UNKNOWN')
                procs=Path('/sys/fs/cgroup/system.slice')/unit/'cgroup.procs'
                need(not procs.exists()or not procs.read_text().strip(),'own unit descendants remain after stop')
                terminal=True;code=0 if successful else 1;break
            time.sleep(5)
        need(terminal,'unit state deadline/UNKNOWN; no restart')
    except BaseException as error:
        start_error+=('\ncontroller: '+type(error).__name__+': '+str(error)).encode()
    (prefix/'systemd-run.stdout').write_bytes(start_output);(prefix/'systemd-run.stderr').write_bytes(start_error)
    target=Path(os.environ['RUNNER_TEMP'])/('onebase-2222-qualification-'+ident['run_id']+'-'+ident['run_attempt'])
    need(target.parent.resolve()==Path(os.environ['RUNNER_TEMP']).resolve()and not target.exists(),'artifact destination')
    target.mkdir()
    for name in ('systemd-run.stdout','systemd-run.stderr'):shutil.copyfile(prefix/name,target/name)
    if terminal:
        for name in ('service.stdout','service.stderr'):
            if (prefix/name).exists():shutil.copyfile(prefix/name,target/name)
        if (prefix/'evidence').exists():shutil.copytree(prefix/'evidence',target/'evidence')
    write_json(target/'controller.json',{'identity':ident,'controller_exit':code,'unit_snapshots':snapshots,
       'start_exit':start_exit,'stop_exit':stop_exit,
       'terminal_wait_observed':terminal,'runtime_prefix':str(prefix),'scope':'synthetic-only; no Mac keeper or product qualification',
       'unknown_or_failure_remains_parked':True})
    print(json.dumps({'artifact':str(target),'controller_exit':code,'terminal_wait_observed':terminal}))
    return 0 if code==0 else 1

def verify(path, expected_sha, expected_run, expected_helper, data=None):
    if data is None:
        raw=Path(path).read_bytes();need(len(raw)<=2*1024*1024,'receipt bound');r=json.loads(raw)
    else:r=data
    need(r.get('schema')=='onebase-2222-synthetic-qualification-v1'and r.get('mode')=='synthetic-only','receipt schema/mode')
    need(r.get('status')=='QUALIFIED'and r.get('errors')==[] and r.get('product_lint_executed')is False and r.get('keeper_lifecycle_qualified')is False,'not qualified/synthetic-only')
    ident=r['identity'];need(identity({'GITHUB_'+k.upper():v for k,v in ident.items()})==ident,'receipt identity')
    need(ident['sha']==expected_sha and ident['run_id']==expected_run,'external source/run binding')
    h=r['host'];need(h['sysname']=='Linux'and h['machine']=='x86_64'and h['physical_source']=='SMBIOS-type17'and h['assigned_bytes']>=HOST_CLASS and h['MemAvailable']>=LIMIT,'actual native physical/free capacity')
    supervisor=r['supervisor'];allowed_caps=sum(1<<cap for cap in (0,1,6,7,8))
    need(supervisor['uid']==0 and supervisor['nnp']==1 and supervisor['allowed_caps']==allowed_caps and int(supervisor['effective_caps'],16)&~allowed_caps==0 and set(supervisor['caps'])=={'CapInh','CapPrm','CapEff','CapBnd','CapAmb'}and all(int(v,16)&~allowed_caps==0 for v in supervisor['caps'].values()),'supervisor privileges')
    a,b=r['resources_before'],r['resources_after']
    expected_cgroup='/system.slice/ob2222-qual-'+expected_run+'-1.service'
    need(a['cgroup']==b['cgroup']==expected_cgroup and a['pid']==b['pid']and type(b['pid'])is int and b['pid']>1 and b['peak']<=LIMIT,'cgroup/peak/PID mismatch')
    for x in (a,b):
        v=x['raw'];need(v['cpu.max']=='200000 100000'and v['cpu.max.burst']=='0'and cpu_set(v['cpuset.cpus.effective'])==[0,1]and v['memory.max']==str(LIMIT)and v['memory.swap.max']=='0','resource readback')
        events={k:int(value)for k,value in (line.split()for line in v['memory.events'].splitlines())}
        need(events==x['events']and int(v['memory.peak'])==x['peak'],'raw memory event/peak semantics')
        need({'max','oom','oom_kill'}<=set(events),'missing kernel memory counters')
        need(isinstance(x['ancestors'],list)and x['ancestors']and all(z['value']=='max'or int(z['value'])>=LIMIT for z in x['ancestors']),'parent memory capacity')
    need(all(a['events'].get(k,0)==b['events'].get(k,0)for k in ('max','oom','oom_kill','oom_group_kill')),'memory limit failure event')
    need([int(x)for x in b['raw']['cgroup.procs'].split()]==[b['pid']],'remaining runtime child')
    w=r['worker'];c=w['identity'];need(UID_FIRST<=c['uid']<=UID_LAST and c['uid']==c['gid']and c['groups']==[]and c['nnp']==1 and all(int(v,16)==0 for v in c['caps'].values())and set(c['caps'])=={'CapInh','CapPrm','CapEff','CapBnd','CapAmb'},'restricted UID/caps')
    need(c['cgroup']==a['cgroup']and c['ppid']==b['pid']and c['write_denied']==['cgroup.procs','memory.max','cpu.max'],'child cgroup/escape guard')
    need(set(c['host_ipc'])=={'/var/run/docker.sock','/run/docker.sock','/run/containerd/containerd.sock'}and all(v['state']in {'absent','inaccessible','present'}and v['readable']is False and v['writable']is False for v in c['host_ipc'].values()),'privileged host IPC access')
    need(w['identity_after']==c,'child privilege identity drift')
    need(all(w['env'].get(k)==v for k,v in ENV_FIXED.items())and set(w['env'])<=set(ENV_FIXED)|{'PATH','HOME','TMPDIR','GOCACHE','GOPATH','GOMODCACHE','PYTHONUTF8'},'worker environment')
    need(w['go_version']=='go version go1.26.10 linux/amd64'and w['lint_version']=='2.11.4'and w['cgo_stdout']=='cgo=42\n'and len(w['enabled_linters'])==7 and set(w['enabled_linters'])==LINTE,'native tools/CGO/full configuration')
    need(w.get('synthetic_lint_executed')is True and w.get('synthetic_inventory')==['example.invalid/qualification'],'synthetic complete linter execution')
    need(w['go_env']=={'GOHOSTOS':'linux','GOHOSTARCH':'amd64','GOOS':'linux','GOARCH':'amd64','CGO_ENABLED':'1','GOTOOLCHAIN':'go1.26.10'},'native Go environment')
    need(r['source_before']==r['source_after']and r['source_before']['config']==CONFIG_SHA and r['source_before']['helper']==expected_helper,'config/helper/tool drift/external helper binding')
    for name,url,sums in (('go',GO_URL,GO_SUMS),('lint',LINT_URL,LINT_SUMS)):
        d=r['downloads'][name];need(d['url']==url and d['checksum_source']==sums and re.fullmatch('[0-9a-f]{64}',d['official_sha256'])and d['archive_sha256']==d['official_sha256'],'official archive checksum')
    need(isinstance(r['files'],dict)and len(r['files'])<=128,'artifact file count')
    total=0
    for name,pin in r['files'].items():
        p=Path(name);need(not p.is_absolute()and '..'not in p.parts,'artifact path traversal')
        actual=Path(path).parent/p;need(not actual.is_symlink()and actual.resolve().is_relative_to(Path(path).parent.resolve()),'artifact escaped path: '+name)
        total+=actual.stat().st_size;need(total<=512*1024**2 and actual.stat().st_size<=256*1024**2,'artifact size bound')
        need(digest(actual)==pin,'artifact byte drift: '+name)
    mandatory={'DMI.bin','canonical-config.yml','helper.py','meminfo-before.txt',
      'go-releases.json','lint-checksums.txt','worker/go-version.stdout','worker/go-env.stdout',
      'worker/lint-version.stdout','worker/configured-linters.stdout','binaries/go','binaries/lint','binaries/go.LICENSE.txt','binaries/lint.LICENSE.txt','worker/synthetic-lint-cpu.pprof'}
    expected_prefix=PARENT.as_posix()+'/'+ident['run_id']+'-1-'+ident['sha'][:12];go=expected_prefix+'/tools/go/go/bin/go';lint=expected_prefix+'/tools/lint/golangci-lint-2.11.4-linux-amd64/golangci-lint'
    commands={'go-version':[go,'version'],'go-env':[go,'env','-json','GOHOSTOS','GOHOSTARCH','GOOS','GOARCH','CGO_ENABLED','GOTOOLCHAIN'],
      'lint-version':[lint,'version','--short'],'configured-linters':[lint,'linters','--json','--config',expected_prefix+'/input/canonical-config.yml'],
      'synthetic-cgo-build':[go,'build','-o',expected_prefix+'/work/synthetic-cgo','.'],
      'synthetic-cgo-execute':[expected_prefix+'/work/synthetic-cgo'],
      'synthetic-inventory':[go,'list','./...'],
      'synthetic-full-lint':[lint,'run','-v','-j','2','--config','.golangci.yml','--timeout','10m','--cpu-profile-path',expected_prefix+'/work/evidence/synthetic-lint-cpu.pprof','./...']}
    mandatory|={f'worker/{name}.{suffix}'for name in commands for suffix in ('command.json','stdout','stderr')}
    need(mandatory<=set(r['files']),'missing raw artifact evidence')
    evidence=Path(path).parent
    physical,count=assigned_memory((evidence/'DMI.bin').read_bytes())
    need(physical==h['assigned_bytes']and count==h['populated_devices']and parse_meminfo((evidence/'meminfo-before.txt').read_text())=={k:h[k]for k in ('MemTotal','MemAvailable')},'raw capacity derivation mismatch')
    need(digest(evidence/'canonical-config.yml')==CONFIG_SHA and digest(evidence/'helper.py')==expected_helper,'raw config/helper binding')
    need((evidence/'worker/go-version.stdout').read_text().strip()==w['go_version']and (evidence/'worker/lint-version.stdout').read_text().strip()==w['lint_version']and (evidence/'worker/synthetic-cgo-execute.stdout').read_text()==w['cgo_stdout'],'raw tool/CGO output mismatch')
    need(json.loads((evidence/'worker/go-env.stdout').read_bytes())==w['go_env'],'raw native environment mismatch')
    enabled=json.loads((evidence/'worker/configured-linters.stdout').read_bytes())['Enabled']
    need(len(enabled)==7 and {x.get('name',x.get('Name'))for x in enabled}==set(w['enabled_linters']),'raw configured linters mismatch')
    need((evidence/'worker/synthetic-inventory.stdout').read_text().splitlines()==w['synthetic_inventory']and (evidence/'worker/synthetic-lint-cpu.pprof').stat().st_size>0,'raw synthetic inventory/profile')
    for name,argv in commands.items():
        command_record=json.loads((evidence/('worker/'+name+'.command.json')).read_bytes())
        need(command_record.get('exit')==0 and command_record.get('argv')==argv and type(command_record.get('seconds'))in (int,float)and 0<=command_record['seconds']<=660,'raw command terminal/argv binding: '+name)
    releases=json.loads((evidence/'go-releases.json').read_bytes())
    go_sums=[f['sha256']for release in releases if release.get('version')=='go1.26.10'for f in release['files']if f.get('filename')=='go1.26.10.linux-amd64.tar.gz']
    need(go_sums==[r['downloads']['go']['official_sha256']],'raw official Go checksum')
    lint_sums=[line.split()[0]for line in (evidence/'lint-checksums.txt').read_text().splitlines()if len(line.split())==2 and line.split()[1].lstrip('*')=='golangci-lint-2.11.4-linux-amd64.tar.gz']
    need(lint_sums==[r['downloads']['lint']['official_sha256']],'raw official linter checksum')
    for name in ('go','lint'):
        binary=evidence/'binaries'/name
        with binary.open('rb')as stream:header=stream.read(64)
        need(len(header)==64 and header[:6]==b'\x7fELF\x02\x01'and struct.unpack_from('<H',header,18)[0]==62 and digest(binary)==r['source_before'][name],'preserved Linux amd64 executable/hash')
    return {'verified':True,'scope':'synthetic environment only; product lint remains parked','sha':expected_sha,'run_id':expected_run}

def verify_controller(path, receipt):
    raw=Path(path).read_bytes();need(len(raw)<=2*1024*1024,'controller bound')
    c=json.loads(raw);ident=receipt['identity']
    need(c['identity']==ident and c['controller_exit']==0 and c['start_exit']==0 and c['stop_exit']==0 and c['terminal_wait_observed']is True,'controller terminal outcome UNKNOWN/nonzero')
    snapshots=c['unit_snapshots'];need(isinstance(snapshots,list)and len(snapshots)>=2,'terminal snapshots missing')
    unit='ob2222-qual-'+ident['run_id']+'-1.service'
    description='OneBase2222 synthetic sha='+ident['sha']+' run='+ident['run_id']+' attempt=1'
    for snap in snapshots:
        need(snap['argv'][:3]==['/usr/bin/systemctl','show',unit],'unit snapshot command identity')
        fields={line.split('=',1)[0]:line.split('=',1)[1]for line in snap['stdout'].splitlines()if '='in line}
        need(fields==snap['fields'],'unit raw field mismatch')
    before,after=snapshots[-2],snapshots[-1]
    need(workload_terminal(before,unit,description),'unit workload not terminal')
    v=before['fields'];need(v['ActiveState']=='active'and v['SubState']=='exited'and v['ExecMainCode']=='1'and v['ExecMainStatus']=='0'and v['Result']=='success'and v['ExecMainPID']==str(receipt['resources_after']['pid'])and v['ControlGroup']==receipt['resources_after']['cgroup'],'successful runtime unit binding')
    v=after['fields'];need((after['exit']==0 and v.get('ActiveState')=='inactive'and v.get('MainPID')=='0')or v.get('LoadState')=='not-found','unit cleanup not terminal')
    return True

def main():
    parser=argparse.ArgumentParser(description=__doc__);sub=parser.add_subparsers(dest='mode',required=True)
    sub.add_parser('bootstrap')
    for name in ('inside','worker'):
        p=sub.add_parser(name);p.add_argument('prefix',type=Path)
    p=sub.add_parser('verify');p.add_argument('receipt',type=Path);p.add_argument('--controller',required=True,type=Path);p.add_argument('--expected-sha',required=True);p.add_argument('--expected-run-id',required=True);p.add_argument('--expected-helper-sha',required=True)
    args=parser.parse_args()
    try:
        if args.mode=='verify':
            value=verify(args.receipt,args.expected_sha,args.expected_run_id,args.expected_helper_sha)
            verify_controller(args.controller,json.loads(args.receipt.read_bytes()))
            print(json.dumps(value));return 0
        if args.mode=='bootstrap':return bootstrap()
        need(sys.platform=='linux','Linux runtime subcommand only')
        need(args.prefix.parent==PARENT and re.fullmatch(r'[1-9][0-9]{0,19}-1-[0-9a-f]{12}',args.prefix.name)and args.prefix.resolve()==args.prefix,'owned runtime prefix')
        if args.mode=='worker':worker(args.prefix);return 0
        return inside(args.prefix,identity(os.environ))
    except BaseException as error:
        print(json.dumps({'verified':False,'error':type(error).__name__+': '+str(error),'product_lint_executed':False}),file=sys.stderr)
        return 1

if __name__=='__main__':sys.exit(main())
