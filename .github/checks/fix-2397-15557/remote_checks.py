import hashlib
import json
import os
from pathlib import Path
import platform
import signal
import shutil
import subprocess
import sys
import time

ROOT = Path(os.environ['CANDIDATE_ROOT']).resolve()
OUT = Path(os.environ['CHECK_ARTIFACTS']).resolve()
HARNESS = Path(__file__).resolve().parent
OUT.mkdir(mode=0o700, parents=True)
ENV = os.environ.copy()
ENV.update(GOMAXPROCS='2', GOMEMLIMIT='7GiB', GOGC='100', GOTOOLCHAIN='local',
           GOFLAGS='', GOWORK='off', CGO_ENABLED='1', PYTHONDONTWRITEBYTECODE='1')
ENV['GOLANGCI_LINT_CACHE'] = str(OUT / 'lint-cache')
RESULTS = []

def sha(p):
    return hashlib.sha256(p.read_bytes()).hexdigest()

def save(name, value):
    (OUT / name).write_text(json.dumps(value, ensure_ascii=False, indent=2), encoding='utf-8')

def run(name, argv, cwd=ROOT, timeout=1500, pg=False, expected_failure=False):
    child = ENV.copy()
    if not pg:
        child.pop('TEST_DATABASE_URL', None)
    started = time.monotonic()
    print('START ' + name, flush=True)
    save('current.json', {'stage': name, 'status': 'running', 'argv': argv})
    with (OUT / (name + '.stdout')).open('wb') as out, (OUT / (name + '.stderr')).open('wb') as err:
        process = subprocess.Popen(argv, cwd=cwd, env=child, stdout=out, stderr=err, start_new_session=True)
        try:
            code = process.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGQUIT)
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait()
            code = 124
    record = {'stage': name, 'argv': argv, 'cwd': str(cwd), 'postgres': pg,
              'exit': code, 'expected_failure': expected_failure, 'seconds': round(time.monotonic() - started, 3)}
    RESULTS.append(record)
    save('results.json', RESULTS)
    save('current.json', record)
    print('END ' + name + ' exit=' + str(code), flush=True)
    if expected_failure:
        assert code == 1, (name, code)
        events = [json.loads(line) for line in (OUT / (name + '.stdout')).read_text().splitlines() if line.startswith('{')]
        assert any(e.get('Action') == 'fail' and e.get('Test', '').startswith('TestFormNotifyQuestionFieldsHTTP/') for e in events), 'negative control did not execute the regression'
    elif code:
        raise RuntimeError(name + ' failed')
    return (OUT / (name + '.stdout')).read_bytes()

def verify():
    expected = json.loads((HARNESS / 'manifest.json').read_text(encoding='utf-8'))
    tracked = subprocess.check_output(['git', 'ls-files', '--stage', '-z'], cwd=ROOT)
    actual = []
    for row in tracked.split(b'\0'):
        if not row:
            continue
        header, path = row.split(b'\t')
        mode, oid, stage = header.decode().split()
        name = path.decode('utf-8')
        p = ROOT / name
        assert stage == '0' and p.is_file() and not p.is_symlink()
        raw = p.read_bytes()
        assert hashlib.sha1(b'blob ' + str(len(raw)).encode() + b'\0' + raw).hexdigest() == oid
        assert bool(p.stat().st_mode & 0o111) == (mode == '100755')
        actual.append(dict(path=name, mode=mode, oid=oid, sha256=hashlib.sha256(raw).hexdigest()))
    assert actual == expected
    assert subprocess.check_output(['git', 'rev-parse', 'HEAD^{tree}'], cwd=ROOT).decode().strip() == os.environ['CANDIDATE_TREE']
    assert subprocess.check_output(['git', 'diff', '--exit-code', 'HEAD'], cwd=ROOT) == b''
    save('source-verified.json', {'tree': os.environ['CANDIDATE_TREE'], 'files': len(actual)})

