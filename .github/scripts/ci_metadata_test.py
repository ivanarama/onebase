"""Request routing and conservative CI proof validation on explicit fixtures."""
import copy
from datetime import datetime, timezone
import hashlib
import importlib.util
import io
import json
from pathlib import Path
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
        if '/attempts/1/jobs?' in endpoint: return dict(total_count=len(self.jobs), jobs=self.jobs)
        if '/runs/100/artifacts?' in endpoint:
            return dict(total_count=1, artifacts=[dict(id=200, name='ci-inputs-100-1', expired=False, size_in_bytes=1000)])
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


if __name__ == '__main__': unittest.main()
