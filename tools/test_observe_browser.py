import json
import subprocess
import sys
import tempfile
import time
import unittest
from datetime import datetime, timedelta, timezone
from pathlib import Path
from unittest import mock

from observe_browser import (
    ObserverLockUnavailable,
    append_event,
    capture_end,
    event,
    output_directory_lock,
    validate_cdp,
)


class BrowserObservationTests(unittest.TestCase):
    def test_loopback_only(self):
        self.assertEqual(validate_cdp('http://127.0.0.1:9222'), 'http://127.0.0.1:9222')
        for value in ('https://127.0.0.1:9222', 'http://example.org:9222',
                      'http://localhost:9222/?secret=x', 'http://user@localhost:9222',
                      'http://localhost:9222/private', 'http://localhost'):
            with self.subTest(value=value), self.assertRaises(ValueError):
                validate_cdp(value)

    def test_privacy_and_passive_methods(self):
        record = event('https://changjiang.yuketang.cn/private-name/123?token=private-token',
                       'POST', 200, 12, ['sessionid=private-cookie; Path=/', 'csrftoken=private-csrf'])
        self.assertEqual(record['endpoint'], 'other')
        self.assertEqual(record['method'], 'POST')
        self.assertTrue(record['set_sessionid'])
        self.assertTrue(record['set_csrftoken'])
        self.assertNotIn('private-', json.dumps(record))
        self.assertIsNone(event('https://yuketang.cn.example.org/private', 'GET', 200, 1))
        self.assertIsNone(event('https://example.org/private', 'GET', 200, 1))

    def test_capture_persists_and_writes_json_only(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            expected = datetime.now(timezone.utc) - timedelta(hours=25)
            (directory / 'browser-window.json').write_text(json.dumps({'started_at': expected.isoformat()}))
            self.assertEqual(capture_end(directory), expected + timedelta(hours=24))
            self.assertEqual(capture_end(directory), capture_end(directory))
            append_event(directory, None)
            self.assertEqual(list(directory.glob('*.jsonl')), [])
            append_event(directory, event('https://changjiang.yuketang.cn/login/', 'GET', 503, -1))
            data = json.loads(next(directory.glob('browser-*.jsonl')).read_text())
            self.assertEqual(data['endpoint'], 'auth')
            self.assertEqual(data['outcome'], 'upstream')
            self.assertEqual(data['duration_ms'], 0)

    def test_lock_file_is_retained_and_released(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            lock_path = directory / 'browser-observer.lock'
            with output_directory_lock(directory):
                self.assertEqual(lock_path.stat().st_size, 1)
            self.assertTrue(lock_path.exists())
            with output_directory_lock(directory):
                pass

    def test_lock_released_after_body_exception(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            with self.assertRaisesRegex(RuntimeError, 'body failed'):
                with output_directory_lock(directory):
                    raise RuntimeError('body failed')
            with output_directory_lock(directory):
                pass

    def test_cross_process_lock_contention_and_reacquire(self):
        module_directory = str(Path(__file__).resolve().parent)
        child_code = """
import sys
from pathlib import Path
sys.path.insert(0, sys.argv[3])
from observe_browser import output_directory_lock
with output_directory_lock(Path(sys.argv[1])):
    Path(sys.argv[2]).write_text('ready', encoding='ascii')
    sys.stdin.readline()
"""
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            ready = directory / 'child-ready'
            process = subprocess.Popen(
                [sys.executable, '-c', child_code, str(directory), str(ready), module_directory],
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
            )
            try:
                deadline = time.monotonic() + 5
                while not ready.exists():
                    if process.poll() is not None:
                        _, stderr = process.communicate()
                        self.fail('lock holder exited before ready: ' + stderr)
                    if time.monotonic() >= deadline:
                        self.fail('timed out waiting for lock holder')
                    time.sleep(0.01)

                with self.assertRaises(ObserverLockUnavailable):
                    with output_directory_lock(directory):
                        pass

                _, stderr = process.communicate(input='\n', timeout=5)
                self.assertEqual(process.returncode, 0, stderr)
                with output_directory_lock(directory):
                    pass
            finally:
                if process.poll() is None:
                    process.kill()
                    process.communicate()

    def test_non_lock_permission_error_is_not_reported_as_contention(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            denied = PermissionError(13, 'permission denied')
            with mock.patch.object(Path, 'open', side_effect=denied):
                with self.assertRaises(PermissionError) as raised:
                    with output_directory_lock(directory):
                        pass
            self.assertNotIsInstance(raised.exception, ObserverLockUnavailable)

    def test_existing_lock_file_is_not_truncated(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            lock_path = directory / 'browser-observer.lock'
            lock_path.write_bytes(b'existing')
            with output_directory_lock(directory):
                pass
            self.assertEqual(lock_path.read_bytes(), b'existing')


if __name__ == '__main__':
    unittest.main()
