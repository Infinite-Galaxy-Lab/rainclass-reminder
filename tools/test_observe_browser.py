import json
import tempfile
import unittest
from datetime import datetime, timedelta, timezone
from pathlib import Path

from observe_browser import append_event, capture_end, event, validate_cdp


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


if __name__ == '__main__':
    unittest.main()
