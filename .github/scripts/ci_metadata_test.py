"""Request routing and conservative CI proof validation on explicit fixtures."""
import copy
from datetime import datetime, timezone
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
import zipfile

path = Path(__file__).with_name('ci-metadata.py')
spec = importlib.util.spec_from_file_location('ci_metadata', path)
ci = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ci)
NOW = datetime(2026, 11, 3, tzinfo=timezone.utc)


class API:
    def __init__(self):
        self.writes = []
        self.pr = dict(number=42, state='open', draft=False, title='Feature', body='Fixes #1', updated_at='now',
                       merge_commit_sha='c'*40,
                       head=dict(sha='a'*40, ref='feature/example', repo=dict(full_name='owner/repo')),
                       base=dict(sha='b'*40, ref='main'))
        self.run = dict(id=100, run_attempt=1, status='completed', conclusion='success', head_sha='a'*40,
                        event='pull_request', path='.github/workflows/ci.yml', updated_at='2026-11-03T00:00:00Z',
                        html_url='https://example.test/100', pull_requests=[dict(number=42)])
        self.proof = dict(version=1, run_id=100, run_attempt=1, repository='owner/repo', head='a'*40,
                          base='b'*40, checkout='c'*40, workflow_blob='workflow', mode='full',
                          environment_epoch='', event='pull_request',
                          body_sha256=hashlib.sha256(b'Fixes #1').hexdigest())
        self.jobs = [dict(name=n, status='completed', conclusion='success') for n in ci.REQUIRED]
        self.changed_blob = False
        self.bad_archive = False

    def __call__(self, endpoint, body=None, raw=False):
        if body is not None:
            self.writes.append((endpoint, body));return {}
        if '/pulls/' in endpoint: return copy.deepcopy(self.pr)
        if '/contents/' in endpoint:
            return dict(sha='modified' if self.changed_blob and endpoint.endswith('a'*40) else 'workflow')
        if '/git/ref/heads/' in endpoint: return dict(object=dict(sha=self.pr['head']['sha']))
        if '/workflows/ci.yml/runs?' in endpoint: return dict(total_count=1, workflow_runs=[self.run])
        if f'/attempts/{self.run["run_attempt"]}/jobs?' in endpoint: return dict(total_count=len(self.jobs), jobs=self.jobs)
        if f'/runs/{self.run["id"]}/artifacts?' in endpoint:
            return dict(total_count=1, artifacts=[dict(id=200, name=f'ci-inputs-{self.run["id"]}-{self.run["run_attempt"]}', expired=False, size_in_bytes=1000)])
        if '/artifacts/200/zip' in endpoint:
            if self.bad_archive: return b'broken'
            stream = io.BytesIO()
            with zipfile.ZipFile(stream, 'w') as z: z.writestr('ci-inputs.json', json.dumps(self.proof))
            return stream.getvalue()
        raise AssertionError(endpoint)


