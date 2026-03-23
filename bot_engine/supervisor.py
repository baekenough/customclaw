"""Process supervisor for managed worker/app restarts."""

from __future__ import annotations

import logging
import os
import signal
import subprocess
import sys
import time

from bot_engine.runtime_control import (
    RESTART_EXIT_CODE,
    RUNTIME_TARGET_ENV,
    SUPERVISED_ENV,
    consume_restart_request,
)

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(name)s] %(levelname)s: %(message)s",
)
log = logging.getLogger(__name__)

MODULE_BY_TARGET = {
    "worker": "bot_engine.worker",
    "app": "bot_engine.app",
}


def _terminate_child(child: subprocess.Popen) -> None:
    if child.poll() is not None:
        return

    child.terminate()
    try:
        child.wait(timeout=15)
        return
    except subprocess.TimeoutExpired:
        log.warning("Child did not exit after SIGTERM; forcing kill")

    child.kill()
    child.wait(timeout=5)


def _spawn_child(target: str) -> subprocess.Popen:
    env = os.environ.copy()
    env[SUPERVISED_ENV] = "1"
    env[RUNTIME_TARGET_ENV] = target
    module = MODULE_BY_TARGET[target]
    cmd = [sys.executable, "-m", module]
    log.info("Starting supervised child for %s: %s", target, " ".join(cmd))
    return subprocess.Popen(cmd, env=env)


def run_supervisor(target: str) -> int:
    if target not in MODULE_BY_TARGET:
        raise ValueError(f"Unsupported supervisor target: {target}")

    stop_requested = False
    child = _spawn_child(target)

    def handle_signal(signum, frame) -> None:
        nonlocal stop_requested
        log.warning("Supervisor received signal %d", signum)
        stop_requested = True
        _terminate_child(child)

    signal.signal(signal.SIGINT, handle_signal)
    signal.signal(signal.SIGTERM, handle_signal)

    while not stop_requested:
        exit_code = child.poll()
        if exit_code is not None:
            if exit_code == RESTART_EXIT_CODE:
                log.warning("Child requested restart for %s", target)
                child = _spawn_child(target)
                continue
            log.warning("Child exited for %s with code %s", target, exit_code)
            return exit_code

        request = consume_restart_request(target)
        if request:
            log.warning("Supervisor received restart request for %s: %s", target, request)
            _terminate_child(child)
            child = _spawn_child(target)
            continue

        time.sleep(1)

    return 0


def main() -> None:
    target = (
        sys.argv[1]
        if len(sys.argv) > 1
        else os.environ.get(RUNTIME_TARGET_ENV, "worker")
    )
    sys.exit(run_supervisor(target))


if __name__ == "__main__":
    main()
