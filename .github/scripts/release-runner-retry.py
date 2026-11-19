"""Retry one macOS build when GitHub never assigned its runner (build-1669)."""
import json
import os
from pathlib import Path
import re
import subprocess

REPO = 'ivanarama/onebase'
BASE = f'repos/{REPO}'
WORKFLOW = '.github/workflows/release.yml'
BUILDS = {f'Build {os_name}/{arch}' for os_name in ('windows', 'linux', 'darwin')
          for arch in ('amd64', 'arm64')}
CI = 'Require green CI for this commit'
PUBLISH = {'Dev Pre-Release', 'Create GitHub Release'}
ALLOCATION_FAILURE = 'The job was not acquired by Runner of type hosted even after multiple attempts'


def api(endpoint, method='GET'):
    response = subprocess.check_output(['gh', 'api', '--method', method, f'{BASE}/{endpoint}'],
                                       timeout=40)
    return json.loads(response) if response.strip() else None


def unallocated_job(page, run):
    jobs = page['jobs']
    expected = BUILDS | {CI} | PUBLISH
    if (page['total_count'] != len(jobs) or len(jobs) != len(expected)
            or {job['name'] for job in jobs} != expected):
        return None
    cancelled = [job for job in jobs if job['conclusion'] == 'cancelled']
    if len(cancelled) != 1:
        return None
    candidate = cancelled[0]
    if (candidate['name'] not in {'Build darwin/amd64', 'Build darwin/arm64'}
            or candidate['labels'] != ['macos-latest'] or candidate['steps'] != []
            or candidate['runner_id'] != 0 or candidate['runner_name'] != ''):
        return None

    for job in jobs:
        if (job['run_id'] != run['id'] or job['run_attempt'] != 1
                or job['head_sha'] != run['head_sha'] or job['status'] != 'completed'):
            return None
        if job is not candidate:
            required = 'skipped' if job['name'] in PUBLISH else 'success'
            if job['conclusion'] != required:
                return None
    return candidate


def handle(event, trusted_release_blob, request=api):
    run = event['workflow_run']
    if (event['action'] != 'completed' or event['repository']['full_name'] != REPO
            or event['workflow']['path'] != WORKFLOW or event['workflow']['id'] != run['workflow_id']
            or run['path'] != WORKFLOW or run['head_repository']['full_name'] != REPO
            or run['event'] != 'push' or run['head_branch'] != 'main' or run['run_attempt'] != 1
            or run['status'] != 'completed' or run['conclusion'] != 'failure'
            or not re.fullmatch('[0-9a-f]{40}', run['head_sha'])):
        return 'Skipped: not a failed first release attempt on main.'

    run_path = f"actions/runs/{run['id']}"
    job = unallocated_job(request(f'{run_path}/attempts/1/jobs?per_page=100'), run)
    if not job:
        return 'Skipped: not an isolated unallocated macOS job.'
    job_id = job['id']
    check_path = f'check-runs/{job_id}'
    if job['check_run_url'] != f'https://api.github.com/{BASE}/{check_path}':
        return 'Skipped: check run does not belong to the cancelled job.'
    annotations = request(f'{check_path}/annotations?per_page=100')
    failures = [a for a in annotations if a['annotation_level'] == 'failure']
    # 100 rows can be a partial page. An unrelated failure must not trigger a retry.
    if (len(annotations) >= 100 or len(failures) != 1 or failures[0]['path'] != '.github'
            or failures[0]['message'] != ALLOCATION_FAILURE):
        return 'Skipped: no isolated GitHub hosted runner allocation failure.'

    # A rerun executes the ORIGINAL release workflow. An older version could
    # create its build tag at the new main tip or publish an incomplete matrix.
    if request(f"contents/{WORKFLOW}?ref={run['head_sha']}")['sha'] != trusted_release_blob:
        return 'Skipped: release workflow differs from the trusted version.'
    tag = f"build-{run['run_number']}"
    refs = request(f'git/matching-refs/tags/{tag}')
    if any(ref['ref'] == f'refs/tags/{tag}' for ref in refs):
        return f'Skipped: tag {tag} already exists.'

    # Deliveries are serialized per run. A manual or already accepted rerun
    # changes the current attempt/status, while the historical event stays failed.
    current = request(run_path)
    identity = ('id', 'workflow_id', 'head_sha', 'run_number')
    if any(current[k] != run[k] for k in (*identity, 'run_attempt', 'status', 'conclusion')):
        return 'Skipped: the release run changed before retry.'
    try:
        request(f'actions/jobs/{job_id}/rerun', method='POST')
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired):
        # A manual retry may win after the final GET, or the POST response may
        # be lost. Read the run once, without resending the mutation.
        observed = request(run_path)
        advancing = (observed['run_attempt'] > 1 or
                     (observed['run_attempt'] == 1 and observed['status'] in {'queued', 'in_progress'}))
        if not advancing or any(observed[k] != run[k] for k in identity):
            raise
        return f'Skipped: GitHub already advanced {tag} to another attempt.'
    return f"Requested one retry of {job['name']} (job {job_id}) for {tag}."


if __name__ == '__main__':
    event = json.loads(Path(os.environ['GITHUB_EVENT_PATH']).read_text(encoding='utf-8'))
    blob = subprocess.check_output(['git', 'rev-parse', f'HEAD:{WORKFLOW}'], text=True).strip()
    print(handle(event, blob))
