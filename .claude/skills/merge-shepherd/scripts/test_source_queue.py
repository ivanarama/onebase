"""Public CLI tests: disposable native DBs, no network and no real task execution."""
import hashlib
from contextlib import closing
import json
from pathlib import Path
import sqlite3
import subprocess
import sys
import tempfile
import unittest

CLI = Path(__file__).with_name("source_queue.py")


class SourceQueueTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.queue = self.root / "queue.sqlite3"
        self.native = self.root / "native.sqlite3"
        self.db = sqlite3.connect(self.native)
        self.addCleanup(self.db.close)
        self.db.executescript("""
            CREATE TABLE task_series(id INTEGER PRIMARY KEY,prompt TEXT,paused INTEGER,ended_at TEXT);
            CREATE TABLE tasks(id INTEGER PRIMARY KEY,series_id INTEGER,prompt TEXT,status TEXT,started_at TEXT,session_id TEXT);
            INSERT INTO task_series VALUES(4,'original template',0,NULL);
            INSERT INTO tasks VALUES(10,4,'running task special instructions','running','start','session');
            INSERT INTO tasks VALUES(11,4,'pending task special instructions','pending',NULL,NULL);
            INSERT INTO task_series VALUES(9,'unrelated template',0,NULL);
            INSERT INTO tasks VALUES(90,9,'unrelated task','pending',NULL,NULL);
        """)
        self.scope = self.root / "scope.json"
        self.scope.write_text('{"exact":"authored scope"}', encoding="utf-8")
        self.scope_hash = hashlib.sha256(self.scope.read_bytes()).hexdigest()
        self.head = "a" * 40
        self.key = "ivanarama/onebase#2243@" + self.head
        self.descriptor = self.root / "descriptor.json"
        self.descriptor.write_text(json.dumps({"repository":"ivanarama/onebase","number":2243,"source":self.head,
                                              "scope_path":str(self.scope),"scope_sha256":self.scope_hash}),encoding="utf-8")
        self.directive = self.root / "directive.txt"
        self.directive.write_text(f"Operator source {self.head}; read {self.scope}; SHA256 {self.scope_hash}. All canonical gates remain mandatory.",encoding="utf-8")
        self.directive_hash = hashlib.sha256(self.directive.read_bytes()).hexdigest()
        self.cli("register","--descriptor",str(self.descriptor))

    def cli(self, *args, ok=True):
        result = subprocess.run([sys.executable,"-X","utf8",str(CLI),"--queue",str(self.queue),*args],capture_output=True,text=True,encoding="utf-8",timeout=15)
        self.assertEqual(result.returncode,0 if ok else 1,result.stderr+result.stdout)
        value = json.loads(result.stdout)
        self.assertIs(value["mutation_authorized"],False)
        return value

    def deliver(self, ok=True):
        self.db.commit()
        return self.cli("deliver","--key",self.key,"--native-db",str(self.native),"--series","4","--directive",str(self.directive),"--directive-sha256",self.directive_hash,ok=ok)

    def snapshot(self):
        return {"series":self.db.execute("SELECT * FROM task_series ORDER BY id").fetchall(),"tasks":self.db.execute("SELECT * FROM tasks ORDER BY id").fetchall()}

    def test_no_pending_waits_without_touching_template_then_recovers(self):
        self.db.execute("DELETE FROM tasks WHERE id=11")
        before = self.snapshot()
        self.assertEqual(self.deliver()["reason"],"actual-unstarted-pending-missing")
        self.assertEqual(self.snapshot(),before)
        self.assertEqual(self.cli("list")["sources"][0]["phase"],"awaiting-pending-authority")
        self.db.execute("INSERT INTO tasks VALUES(12,4,'new occurrence','pending',NULL,NULL)")
        self.assertEqual(self.deliver()["task_id"],12)
        self.assertEqual(self.cli("verify","--key",self.key)["task_id"],12)

    def test_actual_task_and_template_preserve_special_instructions_and_other_tasks(self):
        before = self.snapshot()
        self.deliver()
        after = self.snapshot()
        self.assertEqual(after["tasks"][0],before["tasks"][0])
        self.assertEqual(after["tasks"][2],before["tasks"][2])
        self.assertEqual(after["series"][1],before["series"][1])
        self.assertTrue(after["tasks"][1][2].startswith("pending task special instructions\n"))
        self.assertTrue(after["series"][0][1].startswith("original template\n"))

    def test_repeated_delivery_and_native_commit_before_receipt_are_idempotent(self):
        self.deliver()
        before = self.snapshot()
        self.deliver()
        self.assertEqual(self.snapshot(),before)
        with closing(sqlite3.connect(self.queue)) as queue:
            queue.execute("UPDATE sources SET phase='prepared',receipt=NULL")
            queue.commit()
        self.deliver()
        self.assertEqual(self.snapshot(),before)

    def test_started_or_session_bound_pending_refused(self):
        for field in ("started_at","session_id"):
            with self.subTest(field=field):
                self.db.execute(f"UPDATE tasks SET {field}='previous-attempt' WHERE id=11")
                before = self.snapshot()
                self.deliver(ok=False)
                self.assertEqual(self.snapshot(),before)
                self.db.execute(f"UPDATE tasks SET {field}=NULL WHERE id=11")

    def test_manual_pause_or_ended_series_refused(self):
        for field,value in (("paused",1),("ended_at","ended")):
            with self.subTest(field=field):
                self.db.execute(f"UPDATE task_series SET {field}=? WHERE id=4",(value,))
                before = self.snapshot()
                self.deliver(ok=False)
                self.assertEqual(self.snapshot(),before)
                self.db.execute(f"UPDATE task_series SET {field}=? WHERE id=4",(0 if field=="paused" else None,))

    def test_multiple_pending_refused(self):
        self.db.execute("INSERT INTO tasks VALUES(12,4,'second','pending',NULL,NULL)")
        before = self.snapshot()
        self.deliver(ok=False)
        self.assertEqual(self.snapshot(),before)

    def test_cas_rejection_rolls_back_template_and_retains_source(self):
        self.db.executescript("CREATE TRIGGER refuse BEFORE UPDATE ON tasks BEGIN SELECT RAISE(IGNORE); END;")
        before = self.snapshot()
        self.deliver(ok=False)
        self.assertEqual(self.snapshot(),before)
        self.assertEqual(self.cli("list")["sources"][0]["phase"],"prepared")

    def test_changed_scope_or_directive_refused(self):
        before = self.snapshot()
        self.scope.write_text("changed",encoding="utf-8")
        self.deliver(ok=False)
        self.assertEqual(self.snapshot(),before)
        self.scope.write_text('{"exact":"authored scope"}',encoding="utf-8")
        self.directive.write_text("changed",encoding="utf-8")
        self.deliver(ok=False)
        self.assertEqual(self.snapshot(),before)

    def test_incomplete_marker_refused(self):
        self.deliver()
        self.db.execute("UPDATE tasks SET prompt=substr(prompt,1,length(prompt)-10) WHERE id=11")
        before = self.snapshot()
        self.deliver(ok=False)
        self.assertEqual(self.snapshot(),before)

    def test_verify_rejects_started_task_and_late_pause(self):
        self.deliver()
        self.db.execute("UPDATE tasks SET status='running',started_at='new-start' WHERE id=11")
        self.db.commit()
        self.cli("verify","--key",self.key,ok=False)
        self.db.execute("UPDATE tasks SET status='pending',started_at=NULL WHERE id=11")
        self.db.execute("UPDATE task_series SET paused=1 WHERE id=4")
        self.db.commit()
        self.cli("verify","--key",self.key,ok=False)

    def test_other_source_scope_is_not_overwritten(self):
        payload = json.loads(self.descriptor.read_text())
        payload["scope_sha256"] = "b"*64
        self.descriptor.write_text(json.dumps(payload),encoding="utf-8")
        self.cli("register","--descriptor",str(self.descriptor),ok=False)
        self.assertEqual(len(self.cli("list")["sources"]),1)

    def test_terminal_source_does_not_replay(self):
        self.deliver()
        evidence = self.root / "superseded.json"
        evidence.write_text('{"new_head":"different; requires new source gates"}',encoding="utf-8")
        evidence_hash = hashlib.sha256(evidence.read_bytes()).hexdigest()
        self.cli("record","--key",self.key,"--phase","superseded","--evidence",str(evidence),"--evidence-sha256",evidence_hash)
        before = self.snapshot()
        self.deliver(ok=False)
        self.assertEqual(self.snapshot(),before)

    def test_released_source_is_not_reset_to_delivery(self):
        self.deliver()
        evidence = self.root / "release.json"
        evidence.write_text('{"verified_unpark_receipt":"operator-only observation"}',encoding="utf-8")
        evidence_hash = hashlib.sha256(evidence.read_bytes()).hexdigest()
        self.cli("record","--key",self.key,"--phase","released-to-merge","--evidence",str(evidence),"--evidence-sha256",evidence_hash)
        before = self.snapshot()
        self.assertEqual(self.deliver()["reason"],"released-to-merge")
        self.assertEqual(self.snapshot(),before)
        self.assertEqual(self.cli("list")["sources"][0]["phase"],"released-to-merge")

    def test_malformed_source_types_return_structured_error(self):
        payload = json.loads(self.descriptor.read_text())
        payload["source"] = 12
        self.descriptor.write_text(json.dumps(payload),encoding="utf-8")
        self.cli("register","--descriptor",str(self.descriptor),ok=False)

    def test_different_directive_cannot_retarget_delivered_source(self):
        self.deliver()
        before = self.snapshot()
        self.directive.write_text(self.directive.read_text()+"\nNew authority",encoding="utf-8")
        self.directive_hash = hashlib.sha256(self.directive.read_bytes()).hexdigest()
        self.deliver(ok=False)
        self.assertEqual(self.snapshot(),before)

    def test_native_commit_crash_preserves_pin_and_refuses_retarget(self):
        with closing(sqlite3.connect(self.queue)) as queue:
            queue.executescript("CREATE TRIGGER crash_receipt BEFORE UPDATE ON sources WHEN NEW.phase='delivered' BEGIN SELECT RAISE(FAIL,'simulated crash after native commit'); END;")
        self.deliver(ok=False)
        native_after_commit = self.snapshot()
        self.assertIn(self.directive.read_text(),native_after_commit["tasks"][1][2])
        self.assertEqual(self.cli("list")["sources"][0]["phase"],"prepared")
        with closing(sqlite3.connect(self.queue)) as queue:
            queue.execute("DROP TRIGGER crash_receipt")
            queue.commit()
        original = self.directive.read_text()
        self.directive.write_text(original+"\nchanged after crash",encoding="utf-8")
        self.directive_hash = hashlib.sha256(self.directive.read_bytes()).hexdigest()
        self.deliver(ok=False)
        self.assertEqual(self.snapshot(),native_after_commit)
        self.directive.write_text(original,encoding="utf-8")
        self.directive_hash = hashlib.sha256(self.directive.read_bytes()).hexdigest()
        self.deliver()
        self.assertEqual(self.snapshot(),native_after_commit)

    def test_late_prompt_edit_outside_block_refuses_verification(self):
        self.deliver()
        self.db.execute("UPDATE tasks SET prompt='do not execute\n'||prompt WHERE id=11")
        self.db.commit()
        self.cli("verify","--key",self.key,ok=False)

    def test_late_series_edit_outside_block_refuses_verification(self):
        self.deliver()
        self.db.execute("UPDATE task_series SET prompt=prompt||' new owner decision' WHERE id=4")
        self.db.commit()
        self.cli("verify","--key",self.key,ok=False)


if __name__ == "__main__":
    unittest.main()