def main():
    verify()
    save('platform.json', {'platform': platform.platform(), 'machine': platform.machine(),
                         'cpus': os.cpu_count(), 'python': sys.version,
                         'environment_sha256': hashlib.sha256(json.dumps(sorted(ENV.items())).encode()).hexdigest()})
    assert platform.system() == 'Linux' and platform.machine() == 'x86_64'
    go = run('go-version', ['go', 'version']).decode()
    assert go.strip() == 'go version go1.26.10 linux/amd64'
    go_env = json.loads(run('go-env', ['go', 'env', '-json']))
    actual_tools = [Path(shutil.which('go')), Path(shutil.which('node')),
                    Path(go_env['GOROOT']) / 'bin/go']
    actual_tools.extend(Path(go_env['GOTOOLDIR']) / n for n in ['compile', 'link', 'asm', 'cover', 'cgo', 'vet'])
    save('selected-tools.json', {str(p.resolve()): sha(p) for p in actual_tools})
    run('node-version', ['node', '--version'])
    run('memory', ['cat', '/proc/meminfo'])
    run('compiler', ['gcc', '--version'])
    tools = OUT / 'tools'
    tools.mkdir()
    archive = tools / 'golangci-lint-2.11.4-linux-amd64.tar.gz'
    run('download-lint', ['curl', '--fail', '--location', '--silent', '--show-error', '--max-time', '180',
                         'https://github.com/golangci/golangci-lint/releases/download/v2.11.4/' + archive.name,
                         '--output', str(archive)], timeout=190)
    assert sha(archive) == '200c5b7503f67b59a6743ccf32133026c174e272b930ee79aa2aa6f37aca7ef1'
    run('extract-lint', ['tar', '-xzf', str(archive), '-C', str(tools)])
    binary = tools / 'golangci-lint-2.11.4-linux-amd64/golangci-lint'
    version = run('lint-version', [str(binary), 'version']).decode()
    assert '2.11.4' in version and 'go1.26.1' in version
    run('lint-build-info', ['go', 'version', '-m', str(binary)])
    run('lint-config', [str(binary), 'config', 'verify'])
    enabled = run('lint-enabled', [str(binary), 'linters']).decode()
    enabled_section = enabled.split('Disabled by your configuration linters:')[0]
    import re
    enabled_names = set(re.findall(r'^([a-z0-9]+):', enabled_section, re.M))
    assert enabled_names == {'bodyclose','errcheck','gosec','govet','ineffassign','staticcheck','unused'}, enabled_names
    save('tool-inputs.json', {'archive_sha256': sha(archive), 'binary_sha256': sha(binary),
                            'config_sha256': sha(ROOT / '.golangci.yml'),
                            'runner_sha256': sha(HARNESS / 'test-coverage.py'),
                            'version': version})
    assert sha(HARNESS / 'test-coverage.py') == '44cf1e31adeab3c7680b62ba8e3dff186ef6467d9f9f21c819aa183a0969552d'
    for name in ['test-race-coverage_test.py', 'test_coverage_test.py', 'lint_runner_test.py', 'lint_runner_driver_test.py']:
        run('selftest-' + name, ['python3', '.github/scripts/' + name])
    # Short public regressions precede long lint/coverage. Mutations are temporary
    # negative controls only; immutable candidate bytes are restored and reverified.
    target = ROOT / 'internal/ui/templates_managed.go'
    original = target.read_bytes()
    controls = [
        ('negative-missing-notify', b'  "notify" (hasFormHandler .Form "' + 'ОбработкаОповещения'.encode() + b'")\n'),
        ('negative-missing-service-fields', b'  "serviceFields" (dict "_question_fields" (entityQuestionFieldsName .Entity .Form))\n'),
    ]
    for name, fragment in controls:
        assert original.count(fragment) == 1
        try:
            target.write_bytes(original.replace(fragment, b'', 1))
            run(name, ['go', 'test', '-json', '-count=1', '-p', '2', '-timeout=10m',
                       './internal/ui', '-run', '^TestFormNotifyQuestionFieldsHTTP$'],
                pg=True, expected_failure=True, timeout=630)
        finally:
            target.write_bytes(original)
        verify()
    run('postgres-public', ['go', 'test', '-json', '-count=1', '-p', '2', '-timeout=10m', './internal/ui',
        '-run', '^Test(FormNotify|ShowQuestion_|FormQuestionFields_BeforePostHTTPMatrix|FormQuestions_OnOpenAndBeforePostHTTP)'], pg=True)
    events = [json.loads(line) for line in (OUT / 'postgres-public.stdout').read_text(encoding='utf-8').splitlines()]
    assert not any(e.get('Action') == 'skip' and e.get('Test') for e in events)
    assert any(e.get('Action') == 'pass' and '/postgres' in e.get('Test', '') for e in events)
    run('node', ['node', '--test', 'static/form_notify_behavior_test.js', 'static/managed_close_intent_behavior_test.js',
                 'static/form_close_bridge_behavior_test.js', 'static/question_modal_behavior_test.js'], cwd=ROOT / 'internal/ui')
    run('full-lint', [str(binary), 'run', '-v', '-j', '2', '--config', '.golangci.yml',
                      '--cpu-profile-path', str(OUT / 'lint.cpu.pprof'), './...'], timeout=630)
    run('build', ['go', 'build', '-p', '2', './...'])
    packages = ['./internal/ui', './internal/dsl/interpreter', './internal/metadata', './internal/dsl/langref',
                './internal/cli', './internal/dslvars', './internal/realtime', './internal/runtime',
                './internal/entityservice', './internal/processor', './internal/dsl/loader',
                './internal/configcheck', './internal/converter', './internal/onec_forms']
    run('affected-inventory', ['go', 'test', '-json', '-p', '2', '-list', '^(Test|Example|Fuzz)', *packages])
    run('affected-tests', ['go', 'test', '-json', '-count=1', '-p', '2', '-timeout=10m', *packages])
    run('vet', ['go', 'vet', '-p', '2', *packages])
    run('trade', ['go', 'run', './cmd/onebase', 'check', '--project', 'examples/trade'])
    run('module-graph', ['go', 'mod', 'graph'])
    run('module-verify', ['go', 'mod', 'verify'])
    formatting = run('format', ['gofmt', '-l', 'internal/ui/form_notify_question_fields_test.go'])
    assert not formatting.strip()
    run('diff', ['git', 'diff', '--check', 'HEAD'])
    race_artifacts = OUT / 'race'
    run('full-race-atomic', ['python3', str(HARNESS / 'test-coverage.py'), '--quiet', '--artifacts',
                            str(race_artifacts), '--output', str(OUT / 'coverage.out')], timeout=3500)
    verify()
    save('complete.json', {'status': 'passed', 'tree': os.environ['CANDIDATE_TREE']})

try:
    main()
except Exception as error:
    save('failed.json', {'error': str(error), 'tree': os.environ['CANDIDATE_TREE']})
    print('CHECK FAILED; detailed evidence retained privately', flush=True)
    sys.exit(1)
