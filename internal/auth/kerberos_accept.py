"""Accept one HTTP Negotiate exchange using Ubuntu's system Kerberos libraries."""

import re
import sys

try:
    import gssapi
    from samba.dcerpc import krb5pac
    from samba.ndr import ndr_unpack
except ImportError:
    sys.exit(2)


def accept(spn, realm, keytab, token):
    kerberos = gssapi.MechType.kerberos
    spnego = gssapi.OID.from_int_seq("1.3.6.1.5.5.2")
    principal = f"{spn}@{realm}"
    service = gssapi.Name(principal, name_type=gssapi.NameType.kerberos_principal)
    try:
        creds = gssapi.Credentials(
            usage="accept", name=service, mechs=[kerberos, spnego],
            store={"keytab": "FILE:" + keytab, "rcache": "dfl:"},
        )
    except gssapi.exceptions.GSSError:
        sys.exit(3)
    gssapi.raw.set_neg_mechs(creds, [kerberos])
    context = gssapi.SecurityContext(creds=creds, usage="accept")
    reply = context.step(token) or b""
    if not context.complete or context.mech != kerberos:
        raise ValueError("incomplete Kerberos context")
    if str(context.target_name) != principal or context.delegated_creds is not None:
        raise ValueError("unexpected service or delegated credentials")
    initiator = context.initiator_name
    # Match the final, unescaped realm separator in a Kerberos principal.
    if not re.fullmatch(r"(?:\\.|[^@\\])+@" + re.escape(realm), str(initiator)):
        raise ValueError("unexpected client realm")

    # MIT verifies the PAC before marking this RFC 6680 attribute authenticated.
    # Recover the primary account SID from LOGON_INFO. An account with the same
    # name created later receives a new SID, and group/SID history entries do
    # not identify the user.
    attr = initiator.attributes[b"urn:mspac:"]
    if not attr.authenticated or not attr.complete or len(attr.values) != 1:
        raise ValueError("expected a verified AD PAC")
    pac = ndr_unpack(krb5pac.PAC_DATA, attr.values[0])
    accounts = [buf.info.info.info3.base for buf in pac.buffers
                if buf.type == krb5pac.PAC_TYPE_LOGON_INFO]
    if len(accounts) != 1:
        raise ValueError("expected one PAC logon record")
    account = accounts[0]
    return f"{account.domain_sid}-{account.rid}", reply


if __name__ == "__main__":
    sid, reply = accept(*sys.argv[1:], sys.stdin.buffer.read())
    sys.stdout.buffer.write(sid.encode("ascii") + b"\n" + reply)
