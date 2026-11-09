#!/usr/bin/env python3
"""Mechanical candidate full lint; dispatch requires upstream Native FIX and keeper admission."""
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
import traceback
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

def privilege_state():
    status={line.split(':',1)[0]:line.split(':',1)[1].strip()for line in Path('/proc/self/status').read_text().splitlines()if ':'in line}
    return {'uid':os.getuid(),'euid':os.geteuid(),'gid':os.getgid(),'egid':os.getegid(),
      'groups':os.getgroups(),'nnp':int(status['NoNewPrivs']),
      'caps':{k:status[k]for k in ('CapInh','CapPrm','CapEff','CapBnd','CapAmb')},'pid':os.getpid(),'ppid':os.getppid()}

def supervisor_snapshot():
    state=privilege_state();allowed=sum(1<<cap for cap in (0,1,6,7,8))
    return {'uid':state['uid'],'nnp':state['nnp'],'effective_caps':state['caps']['CapEff'],
      'caps':state['caps'],'allowed_caps':allowed,'required_caps':allowed}

def require_supervisor(snapshot):
    allowed=sum(1<<cap for cap in (0,1,6,7,8));caps=snapshot['caps']
    need(snapshot['uid']==0 and snapshot['nnp']==1 and snapshot['allowed_caps']==snapshot['required_caps']==allowed,'supervisor root/NNP/fixed capability policy')
    need(set(caps)=={'CapInh','CapPrm','CapEff','CapBnd','CapAmb'}and all(int(v,16)&~allowed==0 for v in caps.values()),'supervisor capability ceiling')
    need(snapshot['effective_caps']==caps['CapEff'],'supervisor actual/claimed effective capability drift')
    need(int(caps['CapEff'],16)==allowed and int(caps['CapPrm'],16)==allowed and int(caps['CapBnd'],16)==allowed,'required supervisor effective/permitted capability missing before validation')
    need(int(caps['CapAmb'],16)==(1<<7)and int(caps['CapInh'],16)&(1<<7),'supervisor requested ambient SETUID readback')

def trace_privilege(fd, stage, status, error=None):
    try:
        record={'stage':stage,'status':status,'state':privilege_state()}
        if error is not None:record['error']={'type':type(error).__name__,'errno':getattr(error,'errno',None),'message':str(error)}
        raw=(json.dumps(record,sort_keys=True)+'\n').encode('utf8')
        if os.write(fd,raw)!=len(raw):raise OSError('short privilege audit write')
        os.fsync(fd)
    except BaseException as audit_error:
        raise RuntimeError('privilege audit failed at '+stage+'/'+status+': '+str(audit_error))from audit_error

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

def clear_and_drop(uid, trace_fd):
    libc=ctypes.CDLL(None,use_errno=True)
    def call(stage, operation):
        trace_privilege(trace_fd,stage,'before')
        try:operation()
        except BaseException as error:
            try:trace_privilege(trace_fd,stage,'error',error)
            except BaseException as audit_error:
                try:print('additional privilege audit failure at '+stage+': '+str(audit_error),file=sys.stderr,flush=True)
                except BaseException:pass  # Keep the original operation exception active.
            raise  # Preserve the original failing syscall/stage traceback.
        trace_privilege(trace_fd,stage,'after')
    def prctl(option, value, description):
        if libc.prctl(option,value,0,0,0)!=0:raise OSError(ctypes.get_errno(),description)
    def drop_bounding():
        for cap in range(64):
            if libc.prctl(24,cap,0,0,0)!=0 and ctypes.get_errno()!=22:raise OSError(ctypes.get_errno(),'bounding capability drop '+str(cap))
    trace_privilege(trace_fd,'child-start','ok')
    call('no-new-privileges',lambda:prctl(38,1,'NoNewPrivileges'))
    call('clear-ambient',lambda:prctl(47,4,'ambient capability clear'))
    call('drop-bounding',drop_bounding)
    call('setgroups',lambda:os.setgroups([]))
    call('setgid',lambda:os.setgid(uid))
    call('setuid',lambda:os.setuid(uid))
    class Header(ctypes.Structure): _fields_=[('version',ctypes.c_uint),('pid',ctypes.c_int)]
    class Data(ctypes.Structure): _fields_=[('effective',ctypes.c_uint),('permitted',ctypes.c_uint),('inheritable',ctypes.c_uint)]
    h=Header(0x20080522,0); data=(Data*2)()
    def zero_capabilities():
        if libc.capset(ctypes.byref(h),data)!=0:raise OSError(ctypes.get_errno(),'capability zero')
    call('zero-capabilities',zero_capabilities)

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

TRANSPORT_SHA = '0757623e8c82b7e3f68f844fffae146bfdb20663bfd23262e8110afaed470eeb'
PRODUCT_RUNTIME_ARMED = False  # Requires a separately reviewed fresh Native/cutover revision.
SOURCE_SHA = '22cac7a508365b4b9a6dd5ff97090a6447b15531'
OWNER_CONTRACT_SHA = 'd6baed03bf74e5150d6eceb96e7c35137df24a73ab06cff7adf2e22c778649c9'
PROOF_EPOCH = '2360ab12ac3e79356ab9a526883f27dac78f37bfbd770144afdfc1a1722402fe'
FIXTURE_SHA = '58b098edfc8b1aa05a0f86f8bb3ab267858fdbcf1c35ace952fba5e6abb62563'
INPUT_KEYS = {'mode','candidate_packet','candidate_admission','admission_sha256','task_id','nonce','expected_operator_sha'}
MODULE_PREP = {'GOPROXY':'https://proxy.golang.org','GOSUMDB':'sum.golang.org','GOVCS':'*:off','GOPRIVATE':'','GONOPROXY':'','GONOSUMDB':''}
PUBLIC_PARENTS = {'source':'https://github.com/AnnaSIceberg/onebase.git','main':'https://github.com/ivanarama/onebase.git'}

def transport_module():
    import importlib.util
    path=Path(__file__).with_name('candidate_transport.py')
    need(digest(path)==TRANSPORT_SHA,'frozen transport source drift')
    spec=importlib.util.spec_from_file_location('candidate_transport_frozen',path)
    module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module);return module

def validate_request(event,ident):
    t=transport_module();values=event.get('inputs')
    need(isinstance(values,dict)and set(values)==INPUT_KEYS and all(isinstance(v,str)for v in values.values()),'fixed workflow inputs required')
    need(len(t.canonical(values))<=t.MAX_WIRE,'complete dispatch inputs bound')
    need(values['mode']in {'transport-fixture','product-full-lint'},'unknown fixed runtime mode')
    need(re.fullmatch('[A-Za-z0-9_-]{1,80}',values['task_id'])is not None and re.fullmatch('[0-9a-f]{32,64}',values['nonce'])is not None,'bounded task/nonce')
    need(values['expected_operator_sha']==ident['sha'],'external operator SHA binding')
    title='onebase-2222:'+values['task_id']+':'+values['nonce']
    if values['mode']=='transport-fixture':
        need(values['task_id'].startswith('fixture-2222-')and all(values[k]==''for k in ('candidate_packet','candidate_admission','admission_sha256')),'fixture must have a fixture task and no product input')
        return {'inputs':values,'runtime_mode':'transport-fixture','display_title':title,'native_FIX_admission_verified':False,'keeper_lifecycle_qualified':False}
    admission_raw=values['candidate_admission'].encode('ascii');t.sha(values['admission_sha256'])
    need(hashlib.sha256(admission_raw).hexdigest()==values['admission_sha256'],'externally supplied admission hash mismatch')
    admission=t.load(admission_raw)
    need(t.canonical(admission)==admission_raw and set(admission)=={'format','context','tree','manifest_sha256','packet_sha256'}and admission['format']=='onebase-candidate-admission-v1','canonical admission schema')
    packet=t.decode(values['candidate_packet']);ctx=t.context(packet['context'])
    need(all(admission[k]==packet[k]for k in ('context','tree','manifest_sha256'))and admission['packet_sha256']==hashlib.sha256(values['candidate_packet'].encode('ascii')).hexdigest(),'packet/admission binding')
    need(ctx['purpose']=='admitted-candidate'and ctx['source']==SOURCE_SHA and ctx['owner_contract_sha256']==OWNER_CONTRACT_SHA and ctx['proof_epoch']==PROOF_EPOCH,'wrong source/owner contract/proof/purpose')
    need(ctx['operator_sha']==values['expected_operator_sha']==ident['sha'],'external operator SHA binding')
    need(ctx['task_id']==values['task_id']and ctx['nonce']==values['nonce'],'display task/nonce binding')
    need(not ctx['task_id'].startswith('fixture-'),'fixture task cannot request product analysis')
    return {'inputs':values,'runtime_mode':'product-full-lint','admission':admission,'packet':packet,'display_title':title,
      'native_FIX_admission_verified':False,'keeper_lifecycle_qualified':False}

