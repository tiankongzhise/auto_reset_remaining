from __future__ import annotations

import json
import threading
from dataclasses import asdict, dataclass
from datetime import datetime
from pathlib import Path


@dataclass
class QueryLogEntry:
    time: str
    status: str
    duration_ms: int
    balance: float | None = None
    error: str = ""


class QueryLogger:
    def __init__(self, directory: str) -> None:
        self.directory = Path(directory)
        self._lock = threading.Lock()

    def log(self, entry: QueryLogEntry) -> None:
        with self._lock:
            self.directory.mkdir(parents=True, exist_ok=True)
            date = datetime.fromisoformat(entry.time).date().isoformat()
            path = self.directory / f"query-{date}.jsonl"
            payload = {key: value for key, value in asdict(entry).items() if value not in (None, "")}
            with path.open("a", encoding="utf-8") as handle:
                json.dump(payload, handle, ensure_ascii=False, separators=(",", ":"))
                handle.write("\n")
