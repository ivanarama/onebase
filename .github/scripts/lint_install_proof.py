"""TEMP validation only: execute official compiled action, never private install()."""
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile


def need(value, message):
    if not value:
        raise RuntimeError(message)


def main():
    need(not sys.flags.optimize and os.environ.get('GITHUB_ACTIONS') == 'true', 'GitHub Ubuntu proof context required')
    upstream = Path(sys.argv[1]).resolve(strict=True)
    metadata = (upstream / 'action.yml').read_text()
    need(re.search(r'main:\s*["\']?dist/run/index\.js', metadata), 'public entrypoint changed')
    need(re.search(r'using:\s*["\']?node20', metadata), 'official Node runtime changed')
    entrypoint = upstream / 'dist/run/index.js'
    temporary = Path(os.environ['RUNNER_TEMP']).resolve(strict=True)
    output = temporary / 'lint-install-proof'
    need(output.is_dir(), 'proof output must already be owned by this job')
    binary = Path(os.environ['GOBIN']) / 'golangci-lint'
    buildinfo = subprocess.run(['go', 'version', '-m', str(binary)], capture_output=True, text=True, check=True).stdout
    need('CGO_ENABLED=1' in buildinfo and 'v2.11.4' in buildinfo, 'do not reuse CGO0/unpinned binary')
    fixtures = Path(tempfile.mkdtemp(prefix='lint-action-negative-', dir=temporary))
    action_commit = subprocess.run(['git', '-C', str(upstream), 'rev-parse', 'HEAD'], check=True, capture_output=True, text=True).stdout.strip()
    need(action_commit == '4afd733a84b1f43292c63897423277bb7f4313a9', 'negative action differs from cold/warm official v8 source')
    reports = []
    for name in ('bad-lint', 'invalid-config'):
        fixture = fixtures / name
        fixture.mkdir()
        (fixture / 'go.mod').write_text('module example.com/lint-proof\n\ngo 1.26.10\n')
        (fixture / 'main.go').write_text('package proof\n\nfunc Value() int {\n n := 1\n n = 2\n return n\n}\n')
        config = 'version: "2"\nlinters:\n  default: none\n  enable:\n    - ineffassign\n'
        if name == 'invalid-config':
            config += '  typo_field_for_proof: true\n'
        (fixture / '.golangci.yml').write_text(config)
        environment = os.environ.copy()
        for key in ('GH_TOKEN', 'GITHUB_TOKEN', 'INPUT_GITHUB-TOKEN'):
            environment.pop(key, None)
        environment.update({'CGO_ENABLED': '1', 'INPUT_VERSION': 'v2.11.4', 'INPUT_INSTALL-MODE': 'goinstall',
                            'INPUT_WORKING-DIRECTORY': str(fixture), 'INPUT_VERIFY': 'true',
                            'INPUT_SKIP-CACHE': 'true', 'INPUT_SKIP-SAVE-CACHE': 'true',
                            'INPUT_ONLY-NEW-ISSUES': 'false', 'INPUT_PROBLEM-MATCHERS': 'false',
                            'INPUT_CACHE-INVALIDATION-INTERVAL': '7', 'INPUT_ARGS': '', 'INPUT_DEBUG': ''})
        result = subprocess.run(['node', str(entrypoint)], cwd=fixture, env=environment,
                                capture_output=True, text=True, timeout=120)
        (output / (name + '.stdout')).write_text(result.stdout)
        (output / (name + '.stderr')).write_text(result.stderr)
        text = result.stdout + result.stderr
        need(result.returncode != 0, name + ': action incorrectly succeeded')
        need('Installation mode: goinstall' in text and 'config verify' in text, name + ': not the public verified source-install path')
        if name == 'bad-lint':
            need('ineffectual assignment' in text and 'issues found' in text, 'expected lint diagnostic missing; unrelated failure is not proof')
        else:
            need('typo_field_for_proof' in text and re.search(r'additional|not allowed|not permitted', text, re.I),
                 'expected configuration schema rejection missing')
        reports.append({'fixture': name, 'exit_code': result.returncode,
                        'stdout_sha256': hashlib.sha256(result.stdout.encode()).hexdigest(),
                        'stderr_sha256': hashlib.sha256(result.stderr.encode()).hexdigest()})
    report = {'action_commit': action_commit, 'entrypoint_sha256': hashlib.sha256(entrypoint.read_bytes()).hexdigest(),
              'binary_sha256': hashlib.sha256(binary.read_bytes()).hexdigest(), 'CGO_ENABLED': '1',
              'module': 'github.com/golangci/golangci-lint/v2@v2.11.4', 'fixtures': reports,
              'HOME_overridden': False, 'CODEX_HOME_overridden': False,
              'expected_child_failures_verified': True, 'production_lint_failure_waiver': False}
    (output / 'public-negative-proof.json').write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps(report))


if __name__ == '__main__':
    main()
