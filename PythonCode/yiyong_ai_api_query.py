#!/usr/bin/env python3
"""Small helper for manually inspecting RayPlus usage responses.

This helper intentionally reads credentials from environment variables so no
secret is stored in the repository.
"""

from __future__ import annotations

import json
import os
import sys
import urllib.error
import urllib.request
from typing import Any


API_KEY = os.getenv("RAYPLUS_API_KEY", "")
BASE_URL = os.getenv("RAYPLUS_BASE_URL", "https://rayplus.site")
USER_AGENT = os.getenv("USER_AGENT", "auto-reset-remaining/1.0")


def request_json(path: str) -> Any:
    url = f"{BASE_URL.rstrip('/')}{path}"
    req = urllib.request.Request(
        url,
        headers={
            "Authorization": f"Bearer {API_KEY}",
            "Accept": "application/json",
            "Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
            "Cache-Control": "no-cache",
            "User-Agent": USER_AGENT,
        },
        method="GET",
    )

    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            body = resp.read().decode("utf-8", errors="replace")
            return json.loads(body) if body else None
    except urllib.error.HTTPError as exc:
        body = exc.read().decode("utf-8", errors="replace")
        print(f"[error] GET {path} -> HTTP {exc.code}: {body}", file=sys.stderr)
        return None
    except urllib.error.URLError as exc:
        print(f"[error] GET {path} failed: {exc}", file=sys.stderr)
        return None
    except json.JSONDecodeError as exc:
        print(f"[error] GET {path} returned invalid JSON: {exc}", file=sys.stderr)
        return None


def main() -> int:
    if not API_KEY:
        print("Set RAYPLUS_API_KEY before running this helper.", file=sys.stderr)
        return 2
    usage = request_json("/v1/usage")
    print(json.dumps(usage, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
