import base64,hashlib,json,os,subprocess,sys,tempfile,unittest,zlib
from pathlib import Path

HELPER=Path(__file__).with_name('candidate_transport.py')
def canon(v):return json.dumps(v,ensure_ascii=True,sort_keys=True,separators=(',',':')).encode('ascii')
def sha(b):return hashlib.sha256(b).hexdigest()

class TransportCLI(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory(prefix='transport2222-');self.root=Path(self.temp.name);self.repo=self.root/'public.git'
        self.env={k:v for k,v in os.environ.items()if not k.upper().startswith('GIT_')}
        self.env.update(GIT_CONFIG_NOSYSTEM='1',GIT_CONFIG_GLOBAL=os.devnull,GIT_AUTHOR_NAME='Fixture',GIT_AUTHOR_EMAIL='fixture@example.invalid',GIT_COMMITTER_NAME='Fixture',GIT_COMMITTER_EMAIL='fixture@example.invalid')
        subprocess.run(['git','init','--bare',str(self.repo)],env=self.env,check=True,capture_output=True)
        self.basefiles={'CHANGELOG.md':b'base changelog\n','go.sum':b'base sum\n','internal/i18n/locales/en.json':b'{"base": "translation"}\n','stable.txt':b'stable\n','run.sh':b'#!/bin/sh\nexit 0\n'}
        self.base=self.commit(self.basefiles)
        left=dict(self.basefiles);left.update({'CHANGELOG.md':b'source changelog\n','go.sum':b'source sum\n','internal/i18n/locales/en.json':b'{"source": "translation"}\n','source.txt':b'source-only\n'})
        right=dict(self.basefiles);right.update({'CHANGELOG.md':b'main changelog\n','go.sum':b'main sum\n','internal/i18n/locales/en.json':b'{"main": "translation"}\n','main.txt':b'main-only\n'})
        self.source=self.commit(left,self.base);self.main=self.commit(right,self.base)
        self.candidate=dict(right);self.candidate.update({'source.txt':b'source-only\n','CHANGELOG.md':b'main changelog\nsource changelog\n','go.sum':b'main sum\nsource sum\n','internal/i18n/locales/en.json':b'{"main": "translation", "source": "translation"}\n'})
        self.tree=self.make_tree(self.candidate)
        self.ctx={'source':self.source,'main':self.main,'base':self.base,'owner_contract_sha256':'a'*64,'proof_epoch':'b'*64,'task_id':'fixture-only','started_at':'2026-11-09T00:00:00Z','operator_sha':'c'*40,'repository':'ivanarama/onebase','operator_ref':'refs/heads/operator/2222-linux-qualification-20261109','workflow':'.github/workflows/ci.yml','nonce':'d'*32,'purpose':'archived-pilot'}
        self.context=self.root/'context.json';self.context.write_bytes(canon(self.ctx));self.pack=self.root/'packed';self.receiver=self.root/'received'
    def tearDown(self):self.temp.cleanup()
    def git(self,*args,data=None):return subprocess.run(['git','--git-dir='+str(self.repo),*args],input=data,env=self.env,capture_output=True,check=True).stdout
    def make_tree(self,files,modes=None):
        modes=modes or {};dirs={'':{}}
        for path,data in files.items():
            parts=path.split('/');parent=''
            for name in parts[:-1]:
                child=(parent+'/'+name).lstrip('/');dirs.setdefault(child,{});dirs[parent][name]=('040000','tree',child);parent=child
            blob=self.git('hash-object','-w','--stdin',data=data).decode().strip();dirs[parent][parts[-1]]=(modes.get(path,'100755'if path=='run.sh'else '100644'),'blob',blob)
        trees={}
        for d in sorted(dirs,key=lambda x:x.count('/')+(1 if x else 0),reverse=True):
            rows=[]
            for name,(mode,kind,value)in sorted(dirs[d].items()):
                rows.append((mode+' '+kind+' '+(trees[value]if kind=='tree'else value)+'\t'+name).encode()+b'\0')
            trees[d]=self.git('mktree','-z',data=b''.join(rows)).decode().strip()
        return trees['']
    def commit(self,files,parent=None):
        args=['commit-tree',self.make_tree(files)]
        if parent:args+=['-p',parent]
        return self.git(*args,data=b'fixture\n').decode().strip()
    def cli(self,*args,expected=0):
        p=subprocess.run([sys.executable,'-B','-X','utf8',str(HELPER),*map(str,args)],capture_output=True,timeout=45)
        self.assertEqual(expected,p.returncode,p.stderr.decode('utf8','replace'))
        return json.loads(p.stdout)if expected==0 else p.stderr.decode('utf8','replace')
    def packing(self,tree=None,expected=0):return self.cli('pack','--objects',self.repo/'objects','--context',self.context,'--candidate-tree',tree or self.tree,'--out',self.pack,expected=expected)
    def receiving(self,expected=0,admission_sha=None):return self.cli('reconstruct','--objects',self.repo/'objects','--admission',self.pack/'admission.json','--expected-admission-sha',admission_sha or sha((self.pack/'admission.json').read_bytes()),'--packet',self.pack/'candidate.packet','--out',self.receiver,expected=expected)
    def mutate_packet(self,change):
        value=json.loads(zlib.decompress(base64.b64decode((self.pack/'candidate.packet').read_bytes())));change(value)
        packet=base64.b64encode(zlib.compress(canon(value),9));(self.pack/'candidate.packet').write_bytes(packet)
        admission=json.loads((self.pack/'admission.json').read_bytes());admission['packet_sha256']=sha(packet)
        for key in ('context','tree','manifest_sha256'):admission[key]=value[key]
        (self.pack/'admission.json').write_bytes(canon(admission))
    def test_pack_reconstruct_verify_public_roundtrip(self):
        packed=self.packing();self.assertLess(packed['dispatch_ascii_bytes'],60000);result=self.receiving();self.assertTrue(result['before_after_identical']);self.assertFalse(result['product_lint_authorized'])
        before=(self.receiver/'before-manifest.json').read_bytes();self.assertEqual(before,(self.receiver/'after-manifest.json').read_bytes())
        for path,data in self.candidate.items():self.assertEqual(data,(self.receiver/'tree'/path).read_bytes())
        self.cli('verify','--root',self.receiver/'tree','--manifest',self.receiver/'before-manifest.json','--expected-manifest-sha',sha(before))
    def test_candidate_changes_unapproved_blob_rejected(self):
        files=dict(self.candidate);files['stable.txt']=b'changed\n';self.packing(self.make_tree(files),expected=1)
    def test_candidate_changes_unapproved_mode_rejected(self):self.packing(self.make_tree(self.candidate,{'stable.txt':'100755'}),expected=1)
    def test_candidate_symlink_rejected(self):self.packing(self.make_tree(self.candidate,{'stable.txt':'120000'}),expected=1)
    def test_new_semantic_conflict_rejected(self):
        left=dict(self.basefiles);left['stable.txt']=b'left\n';right=dict(self.basefiles);right['stable.txt']=b'right\n';self.ctx.update(source=self.commit(left,self.base),main=self.commit(right,self.base));self.context.write_bytes(canon(self.ctx));self.packing(expected=1)
    def test_externally_pinned_admission_cannot_change(self):self.packing();self.receiving(expected=1,admission_sha='e'*64)
    def test_corrupt_packet_rejected(self):
        self.packing();p=self.pack/'candidate.packet';p.write_bytes(p.read_bytes()+b'x');self.receiving(expected=1)
    def test_unknown_packet_field_rejected_with_consistent_hash(self):self.packing();self.mutate_packet(lambda v:v.update(command='arbitrary'));self.receiving(expected=1)
    def test_path_traversal_rejected_with_consistent_hash(self):self.packing();self.mutate_packet(lambda v:v['resolutions'][0].update(path='../escape'));self.receiving(expected=1)
    def test_resolution_blob_change_rejected_with_consistent_hash(self):self.packing();self.mutate_packet(lambda v:v['resolutions'][0].update(blob='0'*40));self.receiving(expected=1)
    def test_resolution_mode_change_rejected_with_consistent_hash(self):self.packing();self.mutate_packet(lambda v:v['resolutions'][0].update(mode='100755'));self.receiving(expected=1)
    def test_candidate_tree_change_rejected(self):self.packing();self.mutate_packet(lambda v:v.update(tree='0'*40));self.receiving(expected=1)
    def test_candidate_manifest_digest_change_rejected(self):self.packing();self.mutate_packet(lambda v:v.update(manifest_sha256='0'*64));self.receiving(expected=1)
    def test_oversized_encoded_input_rejected(self):
        self.packing();p=self.pack/'candidate.packet';p.write_bytes(b'A'*60001);a=json.loads((self.pack/'admission.json').read_bytes());a['packet_sha256']=sha(p.read_bytes());(self.pack/'admission.json').write_bytes(canon(a));self.receiving(expected=1)
    def test_decompression_bomb_rejected(self):
        self.packing();p=self.pack/'candidate.packet';p.write_bytes(base64.b64encode(zlib.compress(b' '*2097153)));a=json.loads((self.pack/'admission.json').read_bytes());a['packet_sha256']=sha(p.read_bytes());(self.pack/'admission.json').write_bytes(canon(a));self.receiving(expected=1)
    def test_changed_exported_blob_rejected(self):
        self.packing();self.receiving();(self.receiver/'tree/stable.txt').write_bytes(b'changed');raw=(self.receiver/'before-manifest.json').read_bytes();self.cli('verify','--root',self.receiver/'tree','--manifest',self.receiver/'before-manifest.json','--expected-manifest-sha',sha(raw),expected=1)
    def test_extra_exported_path_rejected(self):
        self.packing();self.receiving();(self.receiver/'tree/extra.txt').write_bytes(b'extra');raw=(self.receiver/'before-manifest.json').read_bytes();self.cli('verify','--root',self.receiver/'tree','--manifest',self.receiver/'before-manifest.json','--expected-manifest-sha',sha(raw),expected=1)
    def test_extra_empty_directory_rejected(self):
        self.packing();self.receiving();(self.receiver/'tree/extra-empty').mkdir();raw=(self.receiver/'before-manifest.json').read_bytes();self.cli('verify','--root',self.receiver/'tree','--manifest',self.receiver/'before-manifest.json','--expected-manifest-sha',sha(raw),expected=1)
    def test_missing_public_parent_rejected(self):
        self.ctx['source']='0'*40;self.context.write_bytes(canon(self.ctx));self.packing(expected=1)
    @unittest.skipUnless(os.name=='posix','POSIX physical mode readback requires Linux; Git-mode mutation is tested portably')
    def test_group_only_executable_mode_cannot_replace_owner_execute(self):
        self.packing();self.receiving();(self.receiver/'tree/run.sh').chmod(0o654);raw=(self.receiver/'before-manifest.json').read_bytes();self.cli('verify','--root',self.receiver/'tree','--manifest',self.receiver/'before-manifest.json','--expected-manifest-sha',sha(raw),expected=1)

if __name__=='__main__':unittest.main(verbosity=2)
