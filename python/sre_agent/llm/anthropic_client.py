"""Anthropic Claude client for LLM integration."""

import os
from dataclasses import dataclass
from typing import Any, Dict, List, Optional

try:
    from anthropic import Anthropic  # type: ignore
except Exception:  # pragma: no cover - optional dependency for local unit tests

    class Anthropic:  # type: ignore
        def __init__(self, *_args: Any, **_kwargs: Any) -> None:
            raise ImportError("anthropic package is required to use AnthropicClient")


@dataclass
class Message:
    """A message for Claude."""

    role: str
    content: str


@dataclass
class CompletionResponse:
    """A completion response."""

    content: str
    role: str = "assistant"
    stop_reason: Optional[str] = None
    input_tokens: int = 0
    output_tokens: int = 0


class AnthropicClient:
    """Client for Anthropic Claude API."""

    def __init__(
        self,
        api_key: Optional[str] = None,
        model: str = "claude-3-opus-20240229",
        timeout: float = 30.0,
        max_tokens: int = 4096,
    ):
        """Initialize the Anthropic client.

        Args:
            api_key: Anthropic API key (defaults to ANTHROPIC_API_KEY env var)
            model: Model to use
            timeout: Request timeout in seconds
            max_tokens: Default max tokens for responses
        """
        self.api_key = api_key or os.environ.get("ANTHROPIC_API_KEY")
        if not self.api_key:
            raise ValueError("Anthropic API key required")

        self.model = model
        self.timeout = timeout
        self.max_tokens = max_tokens

        self.client = Anthropic(api_key=self.api_key, timeout=timeout)

    def message(
        self,
        messages: List[Message],
        max_tokens: Optional[int] = None,
        temperature: float = 0.1,
        system: Optional[str] = None,
    ) -> CompletionResponse:
        """Send a message request.

        Args:
            messages: List of messages (conversation history)
            max_tokens: Maximum tokens in response
            temperature: Sampling temperature
            system: Optional system prompt

        Returns:
            CompletionResponse with the response
        """
        if max_tokens is None:
            max_tokens = self.max_tokens

        # Build messages for Claude API
        # Claude uses a different format - combine user/assistant messages
        prompt = self._format_messages(messages)

        request_kwargs = {
            "model": self.model,
            "max_tokens": max_tokens,
            "temperature": temperature,
            "messages": prompt,
        }

        if system:
            request_kwargs["system"] = system

        response = self.client.messages.create(**request_kwargs)

        return CompletionResponse(
            content=response.content[0].text,
            role="assistant",
            stop_reason=response.stop_reason,
            input_tokens=response.usage.input_tokens,
            output_tokens=response.usage.output_tokens,
        )

    def complete(self, prompt: str, **kwargs) -> str:
        """Complete a prompt.

        Args:
            prompt: The prompt to complete
            **kwargs: Additional arguments for message()

        Returns:
            Completed text
        """
        messages = [Message(role="user", content=prompt)]
        response = self.message(messages, **kwargs)
        return response.content

    def analyze(
        self,
        metrics: List[Dict[str, Any]],
        logs: List[str],
        alerts: List[Dict[str, Any]],
        context: str = "",
    ) -> Dict[str, Any]:
        """Analyze system data.

        Args:
            metrics: List of metric data
            logs: List of log entries
            alerts: List of alert data
            context: Additional context

        Returns:
            Analysis result with predictions and recommendations
        """
        system_prompt = """You are Claude, an expert SRE assistant.
Analyze system telemetry and provide actionable insights.
Be concise and factual. Focus on preventing incidents."""

        user_prompt = self._build_analysis_prompt(metrics, logs, alerts, context)

        messages = [Message(role="user", content=user_prompt)]

        response = self.message(messages, system=system_prompt)

        # Try to parse as JSON
        import json

        try:
            return json.loads(response.content)
        except json.JSONDecodeError:
            return {
                "summary": response.content,
                "predictions": [],
                "recommendations": [],
                "confidence": 0.5,
            }

    def _format_messages(self, messages: List[Message]) -> List[Dict[str, str]]:
        """Format messages for Claude API."""
        # Claude expects alternating user/assistant messages
        # Combine consecutive messages of the same role
        formatted = []
        for msg in messages:
            formatted.append({"role": msg.role, "content": msg.content})
        return formatted

    def _build_analysis_prompt(
        self,
        metrics: List[Dict[str, Any]],
        logs: List[str],
        alerts: List[Dict[str, Any]],
        context: str,
    ) -> str:
        """Build an analysis prompt."""
        # Format metrics summary
        metrics_summary = ""
        if metrics:
            metrics_summary = f"\nMetrics ({len(metrics)} data points):"
            for m in metrics[:10]:  # Limit to 10 for brevity
                metrics_summary += f"\n  - {m.get('name', 'unknown')}: {m.get('value', 'N/A')}"

        # Format logs summary
        logs_summary = ""
        if logs:
            logs_summary = f"\nRecent Logs ({len(logs)} entries):"
            for log in logs[:5]:  # Limit to 5 for brevity
                logs_summary += f"\n  - {log}"

        # Format alerts summary
        alerts_summary = ""
        if alerts:
            alerts_summary = f"\nActive Alerts ({len(alerts)}):"
            for alert in alerts:
                alerts_summary += (
                    f"\n  - {alert.get('name', 'unknown')}: {alert.get('severity', 'unknown')}"
                )

        return f"""SRE Analysis Request

Context: {context}
{metrics_summary}
{logs_summary}
{alerts_summary}

Please provide:
1. Current system status summary
2. Detected issues or anomalies
3. Predictions for the next hour
4. Recommended actions

Format your response as JSON with keys: summary, issues, predictions, recommendations, confidence."""


def create_anthropic_client(config: Dict[str, Any]) -> AnthropicClient:
    """Create an Anthropic client from config.

    Args:
        config: Configuration dictionary

    Returns:
        AnthropicClient instance
    """
    return AnthropicClient(
        api_key=config.get("api_key"),
        model=config.get("model", "claude-3-opus-20240229"),
        timeout=config.get("timeout", 30.0),
        max_tokens=config.get("max_tokens", 4096),
    )
