from __future__ import annotations

from dataclasses import asdict, dataclass
from datetime import datetime, timezone
import json
from pathlib import Path
import threading


@dataclass(frozen=True)
class QueryLogEntry:
    time: datetime
    status: str
    duration_ms: int
    balance: float | None = None
    error: str | None = None


class QueryLogger:
    def __init__(self, directory: Path) -> None:
        self.directory = Path(directory)
        self._lock = threading.Lock()

    def log(self, entry: QueryLogEntry) -> Path:
        with self._lock:
            self.directory.mkdir(parents=True, exist_ok=True)
            entry_time = entry.time.astimezone(timezone.utc)
            path = self.directory / f"query-{entry_time.date().isoformat()}.jsonl"
            payload = asdict(entry)
            payload["time"] = entry.time.isoformat()
            payload = {key: value for key, value in payload.items() if value is not None}
            with path.open("a", encoding="utf-8") as handle:
                handle.write(json.dumps(payload, ensure_ascii=False, separators=(",", ":")))
                handle.write("\n")
            return path
