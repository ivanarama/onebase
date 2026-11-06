"""Durable operator worklist. This tool never calls GitHub or grants merge authority."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import sqlite3
import sys


class QueueError(RuntimeError):
    pass


def digest(data):
    return hashlib.sha256(data).hexdigest()


def connect(path):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    db = sqlite3.connect(path, timeout=10, isolation_level=None)
    path.chmod(0o600)
    db.row_factory = sqlite3.Row
    db.execute("PRAGMA busy_timeout=10000")
    db.execute("CREATE TABLE IF NOT EXISTS sources (key TEXT PRIMARY KEY, descriptor TEXT NOT NULL, phase TEXT NOT NULL, receipt TEXT)")
    db.execute("CREATE TABLE IF NOT EXISTS observations (key TEXT NOT NULL, phase TEXT NOT NULL, evidence_path TEXT NOT NULL, evidence_sha256 TEXT NOT NULL, PRIMARY KEY(key,phase))")
    db.execute("CREATE TABLE IF NOT EXISTS delivery_intents (key TEXT PRIMARY KEY, pin TEXT NOT NULL)")
    db.execute("CREATE TABLE IF NOT EXISTS delivery_targets (key TEXT PRIMARY KEY, receipt TEXT NOT NULL)")
    return db


def descriptor(path):
    data = json.loads(Path(path).read_text(encoding="utf-8"))
    if (not isinstance(data,dict) or set(data) != {"repository", "number", "source", "scope_path", "scope_sha256"}
            or data["repository"] != "ivanarama/onebase"
            or type(data["number"]) is not int or data["number"] <= 0
            or not all(isinstance(data[field], str) for field in ("source","scope_path","scope_sha256"))
            or not re.fullmatch(r"[0-9a-f]{40}", data["source"])
            or not re.fullmatch(r"[0-9a-f]{64}", data["scope_sha256"])
            or not Path(data["scope_path"]).is_absolute()):
        raise QueueError("invalid exact-source descriptor")
    scope = Path(data["scope_path"])
    if scope.is_symlink() or not scope.is_file() or digest(scope.read_bytes()) != data["scope_sha256"]:
        raise QueueError("immutable authored scope changed or is missing")
    return data


def key_of(data):
    return f'{data["repository"]}#{data["number"]}@{data["source"]}'


def canonical(data):
    return json.dumps(data, ensure_ascii=False, sort_keys=True, separators=(",", ":"))


def register(db, data):
    key = key_of(data)
    old = db.execute("SELECT descriptor FROM sources WHERE key=?", (key,)).fetchone()
    if old is not None and old[0] != canonical(data):
        raise QueueError("source is already bound to a different scope; preserve its receipt")
    db.execute("INSERT OR IGNORE INTO sources VALUES (?,?,'prepared',NULL)", (key, canonical(data)))
    return {"action": "registered", "key": key, "mutation_authorized": False}


def read_entry(db, key):
    row = db.execute("SELECT * FROM sources WHERE key=?", (key,)).fetchone()
    if row is None:
        raise QueueError("unknown exact source")
    return row


def read_scope(row):
    data = json.loads(row["descriptor"])
    scope = Path(data["scope_path"])
    if scope.is_symlink() or not scope.is_file() or digest(scope.read_bytes()) != data["scope_sha256"]:
        raise QueueError("immutable authored scope changed or is missing")
    return data


def deliver(db, key, native_path, series_id, directive_path, expected_digest):
    """CAS append to an actual unstarted occurrence and its template, or persist WAIT.

    The exact task and full before/after prompt hashes are durable before native
    writes. Recovery checks that same target; it never elects a replacement.
    A receipt alone never permits a GitHub write; the caller must
    repeat all canonical source/proof/ownership/admission gates after delivery.
    """
    if series_id <= 0:
        raise QueueError("invalid native series")
    directive_path = Path(directive_path)
    if directive_path.is_symlink() or not directive_path.is_file():
        raise QueueError("missing regular directive")
    raw = directive_path.read_bytes()
    if not re.fullmatch(r"[0-9a-f]{64}", expected_digest) or digest(raw) != expected_digest:
        raise QueueError("directive hash differs")
    text = raw.decode("utf-8", errors="strict")
    if not text.strip() or "\x00" in text:
        raise QueueError("empty or invalid directive")
    native_path = Path(native_path)
    if not native_path.is_absolute() or not native_path.is_file() or native_path.is_symlink():
        raise QueueError("missing regular native database")
    db.execute("BEGIN IMMEDIATE")
    native = None
    try:
        row = read_entry(db, key)
        data = read_scope(row)
        if row["phase"] in {"superseded", "merged"}:
            raise QueueError("source is terminal; do not replay")
        if row["phase"] not in {"prepared","awaiting-pending-authority","delivered"}:
            db.commit()
            return {"action":"wait", "reason":row["phase"], "key":key, "mutation_authorized":False}
        if data["scope_path"] not in text or data["scope_sha256"] not in text or data["source"] not in text:
            raise QueueError("directive does not bind the exact scope/hash/source")
        marker = "source-queue-v1:" + digest((key + "\n" + expected_digest).encode())
        block = f"\n\n<!-- {marker} -->\n{text}\n<!-- /{marker} -->\n"
        # A legacy unbound intent cannot distinguish pre-target from post-commit
        # crashes. Never adopt it as fresh delivery authority.
        pin = canonical({"target_binding_version":1, "series_id":series_id, "native_path":str(native_path),
                         "directive_sha256":expected_digest, "scope_sha256":data["scope_sha256"], "block":block})
        intent = db.execute("SELECT pin FROM delivery_intents WHERE key=?", (key,)).fetchone()
        if intent is not None and intent[0] != pin:
            raise QueueError("durable delivery intent differs; do not retarget")
        db.execute("INSERT OR IGNORE INTO delivery_intents VALUES (?,?)", (key,pin))
        # This immutable pin must survive a crash after native.commit(), even
        # when the later local receipt UPDATE fails or its transaction rolls back.
        db.commit()
        native = sqlite3.connect(f"file:{native_path.as_posix()}?mode=rw", uri=True, timeout=10, isolation_level=None)
        native.row_factory = sqlite3.Row
        # All deliverers take the native lock before reacquiring the queue lock.
        # Persisting the target below therefore cannot deadlock another deliverer
        # holding the queue lock while waiting for this native transaction.
        native.execute("BEGIN IMMEDIATE")
        db.execute("BEGIN IMMEDIATE")
        row = read_entry(db, key)
        read_scope(row)
        if row["phase"] not in {"prepared","awaiting-pending-authority","delivered"}:
            raise QueueError("source phase changed after delivery intent")
        previous = json.loads(row["receipt"]) if row["receipt"] else None
        if previous and (previous["series_id"] != series_id or previous["directive_sha256"] != expected_digest
                         or previous["native_path"] != str(native_path)):
            raise QueueError("source delivery is already pinned; do not retarget")
        target = db.execute("SELECT receipt FROM delivery_targets WHERE key=?", (key,)).fetchone()
        receipt = json.loads(target[0]) if target is not None else None
        if previous and (receipt is None or previous != receipt):
            raise QueueError("delivery receipt lacks its original durable target or differs")
        series = native.execute("SELECT id,prompt,paused,ended_at FROM task_series WHERE id=?", (series_id,)).fetchone()
        if series is None or series["paused"] or series["ended_at"] is not None:
            raise QueueError("native series is paused, ended or missing")
        pending = native.execute("SELECT id,prompt,status,started_at,session_id FROM tasks WHERE series_id=? AND status IN ('pending','rate_limited') ORDER BY id", (series_id,)).fetchall()
        if receipt and (len(pending) != 1 or pending[0]["id"] != receipt["task_id"]):
            raise QueueError("original native occurrence changed or started; do not retarget")
        if not pending:
            native.rollback()
            db.execute("UPDATE sources SET phase='awaiting-pending-authority' WHERE key=?", (key,))
            db.commit()
            return {"action": "wait", "reason": "actual-unstarted-pending-missing", "key": key, "mutation_authorized": False}
        if len(pending) != 1 or pending[0]["started_at"] is not None or pending[0]["session_id"] is not None:
            raise QueueError("ambiguous or previously started native occurrence")
        task = pending[0]
        before = {"series_prompt_sha256": digest(series["prompt"].encode()), "task_prompt_sha256": digest(task["prompt"].encode())}
        for kind, prompt in (("series", series["prompt"]), ("task", task["prompt"])):
            if marker in prompt and prompt.count(block) != 1:
                raise QueueError(f"{kind} directive marker is incomplete or duplicated")
        if receipt is None:
            if marker in series["prompt"] or marker in task["prompt"]:
                raise QueueError("existing directive has no durable target; do not adopt it")
            receipt = {"series_id":series_id, "task_id":task["id"], "native_path":str(native_path),
                       "scope_sha256":data["scope_sha256"], "directive_sha256":expected_digest,
                       "block":block, "before":before,
                       "after":{"series_prompt_sha256":digest((series["prompt"]+block).encode()),
                                "task_prompt_sha256":digest((task["prompt"]+block).encode())}}
            db.execute("INSERT INTO delivery_targets VALUES (?,?)", (key,canonical(receipt)))
        if (receipt["series_id"] != series_id or receipt["native_path"] != str(native_path)
                or receipt["scope_sha256"] != data["scope_sha256"]
                or receipt["directive_sha256"] != expected_digest or receipt["block"] != block
                or before not in (receipt["before"], receipt["after"])
                or (previous is not None and before != receipt["after"])):
            raise QueueError("original full native prompts changed; preserve delivery fingerprints")
        # Survives even a crash before native commit. The native write lock keeps
        # both prompts and occurrence state fixed while this binding is saved.
        db.commit()
        db.execute("BEGIN IMMEDIATE")
        row = read_entry(db, key)
        read_scope(row)
        if row["phase"] not in {"prepared","awaiting-pending-authority","delivered"}:
            raise QueueError("source phase changed after target binding")
        if block not in series["prompt"]:
            changed = native.execute("UPDATE task_series SET prompt=? WHERE id=? AND prompt=? AND paused=0 AND ended_at IS NULL",
                                     (series["prompt"] + block, series_id, series["prompt"]))
            if changed.rowcount != 1:
                raise QueueError("native series CAS lost")
        if block not in task["prompt"]:
            changed = native.execute("UPDATE tasks SET prompt=? WHERE id=? AND prompt=? AND status=? AND started_at IS NULL AND session_id IS NULL",
                                     (task["prompt"] + block, task["id"], task["prompt"], task["status"]))
            if changed.rowcount != 1:
                raise QueueError("native occurrence CAS lost")
        read_scope(row)
        native.commit()
        # Publish only the original durable receipt, including during recovery.
        db.execute("UPDATE sources SET phase='delivered',receipt=? WHERE key=?", (canonical(receipt), key))
        db.commit()
        return {"action":"delivered", "key":key, "task_id":task["id"], "series_id":series_id, "mutation_authorized":False}
    except BaseException:
        if native is not None:
            native.rollback()
        db.rollback()
        raise
    finally:
        if native is not None:
            native.close()


def verify(db, key):
    row = read_entry(db, key)
    read_scope(row)
    if row["phase"] != "delivered" or not row["receipt"]:
        raise QueueError("no completed native delivery")
    receipt = json.loads(row["receipt"])
    target = db.execute("SELECT receipt FROM delivery_targets WHERE key=?", (key,)).fetchone()
    if target is None or target[0] != canonical(receipt):
        raise QueueError("delivery receipt lacks its original durable target or differs")
    native = sqlite3.connect(f'file:{Path(receipt["native_path"]).as_posix()}?mode=ro', uri=True)
    try:
        series = native.execute("SELECT prompt,paused,ended_at FROM task_series WHERE id=?", (receipt["series_id"],)).fetchone()
        task = native.execute("SELECT prompt,status,started_at,session_id,series_id FROM tasks WHERE id=?", (receipt["task_id"],)).fetchone()
        if (series is None or series[1] or series[2] is not None or task is None
                or task[1] not in {"pending","rate_limited"} or task[2] is not None or task[3] is not None
                or task[4] != receipt["series_id"]
                or digest(series[0].encode()) != receipt["after"]["series_prompt_sha256"]
                or digest(task[0].encode()) != receipt["after"]["task_prompt_sha256"]
                or series[0].count(receipt["block"]) != 1 or task[0].count(receipt["block"]) != 1):
            raise QueueError("actual delivery changed, started, paused or disappeared; repeat normal admission")
        return {"action":"verified", "key":key, "task_id":receipt["task_id"], "mutation_authorized":False}
    finally:
        native.close()


def record(db, key, phase, evidence_path, expected_digest):
    """Record an operator observation, never a proof or mutation permission."""
    evidence_path = Path(evidence_path)
    if (not evidence_path.is_absolute() or evidence_path.is_symlink() or not evidence_path.is_file()
            or not re.fullmatch(r"[0-9a-f]{64}", expected_digest)
            or digest(evidence_path.read_bytes()) != expected_digest):
        raise QueueError("immutable observation evidence differs")
    db.execute("BEGIN IMMEDIATE")
    try:
        row = read_entry(db, key)
        read_scope(row)
        old = db.execute("SELECT * FROM observations WHERE key=? AND phase=?", (key,phase)).fetchone()
        if old is not None:
            if old["evidence_path"] != str(evidence_path) or old["evidence_sha256"] != expected_digest:
                raise QueueError("existing phase evidence cannot be replaced")
            db.commit()
            return {"action":"recorded", "key":key, "phase":row["phase"], "mutation_authorized":False}
        transitions = {"delivered":"released-to-merge", "released-to-merge":"waiting-review-ci", "waiting-review-ci":"merged"}
        if phase != "superseded" and transitions.get(row["phase"]) != phase:
            raise QueueError("invalid phase transition")
        if row["phase"] in {"merged","superseded"}:
            raise QueueError("terminal source cannot be changed")
        db.execute("INSERT INTO observations VALUES (?,?,?,?)", (key,phase,str(evidence_path),expected_digest))
        db.execute("UPDATE sources SET phase=? WHERE key=?", (phase,key))
        db.commit()
        return {"action":"recorded", "key":key, "phase":phase, "mutation_authorized":False}
    except BaseException:
        db.rollback()
        raise


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--queue", type=Path, required=True)
    commands = parser.add_subparsers(dest="command", required=True)
    reg = commands.add_parser("register")
    reg.add_argument("--descriptor", type=Path, required=True)
    send = commands.add_parser("deliver")
    send.add_argument("--key", required=True)
    send.add_argument("--native-db", type=Path, required=True)
    send.add_argument("--series", type=int, required=True)
    send.add_argument("--directive", type=Path, required=True)
    send.add_argument("--directive-sha256", required=True)
    check = commands.add_parser("verify")
    check.add_argument("--key", required=True)
    observation = commands.add_parser("record")
    observation.add_argument("--key", required=True)
    observation.add_argument("--phase", choices=("released-to-merge","waiting-review-ci","merged","superseded"), required=True)
    observation.add_argument("--evidence", type=Path, required=True)
    observation.add_argument("--evidence-sha256", required=True)
    commands.add_parser("list")
    args = parser.parse_args()
    db = connect(args.queue)
    try:
        if args.command == "register":
            result = register(db, descriptor(args.descriptor))
        elif args.command == "deliver":
            result = deliver(db, args.key, args.native_db, args.series, args.directive, args.directive_sha256)
        elif args.command == "verify":
            result = verify(db, args.key)
        elif args.command == "record":
            result = record(db, args.key, args.phase, args.evidence, args.evidence_sha256)
        else:
            result = {"action":"observed", "mutation_authorized":False, "sources":[
                {"key":r["key"], "phase":r["phase"], "descriptor":json.loads(r["descriptor"])}
                for r in db.execute("SELECT * FROM sources ORDER BY rowid")]}
        print(json.dumps(result, ensure_ascii=False))
    except (QueueError, ValueError, OSError, sqlite3.Error) as error:
        print(json.dumps({"action":"error", "error":str(error), "mutation_authorized":False},ensure_ascii=False))
        return 1
    finally:
        db.close()
    return 0


if __name__ == "__main__":
    sys.exit(main())