WIRE_ENV = {'mode':'OB2222_INPUT_MODE','task_id':'OB2222_INPUT_TASK_ID','nonce':'OB2222_INPUT_NONCE',
  'expected_operator_sha':'OB2222_INPUT_OPERATOR_SHA','candidate_packet':'OB2222_INPUT_PACKET',
  'candidate_admission':'OB2222_INPUT_ADMISSION','admission_sha256':'OB2222_INPUT_ADMISSION_SHA'}

def save_input_capture(path,raw):
    if path.exists():
        need(not path.is_symlink()and path.read_bytes()==raw,'captured input changed between reads')
    else:
        with path.open('xb')as f:f.write(raw);f.flush();os.fsync(f.fileno())

def workflow_wire(env):
    names=set(WIRE_ENV.values());present={k for k in env if k.startswith('OB2222_INPUT_')}
    need(present==names,'fixed workflow wire environment map missing/unknown fields')
    need(all(type(env[name])is str for name in names),'workflow wire values must be OS strings')
    return {'inputs':{key:env[name]for key,name in WIRE_ENV.items()}}

def capture_workflow_request(event_path,evidence,ident,env):
    t=transport_module()
    with event_path.open('rb')as f:raw=f.read(256*1024+1)
    complete=len(raw)<=256*1024
    save_input_capture(evidence/'request-event.raw.json',raw)
    shape={'raw_sha256':hashlib.sha256(raw).hexdigest(),'captured_bytes':len(raw),'capture_complete':complete,
      'authority':'diagnostic only; historical GitHub event.inputs shape is not the wire contract'}
    try:
        event=t.load(raw);shape['event_type']=type(event).__name__
        values=event.get('inputs')if isinstance(event,dict)else None;shape['inputs_type']=type(values).__name__
        if isinstance(values,dict):
            shape['input_keys']=sorted(values);shape['input_types']={k:type(v).__name__ for k,v in values.items()}
            shape['missing_declared_keys']=sorted(INPUT_KEYS-set(values));shape['unknown_input_keys']=sorted(set(values)-INPUT_KEYS)
        else:shape['missing_declared_keys']=sorted(INPUT_KEYS)
    except Exception as error:shape['diagnostic_parse_error']=type(error).__name__+': '+str(error)
    save_input_capture(evidence/'request-event-shape.json',t.canonical(shape))
    need(complete,'original GitHub event exceeds256KiB capture bound')
    mapping={'expected_environment_keys':sorted(WIRE_ENV.values()),'present_environment_keys':sorted(k for k in env if k.startswith('OB2222_INPUT_')),
      'types':{name:type(env[name]).__name__ for name in WIRE_ENV.values()if name in env}}
    save_input_capture(evidence/'workflow-wire-map.json',t.canonical(mapping))
    wire=workflow_wire(env);canonical=t.canonical(wire)
    need(len(canonical)<=256*1024,'canonical workflow wire diagnostic capture bound')
    save_input_capture(evidence/'workflow-inputs.canonical.json',canonical)
    save_input_capture(evidence/'workflow-inputs-binding.json',t.canonical({'wire_sha256':hashlib.sha256(canonical).hexdigest(),'wire_bytes':len(canonical),
      'source':'fixed seven declared workflow env values','native_FIX_admission_verified':False,'product_runtime_armed':PRODUCT_RUNTIME_ARMED}))
    return validate_request(wire,ident)

def read_request(prefix,ident):
    return capture_workflow_request(prefix/'input/event.json',prefix/'evidence',ident,os.environ)

def verify_wire_evidence(evidence,ident):
    t=transport_module();raw=(evidence/'request-event.raw.json').read_bytes();shape=t.load((evidence/'request-event-shape.json').read_bytes())
    need(len(raw)<=256*1024 and shape['capture_complete']is True and shape['captured_bytes']==len(raw)and shape['raw_sha256']==hashlib.sha256(raw).hexdigest(),'raw original event capture binding')
    canonical=(evidence/'workflow-inputs.canonical.json').read_bytes();wire=t.load(canonical);need(t.canonical(wire)==canonical,'canonical workflow wire encoding')
    binding=t.load((evidence/'workflow-inputs-binding.json').read_bytes())
    need(binding=={'wire_sha256':hashlib.sha256(canonical).hexdigest(),'wire_bytes':len(canonical),'source':'fixed seven declared workflow env values','native_FIX_admission_verified':False,'product_runtime_armed':False},'canonical fixed wire binding')
    mapping=t.load((evidence/'workflow-wire-map.json').read_bytes());names=sorted(WIRE_ENV.values())
    need(mapping=={'expected_environment_keys':names,'present_environment_keys':names,'types':{name:'str'for name in names}},'fixed complete OS-string environment map evidence')
    return validate_request(wire,ident)

def fixed_command_plan(prefix):
    base=prefix.as_posix();go=base+'/tools/go/go/bin/go';lint=base+'/tools/lint/golangci-lint-2.11.4-linux-amd64/golangci-lint'
    return {'module-download':[go,'mod','download','-json','all'],
      'module-cache-verify':[go,'mod','verify'],
      'module-graph':[go,'list','-m','-json','-mod=readonly','all'],
      'product-inventory':[go,'list','./...'],
      'product-full-lint':[lint,'run','-v','-j','2','--config','.golangci.yml','--timeout','10m','--cpu-profile-path',base+'/work/evidence/product-lint-cpu.pprof','./...']}

def json_stream(raw):
    text=raw.decode('utf8');decoder=json.JSONDecoder();result=[];offset=0
    need(len(raw)<=32*1024*1024,'Go JSON output bound')
    while offset<len(text):
        while offset<len(text)and text[offset].isspace():offset+=1
        if offset==len(text):break
        value,offset=decoder.raw_decode(text,offset);need(isinstance(value,dict)and not value.get('Error'),'Go module JSON error');result.append(value)
        need(len(result)<=10000,'module inventory count bound')
    need(result,'empty module inventory');return result

def module_checks(download_raw,verify_raw,graph_raw,go_sum):
    sums={}
    for line in go_sum.decode('utf8').splitlines():
        parts=line.split();need(len(parts)==3,'go.sum syntax');key=tuple(parts[:2]);need(key not in sums or sums[key]==parts[2],'go.sum checksum conflict');sums[key]=parts[2]
    rows=json_stream(download_raw);normalized=[]
    for row in rows:
        key=(row.get('Path'),row.get('Version'))
        need(all(isinstance(x,str)and x for x in key),'downloaded module identity')
        need(row.get('Sum')==sums.get(key)and row.get('GoModSum')==sums.get((key[0],key[1]+'/go.mod'))and row.get('Sum')and row.get('GoModSum'),'downloaded module/go.mod checksums not pinned in candidate go.sum')
        normalized.append({k:row[k]for k in ('Path','Version','Sum','GoModSum')})
    need(verify_raw==b'all modules verified\n','module cache verification did not complete')
    graph=json_stream(graph_raw);need(sum(x.get('Main')is True for x in graph)==1,'main module graph identity')
    return {'downloaded_modules':normalized,'downloaded_count':len(rows),'graph_count':len(graph),'go_sum_sha256':hashlib.sha256(go_sum).hexdigest(),'go_mod_and_sum_pins_validated':True,'go_mod_verify_completed':True}

def actual_manifest(root,expected):
    t=transport_module();t.validate_manifest(expected);physical=t.verify_files(root,expected)
    records=[]
    for record in expected['entries']:
        path=root.joinpath(*record['path'].split('/'));data=path.read_bytes();item=dict(record)
        item.update(bytes=len(data),sha256=hashlib.sha256(data).hexdigest(),blob=hashlib.sha1(('blob '+str(len(data))+'\0').encode()+data).hexdigest())
        if os.name=='posix':item['mode']='100755'if path.stat().st_mode&0o100 else'100644'
        records.append(item)
    value={'format':expected['format'],'tree':expected['tree'],'entries':records,'total_bytes':sum(x['bytes']for x in records)}
    t.validate_manifest(value);need(value==expected,'complete candidate manifest drift');return value,physical

