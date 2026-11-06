"""Public CLI tests: disposable native DBs, no network and no real task execution."""
import hashlib
from contextlib import closing
import json
import os
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

    def test_native_database_uri_uses_exact_file_and_verifies_that_file(self):
        names = ["native#selected.sqlite3", "native%23selected.sqlite3",
                 "native selected.sqlite3", "native-выбранная.sqlite3"]
        if os.name != "nt":  # '?' is not a valid Windows filename.
            names.append("native?selected.sqlite3")
        self.db.commit()
        for index, name in enumerate(names):
            with self.subTest(filename=name):
                directory = self.root / str(index)
                directory.mkdir()
                selected = directory / name
                decoy = directory / "native"
                for path in (selected, decoy):
                    with closing(sqlite3.connect(path)) as database:
                        self.db.backup(database)
                        if path == decoy:
                            database.execute("UPDATE task_series SET prompt='unrelated database template' WHERE id=4")
                            database.execute("UPDATE tasks SET prompt='unrelated database task' WHERE id=11")
                            database.commit()
                self.queue = directory / "queue.sqlite3"
                self.cli("register", "--descriptor", str(self.descriptor))
                decoy_before = decoy.read_bytes()
                with closing(sqlite3.connect(selected)) as database:
                    series_before = database.execute("SELECT * FROM task_series ORDER BY id").fetchall()
                    tasks_before = database.execute("SELECT * FROM tasks ORDER BY id").fetchall()
                    result = self.cli("deliver", "--key", self.key, "--native-db", str(selected),
                                      "--series", "4", "--directive", str(self.directive),
                                      "--directive-sha256", self.directive_hash)
                    self.assertEqual(result["action"], "delivered")
                    receipt = json.loads(self.saved_delivery())
                    self.assertEqual(receipt["native_path"], str(selected))
                    series_expected = [(r[0], r[1] + receipt["block"] if r[0] == 4 else r[1], *r[2:])
                                       for r in series_before]
                    tasks_expected = [(*r[:2], r[2] + receipt["block"] if r[0] == 11 else r[2], *r[3:])
                                      for r in tasks_before]
                    self.assertEqual(database.execute("SELECT * FROM task_series ORDER BY id").fetchall(), series_expected)
                    self.assertEqual(database.execute("SELECT * FROM tasks ORDER BY id").fetchall(), tasks_expected)
                    self.assertEqual(decoy.read_bytes(), decoy_before)
                    self.assertEqual(self.cli("verify", "--key", self.key)["action"], "verified")
                    database.execute("UPDATE tasks SET prompt='external edit' WHERE id=11")
                    database.commit()
                    selected_before_verify = selected.read_bytes()
                    self.cli("verify", "--key", self.key, ok=False)
                    self.assertEqual(selected.read_bytes(), selected_before_verify)
                    self.assertEqual(decoy.read_bytes(), decoy_before)

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

    def saved_delivery(self):
        with closing(sqlite3.connect(self.queue)) as queue:
            return queue.execute("SELECT receipt FROM sources WHERE key=?", (self.key,)).fetchone()[0]

    def start_delivered_task_and_add_replacement(self):
        self.db.execute("UPDATE tasks SET status='running',started_at='new-start',session_id='new-session' WHERE id=11")
        self.db.execute("INSERT INTO tasks VALUES(12,4,'replacement pending task','pending',NULL,NULL)")
        self.db.commit()

    def test_repeat_delivery_cannot_move_to_replacement_task(self):
        self.deliver()
        receipt = self.saved_delivery()
        self.start_delivered_task_and_add_replacement()
        before = self.snapshot()
        self.cli("verify", "--key", self.key, ok=False)
        self.deliver(ok=False)
        self.assertEqual(self.snapshot(), before)
        self.assertEqual(self.saved_delivery(), receipt)
        self.cli("verify", "--key", self.key, ok=False)

    def test_crash_recovery_cannot_move_to_replacement_task(self):
        with closing(sqlite3.connect(self.queue)) as queue:
            queue.executescript("CREATE TRIGGER crash_receipt BEFORE UPDATE ON sources WHEN NEW.phase='delivered' BEGIN SELECT RAISE(FAIL,'simulated receipt failure'); END;")
        self.deliver(ok=False)
        self.assertIsNone(self.saved_delivery())
        self.assertIn(self.directive.read_text(), self.snapshot()["tasks"][1][2])
        with closing(sqlite3.connect(self.queue)) as queue:
            queue.execute("DROP TRIGGER crash_receipt")
            queue.commit()
        self.start_delivered_task_and_add_replacement()
        before = self.snapshot()
        self.deliver(ok=False)
        self.assertEqual(self.snapshot(), before)
        self.assertIsNone(self.saved_delivery())
        self.cli("verify", "--key", self.key, ok=False)

    def test_legacy_missing_receipt_cannot_rebind_after_blocks_removed(self):
        original = self.snapshot()
        with closing(sqlite3.connect(self.queue)) as queue:
            queue.executescript("CREATE TRIGGER crash_receipt BEFORE UPDATE ON sources WHEN NEW.phase='delivered' BEGIN SELECT RAISE(FAIL,'legacy receipt failure'); END;")
        self.deliver(ok=False)
        self.assertIsNone(self.saved_delivery())
        with closing(sqlite3.connect(self.queue)) as queue:
            queue.execute("DROP TRIGGER crash_receipt")
            # Model the exact old format, which persisted no delivery target.
            pin = json.loads(queue.execute("SELECT pin FROM delivery_intents WHERE key=?", (self.key,)).fetchone()[0])
            legacy_pin = json.dumps({field:pin[field] for field in
                                     ("series_id", "native_path", "directive_sha256", "scope_sha256", "block")},
                                    ensure_ascii=False, sort_keys=True, separators=(",", ":"))
            queue.execute("UPDATE delivery_intents SET pin=? WHERE key=?", (legacy_pin, self.key))
            queue.execute("DELETE FROM delivery_targets WHERE key=?", (self.key,))
            queue.commit()
        self.db.execute("UPDATE task_series SET prompt=? WHERE id=4", (original["series"][0][1],))
        self.db.execute("UPDATE tasks SET prompt=? WHERE id=11", (original["tasks"][1][2],))
        self.start_delivered_task_and_add_replacement()
        before = self.snapshot()
        self.deliver(ok=False)
        self.assertEqual(self.snapshot(), before)
        self.assertIsNone(self.saved_delivery())
        with closing(sqlite3.connect(self.queue)) as queue:
            self.assertEqual(queue.execute("SELECT pin FROM delivery_intents WHERE key=?", (self.key,)).fetchone()[0], legacy_pin)
            self.assertIsNone(queue.execute("SELECT receipt FROM delivery_targets WHERE key=?", (self.key,)).fetchone())

    def test_new_intent_crash_before_target_binding_recovers(self):
        original = self.snapshot()
        with closing(sqlite3.connect(self.queue)) as queue:
            queue.executescript("CREATE TRIGGER crash_target BEFORE INSERT ON delivery_targets BEGIN SELECT RAISE(FAIL,'before target binding'); END;")
        self.deliver(ok=False)
        self.assertEqual(self.snapshot(), original)
        self.assertIsNone(self.saved_delivery())
        with closing(sqlite3.connect(self.queue)) as queue:
            pin = queue.execute("SELECT pin FROM delivery_intents WHERE key=?", (self.key,)).fetchone()[0]
            self.assertIsNone(queue.execute("SELECT receipt FROM delivery_targets WHERE key=?", (self.key,)).fetchone())
            queue.execute("DROP TRIGGER crash_target")
            queue.commit()
        self.assertEqual(self.deliver()["task_id"], 11)
        self.assertEqual(self.cli("verify", "--key", self.key)["task_id"], 11)
        receipt = json.loads(self.saved_delivery())
        self.assertEqual(receipt["before"], {
            "series_prompt_sha256":hashlib.sha256(original["series"][0][1].encode()).hexdigest(),
            "task_prompt_sha256":hashlib.sha256(original["tasks"][1][2].encode()).hexdigest()})
        with closing(sqlite3.connect(self.queue)) as queue:
            self.assertEqual(queue.execute("SELECT pin FROM delivery_intents WHERE key=?", (self.key,)).fetchone()[0], pin)

    def test_repeat_delivery_cannot_adopt_external_prompt_edits(self):
        self.deliver()
        receipt = self.saved_delivery()
        for table, target in (("tasks", 11), ("task_series", 4)):
            with self.subTest(table=table):
                original = self.db.execute(f"SELECT prompt FROM {table} WHERE id=?", (target,)).fetchone()[0]
                self.db.execute(f"UPDATE {table} SET prompt='external decision\n'||prompt WHERE id=?", (target,))
                self.db.commit()
                before = self.snapshot()
                self.cli("verify", "--key", self.key, ok=False)
                self.deliver(ok=False)
                self.assertEqual(self.snapshot(), before)
                self.assertEqual(self.saved_delivery(), receipt)
                self.cli("verify", "--key", self.key, ok=False)
                self.db.execute(f"UPDATE {table} SET prompt=? WHERE id=?", (original, target))
                self.db.commit()

    def test_started_or_session_bound_pending_refused(self):
        for field in ("started_at","session_id"):
            with self.subTest(field=field):
                self.db.execute(f"UPDATE tasks SET {field}='previous-attempt' WHERE id=11")
                before = self.snapshot()
                self.deliver(ok=False)
                self.assertEqual(self.snapshot(),before)
                self.db.execute(f"UPDATE tasks SET {field}=NULL WHERE id=11")

    def test_failed_native_cas_recovers_with_original_fingerprints(self):
        self.db.executescript("CREATE TRIGGER refuse BEFORE UPDATE ON tasks BEGIN SELECT RAISE(IGNORE); END;")
        before = self.snapshot()
        self.deliver(ok=False)
        self.assertEqual(self.snapshot(), before)
        self.db.execute("DROP TRIGGER refuse")
        self.deliver()
        receipt = json.loads(self.saved_delivery())
        self.assertEqual(receipt["before"]["task_prompt_sha256"], hashlib.sha256(before["tasks"][1][2].encode()).hexdigest())
        self.assertEqual(self.cli("verify", "--key", self.key)["task_id"], 11)

    def test_crash_recovery_rejects_external_edit_without_replacing_target(self):
        with closing(sqlite3.connect(self.queue)) as queue:
            queue.executescript("CREATE TRIGGER crash_receipt BEFORE UPDATE ON sources WHEN NEW.phase='delivered' BEGIN SELECT RAISE(FAIL,'simulated receipt failure'); END;")
        self.deliver(ok=False)
        with closing(sqlite3.connect(self.queue)) as queue:
            queue.execute("DROP TRIGGER crash_receipt")
            target = queue.execute("SELECT receipt FROM delivery_targets").fetchone()[0]
            queue.commit()
        self.db.execute("UPDATE tasks SET prompt='external edit'||prompt WHERE id=11")
        before = self.snapshot()
        self.deliver(ok=False)
        self.assertEqual(self.snapshot(), before)
        self.assertIsNone(self.saved_delivery())
        with closing(sqlite3.connect(self.queue)) as queue:
            self.assertEqual(queue.execute("SELECT receipt FROM delivery_targets").fetchone()[0], target)

    def test_repeat_does_not_restore_removed_block_or_adopt_unbound_legacy_receipt(self):
        before = self.snapshot()
        self.deliver()
        receipt = self.saved_delivery()
        self.db.execute("UPDATE tasks SET prompt=? WHERE id=11", (before["tasks"][1][2],))
        self.db.execute("UPDATE task_series SET prompt=? WHERE id=4", (before["series"][0][1],))
        self.deliver(ok=False)
        self.assertEqual(self.snapshot(), before)
        self.assertEqual(self.saved_delivery(), receipt)
        with closing(sqlite3.connect(self.queue)) as queue:
            queue.execute("DELETE FROM delivery_targets")
            queue.commit()
        self.deliver(ok=False)
        self.cli("verify", "--key", self.key, ok=False)
        self.assertEqual(self.snapshot(), before)
        self.assertEqual(self.saved_delivery(), receipt)

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
