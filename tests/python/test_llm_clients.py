"""Unit tests for LLM-related client helpers."""

import unittest
from unittest.mock import patch

from _bootstrap import ensure_pythonpath

ensure_pythonpath()

# Import the package boundary before resolving patch targets. This keeps the
# test independent of whichever other test happened to import an LLM adapter.
from sre_agent import llm as _llm  # noqa: E402, F401


class TestLLMClient(unittest.TestCase):
    """OpenAI/Anthropic client wrappers and prompt builder checks."""

    @patch("sre_agent.llm.openai_client.OpenAI")
    def test_openai_client_init(self, _mock_openai):
        from sre_agent.llm.openai_client import OpenAIClient

        client = OpenAIClient(api_key="test-key")
        self.assertIsNotNone(client)
        self.assertEqual(client.api_key, "test-key")

    @patch("sre_agent.llm.anthropic_client.Anthropic")
    def test_anthropic_client_init(self, _mock_anthropic):
        from sre_agent.llm.anthropic_client import AnthropicClient

        client = AnthropicClient(api_key="test-key")
        self.assertIsNotNone(client)
        self.assertEqual(client.api_key, "test-key")

    def test_prompt_builder_basic(self):
        from sre_agent.llm.prompt_builder import PromptBuilder

        builder = PromptBuilder()
        prompt = builder.build_system_prompt(
            role="SRE Analyst",
            capabilities=["analyze metrics", "predict failures"],
        )

        self.assertIn("SRE Analyst", prompt)
        self.assertIn("analyze metrics", prompt)


if __name__ == "__main__":
    unittest.main()