def prepare_candidate(prefix,ident,evidence):
    need(PRODUCT_RUNTIME_ARMED,'product runtime parked: fresh upstream Native admission and reviewed cutover required')
    t=transport_module();request=read_request(prefix,ident);ctx=request['admission']['context']
    need(request['runtime_mode']=='product-full-lint','product preparation requires explicit mode')
    empty=prefix/'empty-objects';empty.mkdir();public=t.Objects(empty,prefix/'public-fetch')
    for key,url in PUBLIC_PARENTS.items():
        # Exact already-public commits; no caller URL/ref, credentials, shallow
        # history, replace objects, checkout, filters, hooks or candidate commit.
        argv=[t.GIT,'--git-dir='+str(public.repo),'-c','core.attributesFile='+os.devnull,
          '-c','protocol.https.allow=always','fetch','--no-tags','--no-write-fetch-head',url,ctx[key]]
        command(evidence,key+'-public-fetch',argv,public.env,prefix,120)
    for key in ('source','main','base'):
        raw=public.git('cat-file','commit',ctx[key]);need(hashlib.sha1(('commit '+str(len(raw))+'\0').encode()+raw).hexdigest()==ctx[key],'public commit object integrity')
        (evidence/(key+'.commit')).write_bytes(raw)
    write_json(evidence/'public-fetch-commands.json',public.commands)
    admission=prefix/'input/candidate-admission.json';packet=prefix/'input/candidate.packet'
    admission.write_bytes(request['inputs']['candidate_admission'].encode('ascii'));packet.write_text(request['inputs']['candidate_packet'],encoding='ascii')
    args=argparse.Namespace(objects=str(public.repo/'objects'),admission=str(admission),expected_admission_sha=request['inputs']['admission_sha256'],packet=str(packet),out=str(prefix/'candidate'))
    receipt=t.reconstruct(args);need(receipt['filesystem_mode_verified']is True,'Linux physical candidate modes missing')
    (prefix/'candidate').chmod(0o755);(prefix/'candidate/tree').chmod(0o755)
    manifest=t.load((prefix/'candidate/before-manifest.json').read_bytes());root=prefix/'candidate/tree'
    need(digest(root/'.golangci.yml')==CONFIG_SHA,'candidate canonical full lint config changed')
    need({'go.mod','go.sum','.golangci.yml'}<={x['path']for x in manifest['entries']},'candidate module/config inventory')
    write_json(evidence/'candidate-binding.json',{'admission':request['admission'],'admission_sha256':request['inputs']['admission_sha256'],
      'display_title':request['display_title'],'dispatch_inputs_ascii_bytes':len(t.canonical(request['inputs'])),
      'native_FIX_admission_verified':False,'keeper_lifecycle_qualified':False})
    shutil.copyfile(prefix/'candidate/before-manifest.json',evidence/'candidate-reconstructed-manifest.json')
    shutil.copyfile(prefix/'candidate/git-commands.json',evidence/'candidate-reconstruct-commands.json')
    return request

def product_analysis(prefix,info,result,evidence):
    t=transport_module();root=prefix/'candidate/tree';manifest=t.load((prefix/'candidate/before-manifest.json').read_bytes())
    before,physical=actual_manifest(root,manifest);need(physical,'product needs native Linux file modes')
    (evidence/'candidate-before-manifest.json').write_bytes(t.canonical(before))
    shutil.copyfile(root/'go.mod',evidence/'candidate-go.mod');shutil.copyfile(root/'go.sum',evidence/'candidate-go.sum')
    go_mod_before=digest(root/'go.mod');go_sum_before=digest(root/'go.sum')
    env=dict(os.environ);prep_env={**env,**MODULE_PREP};plan=fixed_command_plan(prefix)
    try:
        write_json(evidence/'module-download-env.json',prep_env)
        downloaded=command(evidence,'module-download',plan['module-download'],prep_env,root,180)
        verified=command(evidence,'module-cache-verify',plan['module-cache-verify'],env,root,180)
        graph=command(evidence,'module-graph',plan['module-graph'],env,root,180)
        cache=module_checks(downloaded,verified,graph,(root/'go.sum').read_bytes());cache['go_mod_sha256']=go_mod_before
        need(digest(root/'go.mod')==go_mod_before and digest(root/'go.sum')==go_sum_before,'module cache preparation changed candidate')
        actual_manifest(root,before)
        write_json(evidence/'module-cache-validation.json',cache)
        inventory=command(evidence,'product-inventory',plan['product-inventory'],env,root,180).decode().splitlines()
        need(inventory and len(inventory)==len(set(inventory))and all(x and not any(c.isspace()for c in x)for x in inventory),'full product package inventory')
        need(time.monotonic()+660+30<=info['runtime_deadline_monotonic'],'insufficient outer budget for unchanged full 10m analysis')
        write_json(evidence/'product-analysis-start.json',{'argv':plan['product-full-lint'],'monotonic':time.monotonic(),'deadline_monotonic':info['runtime_deadline_monotonic']})
        command(evidence,'product-full-lint',plan['product-full-lint'],env,root,660)
        need((evidence/'product-lint-cpu.pprof').stat().st_size>0,'full product CPU profile missing')
    finally:
        try:
            after,physical=actual_manifest(root,before);need(physical,'physical mode readback lost');(evidence/'candidate-after-manifest.json').write_bytes(t.canonical(after))
            need(digest(root/'go.mod')==go_mod_before and digest(root/'go.sum')==go_sum_before and digest(root/'.golangci.yml')==CONFIG_SHA,'product modules/config drift')
        except BaseException as error:
            write_json(evidence/'candidate-after-error.json',{'error':str(error)});raise
    result.update(product_inventory=inventory,product_lint_executed=True,module_cache_validation=cache,
      candidate_manifest_sha256=hashlib.sha256(t.canonical(before)).hexdigest(),module_download_env=prep_env)

def runtime_deadline(prefix,evidence):
    ident=identity(os.environ);unit='ob2222-qual-'+ident['run_id']+'-1.service'
    raw=command(evidence,'unit-runtime-start',['/usr/bin/systemctl','show',unit,'--property=ExecMainStartTimestampMonotonic','--value'],dict(os.environ),prefix,15)
    need(re.fullmatch(rb'[1-9][0-9]*\n',raw)is not None,'actual unit monotonic start missing')
    started=int(raw)/1000000;need(0<started<=time.monotonic(),'unit start timestamp contradiction');return started+1080

def fixture_worker(prefix,info):
    work=prefix/'work';evidence=work/'evidence';evidence.mkdir()
    result={'identity':child_identity(info['uid'],info['cgroup']),'env':dict(os.environ),'scope':'transport-fixture-only',
      'product_lint_executed':False,'Go_executed':False,'candidate_fetched':False,'tool_downloads':False,'native_FIX_admission_verified':False}
    need(set(os.environ)<=set(ENV_FIXED)|{'PATH','HOME','TMPDIR','GOCACHE','GOPATH','GOMODCACHE','PYTHONUTF8'}and all(os.environ.get(k)==v for k,v in ENV_FIXED.items()),'fixture child fixed environment')
    script=prefix/'input/test_transport_cli.py';need(digest(script)==FIXTURE_SHA,'frozen public transport fixture changed')
    command(evidence,'transport-fixture',['/usr/bin/python3','-B','-X','utf8',str(script)],dict(os.environ),work,180)
    raw=(evidence/'transport-fixture.stderr').read_bytes()
    need(re.search(rb'Ran 20 tests in [0-9.]+s\n\nOK\n\Z',raw)is not None and b'skipped'not in raw,'all20 Linux public CLI tests must pass without skips')
    result.update(tests=20,skipped=0,identity_after=child_identity(info['uid'],info['cgroup']))
    write_json(work/'worker-result.json',result)