class MetadataTests(unittest.TestCase):
    def handle(self, api): return ci.handle(api, 'owner/repo', 42, '', now=NOW)

    def test_proven_success_does_not_write_or_synthesize_checks(self):
        api = API();result = self.handle(api)
        self.assertEqual(result['action'], 'reuse');self.assertEqual(api.writes, [])

    def test_uncertainty_dispatches_actual_full_ci(self):
        for field, value in [('head', 'd'*40), ('base', 'd'*40), ('checkout', 'd'*40),
                             ('workflow_blob', 'bad'), ('mode', 'docs'), ('run_attempt', 2),
                             ('environment_epoch', 'new image/tool epoch'), ('body_sha256', 'changed')]:
            with self.subTest(field=field):
                api = API();api.proof[field] = value
                self.assertEqual(self.handle(api)['action'], 'full-ci-dispatched')
                self.assertEqual(api.writes, [('repos/owner/repo/actions/workflows/ci.yml/dispatches', {'ref': 'feature/example'})])

    def test_every_required_job_must_really_succeed(self):
        for conclusion in ['failure', 'skipped', 'cancelled', 'timed_out', None]:
            api = API();api.jobs[0]['conclusion'] = conclusion
            self.assertEqual(self.handle(api)['action'], 'full-ci-dispatched')
        api = API();api.jobs.pop()
        self.assertEqual(self.handle(api)['action'], 'full-ci-dispatched')

    def test_running_exact_full_ci_is_joined_without_claiming_success(self):
        api = API();api.run.update(status='in_progress', conclusion=None)
        self.assertEqual(self.handle(api)['action'], 'full-ci-running')
        self.assertEqual(api.writes, [])

    def test_newer_failure_cannot_be_hidden_by_old_green(self):
        api = API();old = copy.deepcopy(api.run);new = dict(old, id=101, conclusion='failure')
        def ordered(endpoint, *args, **kwargs):
            if '/workflows/ci.yml/runs?' in endpoint: return dict(total_count=2, workflow_runs=[old, new])
            return api(endpoint, *args, **kwargs)
        self.assertEqual(self.handle(ordered)['action'], 'full-ci-dispatched')

    def test_late_failed_rerun_of_older_run_supersedes_green(self):
        api = API();api.run.update(id=101,run_number=101,updated_at='2026-11-02T23:00:00Z');api.proof['run_id']=101
        failed = dict(api.run,id=100,run_number=100,run_attempt=2,conclusion='failure',updated_at='2026-11-03T00:00:00Z')
        def ordered(endpoint,*args,**kwargs):
            if '/workflows/ci.yml/runs?' in endpoint:return dict(total_count=2,workflow_runs=[api.run,failed])
            return api(endpoint,*args,**kwargs)
        self.assertEqual(self.handle(ordered)['action'],'full-ci-dispatched')

    def test_missing_emitter_falls_back_but_auth_failure_does_not_write(self):
        for code in [404,401,403,429]:
            with self.subTest(code=code):
                api=API()
                def missing(endpoint,*args,**kwargs):
                    if '/contents/.github/scripts/ci-inputs.py?ref='+('a'*40) in endpoint:
                        raise subprocess.CalledProcessError(1,['gh','api',endpoint],stderr=f'gh: error (HTTP {code})'.encode())
                    return api(endpoint,*args,**kwargs)
                if code==404:self.assertEqual(self.handle(missing)['action'],'full-ci-dispatched')
                else:
                    with self.assertRaises(subprocess.CalledProcessError):self.handle(missing)
                    self.assertEqual(api.writes,[])

    def test_truncated_run_history_cannot_reuse_a_visible_green(self):
        api=API()
        def truncated(endpoint,*args,**kwargs):
            if '/workflows/ci.yml/runs?' in endpoint:return dict(total_count=101,workflow_runs=[api.run])
            return api(endpoint,*args,**kwargs)
        self.assertEqual(self.handle(truncated)['action'],'full-ci-dispatched')

    def test_modified_emitter_old_or_corrupt_proof_never_reuses(self):
        for field in ['changed_blob', 'bad_archive', 'old']:
            api = API()
            if field == 'old': api.run['updated_at'] = '2026-10-01T00:00:00Z'
            else: setattr(api, field, True)
            self.assertEqual(self.handle(api)['action'], 'full-ci-dispatched')

    def test_fork_reruns_real_pr_context_without_privileged_code_dispatch(self):
        api = API();api.pr['head']['repo']['full_name'] = 'contributor/repo';api.proof['mode'] = 'docs'
        self.assertEqual(self.handle(api)['action'], 'full-ci-rerun')
        self.assertEqual(api.writes, [('repos/owner/repo/actions/runs/100/rerun', {})])

    def test_pr_change_during_inspection_stops_all_writes(self):
        api = API();reads = 0
        def changing(endpoint, *args, **kwargs):
            nonlocal reads
            if '/pulls/' in endpoint:
                reads += 1
                if reads == 2: api.pr['head']['sha'] = 'e'*40
            return api(endpoint, *args, **kwargs)
        with self.assertRaises(ValueError): self.handle(changing)
        self.assertEqual(api.writes, [])


GH_STUB = r'''
package main
import("encoding/json";"fmt";"io";"os")
type response struct { Body json.RawMessage `json:"body"`; Bytes []byte `json:"bytes"`; Error int `json:"error"` }
func main(){
 data,err:=os.ReadFile(os.Getenv("METADATA_CLI_FIXTURE"));if err!=nil{panic(err)}
 var responses map[string]response;if json.Unmarshal(data,&responses)!=nil{panic("fixture JSON")}
 if len(os.Args)<3||os.Args[1]!="api"{panic("unexpected executable request")}
 endpoint:=os.Args[2];method:="GET";for i,a:=range os.Args{if a=="--method"&&i+1<len(os.Args){method=os.Args[i+1]}}
 body,_:=io.ReadAll(os.Stdin);call:=map[string]any{"endpoint":endpoint,"method":method,"body":string(body)}
 log,err:=os.OpenFile(os.Getenv("METADATA_CLI_CALLS"),os.O_WRONLY|os.O_CREATE|os.O_APPEND,0600);if err!=nil{panic(err)}
 if json.NewEncoder(log).Encode(call)!=nil{panic("call log")};log.Close()
 if method=="POST"{
  if endpoint!="repos/owner/repo/actions/workflows/ci.yml/dispatches"&&endpoint!="repos/owner/repo/actions/runs/100/rerun"{panic("unexpected write")}
  fmt.Print("{}");return
 }
 r,ok:=responses[endpoint];if !ok{fmt.Fprintln(os.Stderr,"unexpected offline endpoint:",endpoint);os.Exit(93)}
 if r.Error!=0{fmt.Fprintf(os.Stderr,"gh: fixture error (HTTP %d)\n",r.Error);fmt.Printf("{\"status\":\"%d\"}",r.Error);os.Exit(1)}
 if r.Bytes!=nil{os.Stdout.Write(r.Bytes)}else{os.Stdout.Write(r.Body)}
}
'''


