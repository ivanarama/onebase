FROM ubuntu:24.04
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    samba-ad-dc samba-ad-provision samba-dsdb-modules samba-vfs-modules winbind \
    krb5-user python3-gssapi python3-samba python3-requests nginx-light openssl curl \
    && rm -rf /var/lib/apt/lists/*
COPY server.sh configure.py client.py /test/