def verify_product_evidence(evidence,r,expected_admission):
    t=transport_module();binding=json.loads((evidence/'candidate-binding.json').read_bytes());admission=binding['admission']
    need(t.digest(t.canonical(admission))==expected_admission==binding['admission_sha256']==r['admission_sha256'],'external candidate admission binding')
    need(admission==r['candidate']and admission['format']=='onebase-candidate-admission-v1'and set(admission)=={'format','context','tree','manifest_sha256','packet_sha256'},'candidate admission schema')
    ctx=t.context(admission['context']);need(ctx['source']==SOURCE_SHA and ctx['owner_contract_sha256']==OWNER_CONTRACT_SHA and ctx['proof_epoch']==PROOF_EPOCH and ctx['operator_sha']==r['identity']['sha']and ctx['purpose']=='admitted-candidate','candidate source/proof/operator binding')
    need(binding['display_title']=='onebase-2222:'+ctx['task_id']+':'+ctx['nonce']and binding['dispatch_inputs_ascii_bytes']<=60000 and binding['native_FIX_admission_verified']is False and binding['keeper_lifecycle_qualified']is False,'candidate mechanical scope')
    worker=evidence/'worker';before_raw=(worker/'candidate-before-manifest.json').read_bytes();after_raw=(worker/'candidate-after-manifest.json').read_bytes()
    before=t.validate_manifest(t.load(before_raw));after=t.validate_manifest(t.load(after_raw))
    need(before_raw==after_raw==t.canonical(before)and before==after and t.digest(before_raw)==admission['manifest_sha256']==r['worker']['candidate_manifest_sha256']and before['tree']==admission['tree'],'complete candidate before/after modes/blobs/tree')
    reconstructed=(evidence/'candidate-reconstructed-manifest.json').read_bytes();need(reconstructed==before_raw,'reconstructed vs measured manifest')
    records={x['path']:x for x in before['entries']}
    for name in ('go.mod','go.sum'):
        raw=(worker/('candidate-'+name)).read_bytes();pin=records[name]
        need(len(raw)==pin['bytes']and t.digest(raw)==pin['sha256']and hashlib.sha1(('blob '+str(len(raw))+'\0').encode()+raw).hexdigest()==pin['blob'],'candidate module raw bytes binding')
    cache=module_checks((worker/'module-download.stdout').read_bytes(),(worker/'module-cache-verify.stdout').read_bytes(),(worker/'module-graph.stdout').read_bytes(),(worker/'candidate-go.sum').read_bytes())
    cache['go_mod_sha256']=digest(worker/'candidate-go.mod')
    need(cache==r['worker']['module_cache_validation']==json.loads((worker/'module-cache-validation.json').read_bytes()),'raw complete module/go.sum/cache validation')
    prep=json.loads((worker/'module-download-env.json').read_bytes());need(prep==r['worker']['module_download_env']=={**r['worker']['env'],**MODULE_PREP}and 'GOFLAGS'not in prep,'fixed credential-free dependency prep environment')
    inventory=(worker/'product-inventory.stdout').read_text().splitlines();need(inventory==r['worker']['product_inventory']and inventory and len(inventory)==len(set(inventory)),'raw complete product inventory')
    need(digest(evidence/'transport.py')==TRANSPORT_SHA==r['source_before']['transport']and digest(evidence/'fixture-tests.py')==FIXTURE_SHA==r['source_before']['fixture'],'frozen composition dependencies')
    mandatory={'candidate-binding.json','candidate-reconstructed-manifest.json','candidate-reconstruct-commands.json','public-fetch-commands.json','source.commit','main.commit','base.commit','transport.py','fixture-tests.py',
      'worker/candidate-before-manifest.json','worker/candidate-after-manifest.json','worker/candidate-go.mod','worker/candidate-go.sum','worker/module-cache-validation.json','worker/module-download-env.json','worker/product-analysis-start.json',
      *('unit-runtime-start.'+x for x in ('stdout','stderr','command.json'))}
    mandatory|={name+'-public-fetch.'+suffix for name in ('source','main')for suffix in ('stdout','stderr','command.json')}
    need(mandatory<=set(r['files']),'missing product input/resource/cache evidence')
    for name,url in PUBLIC_PARENTS.items():
        rec=json.loads((evidence/(name+'-public-fetch.command.json')).read_bytes());argv=rec['argv']
        need(rec['exit']==0 and argv[-5:]==['fetch','--no-tags','--no-write-fetch-head',url,ctx[name]]and '-c'in argv and 'protocol.https.allow=always'in argv,'fixed public parent fetch')
    for name in ('source','main','base'):
        raw=(evidence/(name+'.commit')).read_bytes();need(hashlib.sha1(('commit '+str(len(raw))+'\0').encode()+raw).hexdigest()==ctx[name],'raw public commit identity')
    analysis=json.loads((worker/'product-analysis-start.json').read_bytes());prefix=Path(PARENT.as_posix()+'/'+r['identity']['run_id']+'-1-'+r['identity']['sha'][:12])
    need(analysis['argv']==fixed_command_plan(prefix)['product-full-lint']and analysis['monotonic']+690<=analysis['deadline_monotonic'],'full unchanged analysis budget at actual start')

def verify_fixture_receipt(path,r,expected_sha,expected_run,expected_helper):
    need(r.get('schema')=='onebase-2222-mechanical-runtime-v1'and r.get('mode')=='transport-fixture'and r.get('status')=='FIXTURE_PASSED'and r.get('errors')==[],'not an actual passed fixture receipt')
    need(r.get('product_lint_executed')is False and r.get('Go_executed')is False and r.get('tool_downloads')is False and r.get('candidate_fetched')is False and r.get('native_FIX_admission_verified')is False and r.get('keeper_lifecycle_qualified')is False,'fixture cannot claim product/Native authority')
    ident=r['identity'];need(identity({'GITHUB_'+k.upper():v for k,v in ident.items()})==ident and ident['sha']==expected_sha and ident['run_id']==expected_run,'fixture actual external identity')
    evidence=Path(path).parent;need(isinstance(r['files'],dict)and len(r['files'])<=128,'fixture file bound');total=0
    for name,pin in r['files'].items():
        rel=Path(name);actual=evidence/rel;need(not rel.is_absolute()and '..'not in rel.parts and not actual.is_symlink()and actual.resolve().is_relative_to(evidence.resolve()),'fixture artifact path')
        total+=actual.stat().st_size;need(total<=512*1024**2 and actual.stat().st_size<=256*1024**2 and digest(actual)==pin,'fixture artifact bound/hash')
    mandatory={'request-event.raw.json','request-event-shape.json','workflow-wire-map.json','workflow-inputs.canonical.json','workflow-inputs-binding.json','DMI.bin','meminfo-before.txt','canonical-config.yml','helper.py','transport.py','fixture-tests.py','supervisor-admission.json','supervisor-prefork.json','privilege-transition.jsonl',
      'fixture-binding.json','worker/transport-fixture.command.json','worker/transport-fixture.stdout','worker/transport-fixture.stderr'}
    need(mandatory<=set(r['files']),'missing fixture raw proof')
    need(r['source_before']==r['source_after']and set(r['source_before'])=={'config','helper','transport','fixture'},'fixture unexpectedly has product tools/source drift')
    need(r['source_before']=={'config':CONFIG_SHA,'helper':expected_helper,'transport':TRANSPORT_SHA,'fixture':FIXTURE_SHA},'fixture composition pins')
    for name,key in (('helper.py','helper'),('transport.py','transport'),('fixture-tests.py','fixture'),('canonical-config.yml','config')):need(digest(evidence/name)==r['source_before'][key],'fixture preserved code/config')
    require_supervisor(r['supervisor']);require_supervisor(r['supervisor_prefork'])
    need(json.loads((evidence/'supervisor-admission.json').read_bytes())==r['supervisor']and json.loads((evidence/'supervisor-prefork.json').read_bytes())==r['supervisor_prefork'],'fixture raw supervisor states')
    h=r['host'];physical,count=assigned_memory((evidence/'DMI.bin').read_bytes());mem=parse_meminfo((evidence/'meminfo-before.txt').read_text())
    need(h=={'sysname':'Linux','machine':'x86_64','assigned_bytes':physical,'physical_source':'SMBIOS-type17','populated_devices':count,**mem}and physical>=HOST_CLASS and mem['MemAvailable']>=LIMIT,'fixture actual host capacity')
    a,b=r['resources_before'],r['resources_after'];cg='/system.slice/ob2222-qual-'+expected_run+'-1.service'
    need(a['cgroup']==b['cgroup']==cg and a['pid']==b['pid']and type(b['pid'])is int and b['pid']>1 and b['peak']<=LIMIT,'fixture own aggregate resource/PID/peak binding')
    for snap in (a,b):
        raw=snap['raw'];events={k:int(v)for k,v in (x.split()for x in raw['memory.events'].splitlines())}
        need(raw['cpu.max']=='200000 100000'and raw['cpu.max.burst']=='0'and cpu_set(raw['cpuset.cpus.effective'])==[0,1]and raw['memory.max']==str(LIMIT)and raw['memory.swap.max']=='0','fixture original2CPU9GiB controls')
        need(snap['events']==events and snap['peak']==int(raw['memory.peak'])and {'max','oom','oom_kill'}<=set(events),'fixture raw events/peak')
        need(snap['ancestors']and all(x['value']=='max'or int(x['value'])>=LIMIT for x in snap['ancestors']),'fixture enclosing capacity')
    need(all(a['events'].get(k,0)==b['events'].get(k,0)for k in ('max','oom','oom_kill','oom_group_kill'))and [int(x)for x in b['raw']['cgroup.procs'].split()]==[b['pid']],'fixture limit events/remaining children')
    w=r['worker'];c=w['identity'];need(w['identity_after']==c and UID_FIRST<=c['uid']<=UID_LAST and c['uid']==c['gid']and c['groups']==[]and c['nnp']==1 and set(c['caps'])=={'CapInh','CapPrm','CapEff','CapBnd','CapAmb'}and all(int(x,16)==0 for x in c['caps'].values()),'fixture restricted UID/caps')
    need(c['cgroup']==cg and c['ppid']==b['pid']and c['write_denied']==['cgroup.procs','memory.max','cpu.max'],'fixture same cgroup/no escape')
    need(set(c['host_ipc'])=={'/var/run/docker.sock','/run/docker.sock','/run/containerd/containerd.sock'}and all(x['state']in {'absent','inaccessible','present'}and x['readable']is False and x['writable']is False for x in c['host_ipc'].values()),'fixture no privileged host sockets')
    need(all(w['env'].get(k)==v for k,v in ENV_FIXED.items())and set(w['env'])<=set(ENV_FIXED)|{'PATH','HOME','TMPDIR','GOCACHE','GOPATH','GOMODCACHE','PYTHONUTF8'},'fixture credential-free environment')
    need(w['scope']=='transport-fixture-only'and w['tests']==20 and w['skipped']==0 and all(w[k]is False for k in ('product_lint_executed','Go_executed','candidate_fetched','tool_downloads','native_FIX_admission_verified')),'fixture only measured20 tests')
    raw=(evidence/'worker/transport-fixture.stderr').read_bytes();need(re.search(rb'Ran 20 tests in [0-9.]+s\n\nOK\n\Z',raw)is not None and b'skipped'not in raw,'fixture all20 actual noSkip raw output')
    rec=json.loads((evidence/'worker/transport-fixture.command.json').read_bytes());prefix=PARENT.as_posix()+'/'+expected_run+'-1-'+expected_sha[:12]
    need(rec['argv']==['/usr/bin/python3','-B','-X','utf8',prefix+'/input/test_transport_cli.py']and rec['exit']==0 and 0<=rec['seconds']<=180,'fixture actual public command/terminal')
    binding=json.loads((evidence/'fixture-binding.json').read_bytes());need(verify_wire_evidence(evidence,ident)==binding,'fixture authoritative fixed wire/mode/task/nonce/operator/data binding')
    need('downloads'not in r and not any(x.startswith('binaries/')for x in r['files']),'fixture unexpectedly prepared product tools')
    events=[json.loads(x)for x in (evidence/'privilege-transition.jsonl').read_text().splitlines()]
    stages=['no-new-privileges','clear-ambient','drop-bounding','setgroups','setgid','setuid','zero-capabilities'];expected=[('child-start','ok')]+[(stage,status)for stage in stages for status in ('before','after')]+[('exec-worker','before')]
    need([(e['stage'],e['status'])for e in events]==expected,'fixture privilege trace stages')
    for e in events:
        state=e['state'];need(state['pid']==c['pid']and state['ppid']==c['ppid']and state['nnp']==1 and set(state['caps'])==set(c['caps'])and all(int(v,16)&~451==0 for v in state['caps'].values()),'fixture trace process/ceiling')
    after_stage={e['stage']:e['state']for e in events if e['status']=='after'}
    need(int(after_stage['clear-ambient']['caps']['CapAmb'],16)==0 and int(after_stage['drop-bounding']['caps']['CapBnd'],16)==0,'fixture ambient/bounding transition')
    need(after_stage['setgroups']['groups']==[]and after_stage['setgid']['gid']==after_stage['setgid']['egid']==c['gid']and after_stage['setuid']['uid']==after_stage['setuid']['euid']==c['uid'],'fixture groups/GID/UID transition')
    end=events[-1]['state'];need(end['uid']==end['euid']==c['uid']and end['gid']==end['egid']==c['gid']and end['groups']==[]and all(int(v,16)==0 for v in end['caps'].values()),'fixture final restricted identity')
    return {'verified':True,'scope':'transport-fixture-only','product_lint_executed':False,'native_FIX_admission_verified':False,'sha':expected_sha,'run_id':expected_run}


