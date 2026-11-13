"""Public CLI regression: run and dev apply the same app.yaml UI keys.

Usage: python e2e/acceptance/ui-launch-config.py --binary /path/to/onebase
Uses only a disposable SQLite database and two sequential loopback processes.
Does not build Go or require a browser.
"""
import argparse
import http.cookiejar
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import tempfile
import time
import urllib.parse
import urllib.request

parser = argparse.ArgumentParser()
parser.add_argument('--binary', required=True)
args = parser.parse_args()
binary = str(Path(args.binary).resolve())
flags = subprocess.CREATE_NO_WINDOW if os.name == 'nt' else 0
env = dict(os.environ)
env.pop('DATABASE_URL', None)
env['ONEBASE_NO_GUI'] = '1'
results = []
with tempfile.TemporaryDirectory(prefix='onebase-ui-launch-') as directory:
    root = Path(directory)
    db = root / 'test.sqlite'

    def write(name, data):
        path = root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(data, ensure_ascii=False), encoding='utf-8')

    write('config/app.yaml', {'name': 'Synthetic launch regression', 'version': '1', 'ui': {
        'list_density': 'compact', 'roles': [], 'except_roles': [],
        'list_detail_panel': False, 'list_row_actions': False,
        'form_roles': [], 'form_except_roles': [], 'attachments': False, 'form_density': 'dense',
        'report_roles': [], 'report_except_roles': [], 'report_density': 'compact', 'short_date_input': True}})
    write('catalogs/LaunchProbe.yaml', {'name': 'LaunchProbe', 'fields': [{'name': 'Day', 'type': 'date'}]})
    write('reports/LaunchReport.yaml', {'name': 'LaunchReport', 'query': 'SELECT 1 AS Value',
                                      'params': [{'name': 'Day', 'type': 'date'}]})

    def cli(*command, stdin=None):
        result = subprocess.run([binary, *command, '--project', str(root), '--sqlite', str(db)],
                                input=stdin, text=True, encoding='utf-8', capture_output=True,
                                env=env, timeout=45, creationflags=flags)
        if result.returncode:
            raise AssertionError('synthetic CLI failed: ' + ' '.join(command[:2]) + '\n' + result.stderr)

    cli('migrate')
    password = 'Aa1!' + secrets.token_urlsafe(24)
    cli('user', 'add', 'launch_admin', '--admin', '--password-stdin', stdin=password + '\n')
    for mode in ['dev', 'run']:
        with socket.socket() as reservation:
            reservation.bind(('127.0.0.1', 0))
            port = reservation.getsockname()[1]
        base = 'http://127.0.0.1:' + str(port)
        with (root / (mode + '.log')).open('wb') as log:
            process = subprocess.Popen([binary, mode, '--project', str(root), '--sqlite', str(db),
                                        '--config-source', 'file', '--port', str(port), '--no-gui'],
                                       env=env, stdout=log, stderr=subprocess.STDOUT, creationflags=flags)
            try:
                deadline = time.monotonic() + 30
                while True:
                    try:
                        with urllib.request.urlopen(base + '/login', timeout=1):
                            break
                    except Exception:
                        if time.monotonic() >= deadline or process.poll() is not None:
                            raise AssertionError(mode + ' did not become ready')
                        time.sleep(0.05)
                client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
                with client.open(base + '/login', data=urllib.parse.urlencode({'login': 'launch_admin', 'password': password}).encode()) as response:
                    assert '/login' not in response.url, mode + ' login failed'
                checks = [('/ui/catalog/LaunchProbe', 'ob-list-panels'),
                          ('/ui/catalog/LaunchProbe/new', 'ob-density-dense'),
                          ('/ui/report/LaunchReport', 'ob-report-compact')]
                for path, marker in checks:
                    with client.open(base + path) as response:
                        html = response.read().decode('utf-8')
                    assert marker in html, mode + ': missing ' + marker
                    assert '/static/date-input.js' in html, mode + ': short date key ignored'
                    if path.endswith('/LaunchProbe'):
                        assert 'data-ob-detail-toggle' not in html, mode + ': details switch ignored'
                    results.append({'mode': mode, 'path': path, 'pass': True})
            finally:
                if process.poll() is None:
                    process.terminate()
                    try:
                        process.wait(timeout=8)
                    except subprocess.TimeoutExpired:
                        process.kill()
                        process.wait(timeout=5)
print(json.dumps({'cli_ui_config': 'PASS', 'cases': results, 'own_processes_stopped': True}))
