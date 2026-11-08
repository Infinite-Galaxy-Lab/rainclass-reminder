import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

import observe_report


class ObserveReportTests(unittest.TestCase):
    def test_auth_recovery_groups_and_ages(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            records = [
                {"at": "2026-01-01T02:00:00Z", "acquired_at": "2026-01-01T00:00:00Z", "outcome": "ok"},
                {"at": "2026-01-01T01:00:00Z", "acquired_at": "2026-01-01T00:00:00Z", "outcome": "auth_expired"},
                {"at": "2026-01-01T00:00:00Z", "acquired_at": "2026-01-01T00:00:00Z", "outcome": "ok"},
                {"at": "0001-01-01T01:00:00Z", "acquired_at": "0001-01-01T00:00:00Z", "outcome": "ok"},
            ]
            self._write_jsonl(root / "auth-2026-01.jsonl", records)

            report = observe_report.summarize(directory)
            self.assertEqual(len(report["auth"]), 1)
            session = report["auth"][0]
            self.assertEqual(session["acquired_at"], "2026-01-01T00:00:00Z")
            self.assertEqual(session["first_seen"], "2026-01-01T00:00:00Z")
            self.assertEqual(session["last_seen"], "2026-01-01T02:00:00Z")
            self.assertEqual(session["last_ok"], "2026-01-01T02:00:00Z")
            self.assertEqual(session["first_auth_expired"], "2026-01-01T01:00:00Z")
            self.assertTrue(session["success_after_expired"])
            self.assertAlmostEqual(session["last_ok_age_hours"], 2.0)
            self.assertAlmostEqual(session["first_expired_age_hours"], 1.0)

    def test_upstream_is_not_auth_expiry(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self._write_jsonl(
                root / "auth-2026-01.jsonl",
                [
                    {
                        "at": "2026-01-01T01:00:00Z",
                        "acquired_at": "2026-01-01T00:00:00Z",
                        "outcome": "upstream",
                    }
                ],
            )
            self._write_jsonl(
                root / "requests-2026-01-01.jsonl",
                [
                    {
                        "at": "2026-01-01T01:00:00Z",
                        "endpoint": "courses",
                        "status": 503,
                        "outcome": "upstream",
                    }
                ],
            )

            report = observe_report.summarize(directory)
            session = report["auth"][0]
            self.assertIsNone(session["first_auth_expired"])
            self.assertFalse(session["success_after_expired"])
            self.assertEqual(report["requests"]["outcome"]["upstream"], 1)
            self.assertEqual(report["requests"]["outcome"]["auth_expired"], 0)
            self.assertEqual(report["requests"]["status"], {"503": 1})
            self.assertEqual(
                report["evidence"],
                "availability_since_local_import_not_server_lifetime",
            )

    def test_malicious_fields_are_dropped_and_browser_allows_auth(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            sentinel = "RAW_SECRET_SENTINEL"
            self._write_jsonl(
                root / "requests-2026-01-01.jsonl",
                [
                    {
                        "at": "2026-01-01T01:00:00Z",
                        "endpoint": "auth",
                        "status": 200,
                        "outcome": "ok",
                        "secret": sentinel,
                    },
                    {
                        "at": "2026-01-01T02:00:00Z",
                        "endpoint": sentinel,
                        "status": 200,
                        "outcome": sentinel,
                        "secret": sentinel,
                    },
                ],
            )
            self._write_jsonl(
                root / "browser-2026-01.jsonl",
                [
                    {
                        "at": "2026-01-01T03:00:00Z",
                        "endpoint": "auth",
                        "status": 200,
                        "outcome": "ok",
                        "secret": sentinel,
                    }
                ],
            )
            self._write_jsonl(
                root / "auth-2026-01.jsonl",
                [
                    {
                        "at": "2026-01-01T04:00:00Z",
                        "acquired_at": "2026-01-01T00:00:00Z",
                        "outcome": sentinel,
                        "secret": sentinel,
                    }
                ],
            )

            report = observe_report.summarize(directory)
            serialized = json.dumps(report)
            self.assertNotIn(sentinel, serialized)
            self.assertNotIn(str(root), serialized)
            self.assertNotIn("auth", report["requests"]["endpoint"])
            self.assertEqual(report["requests"]["endpoint"]["other"], 2)
            self.assertEqual(report["requests"]["outcome"]["upstream"], 1)
            self.assertEqual(report["browser_requests"]["endpoint"]["auth"], 1)
            self.assertIsNone(report["auth"][0]["first_auth_expired"])
            self.assertFalse(report["auth"][0]["success_after_expired"])

            completed = subprocess.run(
                [sys.executable, str(Path(observe_report.__file__)), directory],
                check=True,
                capture_output=True,
                text=True,
            )
            cli_report = json.loads(completed.stdout)
            self.assertEqual(cli_report, report)
            self.assertNotIn(sentinel, completed.stdout)
            self.assertNotIn(str(root), completed.stdout)

    def test_bad_lines_are_counted_and_empty_directory_is_zeroed(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            valid = {"at": "2026-01-01T00:00:00Z", "endpoint": "courses", "status": 200, "outcome": "ok"}
            complete_tail = {"at": "2026-01-01T02:00:00Z", "endpoint": "courses", "status": 200, "outcome": "ok"}
            with (root / "requests-2026-01-01.jsonl").open("wb") as handle:
                handle.write(json.dumps(valid).encode("utf-8") + b"\n")
                handle.write(b"{not-json}\n")
                handle.write(b'{"at":"invalid","endpoint":"chapters","status":200,"outcome":"ok"}\n')
                handle.write(json.dumps(complete_tail).encode("utf-8"))

            report = observe_report.summarize(directory)
            self.assertEqual(report["skipped_lines"], 2)
            self.assertEqual(report["requests"]["endpoint"]["courses"], 1)
            self.assertEqual(report["requests"]["first_seen"], "2026-01-01T00:00:00Z")
            self.assertEqual(report["requests"]["endpoint"]["chapters"], 0)

        with tempfile.TemporaryDirectory() as directory:
            empty = observe_report.summarize(directory)
            self.assertEqual(empty["auth"], [])
            self.assertEqual(empty["requests"]["status"], {})
            self.assertEqual(empty["browser_requests"]["status"], {})
            self.assertEqual(empty["skipped_lines"], 0)
            self.assertEqual(empty["skipped_files"], 0)
            self.assertIsNone(empty["requests"]["first_seen"])
            self.assertIsNone(empty["browser_requests"]["last_seen"])
            self.assertTrue(all(value == 0 for value in empty["requests"]["endpoint"].values()))
            self.assertTrue(all(value == 0 for value in empty["requests"]["outcome"].values()))

    def test_status_and_cookie_counts_are_normalized(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self._write_jsonl(
                root / "requests-2026-01-01.jsonl",
                [
                    {
                        "at": "2026-01-01T00:00:00Z",
                        "endpoint": "courses",
                        "status": 599,
                        "outcome": "ok",
                        "set_sessionid": True,
                        "set_csrftoken": True,
                    },
                    {
                        "at": "2026-01-01T01:00:00Z",
                        "endpoint": "courses",
                        "status": 600,
                        "outcome": "ok",
                        "set_sessionid": False,
                        "set_csrftoken": False,
                    },
                    {
                        "at": "2026-01-01T02:00:00Z",
                        "endpoint": "courses",
                        "status": "200",
                        "outcome": "ok",
                    },
                    {
                        "at": "2026-01-01T03:00:00Z",
                        "endpoint": "courses",
                        "status": True,
                        "outcome": "ok",
                    },
                ],
            )

            requests = observe_report.summarize(directory)["requests"]
            self.assertEqual(requests["status"], {"0": 3, "599": 1})
            self.assertEqual(requests["set_sessionid"], {"false": 3, "true": 1})
            self.assertEqual(requests["set_csrftoken"], {"false": 3, "true": 1})

    def test_line_and_file_limits(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            valid = {"at": "2026-01-01T00:00:00Z", "endpoint": "courses", "status": 200, "outcome": "ok"}
            with (root / "requests-2026-01-01.jsonl").open("wb") as handle:
                handle.write(b"x" * (observe_report.MAX_LINE_BYTES + 1) + b"\n")
                handle.write(json.dumps(valid).encode("utf-8") + b"\n")

            report = observe_report.summarize(directory)
            self.assertEqual(report["skipped_lines"], 1)
            self.assertEqual(report["requests"]["endpoint"]["courses"], 1)

        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            with (root / "requests-2026-01-01.jsonl").open("wb") as handle:
                handle.seek(observe_report.MAX_FILE_BYTES)
                handle.write(b"\n")

            report = observe_report.summarize(directory)
            self.assertEqual(report["skipped_files"], 1)
            self.assertIsNone(report["requests"]["first_seen"])

    @staticmethod
    def _write_jsonl(path: Path, records):
        with path.open("w", encoding="utf-8", newline="\n") as handle:
            for record in records:
                handle.write(json.dumps(record, separators=(",", ":")) + "\n")


if __name__ == "__main__":
    unittest.main()