#!/usr/bin/env python3
"""Emit immutable CI execution inputs, never a test verdict."""
import hashlib
import json
import os
from pathlib import Path
import subprocess


def git(*args):
    return subprocess.check_output(['git', *args], text=True).strip()


def main():
    event = json.loads(Path(os.environ['GITHUB_EVENT_PATH']).read_text(encoding='utf-8'))
    pr = event.get('pull_request') or {}
    value = dict(version=1, run_id=int(os.environ['GITHUB_RUN_ID']),
                 run_attempt=int(os.environ['GITHUB_RUN_ATTEMPT']),
                 repository=os.environ['GITHUB_REPOSITORY'], event=os.environ['GITHUB_EVENT_NAME'],
                 head=(pr.get('head') or {}).get('sha') or git('rev-parse', 'HEAD'),
                 base=(pr.get('base') or {}).get('sha') or git('rev-parse', 'origin/main'),
                 checkout=git('rev-parse', 'HEAD'), mode=os.environ['CI_MODE'],
                 environment_epoch=os.environ.get('CI_ENVIRONMENT_EPOCH', ''),
                 workflow_blob=git('rev-parse', 'HEAD:.github/workflows/ci.yml'),
                 go_version=Path('.go-version').read_text(encoding='utf-8').strip(),
                 lint_config_sha256=hashlib.sha256(Path('.golangci.yml').read_bytes()).hexdigest(),
                 body_sha256=hashlib.sha256((pr.get('body') or '').encode()).hexdigest())
    Path('ci-inputs.json').write_text(json.dumps(value, sort_keys=True), encoding='utf-8')


if __name__ == '__main__':
    main()
