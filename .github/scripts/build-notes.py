"""Write the pre-release body from the changes that actually landed on main."""
import html
import json
import os
import re
import subprocess
import sys


def output(*args):
    return subprocess.check_output(args, encoding='utf-8').strip()


def previous_build(repository, number):
    page = 1
    while True:
        releases = json.loads(output(
            'gh', 'api', f'repos/{repository}/releases?per_page=100&page={page}',
            '--jq', 'map({tag_name, draft})'))
        numbers = []
        for release in releases:
            match = re.fullmatch(r'build-([0-9]+)', release['tag_name'])
            if match and not release['draft'] and int(match[1]) < number:
                numbers.append(int(match[1]))
        if numbers:
            return f'build-{max(numbers)}'
        if len(releases) < 100:
            return None
        page += 1


def mainline_changes(repository, previous, head):
    # Keep only the commit information needed to follow main, including across pages.
    pages = output('gh', 'api', '--paginate',
                   f'repos/{repository}/compare/{previous}...{head}?per_page=100',
                   '--jq', '[.base_commit.sha, [.commits[] | [.sha, .parents[0].sha, .commit.message]]] | @json')
    commits = {}
    base = None
    for line in pages.splitlines():
        base, rows = json.loads(line)
        commits.update((sha, (parent, message)) for sha, parent, message in rows)
    changes = []
    while head in commits:
        parent, message = commits[head]
        changes.append((head, message))
        head = parent
    if head != base:
        raise ValueError('previous build is outside the first-parent range')
    return reversed(changes)


def describe(sha, message, url):
    subject, _, body = message.partition('\n')
    merge = re.match(r'^Merge pull request #([1-9][0-9]*)\b', subject)
    squash = re.search(r' \(#([1-9][0-9]*)\)$', subject)
    if merge:
        title = next((line.strip() for line in body.splitlines() if line.strip()), 'Изменения в PR')
    elif squash:
        title = subject[:squash.start()]
    else:
        title = subject
    title = re.sub(r'^(?:feat|fix|docs|test|chore|refactor|perf|ci|build|style)(?:\([^)]*\))?!?:\s+', '', title)
    title = html.escape(title or sha[:7], quote=False)
    label = re.sub(r'([\\`*_\[\]~|])', r'\\\1', title)
    if merge or squash:
        number = (merge or squash)[1]
        return title, f'- {label} ([#{number}]({url}/pull/{number}))'
    return title, f'- {label} ([`{sha[:7]}`]({url}/commit/{sha}))'


def main():
    repo = os.environ['GITHUB_REPOSITORY']
    head = os.environ['GITHUB_SHA']
    number = int(os.environ['GITHUB_RUN_NUMBER'])
    url = f"{os.environ.get('GITHUB_SERVER_URL', 'https://github.com')}/{repo}"
    previous = None
    changes = [(head, output('git', 'show', '-s', '--format=%B', head))]
    heading = 'Последнее изменение'
    try:
        previous = previous_build(repo, number)
        if previous:
            changes = mainline_changes(repo, previous, head)
            heading = 'Что нового'
    except (subprocess.CalledProcessError, ValueError) as err:
        # A failed range lookup must not hold up binaries ready to publish.
        print(f'::warning::Не удалось получить диапазон сборок: {err}', file=sys.stderr)
    entries = [describe(sha, message, url) for sha, message in changes]
    summary = entries[-1][0] if entries else 'Повторная сборка того же коммита.'
    print(f'<!-- summary: {summary} -->\n\n## {heading}\n')
    print('\n'.join(entry for _, entry in entries) or summary)
    if previous:
        print(f'\n[Все коммиты с {previous}]({url}/compare/{previous}...build-{number})')
    print(f'\n## Сборка\n\nАвтосборка из ветки `main`.\nКоммит: `{head}`\nВерсия платформы: `build-{number}`')
    print(f'\nДля идентификации: в конфигураторе в шапке отображается `платформа build-{number}`.')


if __name__ == '__main__':
    main()
