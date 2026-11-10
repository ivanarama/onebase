#!/usr/bin/env python3
"""Keep proven full CI on metadata edits; otherwise request genuine full CI.

Runs only trusted base code. Never posts check/status success, checks out PR
code, changes its branch/labels, or treats a skipped/cancelled job as green.
"""
import argparse
from datetime import datetime, timezone
import hashlib
import io
import json
import os
from pathlib import Path
import re
import subprocess
from urllib.parse import quote
import zipfile

REQUIRED = {'build', 'lint', 'postgres-integration', 'vuln', 'smoke', 'e2e',
            'test-windows', 'launcher-webview-build'}


def gh(endpoint, body=None, *, raw=False):
    command = ['gh', 'api', endpoint]
    if body is not None:
        command += ['--method', 'POST', '--input', '-']
    result = subprocess.run(command, input=json.dumps(body).encode() if body is not None else None,
                            capture_output=True, check=True, timeout=45)
    return result.stdout if raw else json.loads(result.stdout or b'{}')


def identity(pr):
    return (pr['head']['sha'], pr['base']['sha'], pr['base']['ref'], pr['state'],
            pr['draft'], pr.get('updated_at'), pr.get('body'), pr.get('title'))


def missing_blob(api, endpoint):
    try:
        value = api(endpoint)
        digest = value.get('sha') if isinstance(value, dict) else None
        return digest if isinstance(digest, str) and digest else None
    except subprocess.CalledProcessError as exc:
        error = exc.stderr or b''
        if isinstance(error, bytes): error = error.decode('utf-8', errors='replace')
        # Only a real content 404 is ordinary missing evidence. Authentication,
        # permission, rate-limit and transport errors must remain visible.
        if not re.search(r'\(HTTP 404\)\s*$', error.strip()): raise
        return None


def full_proof(api, repository, run, pr, workflow_blob, epoch, now, *, completed=True):
    bench_failure = completed and run.get('conclusion') == 'failure'
    if ((completed and (run.get('status') != 'completed'
                        or run.get('conclusion') not in {'success', 'failure'}))
            or (not completed and (run.get('status') not in {'queued', 'in_progress'}
                                   or run.get('conclusion') is not None))
            or run.get('head_sha') != pr['head']['sha']
            or run.get('event') not in {'pull_request', 'workflow_dispatch'}
            or run.get('path') != '.github/workflows/ci.yml'):
        return False
    dispatch = run['event'] == 'workflow_dispatch'
    # A branch dispatch runs HEAD rather than the PR merge checkout. It can
    # be observed while running, but cannot substitute for completed PR proof.
    if dispatch and (completed or run.get('head_branch') != pr['head']['ref']
                     or (run.get('head_repository') or {}).get('full_name') != repository):
        return False
    try:
        age = (now - datetime.fromisoformat(run['updated_at'].replace('Z', '+00:00'))).total_seconds()
        if not 0 <= age <= 6 * 3600:
            return False
        prefix = f'repos/{repository}/actions/runs/{run["id"]}'
        artifacts = api(prefix + '/artifacts?per_page=100')
        if (artifacts['total_count'] > 100
                or (bench_failure and (type(artifacts['total_count']) is not int
                                       or artifacts['total_count'] != len(artifacts['artifacts'])))):
            return False
        name = f'ci-inputs-{run["id"]}-{run["run_attempt"]}'
        found = [a for a in artifacts['artifacts'] if a['name'] == name and not a['expired'] and a['size_in_bytes'] <= 65536]
        if len(found) != 1:
            return False
        raw = api(f'repos/{repository}/actions/artifacts/{found[0]["id"]}/zip', raw=True)
        if len(raw) > 65536:
            return False
        with zipfile.ZipFile(io.BytesIO(raw)) as archive:
            if archive.namelist() != ['ci-inputs.json'] or archive.getinfo('ci-inputs.json').file_size > 16384:
                return False
            proof = json.loads(archive.read('ci-inputs.json'))
        expected = dict(version=1, repository=repository, run_id=run['id'], run_attempt=run['run_attempt'],
                        head=pr['head']['sha'], base=pr['base']['sha'], workflow_blob=workflow_blob,
                        environment_epoch=epoch, mode='full', event=run['event'],
                        body_sha256=hashlib.sha256(('' if dispatch else pr.get('body') or '').encode()).hexdigest())
        if any(proof.get(k) != v for k, v in expected.items()):
            return False
        if proof.get('checkout') != (pr['head']['sha'] if dispatch else pr.get('merge_commit_sha')):
            return False
        if not completed:
            return True
        jobs = api(prefix + f'/attempts/{run["run_attempt"]}/jobs?per_page=100')
        if jobs['total_count'] > 100:
            return False
        required = [j for j in jobs['jobs'] if j['name'] in REQUIRED]
        if not (len(required) == len(REQUIRED) and {j['name'] for j in required} == REQUIRED
                and all(j['status'] == 'completed' and j['conclusion'] == 'success' for j in required)):
            return False
        if not bench_failure:
            return True
        # A failed workflow is reusable only for the single optional bench
        # failure. Require the entire attempt's unambiguous job inventory;
        # neither the workflow nor bench is relabelled as successful.
        listed = jobs['jobs']
        return (type(jobs['total_count']) is int and jobs['total_count'] == len(listed)
                and all(type(j.get('id')) is int and j['id'] > 0
                        and isinstance(j.get('name'), str) and j['name'] for j in listed)
                and len({j['id'] for j in listed}) == len(listed)
                and len({j['name'] for j in listed}) == len(listed)
                and sum(j['name'] == 'bench' for j in listed) == 1
                and all(j['status'] == 'completed'
                        and j['conclusion'] == ('failure' if j['name'] == 'bench' else 'success')
                        for j in listed))
    except subprocess.CalledProcessError as exc:
        error = exc.stderr or b''
        if isinstance(error, bytes): error = error.decode('utf-8', errors='replace')
        if not re.search(r'\(HTTP 404\)\s*$', error.strip()): raise
        return False
    except (KeyError, TypeError, ValueError, OSError, zipfile.BadZipFile):
        return False