def worker(prefix):
    info=json.loads((prefix/'input/worker.json').read_text())
    if info['runtime_mode']=='transport-fixture':fixture_worker(prefix,info);return
    need(info['runtime_mode']=='product-full-lint','explicit product worker mode')
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
    product_analysis(prefix,info,result,evidence)
    need(all(digest(module/name)==h for name,h in source.items()),'synthetic source drift')
    result['synthetic_source']=source
    result['identity_after']=child_identity(info['uid'],info['cgroup'])
    write_json(work/'worker-result.json',result)

def inside(prefix, ident):
    evidence=prefix/'evidence'; evidence.mkdir()
    report={'schema':'onebase-2222-mechanical-runtime-v1','mode':'unknown','status':'FAILED',
            'identity':ident,'product_lint_executed':False,'keeper_lifecycle_qualified':False,'native_FIX_admission_verified':False,'errors':[]}
    start=time.monotonic(); before=None
    try:
        need(os.geteuid()==0 and platform.system()=='Linux'and platform.machine()=='x86_64','native Linux amd64 root setup')
        need(not any(re.search('TOKEN|SECRET|PASSWORD|CREDENTIAL',k)for k in os.environ),'service credential environment')
        report['supervisor']=supervisor_snapshot()
        write_json(evidence/'supervisor-admission.json',report['supervisor'])
        before=resource_snapshot(); report['resources_before']=before
        require_supervisor(report['supervisor'])  # Before host qualification/downloads.
        mem_raw=Path('/proc/meminfo').read_text(); (evidence/'meminfo-before.txt').write_text(mem_raw)
        mem=parse_meminfo(mem_raw)
        dmi=Path('/sys/firmware/dmi/tables/DMI').read_bytes(); need(len(dmi)<=2*1024*1024,'DMI bound')
        (evidence/'DMI.bin').write_bytes(dmi)
        physical,count=assigned_memory(dmi)
        report['host']={'sysname':platform.system(),'machine':platform.machine(),'assigned_bytes':physical,
                        'physical_source':'SMBIOS-type17','populated_devices':count,**mem}
        need(physical>=HOST_CLASS,'assigned physical host below16GiB')
        need(mem['MemAvailable']>=LIMIT,'less than9GiB host available at admission')
        request=read_request(prefix,ident);report['mode']=request['runtime_mode'];deadline=None;binaries={}
        if request['runtime_mode']=='transport-fixture':
            report.update(Go_executed=False,tool_downloads=False,candidate_fetched=False)
            write_json(evidence/'fixture-binding.json',request)
        else:
            request=prepare_candidate(prefix,ident,evidence)
            report['candidate']=request['admission'];report['admission_sha256']=request['inputs']['admission_sha256']
            deadline=runtime_deadline(prefix,evidence)
        config=prefix/'input/canonical-config.yml'
        shutil.copyfile(config,evidence/'canonical-config.yml')
        shutil.copyfile(Path(__file__),evidence/'helper.py')
        shutil.copyfile(Path(__file__).with_name('candidate_transport.py'),evidence/'transport.py')
        shutil.copyfile(Path(__file__).with_name('test_transport_cli.py'),evidence/'fixture-tests.py')
        need(digest(config)==CONFIG_SHA,'canonical config before')
        if request['runtime_mode']=='product-full-lint':
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
        report['source_before']={'config':digest(config),'helper':digest(Path(__file__)),'transport':digest(Path(__file__).with_name('candidate_transport.py')),'fixture':digest(Path(__file__).with_name('test_transport_cli.py')),**{k:digest(p)for k,p in binaries.items()}}
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
        write_json(prefix/'input/worker.json',{'uid':uid,'cgroup':before['cgroup'],'runtime_mode':request['runtime_mode'],'runtime_deadline_monotonic':deadline})
        env={**ENV_FIXED,'PATH':(str(binaries['go'].parent)+':/usr/bin:/bin'if binaries else'/usr/bin:/bin'),'PYTHONUTF8':'1',
             **{key:str(work/value)for key,value in {'HOME':'home','TMPDIR':'tmp','GOCACHE':'gocache','GOPATH':'gopath','GOMODCACHE':'gomodcache'}.items()}}
        report['supervisor_prefork']=supervisor_snapshot()
        write_json(evidence/'supervisor-prefork.json',report['supervisor_prefork'])
        require_supervisor(report['supervisor_prefork'])
        trace_fd=os.open(evidence/'privilege-transition.jsonl',os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_APPEND,0o644)
        os.set_inheritable(trace_fd,False)
        child=os.fork()
        if child==0:
            try:
                clear_and_drop(uid,trace_fd)
                trace_privilege(trace_fd,'exec-worker','before')
                os.execve('/usr/bin/python3',['/usr/bin/python3','-B','-X','utf8',str(Path(__file__)),'worker',str(prefix)],env)
            except BaseException as error:
                try:
                    print('restricted child failed before worker: '+str(error),file=sys.stderr,flush=True)
                    traceback.print_exception(type(error),error,error.__traceback__)
                    try:trace_privilege(trace_fd,'child-transition','error',error)
                    except BaseException as audit_error:
                        print('additional child-transition audit failure: '+str(audit_error),file=sys.stderr,flush=True)
                finally:os._exit(1)  # Never fall through into the supervisor, even if diagnostics fail.
        os.close(trace_fd)
        _,status=os.waitpid(child,0)
        need(os.waitstatus_to_exitcode(status)==0,'restricted child nonzero')
        report['worker']=json.loads((work/'worker-result.json').read_text())
        report['product_lint_executed']=report['worker']['product_lint_executed']
        report['source_after']={'config':digest(config),'helper':digest(Path(__file__)),'transport':digest(Path(__file__).with_name('candidate_transport.py')),'fixture':digest(Path(__file__).with_name('test_transport_cli.py')),**{k:digest(p)for k,p in binaries.items()}}
        need(report['source_before']==report['source_after'],'config/helper/tool drift')
        report['status']='FIXTURE_PASSED'if report['mode']=='transport-fixture'else'PASSED'
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
        report['product_lint_executed']=(prefix/'work/evidence/product-analysis-start.json').exists()
        report['seconds']=time.monotonic()-start
        if report['errors']:report['status']='FAILED'
        if (prefix/'work/evidence').exists():shutil.copytree(prefix/'work/evidence',evidence/'worker')
        report['files']={str(p.relative_to(evidence)).replace(os.sep,'/'):digest(p)for p in evidence.rglob('*')if p.is_file()}
        if report['status']in {'PASSED','FIXTURE_PASSED'}:
            try:verify(evidence/'receipt.json',ident['sha'],ident['run_id'],report['source_before']['helper'],data=report,expected_admission=report.get('admission_sha256'))
            except BaseException as error:report['status']='FAILED';report['errors'].append('verifier: '+str(error))
        write_json(evidence/'receipt.json',report)
    return 0 if report['status']in {'PASSED','FIXTURE_PASSED'} else 1

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

