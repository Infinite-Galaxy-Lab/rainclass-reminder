import argparse
import asyncio
import errno
import json
import os
import re
from contextlib import contextmanager
from datetime import datetime, timedelta, timezone
from pathlib import Path
from urllib.parse import urlsplit


MAX_BYTES = 32 << 20
LOCK_CONTENTION_ERRNOS = {
    errno.EACCES,
    errno.EAGAIN,
    getattr(errno, 'EWOULDBLOCK', errno.EAGAIN),
}


class ObserverLockUnavailable(RuntimeError):
    pass


def validate_cdp(value):
    url = urlsplit(value)
    if (url.scheme != 'http' or url.hostname not in {'127.0.0.1', 'localhost', '::1'}
            or not url.port or url.username or url.password or url.query or url.fragment
            or url.path not in {'', '/'}):
        raise ValueError('CDP must use a loopback HTTP address and explicit port')
    return value


def endpoint(url):
    parsed = urlsplit(url)
    host = (parsed.hostname or '').lower()
    if parsed.scheme != 'https' or not any(host == root or host.endswith('.' + root)
                                         for root in ('yuketang.cn', 'xuetangx.com')):
        return None
    path = parsed.path
    exact = {
        '/api/v3/user/basic-info': 'basic_info',
        '/v2/api/web/courses/list': 'courses',
        '/mooc-api/v1/lms/learn/course/chapter': 'chapters',
        '/v/exam/cover': 'exam_cover',
        '/v/course_meta/user_info': 'course_meta',
    }
    if path in exact:
        return exact[path]
    for prefix, category in (('/mooc-api/v1/lms/learn/leaf_info/', 'leaf_info'),
                             ('/v2/api/web/logs/learn/', 'learn_logs')):
        if path.startswith(prefix):
            return category
    # 未知路径可能含姓名或标识，只保留固定类别。
    if re.search(r'/(?:login|oauth|refresh|session|token)(?:/|$)', path, re.I):
        return 'auth'
    return 'other'


def event(url, method, status, duration_ms, set_cookie=()):
    category = endpoint(url)
    if category is None:
        return None
    names = {line.partition('=')[0].strip().lower() for line in set_cookie}
    outcome = ('auth_expired' if status in {401, 403} else
               'rate_limited' if status == 429 else
               'not_found' if status == 404 else
               'ok' if 200 <= status < 400 else 'upstream')
    return {
        'at': datetime.now(timezone.utc).isoformat(),
        'endpoint': category,
        'method': method if method in {'GET', 'POST', 'HEAD', 'OPTIONS', 'PUT', 'PATCH', 'DELETE'} else 'other',
        'status': status if type(status) is int and 0 <= status <= 599 else 0,
        'duration_ms': max(0, int(duration_ms)),
        'outcome': outcome,
        'set_sessionid': 'sessionid' in names,
        'set_csrftoken': 'csrftoken' in names,
    }


def append_event(directory, record):
    if record is None:
        return
    path = directory / ('browser-' + record['at'][:10] + '.jsonl')
    line = (json.dumps(record, separators=(',', ':')) + '\n').encode('utf-8')
    with open(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_APPEND, 0o600), 'ab') as stream:
        if os.fstat(stream.fileno()).st_size + len(line) <= MAX_BYTES:
            stream.write(line)


def capture_end(directory):
    directory.mkdir(parents=True, exist_ok=True, mode=0o700)
    path = directory / 'browser-window.json'
    if path.exists():
        started = datetime.fromisoformat(json.loads(path.read_text(encoding='utf-8'))['started_at'])
        if started.tzinfo is None:
            raise ValueError('Invalid browser capture window')
    else:
        started = datetime.now(timezone.utc)
        with open(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), 'w') as stream:
            json.dump({'started_at': started.isoformat()}, stream)
    return started + timedelta(hours=24)


