"""Git worker placeholder. Full implementation in Phase 4."""

from __future__ import annotations

import logging
import time

logging.basicConfig(level=logging.INFO)
log = logging.getLogger(__name__)


def main():
    log.info("Git worker started (stub). Waiting for Phase 4 implementation...")
    while True:
        time.sleep(60)


if __name__ == "__main__":
    main()