def cleanup_terminal(snapshot, unit, description, invocation):
    """A failed service may remain failed after a successful stop. It is not PASS."""
    v=snapshot['fields']
    if v.get('LoadState')=='not-found':return True  # Only after an exact own stop returned0.
    if not workload_terminal(snapshot,unit,description):return False
    need(v.get('InvocationID')==invocation,'post-stop unit incarnation changed')
    need(v.get('ControlGroup')in ('','/system.slice/'+unit),'post-stop cgroup identity')
    return (v.get('ActiveState'),v.get('SubState'))in {('inactive','dead'),('failed','failed')}

def unit_exit_success(before, after):
    """Terminal diagnostics are collectable even if a late service failure wins."""
    b=before['fields'];a=after['fields']
    before_ok=b.get('ActiveState')=='active'and b.get('SubState')=='exited'and b.get('ExecMainCode')=='1'and b.get('ExecMainStatus')=='0'and b.get('Result')=='success'
    if not before_ok:return False
    # A collected unit is accepted only by the caller's preceding exact own stop
    # and empty-cgroup gate. A retained unit must still have a successful result.
    if a.get('LoadState')=='not-found':
        return (a.get('MainPID')in (None,'','0')and a.get('ActiveState')in (None,'','inactive')
                and a.get('SubState')in (None,'','dead')and a.get('Result')in (None,'','success')
                and a.get('ExecMainStatus')in (None,'','0')and a.get('ExecMainCode')in (None,'','0','1'))
    return (a.get('ActiveState')=='inactive'and a.get('SubState')=='dead'
            and a.get('ExecMainCode')=='1'and a.get('ExecMainStatus')=='0'and a.get('Result')=='success')

def bootstrap():
    need(sys.platform=='linux'and os.geteuid()==0,'bootstrap only root on ephemeral Linux runner')
    ident=identity(os.environ)
    prefix=PARENT/(ident['run_id']+'-'+ident['run_attempt']+'-'+ident['sha'][:12])
    need(not PARENT.is_symlink()and not prefix.exists(),'owned namespace already present or symlink')
    PARENT.mkdir(mode=0o755,exist_ok=True)
    need(PARENT.stat().st_uid==0 and not(PARENT.stat().st_mode&0o022),'trusted prefix parent')
    prefix.mkdir(mode=0o755); (prefix/'input').mkdir()
    script=prefix/'input/full-lint-2222-linux.py';shutil.copyfile(__file__,script)
    shutil.copyfile(Path(__file__).with_name('candidate_transport.py'),prefix/'input/candidate_transport.py')
    shutil.copyfile(Path(__file__).with_name('test_transport_cli.py'),prefix/'input/test_transport_cli.py')
    shutil.copyfile(os.environ['GITHUB_EVENT_PATH'],prefix/'input/event.json')
    config=Path(__file__).resolve().parents[2]/'.golangci.yml';shutil.copyfile(config,prefix/'input/canonical-config.yml')
    props=['User=root','Group=root','NoNewPrivileges=yes','CapabilityBoundingSet=CAP_CHOWN CAP_DAC_OVERRIDE CAP_SETUID CAP_SETGID CAP_SETPCAP','AmbientCapabilities=CAP_SETUID',
      'ProtectControlGroups=yes','ProtectSystem=strict','ProtectHome=yes','ProtectKernelTunables=yes','ProtectKernelModules=yes',
      'ProtectKernelLogs=yes','PrivateDevices=yes','PrivateTmp=yes','RestrictNamespaces=yes','RestrictSUIDSGID=yes','LockPersonality=yes',
      'ProtectClock=yes','ProtectHostname=yes','SystemCallArchitectures=native','Delegate=no','UMask=0022',
      'CPUAccounting=yes','MemoryAccounting=yes','TasksAccounting=yes','TasksMax=256','AllowedCPUs=0,1',
      'CPUQuota=200%','CPUQuotaPeriodSec=100ms','MemoryMax='+str(LIMIT),'MemorySwapMax=0','RuntimeMaxSec=1080s',
      'TimeoutStopSec=15s','KillMode=control-group','RemainAfterExit=yes','ReadWritePaths='+str(prefix),
      'StandardOutput=append:'+str(prefix/'service.stdout'),'StandardError=append:'+str(prefix/'service.stderr')]
    unit='ob2222-qual-'+ident['run_id']+'-'+ident['run_attempt']+'.service'
    description='OneBase2222 mechanical sha='+ident['sha']+' run='+ident['run_id']+' attempt=1'
    argv=['/usr/bin/systemd-run','--no-ask-password','--expand-environment=no','--service-type=exec',
      '--unit='+unit,'--description='+description]
    argv+=['--property='+x for x in props]
    argv+=['--setenv=PATH=/usr/bin:/bin','--setenv=LANG=C.UTF-8','--setenv=PYTHONUTF8=1']
    argv+=['--setenv=GITHUB_'+k.upper()+'='+v for k,v in ident.items()]
    argv+=['--setenv='+name+'='+os.environ[name]for name in WIRE_ENV.values()]
    argv+=['/usr/bin/python3','-B','-X','utf8',str(script),'inside',str(prefix)]
    # Outside the unit this is only fixed host control-plane start/state/wait/stop/copy.
    snapshots=[]; terminal=False; successful=False; code=1; start_output=b''; start_error=b'';start_exit=None;stop_exit=None;post_stop_cgroup=None
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
                # Keep the successful exited unit loaded until its state is captured,
                # then stop only that exact empty, incarnation-bound owned service.
                stopped=subprocess.run(['/usr/bin/systemctl','stop',unit],capture_output=True,timeout=30)
                stop_exit=stopped.returncode
                need(stopped.returncode==0,'own unit stop outcome UNKNOWN')
                after=unit_snapshot(unit);snapshots.append(after);v2=after['fields']
                need(cleanup_terminal(after,unit,description,invocation),'own unit terminal cleanup UNKNOWN')
                procs=Path('/sys/fs/cgroup/system.slice')/unit/'cgroup.procs'
                exists=procs.exists();remaining=[int(x)for x in procs.read_text().split()]if exists else[]
                post_stop_cgroup={'path':str(procs),'exists':exists,'procs':remaining}
                need(not remaining,'own unit descendants remain after stop')
                successful=unit_exit_success(snap,after)
                terminal=True;code=0 if successful else 1;break
            time.sleep(5)
        need(terminal,'unit state deadline/UNKNOWN; no restart')
    except BaseException as error:
        start_error+=('\ncontroller: '+type(error).__name__+': '+str(error)).encode()
    (prefix/'systemd-run.stdout').write_bytes(start_output);(prefix/'systemd-run.stderr').write_bytes(start_error)
    journal_exit=None
    if terminal:
        # Scoped control-plane diagnostics also retain manager/setup failures that
        # occurred before the inside Python process could write a receipt.
        try:
            journal=subprocess.run(['/usr/bin/journalctl','--unit='+unit,'--no-pager','--output=short-iso'],capture_output=True,timeout=15)
            journal_exit=journal.returncode;jout,jerr=journal.stdout,journal.stderr
        except subprocess.TimeoutExpired as error:
            journal_exit=124;jout,jerr=error.stdout or b'',error.stderr or b''
        except OSError as error:
            journal_exit=127;jout,jerr=b'',str(error).encode()
        (prefix/'unit-journal.stdout').write_bytes(jout);(prefix/'unit-journal.stderr').write_bytes(jerr)
    target=Path(os.environ['RUNNER_TEMP'])/('onebase-2222-qualification-'+ident['run_id']+'-'+ident['run_attempt'])
    need(target.parent.resolve()==Path(os.environ['RUNNER_TEMP']).resolve()and not target.exists(),'artifact destination')
    target.mkdir()
    for name in ('systemd-run.stdout','systemd-run.stderr'):shutil.copyfile(prefix/name,target/name)
    if terminal:
        for name in ('service.stdout','service.stderr','unit-journal.stdout','unit-journal.stderr'):
            if (prefix/name).exists():shutil.copyfile(prefix/name,target/name)
        if (prefix/'evidence').exists():shutil.copytree(prefix/'evidence',target/'evidence')
    write_json(target/'controller.json',{'identity':ident,'controller_exit':code,'unit_snapshots':snapshots,
       'start_exit':start_exit,'stop_exit':stop_exit,'post_stop_cgroup':post_stop_cgroup,'journal_exit':journal_exit,
       'terminal_wait_observed':terminal,'runtime_prefix':str(prefix),'scope':'mechanical runtime; receipt mode distinguishes fixture/product; Native authority and keeper remain upstream',
       'unknown_or_failure_remains_parked':True})
    print(json.dumps({'artifact':str(target),'controller_exit':code,'terminal_wait_observed':terminal}))
    return 0 if code==0 else 1

