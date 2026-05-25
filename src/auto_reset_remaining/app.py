"""Application entrypoint.

The real UI and monitoring code is added in follow-up commits. Keeping this
entrypoint tiny lets `uv run auto-reset-remaining` work from the first commit.
"""


def main() -> None:
    print("auto-reset-remaining Python local edition")
