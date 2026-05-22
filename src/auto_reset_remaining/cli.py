from __future__ import annotations

import logging
import os
import signal
import sys
import threading

from .api import APIClient
from .config import load_config
from .http_server import build_server
from .mailer import SMTPMailer
from .monitor import Monitor
from .query_logger import QueryLogger
from .store import PostgresStore


def main(argv: list[str] | None = None) -> int:
    _ = argv
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
    logger = logging.getLogger("auto_reset_remaining")
    env_path = os.environ.get("ENV_FILE", ".env")

    try:
        config = load_config(env_path)
        config.validate()
    except Exception as exc:
        logger.error("invalid config: %s", exc)
        return 2

    try:
        store = PostgresStore(config.pg)
        store.init()
    except Exception as exc:
        logger.error("init postgres: %s", exc)
        return 1

    api_client = APIClient(
        rayplus_base_url=config.rayplus_base_url,
        rayplus_api_key=config.rayplus_api_key,
        rayplus_email=config.rayplus_email,
        rayplus_password=config.rayplus_password,
        codex_base_url=config.codex_base_url,
        subscription_id=config.subscription_id,
        balance_json_path=config.balance_json_path,
        user_agent=config.user_agent,
    )
    monitor = Monitor(
        config=config,
        api_client=api_client,
        mailer=SMTPMailer(config.smtp),
        store=store,
        query_logger=QueryLogger(config.query_log_dir),
        logger=logger,
    )
    try:
        monitor.initialize()
    except Exception as exc:
        logger.error("initialize monitor: %s", exc)
        store.close()
        return 1

    stop_event = threading.Event()
    monitor_thread = threading.Thread(target=monitor.run, args=(stop_event,), name="balance-monitor", daemon=True)
    host, port = config.http_bind()
    server = build_server(host, port, monitor, logger)

    def request_shutdown(signum: int, _frame: object) -> None:
        logger.info("received signal %s, shutting down", signum)
        stop_event.set()
        threading.Thread(target=server.shutdown, name="http-shutdown", daemon=True).start()

    for signum in (signal.SIGINT, signal.SIGTERM):
        try:
            signal.signal(signum, request_shutdown)
        except ValueError:
            pass

    monitor_thread.start()
    logger.info("HTTP server listening on %s:%s", host or "0.0.0.0", port)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        request_shutdown(signal.SIGINT, None)
    finally:
        stop_event.set()
        server.server_close()
        monitor_thread.join(timeout=10)
        store.close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