def verify(path, expected_sha, expected_run, expected_helper, data=None,expected_admission=None):
    if data is None:
        raw=Path(path).read_bytes();need(len(raw)<=2*1024*1024,'receipt bound');r=json.loads(raw)
    else:r=data
    if r.get('mode')=='transport-fixture':
        need(expected_admission in (None,''),'fixture may not consume candidate admission')
        return verify_fixture_receipt(path,r,expected_sha,expected_run,expected_helper)
    need(re.fullmatch('[0-9a-f]{64}',expected_admission or '')is not None,'external reviewed admission SHA required for product evidence')
    need(r.get('schema')=='onebase-2222-mechanical-runtime-v1'and r.get('mode')=='product-full-lint','receipt schema/mode')
    need(r.get('status')=='PASSED'and r.get('errors')==[] and r.get('product_lint_executed')is True and r.get('native_FIX_admission_verified')is False and r.get('keeper_lifecycle_qualified')is False,'not qualified/synthetic-only')
    ident=r['identity'];need(identity({'GITHUB_'+k.upper():v for k,v in ident.items()})==ident,'receipt identity')
    need(ident['sha']==expected_sha and ident['run_id']==expected_run,'external source/run binding')
    h=r['host'];need(h['sysname']=='Linux'and h['machine']=='x86_64'and h['physical_source']=='SMBIOS-type17'and h['assigned_bytes']>=HOST_CLASS and h['MemAvailable']>=LIMIT,'actual native physical/free capacity')
    require_supervisor(r['supervisor']);require_supervisor(r['supervisor_prefork'])
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
    need(w.get('product_lint_executed')is True and w.get('product_inventory')and w.get('synthetic_inventory')==['example.invalid/qualification'],'actual complete product analysis')
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
      'worker/lint-version.stdout','worker/configured-linters.stdout','binaries/go','binaries/lint','binaries/go.LICENSE.txt','binaries/lint.LICENSE.txt','worker/product-lint-cpu.pprof',
      'supervisor-admission.json','supervisor-prefork.json','privilege-transition.jsonl'}
    expected_prefix=PARENT.as_posix()+'/'+ident['run_id']+'-1-'+ident['sha'][:12];go=expected_prefix+'/tools/go/go/bin/go';lint=expected_prefix+'/tools/lint/golangci-lint-2.11.4-linux-amd64/golangci-lint'
    commands={'go-version':[go,'version'],'go-env':[go,'env','-json','GOHOSTOS','GOHOSTARCH','GOOS','GOARCH','CGO_ENABLED','GOTOOLCHAIN'],
      'lint-version':[lint,'version','--short'],'configured-linters':[lint,'linters','--json','--config',expected_prefix+'/input/canonical-config.yml'],
      'synthetic-cgo-build':[go,'build','-o',expected_prefix+'/work/synthetic-cgo','.'],
      'synthetic-cgo-execute':[expected_prefix+'/work/synthetic-cgo'],
      'synthetic-inventory':[go,'list','./...'],
      **fixed_command_plan(Path(expected_prefix))}
    mandatory|={f'worker/{name}.{suffix}'for name in commands for suffix in ('command.json','stdout','stderr')}
    need(mandatory<=set(r['files']),'missing raw artifact evidence')
    evidence=Path(path).parent
    need(json.loads((evidence/'supervisor-admission.json').read_bytes())==r['supervisor']and json.loads((evidence/'supervisor-prefork.json').read_bytes())==r['supervisor_prefork'],'raw supervisor admission/prefork state')
    physical,count=assigned_memory((evidence/'DMI.bin').read_bytes())
    need(physical==h['assigned_bytes']and count==h['populated_devices']and parse_meminfo((evidence/'meminfo-before.txt').read_text())=={k:h[k]for k in ('MemTotal','MemAvailable')},'raw capacity derivation mismatch')
    need(digest(evidence/'canonical-config.yml')==CONFIG_SHA and digest(evidence/'helper.py')==expected_helper,'raw config/helper binding')
    need((evidence/'worker/go-version.stdout').read_text().strip()==w['go_version']and (evidence/'worker/lint-version.stdout').read_text().strip()==w['lint_version']and (evidence/'worker/synthetic-cgo-execute.stdout').read_text()==w['cgo_stdout'],'raw tool/CGO output mismatch')
    need(json.loads((evidence/'worker/go-env.stdout').read_bytes())==w['go_env'],'raw native environment mismatch')
    enabled=json.loads((evidence/'worker/configured-linters.stdout').read_bytes())['Enabled']
    need(len(enabled)==7 and {x.get('name',x.get('Name'))for x in enabled}==set(w['enabled_linters']),'raw configured linters mismatch')
    need((evidence/'worker/synthetic-inventory.stdout').read_text().splitlines()==w['synthetic_inventory']and (evidence/'worker/product-lint-cpu.pprof').stat().st_size>0,'raw synthetic inventory/profile')
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
    stages=['no-new-privileges','clear-ambient','drop-bounding','setgroups','setgid','setuid','zero-capabilities']
    events=[json.loads(line)for line in (evidence/'privilege-transition.jsonl').read_text().splitlines()]
    expected=[('child-start','ok')]+[(stage,status)for stage in stages for status in ('before','after')]+[('exec-worker','before')]
    need([(e['stage'],e['status'])for e in events]==expected,'privilege transition stage/error evidence')
    for event in events:
        state=event['state'];need(state['pid']==c['pid']and state['ppid']==c['ppid']and state['nnp']==1 and set(state['caps'])==set(c['caps'])and all(int(v,16)&~451==0 for v in state['caps'].values()),'transition process/capability ceiling')
    after_stage={e['stage']:e['state']for e in events if e['status']=='after'}
    need(int(after_stage['clear-ambient']['caps']['CapAmb'],16)==0 and int(after_stage['drop-bounding']['caps']['CapBnd'],16)==0,'ambient/bounding transition')
    need(after_stage['setgroups']['groups']==[]and after_stage['setgid']['gid']==after_stage['setgid']['egid']==c['gid']and after_stage['setuid']['uid']==after_stage['setuid']['euid']==c['uid'],'groups/GID/UID transition')
    end=events[-1]['state'];need(end['uid']==end['euid']==c['uid']and end['gid']==end['egid']==c['gid']and end['groups']==[]and all(int(v,16)==0 for v in end['caps'].values()),'pre-exec restricted identity/capability zero')
    verify_product_evidence(evidence,r,expected_admission)
    return {'verified':True,'scope':'mechanical full product lint only; Native authority and keeper remain upstream gates','sha':expected_sha,'run_id':expected_run,'native_FIX_admission_verified':False}

