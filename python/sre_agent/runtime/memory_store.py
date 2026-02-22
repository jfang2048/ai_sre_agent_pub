"""Bounded memory store for prior incident context."""

from collections import deque
from dataclasses import dataclass, field
from datetime import datetime, timezone
import json
import logging
import os
from threading import RLock
from typing import Any, Deque, Dict, List

from .structured_log import emit


def _utc_now() -> datetime:
    return datetime.now(timezone.utc)


@dataclass
class MemoryEntry:
    """Persistent record for one analyzed incident."""

    node_name: str
    issue_type: str
    severity: str
    confidence: float
    root_cause: str
    remediation: str
    metadata: Dict[str, Any] = field(default_factory=dict)
    timestamp: datetime = field(default_factory=_utc_now)


class BoundedMemoryStore:
    """Thread-safe bounded memory with optional JSONL persistence."""

    def __init__(self, max_events: int, file_path: str, logger: logging.Logger) -> None:
        self._entries: Deque[MemoryEntry] = deque(maxlen=max(10, max_events))
        self._file_path = file_path
        self._logger = logger
        self._lock = RLock()
        self._load_existing()

    def record(self, entry: MemoryEntry) -> None:
        with self._lock:
            self._entries.append(entry)
            if self._file_path:
                self._append_jsonl(entry)

    def recent(self, node_name: str, limit: int = 5) -> List[MemoryEntry]:
        if limit <= 0:
            return []
        with self._lock:
            matched = [entry for entry in reversed(self._entries) if entry.node_name == node_name]
        return matched[:limit]

    def _load_existing(self) -> None:
        if not self._file_path or not os.path.exists(self._file_path):
            return

        loaded = 0
        try:
            with open(self._file_path, "r", encoding="utf-8") as handle:
                for line in handle:
                    raw = line.strip()
                    if not raw:
                        continue
                    try:
                        payload = json.loads(raw)
                        timestamp = payload.get("timestamp")
                        entry = MemoryEntry(
                            node_name=str(payload.get("node_name", "")),
                            issue_type=str(payload.get("issue_type", "")),
                            severity=str(payload.get("severity", "info")),
                            confidence=float(payload.get("confidence", 0.0)),
                            root_cause=str(payload.get("root_cause", "")),
                            remediation=str(payload.get("remediation", "")),
                            metadata=dict(payload.get("metadata", {})),
                            timestamp=(
                                datetime.fromisoformat(timestamp)
                                if isinstance(timestamp, str)
                                else _utc_now()
                            ),
                        )
                        self._entries.append(entry)
                        loaded += 1
                    except (ValueError, TypeError, json.JSONDecodeError):
                        continue
        except OSError as exc:
            emit(self._logger, "memory_load_failed", level="warning", error=str(exc))
            return

        emit(self._logger, "memory_loaded", count=loaded, file_path=self._file_path)

    def _append_jsonl(self, entry: MemoryEntry) -> None:
        directory = os.path.dirname(self._file_path)
        if directory:
            os.makedirs(directory, exist_ok=True)

        payload = {
            "node_name": entry.node_name,
            "issue_type": entry.issue_type,
            "severity": entry.severity,
            "confidence": entry.confidence,
            "root_cause": entry.root_cause,
            "remediation": entry.remediation,
            "metadata": entry.metadata,
            "timestamp": entry.timestamp.isoformat(),
        }
        try:
            with open(self._file_path, "a", encoding="utf-8") as handle:
                handle.write(json.dumps(payload, ensure_ascii=True, sort_keys=True))
                handle.write("\n")
        except OSError as exc:
            emit(self._logger, "memory_persist_failed", level="warning", error=str(exc))

