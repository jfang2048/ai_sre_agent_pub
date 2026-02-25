"""Simple structured logging helper."""

import json
import logging
from datetime import datetime, timezone
from typing import Any, Dict


def _default(value: Any) -> str:
    if isinstance(value, datetime):
        return value.isoformat()
    return str(value)


def emit(logger: logging.Logger, event: str, level: str = "info", **fields: Any) -> None:
    """Emit a compact JSON log line."""
    payload: Dict[str, Any] = {
        "ts": datetime.now(timezone.utc).isoformat(),
        "event": event,
        **fields,
    }
    line = json.dumps(payload, ensure_ascii=True, default=_default, sort_keys=True)
    log_fn = getattr(logger, level, logger.info)
    log_fn(line)

