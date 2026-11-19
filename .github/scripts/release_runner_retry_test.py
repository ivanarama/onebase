"""Replay the watchdog entry point with a recording gh API (no network)."""
import contextlib
import copy
import io
import json
import os
from pathlib import Path
import runpy
import subprocess
import tempfile
import unittest
from unittest.mock import patch

SCRIPT = Path(__file__).with_name('release-runner-retry.py')
REPO = 'ivanarama/onebase'
PATH = '.github/workflows/release.yml'
SHA = '657e975760ebd2871fcec91b7b781e11eef875d2'
RUN = 44558908685
JOB = 135682460012
MESSAGE = 'The job was not acquired by Runner of type hosted even after multiple attempts'


class RetryTest(unittest.TestCase):
    def setUp(self):
        self.blob = 'f' * 40
        self.run = dict(id=RUN, workflow_id=265454105, run_number=1669, run_attempt=1,
                        head_sha=SHA, path=PATH, head_branch='main', event='push',
                        head_repository={'full_name': REPO}, status='completed', conclusion='failure')
        self.event = dict(action='completed', workflow={'id': 265454105, 'path': PATH},
                          repository={'full_name': REPO}, workflow_run=self.run)
        self.current = copy.deepcopy(self.run)
        names = ['Require green CI for this commit', 'Build linux/amd64', 'Build linux/arm64',
                 'Build windows/amd64', 'Build windows/arm64', 'Build darwin/arm64']
        self.candidate = dict(id=JOB, name='Build darwin/amd64', labels=['macos-latest'], steps=[],
                              runner_id=0, runner_name='', conclusion='cancelled',
                              check_run_url=f'https://api.github.com/repos/{REPO}/check-runs/{JOB}')
        self.jobs = [self.candidate, *(dict(name=n, conclusion='success') for n in names),
                     dict(name='Dev Pre-Release', conclusion='skipped'),
                     dict(name='Create GitHub Release', conclusion='skipped')]
        for job in self.jobs:
            job.update(run_id=RUN, run_attempt=1, head_sha=SHA, status='completed')
        self.annotations = [dict(path='.github', annotation_level='failure', message=MESSAGE),
                            dict(path='.github', annotation_level='notice', message='Capacity constraints')]
        self.refs = [{'ref': 'refs/tags/build-16690'}]  # The endpoint returns prefix matches.
        self.writes = []
        self.post_error = None
        self.post_run_update = dict(status='queued', conclusion=None)
        self.attempt_blob = self.blob

    def command(self, args, **kwargs):
        if args == ['git', 'rev-parse', f'HEAD:{PATH}']:
            return self.blob
        cmd, api, flag, method, endpoint = args
        self.assertEqual([cmd, api, flag], ['gh', 'api', '--method'])
        prefix = f'repos/{REPO}/'
        self.assertTrue(endpoint.startswith(prefix))
        endpoint = endpoint[len(prefix):]
        if method == 'POST':
            self.writes.append(endpoint)
            self.current.update(self.post_run_update)
            if self.post_error:
                raise self.post_error
            return b''
        self.assertEqual(method, 'GET')
        responses = {
            f'actions/runs/{RUN}/attempts/1/jobs?per_page=100': dict(total_count=9, jobs=self.jobs),
            f'check-runs/{JOB}/annotations?per_page=100': self.annotations,
            f'contents/{PATH}?ref={SHA}': {'sha': self.attempt_blob},
            'git/matching-refs/tags/build-1669': self.refs,
            f'actions/runs/{RUN}': self.current,
        }
        return json.dumps(responses[endpoint]).encode()

    def invoke(self):
        with tempfile.TemporaryDirectory() as directory:
            event_path = Path(directory, 'event.json')
            event_path.write_text(json.dumps(self.event), encoding='utf-8')
            with patch.dict(os.environ, GITHUB_EVENT_PATH=str(event_path)), \
                    patch('subprocess.check_output', side_effect=self.command), \
                    contextlib.redirect_stdout(io.StringIO()):
                runpy.run_path(str(SCRIPT), run_name='__main__')

    def test_build_1669_both_architectures_and_duplicate_delivery(self):
        for other_arch in (False, True):
            with self.subTest(other_arch=other_arch):
                self.setUp()
                if other_arch:
                    self.candidate['name'] = 'Build darwin/arm64'
                    self.jobs[6]['name'] = 'Build darwin/amd64'
                self.invoke()
                self.invoke()  # GitHub already accepted the first POST.
                self.current.update(run_attempt=2, status='completed', conclusion='failure')
                self.invoke()  # Attempt 2 failed too; the original event is redelivered.
                self.assertEqual(self.writes, [f'actions/jobs/{JOB}/rerun'])

    def test_release_scope(self):
        for obj, field, value in [
            (self.event, 'action', 'requested'), (self.event['repository'], 'full_name', 'other/repo'),
            (self.event['workflow'], 'path', '.github/workflows/other.yml'),
            (self.run, 'workflow_id', 123), (self.run, 'path', '.github/workflows/other.yml'),
            (self.run['head_repository'], 'full_name', 'other/repo'), (self.run, 'event', 'pull_request'),
            (self.run, 'event', 'workflow_dispatch'), (self.run, 'head_branch', 'v0.14.0'),
            (self.run, 'run_attempt', 2), (self.run, 'conclusion', 'cancelled'),
            (self.run, 'status', 'queued'), (self.current, 'status', 'in_progress'),
            (self.current, 'run_attempt', 2), (self.current, 'head_sha', 'c' * 40),
        ]:
            with self.subTest(field=field, value=value), patch.dict(obj, {field: value}):
                self.invoke()
                self.assertEqual(self.writes, [])

    def test_racing_rerun_or_lost_response_is_observed_without_resending(self):
        self.post_error = subprocess.TimeoutExpired('gh api', 40)
        for update in [dict(status='queued'), dict(status='completed', run_attempt=2)]:
            with self.subTest(update=update):
                self.current = copy.deepcopy(self.run)
                self.writes.clear()
                self.post_run_update = update
                self.invoke()
                self.assertEqual(self.writes, [f'actions/jobs/{JOB}/rerun'])
        self.current = copy.deepcopy(self.run)
        self.writes.clear()
        self.post_error = subprocess.CalledProcessError(1, 'gh api', stderr=b'HTTP 403')
        self.post_run_update = {}  # No rerun accepted. Surface the original error.
        with self.assertRaises(subprocess.CalledProcessError) as caught:
            self.invoke()
        self.assertIs(caught.exception, self.post_error)
        self.assertEqual(self.writes, [f'actions/jobs/{JOB}/rerun'])

    def test_matrix_must_point_to_one_unassigned_mac_job(self):
        for obj, field, value in [
            (self.candidate, 'runner_id', 9), (self.candidate, 'runner_name', 'GitHub Actions 9'),
            (self.candidate, 'steps', [{'name': 'Set up job'}]), (self.candidate, 'labels', ['self-hosted']),
            (self.candidate, 'run_id', 1), (self.candidate, 'head_sha', 'd' * 40),
            (self.candidate, 'check_run_url', 'https://example.com/other-job'),
            (self.candidate, 'conclusion', 'failure'), (self.jobs[1], 'conclusion', 'failure'),
            (self.jobs[2], 'conclusion', 'cancelled'), (self.jobs[-2], 'conclusion', 'success'),
            (self.jobs[2], 'name', 'unexpected'),
        ]:
            with self.subTest(field=field, value=value), patch.dict(obj, {field: value}):
                self.invoke()
                self.assertEqual(self.writes, [])
        self.jobs.pop()
        self.invoke()
        self.assertEqual(self.writes, [])

    def test_allocation_error_must_come_from_github(self):
        for replacement in [[], self.annotations[1:], [dict(self.annotations[0], path='src/file')],
                            [dict(self.annotations[0], annotation_level='notice')],
                            [dict(self.annotations[0], message='Runner cancelled by user')],
                            [*self.annotations, dict(self.annotations[0], message='Compile failed')],
                            [*self.annotations, *([self.annotations[1]] * 98)]]:
            with self.subTest(annotations=len(replacement)), patch.object(self, 'annotations', replacement):
                self.invoke()
                self.assertEqual(self.writes, [])

    def test_old_workflow_or_existing_tag_stops_retry(self):
        with patch.object(self, 'attempt_blob', '0' * 40):
            self.invoke()
        self.refs.append({'ref': 'refs/tags/build-1669'})
        self.invoke()
        self.assertEqual(self.writes, [])


if __name__ == '__main__':
    unittest.main()
