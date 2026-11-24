"""Replay the command used by the release workflow, without modifying a release."""
import contextlib
import io
import json
import os
from pathlib import Path
import runpy
import subprocess
import unittest
from unittest.mock import patch


SCRIPT = Path(__file__).with_name('build-notes.py')
REPO = 'example/onebase'
BASE, DIRECT, SQUASH, TOPIC, SYNC, MERGE = [c * 40 for c in 'abcdef']
MESSAGE = 'Merge pull request #2722 from contributor/feature\n\nСравнение значений'


class BuildNotesTest(unittest.TestCase):
    def render(self, releases, pages=None):
        def invoke(args, **kwargs):
            if args[0] == 'git':
                self.assertEqual(args, ('git', 'show', '-s', '--format=%B', MERGE))
                return MESSAGE
            if args[2].startswith(f'repos/{REPO}/releases?'):
                index = int(args[2].split('&page=')[1]) - 1
                return json.dumps(releases[index])
            self.assertEqual(args[:4], ('gh', 'api', '--paginate',
                                        f'repos/{REPO}/compare/build-1686...{MERGE}?per_page=100'))
            if pages is None:
                raise subprocess.CalledProcessError(1, 'gh api')
            return '\n'.join(json.dumps(page) for page in pages)

        env = {'GITHUB_REPOSITORY': REPO, 'GITHUB_SHA': MERGE, 'GITHUB_RUN_NUMBER': '1687',
               'GITHUB_SERVER_URL': 'https://github.com'}
        out, err = io.StringIO(), io.StringIO()
        with patch.dict(os.environ, env), patch('subprocess.check_output', side_effect=invoke), \
                contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            runpy.run_path(str(SCRIPT), run_name='__main__')
        self.assertEqual(out.getvalue().count('<!-- summary:'), 1)
        return out.getvalue(), err.getvalue()

    def test_build_lists_each_main_change_once_across_pages(self):
        releases = [[{'tag_name': 'build-1687', 'draft': False}] * 100,
                    [{'tag_name': 'build-1686', 'draft': False}]]
        pages = [[BASE, [[DIRECT, BASE, 'fix(ui): Прямая правка'],
                         [TOPIC, BASE, 'feat(ui): Реализация'],
                         [SYNC, TOPIC, 'chore(pr): синхронизировать']]],
                 [BASE, [[SQUASH, DIRECT, 'fix(cli): Исправить чтение (#122)'], [MERGE, SQUASH, MESSAGE]]]]
        body, err = self.render(releases, pages)
        self.assertFalse(err)
        bullets = [line for line in body.splitlines() if line.startswith('- ')]
        self.assertEqual(len(bullets), 3)
        self.assertIn(f'/commit/{DIRECT}', bullets[0])
        self.assertIn('Исправить чтение ([#122]', bullets[1])
        self.assertIn('Сравнение значений ([#2722]', bullets[2])
        self.assertNotIn('синхронизировать', body)
        self.assertNotIn('Реализация', body)
        self.assertNotIn('Merge pull request', body)
        self.assertIn('/compare/build-1686...build-1687', body)

    def test_no_previous_build(self):
        body, err = self.render([[{'tag_name': 'build-1686', 'draft': True},
                                  {'tag_name': 'v0.1.0', 'draft': False}]])
        self.assertFalse(err)
        self.assertIn('Сравнение значений ([#2722]', body)
        self.assertNotIn('/compare/', body)

    def test_empty_range(self):
        body, err = self.render([[{'tag_name': 'build-1686', 'draft': False}]], [[MERGE, []]])
        self.assertFalse(err)
        self.assertIn('Повторная сборка', body)
        self.assertNotIn('#2722', body)

    def test_unknown_range_keeps_last_change_and_link(self):
        releases = [[{'tag_name': 'build-1686', 'draft': False}]]
        for pages in (None, [[BASE, [[MERGE, SYNC, MESSAGE]]]]):
            with self.subTest(pages=pages):
                body, err = self.render(releases, pages)
                self.assertIn('::warning::', err)
                self.assertIn('Последнее изменение', body)
                self.assertIn('/compare/', body)
                self.assertIn('Сравнение значений ([#2722]', body)


if __name__ == '__main__':
    unittest.main()
