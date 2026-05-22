from __future__ import annotations

import logging
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse

from .monitor import Monitor
from .store import InvalidTokenError


def build_server(host: str, port: int, monitor: Monitor, logger: logging.Logger) -> ThreadingHTTPServer:
    class Handler(BaseHTTPRequestHandler):
        def do_GET(self) -> None:  # noqa: N802
            parsed = urlparse(self.path)
            if parsed.path == "/healthz":
                self._send_text(HTTPStatus.OK, "ok\n")
                return
            if parsed.path == "/confirm-reset":
                token = parse_qs(parsed.query).get("token", [""])[0]
                try:
                    result = monitor.confirm(token)
                except InvalidTokenError as exc:
                    self._send_text(HTTPStatus.BAD_REQUEST, f"{exc}\n")
                    return
                except Exception as exc:
                    self._send_text(HTTPStatus.INTERNAL_SERVER_ERROR, f"{exc}\n")
                    return
                self._send_text(
                    HTTPStatus.OK,
                    (
                        "订阅已重置成功。\n"
                        f"订阅 ID: {result.subscription_id}\n"
                        f"余额: {result.balance:.6f}\n"
                        f"人工确认成功次数: {result.manual_confirm_success_count}\n"
                        f"自动重置: {str(result.auto_reset_enabled).lower()}\n"
                        f"重置日志 ID: {result.reset_log_id}\n"
                    ),
                )
                return
            self._send_text(HTTPStatus.NOT_FOUND, "not found\n")

        def log_message(self, fmt: str, *args: object) -> None:
            logger.debug("%s - %s", self.address_string(), fmt % args)

        def _send_text(self, status: HTTPStatus, body: str) -> None:
            payload = body.encode("utf-8")
            self.send_response(status)
            self.send_header("Content-Type", "text/plain; charset=utf-8")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)

    return ThreadingHTTPServer((host, port), Handler)
