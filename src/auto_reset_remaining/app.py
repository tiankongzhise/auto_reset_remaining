"""Application entrypoint."""

from auto_reset_remaining.config_ui import confirm_config_on_startup


def main() -> None:
    config = confirm_config_on_startup()
    print(f"配置已确认：SQLite={config.sqlite.path}，日志目录={config.logs.query_log_dir}")