def inspect_terminal_controller(path, expected_sha, expected_run):
    raw=Path(path).read_bytes();need(len(raw)<=2*1024*1024,'controller bound')
    c=json.loads(raw);ident=c['identity']
    need(identity({'GITHUB_'+k.upper():v for k,v in ident.items()})==ident and ident['sha']==expected_sha and ident['run_id']==expected_run,'controller external source/run binding')
    need(type(c['controller_exit'])is int and c['controller_exit']in (0,1)and c['start_exit']==0 and c['stop_exit']==0 and c['terminal_wait_observed']is True,'controller terminal outcome UNKNOWN/nonzero start/stop')
    snapshots=c['unit_snapshots'];need(isinstance(snapshots,list)and len(snapshots)>=2,'terminal snapshots missing')
    unit='ob2222-qual-'+ident['run_id']+'-1.service'
    description='OneBase2222 mechanical sha='+ident['sha']+' run='+ident['run_id']+' attempt=1'
    for snap in snapshots:
        need(snap['argv'][:3]==['/usr/bin/systemctl','show',unit],'unit snapshot command identity')
        fields={line.split('=',1)[0]:line.split('=',1)[1]for line in snap['stdout'].splitlines()if '='in line}
        need(fields==snap['fields'],'unit raw field mismatch')
    before,after=snapshots[-2],snapshots[-1]
    need(workload_terminal(before,unit,description),'unit workload not terminal')
    need(cleanup_terminal(after,unit,description,before['fields']['InvocationID']),'unit cleanup not terminal')
    cg=c['post_stop_cgroup'];need(cg['path']=='/sys/fs/cgroup/system.slice/'+unit+'/cgroup.procs'and type(cg['exists'])is bool and cg['procs']==[],'remaining or unproved own cgroup processes')
    successful=unit_exit_success(before,after)
    need(c['controller_exit']==(0 if successful else 1),'failure changed to success or controller contradiction')
    return {'terminal':True,'unit_exit_success':successful,'controller_outcome':'success'if successful else'failure',
            'environment_qualification':'not_evaluated','controller':c}

def verify_controller(path, receipt):
    result=inspect_terminal_controller(path,receipt['identity']['sha'],receipt['identity']['run_id'])
    c=result['controller'];need(c['identity']==receipt['identity']and result['unit_exit_success']is True,'failed unit is not qualified')
    snapshots=c['unit_snapshots'];terminal=snapshots[-2]['fields']
    pid=str(receipt['resources_after']['pid']);group=receipt['resources_after']['cgroup']
    need(terminal['ExecMainPID']==pid and terminal['ControlGroup']in ('',group),'successful terminal process/cgroup binding')
    running=snapshots[:-2]
    for snapshot in running:
        v=snapshot['fields']
        need(snapshot['exit']==0 and v.get('LoadState')=='loaded'
          and v.get('Id')==terminal['Id']and v.get('Description')==terminal['Description']
          and v.get('InvocationID')==terminal['InvocationID'],'running snapshot identity/incarnation drift')
        need(v.get('ActiveState')=='active'and v.get('SubState')=='running'
          and v.get('MainPID')==v.get('ExecMainPID')==pid and v.get('ControlGroup')==group
          and v.get('ExecMainCode')=='0'and v.get('ExecMainStatus')=='0'and v.get('Result')=='success',
          'running process/cgroup sequence contradicts receipt')
    # systemd can prune an empty cgroup on SERVICE_EXITED while retaining the
    # active/exited unit and ExecMainPID. Then an earlier exact running snapshot
    # proves its cgroup membership; never substitute a foreign or missing one.
    need(terminal['ControlGroup']==group or bool(running),'empty terminal cgroup lacks prior running binding')
    return True

def main():
    parser=argparse.ArgumentParser(description=__doc__);sub=parser.add_subparsers(dest='mode',required=True)
    sub.add_parser('bootstrap')
    p=sub.add_parser('plan');p.add_argument('event',type=Path)
    p=sub.add_parser('wire-plan');p.add_argument('event',type=Path);p.add_argument('--out',type=Path,required=True);p.add_argument('--wire-env',type=Path)
    p=sub.add_parser('snapshot');p.add_argument('--root',type=Path,required=True);p.add_argument('--manifest',type=Path,required=True);p.add_argument('--expected-manifest-sha',required=True);p.add_argument('--out',type=Path,required=True)
    for name in ('inside','worker'):
        p=sub.add_parser(name);p.add_argument('prefix',type=Path)
    p=sub.add_parser('verify');p.add_argument('receipt',type=Path);p.add_argument('--controller',required=True,type=Path);p.add_argument('--expected-sha',required=True);p.add_argument('--expected-run-id',required=True);p.add_argument('--expected-helper-sha',required=True);p.add_argument('--expected-admission-sha')
    p=sub.add_parser('inspect-terminal');p.add_argument('controller',type=Path);p.add_argument('--expected-sha',required=True);p.add_argument('--expected-run-id',required=True)
    args=parser.parse_args()
    try:
        if args.mode=='wire-plan':
            ident=identity(os.environ);args.out.mkdir(mode=0o700)
            env=transport_module().load(args.wire_env.read_bytes())if args.wire_env else os.environ
            request=capture_workflow_request(args.event,args.out,ident,env)
            print(json.dumps({'scope':'WIRE_DATA_PLAN_ONLY','runtime_mode':request['runtime_mode'],'display_title':request['display_title'],'product_lint_executed':False,'native_FIX_admission_verified':False,'product_runtime_armed':PRODUCT_RUNTIME_ARMED}));return 0
        if args.mode=='plan':
            ident=identity(os.environ);request=validate_request(json.loads(args.event.read_bytes()),ident)
            prefix=PARENT/(ident['run_id']+'-1-'+ident['sha'][:12]);fixture=request['runtime_mode']=='transport-fixture'
            commands={'transport-fixture':['/usr/bin/python3','-B','-X','utf8',prefix.as_posix()+'/input/test_transport_cli.py']}if fixture else fixed_command_plan(prefix)
            print(json.dumps({'scope':'PLAN_ONLY','runtime_mode':request['runtime_mode'],'product_runtime_armed':PRODUCT_RUNTIME_ARMED,'product_lint_executed':False,'native_FIX_admission_verified':False,'display_title':request['display_title'],'candidate':request.get('admission'),'fixed_commands':commands,'module_prep_overrides':{}if fixture else MODULE_PREP,'analysis_env':ENV_FIXED,'dispatch_inputs_ascii_bytes':len(transport_module().canonical(request['inputs']))}));return 0
        if args.mode=='snapshot':
            t=transport_module();raw=args.manifest.read_bytes();need(hashlib.sha256(raw).hexdigest()==args.expected_manifest_sha,'external manifest SHA')
            value,physical=actual_manifest(args.root,t.load(raw))
            with args.out.open('xb')as output:output.write(t.canonical(value))
            print(json.dumps({'scope':'DATA_SNAPSHOT_ONLY','filesystem_mode_verified':physical,'product_lint_executed':None}));return 0
        if args.mode=='verify':
            value=verify(args.receipt,args.expected_sha,args.expected_run_id,args.expected_helper_sha,expected_admission=args.expected_admission_sha)
            verify_controller(args.controller,json.loads(args.receipt.read_bytes()))
            print(json.dumps(value));return 0
        if args.mode=='inspect-terminal':
            value=inspect_terminal_controller(args.controller,args.expected_sha,args.expected_run_id)
            value.pop('controller');print(json.dumps(value));return 0
        if args.mode=='bootstrap':return bootstrap()
        need(sys.platform=='linux','Linux runtime subcommand only')
        need(args.prefix.parent==PARENT and re.fullmatch(r'[1-9][0-9]{0,19}-1-[0-9a-f]{12}',args.prefix.name)and args.prefix.resolve()==args.prefix,'owned runtime prefix')
        if args.mode=='worker':worker(args.prefix);return 0
        return inside(args.prefix,identity(os.environ))
    except BaseException as error:
        print(json.dumps({'verified':False,'error':type(error).__name__+': '+str(error),'product_lint_executed':None}),file=sys.stderr)
        return 1

if __name__=='__main__':sys.exit(main())
