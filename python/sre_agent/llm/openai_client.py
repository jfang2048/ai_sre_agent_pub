"""OpenAI client for LLM integration."""

import os
from typing import Optional, List, Dict, Any
from dataclasses import dataclass
import httpx
from openai import OpenAI


@dataclass
class ChatMessage:
    """A chat message."""
    role: str
    content: str


@dataclass
class ChatResponse:
    """A chat response."""
    content: str
    role: str = "assistant"
    finish_reason: Optional[str] = None
    prompt_tokens: int = 0
    completion_tokens: int = 0


class OpenAIClient:
    """Client for OpenAI API."""

    def __init__(
        self,
        api_key: Optional[str] = None,
        model: str = "gpt-4",
        base_url: Optional[str] = None,
        timeout: float = 30.0,
    ):
        """Initialize the OpenAI client.

        Args:
            api_key: OpenAI API key (defaults to OPENAI_API_KEY env var)
            model: Model to use
            base_url: Base URL for API (for custom endpoints)
            timeout: Request timeout in seconds
        """
        self.api_key = api_key or os.environ.get("OPENAI_API_KEY")
        if not self.api_key:
            raise ValueError("OpenAI API key required")

        self.model = model
        self.timeout = timeout

        client_kwargs = {"api_key": self.api_key}
        if base_url:
            client_kwargs["base_url"] = base_url

        self.client = OpenAI(**client_kwargs)

    def chat(
        self,
        messages: List[ChatMessage],
        max_tokens: int = 2000,
        temperature: float = 0.1,
        functions: Optional[List[Dict[str, Any]]] = None,
    ) -> ChatResponse:
        """Send a chat completion request.

        Args:
            messages: List of chat messages
            max_tokens: Maximum tokens in response
            temperature: Sampling temperature
            functions: Optional function definitions for function calling

        Returns:
            ChatResponse with the completion
        """
        request_messages = [
            {"role": m.role, "content": m.content}
            for m in messages
        ]

        request_kwargs = {
            "model": self.model,
            "messages": request_messages,
            "max_tokens": max_tokens,
            "temperature": temperature,
        }

        if functions:
            request_kwargs["functions"] = functions

        response = self.client.chat.completions.create(**request_kwargs)

        choice = response.choices[0]
        return ChatResponse(
            content=choice.message.content or "",
            role=choice.message.role,
            finish_reason=choice.finish_reason,
            prompt_tokens=response.usage.prompt_tokens,
            completion_tokens=response.usage.completion_tokens,
        )

    def complete(self, prompt: str, **kwargs) -> str:
        """Complete a prompt.

        Args:
            prompt: The prompt to complete
            **kwargs: Additional arguments for chat()

        Returns:
            Completed text
        """
        messages = [ChatMessage(role="user", content=prompt)]
        response = self.chat(messages, **kwargs)
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
        prompt = self._build_analysis_prompt(metrics, logs, alerts, context)

        response = self.complete(prompt)

        # Try to parse as JSON
        import json
        try:
            return json.loads(response)
        except json.JSONDecodeError:
            return {
                "summary": response,
                "predictions": [],
                "recommendations": [],
                "confidence": 0.5,
            }

    def _build_analysis_prompt(
        self,
        metrics: List[Dict[str, Any]],
        logs: List[str],
        alerts: List[Dict[str, Any]],
        context: str,
    ) -> str:
        """Build an analysis prompt."""
        return f"""You are an expert SRE analyzing system telemetry.

Context: {context}

Available Data:
- Metrics: {len(metrics)} data points
- Log entries: {len(logs)}
- Active alerts: {len(alerts)}

Analyze this data and provide:
1. Current system status summary
2. Any detected anomalies or issues
3. Predictions for the next hour
4. Recommended remediation actions

Be concise and focus on actionable insights. Format as JSON."""


def create_openai_client(config: Dict[str, Any]) -> OpenAIClient:
    """Create an OpenAI client from config.

    Args:
        config: Configuration dictionary

    Returns:
        OpenAIClient instance
    """
    return OpenAIClient(
        api_key=config.get("api_key"),
        model=config.get("model", "gpt-4"),
        base_url=config.get("base_url"),
        timeout=config.get("timeout", 30.0),
    )
