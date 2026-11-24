"""Exercise the user card and the same HTTPS Negotiate exchange as the web UI."""

import base64
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile

import gssapi
import requests


ORIGIN = "https://erp.onebase.test"
SPNEGO = gssapi.OID.from_int_seq("1.3.6.1.5.5.2")


def browser(certificate):
    session = requests.Session()
    session.verify = str(certificate)
    session.trust_env = False
    session.headers["Origin"] = ORIGIN
    return session


def domain_login(user, certificate, work, expected):
    ccache = f"FILE:{work}/{user}.ccache"
    subprocess.run(["kinit", "-c", ccache, user + "@ONEBASE.TEST"],
                   input=os.environ["TEST_AD_PASSWORD"] + "\n", text=True,
                   stdout=subprocess.DEVNULL, check=True, timeout=15)
    creds = gssapi.Credentials(usage="initiate", store={"ccache": ccache})
    service = gssapi.Name("HTTP@erp.onebase.test", gssapi.NameType.hostbased_service)
    context = gssapi.SecurityContext(name=service, creds=creds, mech=SPNEGO,
                                     flags=[gssapi.RequirementFlag.mutual_authentication])
    session = browser(certificate)
    session.headers["X-OneBase-Windows"] = "1"
    endpoint = ORIGIN + "/auth/windows"
    challenge = session.get(endpoint, allow_redirects=False, timeout=10)
    assert challenge.status_code == 401 and challenge.headers["WWW-Authenticate"] == "Negotiate"
    request = base64.b64encode(context.step()).decode("ascii")
    response = session.get(endpoint, headers={"Authorization": "Negotiate " + request},
                           allow_redirects=False, timeout=15)
    assert response.status_code == expected, f"{user}: expected {expected}, received {response.status_code}"
    if expected == 200:
        scheme, reply = response.headers["WWW-Authenticate"].split(" ", 1)
        assert scheme == "Negotiate"
        context.step(base64.b64decode(reply, validate=True))
        assert context.complete and response.json()["redirect"] == "/ui"
        about = session.get(ORIGIN + "/ui/about", allow_redirects=False, timeout=10)
        assert about.status_code == 200 and ">operator</span>" in about.text
        assert session.get(ORIGIN + "/ui/admin/users", timeout=10).status_code == 403
    else:
        assert "onebase_session" not in session.cookies, f"{user}: unexpectedly received a session"


def check(work):
    certificate = work / "ca.pem"
    certificate.write_bytes(sys.stdin.buffer.read())
    config = work / "krb5.conf"
    config.write_text("""[libdefaults]
 default_realm = ONEBASE.TEST
 dns_canonicalize_hostname = false
 rdns = false
[realms]
 ONEBASE.TEST = {
    kdc = dc.onebase.test
 }
[domain_realm]
 .onebase.test = ONEBASE.TEST
""")
    os.environ["KRB5_CONFIG"] = str(config)

    admin = browser(certificate)
    login = admin.post(ORIGIN + "/auth/login",
                       json={"login": "admin", "password": os.environ["TEST_ADMIN_PASSWORD"]}, timeout=10)
    login.raise_for_status()
    listing = admin.get(ORIGIN + "/ui/admin/users", timeout=10)
    listing.raise_for_status()
    card = re.search(r'<a href="(/ui/admin/users/[\w-]+)"[^>]*>operator</a>', listing.text)
    assert card, "operator is missing from the user list"
    url = ORIGIN + card[1]
    bound = admin.post(url, data={"action": "windows_account", "windows_enabled": "1",
                                  "windows_account": r"ONEBASE\employee"}, timeout=15)
    bound.raise_for_status()
    persisted = admin.get(url, timeout=10)
    assert r"onebase\employee" in persisted.text.lower(), "user card did not save the Windows account"

    domain_login("employee", certificate, work, 200)
    domain_login("colleague", certificate, work, 403)
    admin.post(url, data={"action": "account_status", "disabled": "1"}, timeout=10).raise_for_status()
    domain_login("employee", certificate, work, 403)
    admin.post(url, data={"action": "account_status"}, timeout=10).raise_for_status()
    domain_login("employee", certificate, work, 200)
    admin.post(url, data={"action": "windows_account"}, timeout=10).raise_for_status()
    domain_login("employee", certificate, work, 403)
    print("Ubuntu accepted the bound domain identity, enforced operator permissions, "
          "and denied unbound and disabled users.")


if __name__ == "__main__":
    with tempfile.TemporaryDirectory() as directory:
        check(Path(directory))
