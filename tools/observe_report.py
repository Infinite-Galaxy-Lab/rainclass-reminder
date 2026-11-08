from __future__ import annotations

import argparse
import json
import os
import re
import sys
from collections import Counter, defaultdict
from datetime import datetime, timezone
from typing import Any, BinaryIO

MAX_LINE_BYTES = 64 * 1024
MAX_FILE_BYTES = 32 * 1024 * 1024
EVIDENCE = "availability_since_local_import_not_server_lifetime"

ENDPOINT_CATEGORIES = (
    "basic_info",
    "courses",
    "chapters",
    "leaf_info",
    "exam_cover",
    "learn_logs",
    "course_meta",
    "other",
)
BROWSER_ENDPOINT_CATEGORIES = ENDPOINT_CATEGORIES + ("auth",)
OUTCOME_CATEGORIES = (
    "ok",
    "auth_expired",
    "rate_limited",
    "not_found",
    "upstream",
    "canceled",
)
REQUEST_FIELDS = (
    "at",
    "endpoint",
    "status",
    "duration_ms",
    "outcome",
    "set_sessionid",
    "set_csrftoken",
)
AUTH_FIELDS = ("at", "acquired_at", "outcome")
FILE_NAME = re.compile(r"^(requests|auth|browser)-.+\.jsonl$")
ZERO_UTC = datetime.min.replace(tzinfo=timezone.utc)
_BAD_LINE = object()


def _reject_json_constant(value: str) -> None:
    raise ValueError(value)


def _read_line(handle: BinaryIO) -> bytes | object | None:
    raw = handle.readline(MAX_LINE_BYTES + 2)
    if not raw:
        return None
    if not raw.endswith(b"\n"):
        while True:
            tail = handle.readline(MAX_LINE_BYTES + 2)
            if not tail or tail.endswith(b"\n"):
                break
        return _BAD_LINE
    raw = raw[:-1]
    if raw.endswith(b"\r"):
        raw = raw[:-1]
    if len(raw) > MAX_LINE_BYTES:
        return _BAD_LINE
    return raw


def _sanitize(kind: str, value: Any) -> dict[str, Any] | None:
    if not isinstance(value, dict):
        return None
    fields = AUTH_FIELDS if kind == "auth" else REQUEST_FIELDS
    return {field: value[field] for field in fields if field in value}


def read_jsonl(directory: str) -> dict[str, Any]:
    result: dict[str, Any] = {
        "requests": [],
        "auth": [],
        "browser": [],
        "skipped_lines": 0,
        "skipped_files": 0,
    }
    with os.scandir(directory) as scan:
        entries = sorted(scan, key=lambda entry: entry.name)
    for entry in entries:
        match = FILE_NAME.fullmatch(entry.name)
        if match is None:
            continue
        try:
            if not entry.is_file(follow_symlinks=False):
                continue
            if entry.stat(follow_symlinks=False).st_size > MAX_FILE_BYTES:
                result["skipped_files"] += 1
                continue
            handle = open(entry.path, "rb")
        except OSError:
            result["skipped_files"] += 1
            continue

        kind = match.group(1)
        with handle:
            while True:
                raw = _read_line(handle)
                if raw is None:
                    break
                if raw is _BAD_LINE:
                    result["skipped_lines"] += 1
                    continue
                try:
                    decoded = raw.decode("utf-8")
                    value = json.loads(decoded, parse_constant=_reject_json_constant)
                except (UnicodeDecodeError, ValueError, json.JSONDecodeError):
                    result["skipped_lines"] += 1
                    continue
                record = _sanitize(kind, value)
                if record is None:
                    result["skipped_lines"] += 1
                    continue
                result[kind].append(record)
    return result


def _parse_time(value: Any) -> datetime | None:
    if not isinstance(value, str) or not value:
        return None
    raw = value[:-1] + "+00:00" if value.endswith("Z") else value
    try:
        parsed = datetime.fromisoformat(raw)
    except ValueError:
        return None
    if parsed.tzinfo is None:
        return None
    return parsed.astimezone(timezone.utc)


def _format_time(value: datetime) -> str:
    return value.astimezone(timezone.utc).isoformat().replace("+00:00", "Z")


def _normalize_endpoint(value: Any, categories: tuple[str, ...]) -> str:
    return value if isinstance(value, str) and value in categories else "other"


def _normalize_outcome(value: Any) -> str:
    return value if isinstance(value, str) and value in OUTCOME_CATEGORIES else "upstream"


