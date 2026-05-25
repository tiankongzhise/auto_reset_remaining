"""Application entrypoint."""

from auto_reset_remaining.config_ui import confirm_config_on_startup
from auto_reset_remaining.ui import run_main_window


def main() -> None:
    config = confirm_config_on_startup()
    run_main_window(config)