def _acquire_lock(stream):
    try:
        if os.name == 'nt':
            import msvcrt
            msvcrt.locking(stream.fileno(), msvcrt.LK_NBLCK, 1)
        else:
            import fcntl
            fcntl.flock(stream.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
    except OSError as error:
        if error.errno in LOCK_CONTENTION_ERRNOS:
            raise ObserverLockUnavailable('Observer output directory is already locked') from error
        raise


def _release_lock(stream):
    if os.name == 'nt':
        import msvcrt
        msvcrt.locking(stream.fileno(), msvcrt.LK_UNLCK, 1)
    else:
        import fcntl
        fcntl.flock(stream.fileno(), fcntl.LOCK_UN)


@contextmanager
def output_directory_lock(directory):
    directory.mkdir(parents=True, exist_ok=True, mode=0o700)
    lock_path = directory / 'browser-observer.lock'
    with lock_path.open('a+b') as stream:
        stream.seek(0, os.SEEK_END)
        if stream.tell() == 0:
            stream.write(b'\0')
            stream.flush()
        stream.seek(0)
        _acquire_lock(stream)
        try:
            yield
        finally:
            _release_lock(stream)


async def observe(cdp, directory):
    from playwright.async_api import async_playwright

    with output_directory_lock(directory):
        end = capture_end(directory)
        if datetime.now(timezone.utc) >= end:
            print('Browser capture window has ended; no connection made.')
            return
        pending, contexts = set(), set()
        failures = 0
        async with async_playwright() as driver:
            browser = await driver.chromium.connect_over_cdp(cdp)

            async def response_received(response):
                nonlocal failures
                try:
                    if datetime.now(timezone.utc) >= end or endpoint(response.url) is None:
                        return
                    cookies = [header['value'] for header in await response.headers_array()
                               if header['name'].lower() == 'set-cookie']
                    duration = response.request.timing.get('responseStart', 0)
                    append_event(directory, event(response.url, response.request.method,
                                                  response.status, duration, cookies))
                except Exception:
                    failures += 1

            def on_response(response):
                task = asyncio.create_task(response_received(response))
                pending.add(task)
                task.add_done_callback(pending.discard)

            def on_failed(request):
                nonlocal failures
                try:
                    if datetime.now(timezone.utc) < end:
                        append_event(directory, event(request.url, request.method, 0, 0))
                except Exception:
                    failures += 1

            def register_context(context):
                if context in contexts:
                    return
                context.on('response', on_response)
                context.on('requestfailed', on_failed)
                contexts.add(context)

            try:
                for context in browser.contexts:
                    register_context(context)
                print('Browser observer connected and listening.', flush=True)
                while browser.is_connected() and datetime.now(timezone.utc) < end:
                    for context in browser.contexts:
                        register_context(context)
                    await asyncio.sleep(min(1, max(0, (end - datetime.now(timezone.utc)).total_seconds())))
            finally:
                for context in contexts:
                    context.remove_listener('response', on_response)
                    context.remove_listener('requestfailed', on_failed)
                if pending:
                    await asyncio.gather(*pending, return_exceptions=True)
                # 退出 Playwright 会断开 CDP；不调用 browser.close()，避免关闭用户窗口。
        print(json.dumps({'capture_finished': True, 'skipped_events': failures}))


def main():
    parser = argparse.ArgumentParser(description='Passively observe an already-open Rain Classroom browser session')
    parser.add_argument('--cdp', required=True, type=validate_cdp)
    parser.add_argument('--output', required=True, type=Path)
    args = parser.parse_args()
    try:
        asyncio.run(observe(args.cdp, args.output))
    except KeyboardInterrupt:
        return 0
    except ObserverLockUnavailable:
        print('Browser observer is already running.', flush=True)
        return 1
    except Exception:
        print('Observation stopped. Check local CDP, Playwright installation and output permissions.')
        return 1
    return 0


if __name__ == '__main__':
    raise SystemExit(main())