def _normalize_status(value: Any) -> int:
    if isinstance(value, bool) or not isinstance(value, int) or not 0 <= value <= 599:
        return 0
    return value


def _age_hours(later: datetime, base: datetime) -> float:
    return max(0.0, (later - base).total_seconds() / 3600.0)


def _summarize_requests(
    records: list[dict[str, Any]], endpoints: tuple[str, ...]
) -> dict[str, Any]:
    endpoint_counts = {category: 0 for category in endpoints}
    status_counts: Counter[int] = Counter()
    outcome_counts = {category: 0 for category in OUTCOME_CATEGORIES}
    cookie_counts = {
        "set_sessionid": {"false": 0, "true": 0},
        "set_csrftoken": {"false": 0, "true": 0},
    }
    first_seen: datetime | None = None
    last_seen: datetime | None = None

    for record in records:
        observed_at = _parse_time(record.get("at"))
        if observed_at is None:
            continue
        endpoint = _normalize_endpoint(record.get("endpoint"), endpoints)
        outcome = _normalize_outcome(record.get("outcome"))
        status = _normalize_status(record.get("status"))

        endpoint_counts[endpoint] += 1
        status_counts[status] += 1
        outcome_counts[outcome] += 1
        for field in cookie_counts:
            key = "true" if record.get(field) is True else "false"
            cookie_counts[field][key] += 1
        first_seen = observed_at if first_seen is None else min(first_seen, observed_at)
        last_seen = observed_at if last_seen is None else max(last_seen, observed_at)

    return {
        "endpoint": endpoint_counts,
        "status": {str(status): status_counts[status] for status in sorted(status_counts)},
        "outcome": outcome_counts,
        "set_sessionid": cookie_counts["set_sessionid"],
        "set_csrftoken": cookie_counts["set_csrftoken"],
        "first_seen": _format_time(first_seen) if first_seen is not None else None,
        "last_seen": _format_time(last_seen) if last_seen is not None else None,
    }


def _summarize_auth(records: list[dict[str, Any]]) -> list[dict[str, Any]]:
    groups: dict[datetime, list[tuple[datetime, str]]] = defaultdict(list)
    for record in records:
        observed_at = _parse_time(record.get("at"))
        acquired_at = _parse_time(record.get("acquired_at"))
        if observed_at is None or acquired_at is None or acquired_at == ZERO_UTC:
            continue
        groups[acquired_at].append((observed_at, _normalize_outcome(record.get("outcome"))))

    summaries: list[dict[str, Any]] = []
    for acquired_at in sorted(groups):
        events = sorted(groups[acquired_at], key=lambda event: event[0])
        times = [event[0] for event in events]
        ok_times = [event[0] for event in events if event[1] == "ok"]
        expired_times = [event[0] for event in events if event[1] == "auth_expired"]
        last_ok = max(ok_times) if ok_times else None
        first_expired = min(expired_times) if expired_times else None
        summaries.append(
            {
                "acquired_at": _format_time(acquired_at),
                "first_seen": _format_time(min(times)),
                "last_seen": _format_time(max(times)),
                "last_ok": _format_time(last_ok) if last_ok is not None else None,
                "first_auth_expired": (
                    _format_time(first_expired) if first_expired is not None else None
                ),
                "success_after_expired": bool(
                    last_ok is not None
                    and first_expired is not None
                    and last_ok > first_expired
                ),
                "last_ok_age_hours": (
                    _age_hours(last_ok, acquired_at) if last_ok is not None else None
                ),
                "first_expired_age_hours": (
                    _age_hours(first_expired, acquired_at)
                    if first_expired is not None
                    else None
                ),
            }
        )
    return summaries


def summarize(directory: str) -> dict[str, Any]:
    records = read_jsonl(directory)
    return {
        "evidence": EVIDENCE,
        "requests": _summarize_requests(records["requests"], ENDPOINT_CATEGORIES),
        "browser_requests": _summarize_requests(
            records["browser"], BROWSER_ENDPOINT_CATEGORIES
        ),
        "auth": _summarize_auth(records["auth"]),
        "skipped_lines": records["skipped_lines"],
        "skipped_files": records["skipped_files"],
    }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("directory")
    args = parser.parse_args(argv)
    try:
        report = summarize(args.directory)
    except OSError:
        report = {"evidence": EVIDENCE, "error": "observation_directory_unreadable"}
        print(json.dumps(report, ensure_ascii=False, sort_keys=True, separators=(",", ":")))
        return 1
    print(json.dumps(report, ensure_ascii=False, sort_keys=True, separators=(",", ":")))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())