class PublicCLI(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temporary=tempfile.TemporaryDirectory(prefix='ci-metadata-public-cli-')
        cls.root=Path(cls.temporary.name);cls.binary=cls.root/('gh.exe' if os.name=='nt' else 'gh')
        stub=cls.root/'main.go';stub.write_text(GH_STUB,encoding='utf-8')
        environment=dict(os.environ,GOTOOLCHAIN='local',GOWORK='off',GOPROXY='off',GOSUMDB='off')
        subprocess.run(['go','build','-o',str(cls.binary),str(stub)],cwd=cls.root,env=environment,check=True,capture_output=True)

    @classmethod
    def tearDownClass(cls):cls.temporary.cleanup()

    def invoke(self, *, missing=None, late_failure=False):
        import base64
        a=API();a.run['updated_at']=datetime.now(timezone.utc).isoformat()
        runs=[a.run]
        if late_failure:
            a.run.update(id=101,run_number=101,updated_at='2026-01-01T00:00:00Z');a.proof['run_id']=101
            # A valid green within six hours, followed by a later old-run retry.
            from datetime import timedelta
            a.run['updated_at']=(datetime.now(timezone.utc)-timedelta(hours=1)).isoformat()
            runs=[a.run,dict(a.run,id=100,run_number=100,run_attempt=2,conclusion='failure',updated_at=datetime.now(timezone.utc).isoformat())]
        prefix='repos/owner/repo';responses={}
        def add(endpoint,value):responses[endpoint]=dict(body=value)
        add(prefix+'/pulls/42',a.pr)
        for name in ['.github/workflows/ci.yml','.github/scripts/ci-inputs.py','.github/scripts/classify-ci.sh']:
            for ref in ['a'*40,'b'*40]:add(prefix+'/contents/'+name+'?ref='+ref,dict(sha='workflow'))
        if missing is not None:responses[prefix+'/contents/.github/scripts/ci-inputs.py?ref='+('a'*40)]=dict(error=missing)
        add(prefix+'/actions/workflows/ci.yml/runs?head_sha='+('a'*40)+'&per_page=100',dict(total_count=len(runs),workflow_runs=runs))
        add(prefix+f'/actions/runs/{a.run["id"]}/artifacts?per_page=100',dict(total_count=1,artifacts=[dict(id=200,name=f'ci-inputs-{a.run["id"]}-1',expired=False,size_in_bytes=1000)]))
        stream=io.BytesIO()
        with zipfile.ZipFile(stream,'w') as archive:archive.writestr('ci-inputs.json',json.dumps(a.proof))
        responses[prefix+'/actions/artifacts/200/zip']=dict(bytes=base64.b64encode(stream.getvalue()).decode())
        add(prefix+f'/actions/runs/{a.run["id"]}/attempts/1/jobs?per_page=100',dict(total_count=len(a.jobs),jobs=a.jobs))
        add(prefix+'/git/ref/heads/feature%2Fexample',dict(object=dict(sha='a'*40)))
        fixture=self.root/'responses.json';fixture.write_text(json.dumps(responses),encoding='utf-8')
        calls=self.root/'calls.jsonl';calls.unlink(missing_ok=True)
        event=self.root/'event.json';event.write_text(json.dumps(dict(number=42)),encoding='utf-8')
        env=dict(os.environ,PATH=str(self.root)+os.pathsep+os.environ['PATH'],GITHUB_REPOSITORY='owner/repo',CI_ENVIRONMENT_EPOCH='',METADATA_CLI_FIXTURE=str(fixture),METADATA_CLI_CALLS=str(calls))
        result=subprocess.run([sys.executable,str(path.resolve()),'--event',str(event)],cwd=self.root,env=env,capture_output=True,text=True,encoding='utf-8',timeout=30)
        requests=[json.loads(line) for line in calls.read_text(encoding='utf-8').splitlines()]
        return result,[r for r in requests if r['method']=='POST']

    def test_exact_positive_control_through_public_cli(self):
        result,writes=self.invoke();self.assertEqual(result.returncode,0,result.stderr)
        self.assertEqual(json.loads(result.stdout)['action'],'reuse');self.assertEqual(writes,[])

    def test_missing_emitter_requests_full_ci_through_public_cli(self):
        result,writes=self.invoke(missing=404);self.assertEqual(result.returncode,0,result.stderr)
        self.assertEqual(json.loads(result.stdout)['action'],'full-ci-dispatched')
        self.assertEqual(len(writes),1);self.assertEqual(json.loads(writes[0]['body']),dict(ref='feature/example'))

    def test_late_old_run_failure_requests_full_ci_through_public_cli(self):
        result,writes=self.invoke(late_failure=True);self.assertEqual(result.returncode,0,result.stderr)
        self.assertEqual(json.loads(result.stdout)['action'],'full-ci-dispatched');self.assertEqual(len(writes),1)

    def test_auth_error_stops_public_cli_without_any_write(self):
        result,writes=self.invoke(missing=401);self.assertNotEqual(result.returncode,0);self.assertEqual(writes,[])


if __name__ == '__main__': unittest.main()
