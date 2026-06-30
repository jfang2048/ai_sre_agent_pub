"""Prompt construction for LLM-backed SRE reasoning."""

from __future__ import annotations

from typing import Sequence


class PromptBuilder:
    """Build system prompts for SRE agent LLM clients."""

    def build_system_prompt(self, role: str, capabilities: Sequence[str]) -> str:
        capability_lines = "\n".join(f"- {capability}" for capability in capabilities)
        return (
            f"You are {role}.\n"
            "Your capabilities are:\n"
            f"{capability_lines}\n"
            "Provide concise, actionable, and evidence-based responses."
        )
