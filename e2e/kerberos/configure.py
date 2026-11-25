"""Save the test domain settings using the server administrator's local Configurator."""

from html.parser import HTMLParser
import os
import re
import subprocess

import requests


class Controls(HTMLParser):
    def __init__(self, html):
        super().__init__()
        self.inputs = {}
        self.accounts = {}
        self.feed(html)

    def handle_starttag(self, tag, attrs):
        fields = dict(attrs)
        if tag == "input" and "name" in fields:
            self.inputs[fields["name"]] = fields.get("value")
        if tag == "button" and "data-login" in fields and "data-account" in fields:
            self.accounts[fields["data-login"]] = fields["data-user-id"]


def configure(session, origin, base_id):
    session.headers["Origin"] = origin
    url = f"{origin}/bases/{base_id}/configurator"
    login = session.post(url + "/login",
                         data={"login": "admin", "password": os.environ["TEST_ADMIN_PASSWORD"]},
                         allow_redirects=False, timeout=10)
    assert login.status_code == 302 and "onebase_launcher_session" in session.cookies
    settings = {
        "public_url": "https://erp.onebase.test",
        "spn": "HTTP/erp.onebase.test",
        "realm": "ONEBASE.TEST",
        "keytab": "/tmp/erp.keytab",
    }
    saved = session.post(url + "/admin/settings/domain", json=settings,
                         allow_redirects=False, timeout=10)
    saved.raise_for_status()
    assert saved.json()["ok"] is True
    form = session.get(url + "/admin/settings", allow_redirects=False, timeout=10)
    form.raise_for_status()
    inputs = Controls(form.text).inputs
    for name, value in settings.items():
        assert inputs.get(name) == value, f"Configurator did not reload {name} from the base"

    users = session.get(url + "/admin/users", allow_redirects=False, timeout=10)
    users.raise_for_status()
    operator = Controls(users.text).accounts.get("operator")
    assert operator, "Configurator did not offer a Windows binding for operator"
    bound = session.post(url + "/admin/users/windows-account",
                         json={"id": operator, "enabled": True, "account": r"ONEBASE\employee"},
                         allow_redirects=False, timeout=15)
    bound.raise_for_status()
    assert bound.json()["ok"] is True


def main():
    added = subprocess.check_output(["onebase", "ibases", "add", "--name", "DomainTest",
                                     "--source", "file", "--path", "/tmp/project",
                                     "--sqlite", "/tmp/base.db", "--port", "8080"], text=True)
    base = re.search(r"added: DomainTest \(([a-f0-9-]+)\)", added)
    assert base, "launcher did not register the test base"
    with subprocess.Popen(["onebase", "start"], env={**os.environ, "ONEBASE_NO_GUI": "1"},
                          stdout=subprocess.PIPE, text=True) as launcher, requests.Session() as session:
        try:
            session.trust_env = False
            prefix = "Лаунчер доступен по адресу: "
            entry = next(line[len(prefix):].strip() for line in launcher.stdout if line.startswith(prefix))
            home = session.get(entry, timeout=10)
            home.raise_for_status()
            origin = home.url.rstrip("/")
            configure(session, origin, base[1])
            session.post(origin + "/quit", timeout=10).raise_for_status()
            assert launcher.wait(timeout=10) == 0
        finally:
            if launcher.poll() is None:
                launcher.kill()


if __name__ == "__main__":
    main()
