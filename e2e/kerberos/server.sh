#!/usr/bin/env bash
# Only executed by the E2E Docker runner, inside its isolated server container.
set -euo pipefail
cd /tmp

# SYSVOL file ACLs serve Windows Group Policy. This fixture needs only the KDC
# and directory, so it can run with Docker's default capabilities.
python3 - <<'PY'
import logging
import secrets
import socket
from samba.auth import system_session
from samba.provision import provision

provision(logging.getLogger("fixture"), system_session(), targetdir="/tmp/ad",
          realm="ONEBASE.TEST", domain="ONEBASE", hostname="dc",
          hostip=socket.gethostbyname(socket.gethostname()),
          serverrole="active directory domain controller",
          dns_backend="SAMBA_INTERNAL", adminpass=secrets.token_urlsafe(36),
          skip_sysvolacl=True)
PY
ln -sf /tmp/ad/etc/smb.conf /etc/samba/smb.conf
samba-tool user create employee "$TEST_AD_PASSWORD"
samba-tool user create colleague "$TEST_AD_PASSWORD"
samba-tool user create svc-web --random-password
samba-tool spn add HTTP/erp.onebase.test svc-web
samba-tool domain exportkeytab /tmp/erp.keytab --principal=HTTP/erp.onebase.test@ONEBASE.TEST
chmod 600 /tmp/erp.keytab
klist -k /tmp/erp.keytab | grep -F 'HTTP/erp.onebase.test@ONEBASE.TEST'
samba --daemon

for ((i=0; i<45; i++)); do
    if wbinfo --name-to-sid 'ONEBASE\employee' >/dev/null; then break; fi
    sleep 1
done
wbinfo --name-to-sid 'ONEBASE\employee'

openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=erp.onebase.test \
    -addext subjectAltName=DNS:erp.onebase.test -keyout /tmp/erp.key -out /tmp/erp.crt 2>/dev/null
cat >/tmp/nginx.conf <<'NGINX'
events {}
http {
    access_log off;
    server {
        listen 443 ssl;
        server_name erp.onebase.test;
        ssl_certificate /tmp/erp.crt;
        ssl_certificate_key /tmp/erp.key;
        location / {
            proxy_pass http://127.0.0.1:8080;
            proxy_set_header Host $host;
            proxy_set_header X-Forwarded-Proto https;
        }
    }
}
NGINX
nginx -c /tmp/nginx.conf
onebase init project
db=(--project /tmp/project --sqlite /tmp/base.db)
onebase migrate "${db[@]}"
printf '%s' "$TEST_ADMIN_PASSWORD" | onebase user add admin --admin --password-stdin "${db[@]}"
printf '%s' "$(openssl rand -hex 32)Aa1!" | onebase user add operator --password-stdin "${db[@]}"
export ONEBASE_PUBLIC_URL=https://erp.onebase.test ONEBASE_WINDOWS_SPN=HTTP/erp.onebase.test
export ONEBASE_KRB5_REALM=ONEBASE.TEST ONEBASE_KRB5_KEYTAB=/tmp/erp.keytab
touch /tmp/domain-ready
exec onebase run --host 127.0.0.1 --port 8080 "${db[@]}"
