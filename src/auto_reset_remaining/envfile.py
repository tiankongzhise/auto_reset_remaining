from __future__ import annotations

import os
import re
import tempfile
from pathlib import Path

_KEY_RE = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*$")


def load_env_file(path: str | os.PathLike[str]) -> dict[str, str]:
    env_path = Path(path)
    values: dict[str, str] = {}
    if not env_path.exists():
        return values
    with env_path.open("r", encoding="utf-8-sig") as handle:
        for raw_line in handle:
            parsed = _parse_line(raw_line.rstrip("\n").rstrip("\r"))
            if parsed is not None:
                key, value = parsed
                values[key] = value
    return values


def update_values(path: str | os.PathLike[str], updates: dict[str, str]) -> None:
    if not updates:
        return

    env_path = Path(path)
    lines = env_path.read_text(encoding="utf-8").splitlines() if env_path.exists() else []
    seen: set[str] = set()

    for index, line in enumerate(lines):
        parsed = _parse_line(line)
        if parsed is None:
            continue
        key, _ = parsed
        if key not in updates:
            continue
        lines[index] = f"{key}={format_value(updates[key])}"
        seen.add(key)

    for key in sorted(set(updates) - seen):
        lines.append(f"{key}={format_value(updates[key])}")

    env_path.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp_name = tempfile.mkstemp(prefix=".env-", dir=str(env_path.parent), text=True)
    try:
        with os.fdopen(fd, "w", encoding="utf-8", newline="\n") as handle:
            for line in lines:
                handle.write(line)
                handle.write("\n")
        os.replace(tmp_name, env_path)
    finally:
        if os.path.exists(tmp_name):
            os.unlink(tmp_name)


def format_value(value: str) -> str:
    if value == "":
        return '""'
    if any(ch in value for ch in " \t\r\n#'\"\\"):
        escaped = (
            value.replace("\\", "\\\\")
            .replace('"', '\\"')
            .replace("\n", "\\n")
            .replace("\r", "\\r")
            .replace("\t", "\\t")
        )
        return f'"{escaped}"'
    return value


def _parse_line(line: str) -> tuple[str, str] | None:
    stripped = line.strip()
    if not stripped or stripped.startswith("#"):
        return None
    if stripped.startswith("export "):
        stripped = stripped[len("export ") :].strip()
    if "=" not in stripped:
        return None
    key, value = stripped.split("=", 1)
    key = key.strip()
    if not _KEY_RE.match(key):
        return None
    return key, _unquote(value.strip())


def _unquote(value: str) -> str:
    if len(value) < 2 or value[0] not in {"'", '"'} or value[-1] != value[0]:
        return value
    quote = value[0]
    inner = value[1:-1]
    if quote == "'":
        return inner
    return (
        inner.replace("\\n", "\n")
        .replace("\\r", "\r")
        .replace("\\t", "\t")
        .replace('\\"', '"')
        .replace("\\\\", "\\")
    )