def handle(api, repository, number, epoch, *, now):
    prefix = f'repos/{repository}'
    pr = api(prefix + f'/pulls/{number}')
    if pr['state'] != 'open' or pr['base']['ref'] != 'main':
        return dict(action='inactive')
    blob = missing_blob(api, prefix + '/contents/.github/workflows/ci.yml?ref=' + quote(pr['base']['sha'], safe=''))
    # A PR must not forge the input certificate or docs/full classification.
    trusted = blob is not None
    for path in ['.github/workflows/ci.yml', '.github/scripts/ci-inputs.py', '.github/scripts/classify-ci.sh']:
        base_blob = missing_blob(api, prefix + '/contents/' + path + '?ref=' + pr['base']['sha'])
        head_blob = missing_blob(api, prefix + '/contents/' + path + '?ref=' + pr['head']['sha'])
        trusted = trusted and base_blob is not None and head_blob is not None and base_blob == head_blob
    runs = api(prefix + '/actions/workflows/ci.yml/runs?head_sha=' + pr['head']['sha'] + '&per_page=100')
    # A bounded/uncertain history can only lose reuse, never invent success.
    reuse, running = None, None
    candidates = [r for r in runs['workflow_runs'][:100] if r.get('path') == '.github/workflows/ci.yml'
                  and r.get('head_sha') == pr['head']['sha']]
    # Never reach behind a newer failure/cancellation to find an older green.
    try:
        # API list order/run_number describe creation, not execution. An old
        # run rerun later can supersede a newer run's green result.
        latest = max(candidates, key=lambda r: (datetime.fromisoformat(r['updated_at'].replace('Z', '+00:00')),
                     r['id'], r['run_attempt'])) if candidates else None
        complete = runs['total_count'] == len(runs['workflow_runs']) and len({r['id'] for r in runs['workflow_runs']}) == len(runs['workflow_runs'])
    except (KeyError, TypeError, ValueError):
        latest, complete = None, False
    if latest and complete:
        run = latest
        if trusted and full_proof(api, repository, run, pr, blob, epoch, now): reuse = run
        elif trusted and full_proof(api, repository, run, pr, blob, epoch, now, completed=False): running = run
    latest = api(prefix + f'/pulls/{number}')
    if identity(latest) != identity(pr):
        raise ValueError('PR changed during metadata validation; retry the latest event')
    if reuse:
        return dict(action='reuse', run_id=reuse['id'], url=reuse['html_url'])
    if running:
        return dict(action='full-ci-running', run_id=running['id'], url=running['html_url'])
    # Before classify uploads its certificate (or when its context is not
    # provable), another dispatch to this same ref would cancel the existing
    # run. Refuse rather than claim a matching context or destroy useful work.
    if any(r.get('event') == 'workflow_dispatch' and r.get('head_branch') == pr['head']['ref']
           and r.get('status') in {'queued', 'in_progress'} for r in candidates):
        raise ValueError('CI dispatch for this HEAD/ref is still running without matching input proof; '
                         'wait for its certificate or completion before retrying Ready')
    if (pr['head'].get('repo') or {}).get('full_name') == repository:
        # workflow_dispatch executes the existing workflow and all its checks;
        # an unchanged branch/HEAD is rechecked immediately before the write.
        branch = pr['head']['ref']
        ref = api(prefix + '/git/ref/heads/' + quote(branch, safe=''))
        if ref['object']['sha'] != pr['head']['sha']:
            raise ValueError('branch HEAD changed before full CI dispatch')
        api(prefix + '/actions/workflows/ci.yml/dispatches', {'ref': branch})
        return dict(action='full-ci-dispatched', head=pr['head']['sha'])
    # Reruns preserve the original event, including docs-only classification.
    # No supported safe force-full fork transport exists in this workflow.
    # Explicitly refuse; never call a repeated light run a full CI execution.
    raise ValueError('No matching full CI proof for this fork. A maintainer must arrange a safe full '
                     'pull_request run for the exact HEAD/base before retrying Ready; source changes '
                     'normally start it on synchronize. Rerunning docs-only CI cannot force full mode.')


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--event', required=True, type=Path)
    args = parser.parse_args()
    event = json.loads(args.event.read_text(encoding='utf-8'))
    repository = os.environ['GITHUB_REPOSITORY']
    number = event['number']
    if type(number) is not int or number <= 0:
        raise ValueError('invalid PR number')
    print(json.dumps(handle(gh, repository, number, os.environ.get('CI_ENVIRONMENT_EPOCH', ''),
                            now=datetime.now(timezone.utc))))


if __name__ == '__main__':
    main()
