"""Runtime configuration sourced from environment variables."""

import os
from dataclasses import dataclass, field
from typing import List


def _bool_env(name: str, default: bool = False) -> bool:
    raw = os.getenv(name)
    if raw is None:
        return default
    return raw.strip().lower() in {"1", "true", "yes", "on"}


def _int_env(name: str, default: int) -> int:
    raw = os.getenv(name)
    if raw is None:
        return default
    try:
        return int(raw)
    except ValueError:
        return default


def _float_env(name: str, default: float) -> float:
    raw = os.getenv(name)
    if raw is None:
        return default
    try:
        return float(raw)
    except ValueError:
        return default


def _list_env(name: str, default: List[str]) -> List[str]:
    raw = os.getenv(name, "")
    values = [item.strip() for item in raw.split(",") if item.strip()]
    if values:
        return values
    return default


@dataclass
class RuntimeConfig:
    """Configuration for explicit agent orchestration runtime."""

    reasoning_enabled: bool = True
    max_metrics: int = 400
    max_logs: int = 120

    context_enabled: bool = False
    context_paths: List[str] = field(default_factory=lambda: ["README.md", "docs", "configs"])
    context_top_k: int = 4
    context_max_chars: int = 1200
    context_extensions: List[str] = field(
        default_factory=lambda: [".md", ".txt", ".yaml", ".yml", ".json"]
    )

    tool_timeout_seconds: float = 2.0
    tool_retries: int = 1

    memory_max_events: int = 1000
    memory_file: str = ""

    llm_enabled: bool = False
    llm_provider: str = "openai"
    llm_model: str = "gpt-4o-mini"
    llm_api_key: str = ""
    llm_base_url: str = ""
    llm_timeout_seconds: float = 20.0

    runtime_backend: str = "haystack"
    runtime_fail_open: bool = True

    @classmethod
    def from_env(cls) -> "RuntimeConfig":
        """Load runtime settings from env vars with backward compatibility."""

        # Backward-compatible aliases from the old RAG toggle.
        old_rag_enabled = _bool_env("SRE_AGENT_ENABLE_RAG") or _bool_env("SRE_AGENT_RAG_ENABLED")
        context_enabled = _bool_env("SRE_AGENT_CONTEXT_ENABLED", old_rag_enabled)

        old_rag_paths = _list_env("SRE_AGENT_RAG_PATHS", [])
        context_paths = _list_env(
            "SRE_AGENT_CONTEXT_PATHS", old_rag_paths or ["README.md", "docs", "configs"]
        )

        old_rag_chars = _int_env("SRE_AGENT_RAG_MAX_CHARS", 1200)
        context_max_chars = _int_env(
            "SRE_AGENT_CONTEXT_MAX_CHARS",
            _int_env("SRE_AGENT_RAG_CHUNK_SIZE", old_rag_chars),
        )

        return cls(
            reasoning_enabled=_bool_env("SRE_AGENT_REASONING_ENABLED", True),
            max_metrics=_int_env("SRE_AGENT_REASONING_MAX_METRICS", 400),
            max_logs=_int_env("SRE_AGENT_REASONING_MAX_LOGS", 120),
            context_enabled=context_enabled,
            context_paths=context_paths,
            context_top_k=_int_env("SRE_AGENT_CONTEXT_TOP_K", 4),
            context_max_chars=context_max_chars,
            tool_timeout_seconds=_float_env("SRE_AGENT_TOOL_TIMEOUT_SECONDS", 2.0),
            tool_retries=_int_env("SRE_AGENT_TOOL_RETRIES", 1),
            memory_max_events=_int_env("SRE_AGENT_MEMORY_MAX_EVENTS", 1000),
            memory_file=os.getenv("SRE_AGENT_MEMORY_FILE", "").strip(),
            llm_enabled=_bool_env("SRE_AGENT_LLM_ENABLED", False),
            llm_provider=os.getenv("SRE_AGENT_LLM_PROVIDER", "openai").strip().lower(),
            llm_model=os.getenv("SRE_AGENT_LLM_MODEL", "gpt-4o-mini").strip(),
            llm_api_key=(
                os.getenv("SRE_AGENT_LLM_API_KEY")
                or os.getenv("OPENAI_API_KEY")
                or os.getenv("ANTHROPIC_API_KEY")
                or os.getenv("SRE_AGENT_GEMINI_API_KEY")
                or ""
            ).strip(),
            llm_base_url=os.getenv("SRE_AGENT_LLM_BASE_URL", "").strip(),
            llm_timeout_seconds=_float_env("SRE_AGENT_LLM_TIMEOUT_SECONDS", 20.0),
            runtime_backend=os.getenv("SRE_AGENT_RUNTIME_BACKEND", "haystack").strip().lower(),
            runtime_fail_open=_bool_env("SRE_AGENT_RUNTIME_FAIL_OPEN", True),
        )
