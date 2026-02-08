import json
import logging
import os
from typing import Dict, List, Any, Optional

from .base import BaseAgent, AnalysisResult

logger = logging.getLogger(__name__)


class ReActRAGAgent(BaseAgent):
    """
    ReAct + RAG agent using LangChain.

    Provider options:
      - Gemini (SRE_AGENT_LLM_PROVIDER=gemini)
      - Local Ollama (SRE_AGENT_LLM_PROVIDER=local)
    """

    def __init__(self) -> None:
        super().__init__("react_rag_agent")
        self._initialized = False
        self._executor = None
        self._provider = os.getenv("SRE_AGENT_LLM_PROVIDER", "gemini").lower()
        self._model = None

    def _ensure_setup(self) -> None:
        if self._initialized:
            return

        try:
            from langchain.agents import AgentExecutor, create_react_agent
            from langchain.prompts import PromptTemplate
            from langchain_community.tools import Tool
            from langchain_community.vectorstores import FAISS
            from langchain.text_splitter import RecursiveCharacterTextSplitter
            from langchain_community.document_loaders import DirectoryLoader, TextLoader
            from langchain_google_genai import ChatGoogleGenerativeAI, GoogleGenerativeAIEmbeddings
            from langchain_community.embeddings import OllamaEmbeddings
            from langchain_community.chat_models import ChatOllama
        except Exception as exc:
            logger.error("ReActRAGAgent dependencies missing: %s", exc)
            self._initialized = False
            return

        rag_paths = os.getenv("SRE_AGENT_RAG_PATHS", "README.md,docs,configs")
        index_dir = os.getenv("SRE_AGENT_RAG_INDEX_DIR", ".rag_index")
        chunk_size = int(os.getenv("SRE_AGENT_RAG_CHUNK_SIZE", "1200"))
        chunk_overlap = int(os.getenv("SRE_AGENT_RAG_CHUNK_OVERLAP", "200"))

        docs = []
        for raw_path in [p.strip() for p in rag_paths.split(",") if p.strip()]:
            if os.path.isdir(raw_path):
                for glob_pattern in ["**/*.md", "**/*.yaml", "**/*.yml", "**/*.txt"]:
                    try:
                        docs.extend(DirectoryLoader(raw_path, glob=glob_pattern).load())
                    except Exception as e:
                        logger.warning("Failed to load %s from %s: %s", glob_pattern, raw_path, e)
            elif os.path.isfile(raw_path):
                try:
                    docs.extend(TextLoader(raw_path, encoding="utf-8").load())
                except UnicodeDecodeError:
                    logger.warning("Failed to decode %s as UTF-8, skipping", raw_path)
                except Exception as e:
                    logger.error("Failed to load %s: %s", raw_path, e)

        if not docs:
            logger.warning("No documents loaded from RAG paths: %s", rag_paths)

        splitter = RecursiveCharacterTextSplitter(
            chunk_size=chunk_size,
            chunk_overlap=chunk_overlap,
            separators=["\n\n", "\n", " ", ""],
        )
        split_docs = splitter.split_documents(docs) if docs else []

        if not split_docs:
            logger.warning("No document chunks created, RAG will have no context")

        embeddings = self._build_embeddings(
            GoogleGenerativeAIEmbeddings=GoogleGenerativeAIEmbeddings,
            OllamaEmbeddings=OllamaEmbeddings,
        )
        if embeddings is None:
            logger.error("ReActRAGAgent embeddings unavailable")
            return

        vectorstore = None
        if os.path.isdir(index_dir):
            try:
                vectorstore = FAISS.load_local(
                    index_dir, embeddings, allow_dangerous_deserialization=True
                )
            except Exception:
                vectorstore = None

        if vectorstore is None:
            vectorstore = FAISS.from_documents(split_docs, embeddings)
            os.makedirs(index_dir, exist_ok=True)
            vectorstore.save_local(index_dir)

        retriever = vectorstore.as_retriever(search_kwargs={"k": 4})

        tool = Tool(
            name="sre_docs_search",
            description="Searches SRE Agent docs/configs/runbooks for relevant context.",
            func=lambda q: "\n".join(
                d.page_content for d in retriever.get_relevant_documents(q)
            ),
        )

        llm = self._build_llm(ChatGoogleGenerativeAI, ChatOllama)
        if llm is None:
            logger.error("ReActRAGAgent LLM unavailable")
            return

        prompt = PromptTemplate.from_template(
            """You are an SRE incident analyst using tools to gather context.

You have access to the following tools:
{tools}

Use the following format:
Question: {input}
Thought: you should always think about what to do
Action: the action to take, should be one of [{tool_names}]
Action Input: the input to the action
Observation: the result of the action
Thought: I now know the final answer
Final: respond as JSON with keys:
  issue_detected (bool), issue_type (str), severity (str), confidence (float),
  root_cause (str), remediation (str), notes (str)
"""
        )

        agent = create_react_agent(llm, [tool], prompt)
        self._executor = AgentExecutor(agent=agent, tools=[tool], handle_parsing_errors=True)
        self._initialized = True

    def _build_embeddings(self, GoogleGenerativeAIEmbeddings, OllamaEmbeddings):
        if self._provider == "gemini":
            api_key = os.getenv("SRE_AGENT_GEMINI_API_KEY", "")
            if not api_key:
                logger.error("SRE_AGENT_GEMINI_API_KEY is required for Gemini embeddings")
                return None
            return GoogleGenerativeAIEmbeddings(
                model=os.getenv("SRE_AGENT_GEMINI_EMBEDDING_MODEL", "models/embedding-001"),
                google_api_key=api_key,
            )

        if self._provider == "local":
            return OllamaEmbeddings(
                model=os.getenv("SRE_AGENT_OLLAMA_EMBEDDING_MODEL", "nomic-embed-text"),
                base_url=os.getenv("SRE_AGENT_OLLAMA_BASE_URL", "http://localhost:11434"),
            )

        logger.error("Unknown SRE_AGENT_LLM_PROVIDER: %s", self._provider)
        return None

    def _build_llm(self, ChatGoogleGenerativeAI, ChatOllama):
        if self._provider == "gemini":
            api_key = os.getenv("SRE_AGENT_GEMINI_API_KEY", "")
            if not api_key:
                logger.error("SRE_AGENT_GEMINI_API_KEY is required for Gemini")
                return None
            self._model = os.getenv("SRE_AGENT_GEMINI_MODEL", "gemini-1.5-flash")
            return ChatGoogleGenerativeAI(
                model=self._model,
                google_api_key=api_key,
                temperature=0.2,
            )

        if self._provider == "local":
            self._model = os.getenv("SRE_AGENT_OLLAMA_MODEL", "llama3.1")
            return ChatOllama(
                model=self._model,
                base_url=os.getenv("SRE_AGENT_OLLAMA_BASE_URL", "http://localhost:11434"),
                temperature=0.2,
            )

        return None

    def analyze(
        self, metrics: List[Dict[str, Any]], logs: List[Dict[str, Any]]
    ) -> Optional[AnalysisResult]:
        self._ensure_setup()
        if not self._initialized or self._executor is None:
            return None

        metric_lines = []
        for m in metrics[:50]:
            name = m.get("name", "unknown")
            value = m.get("value", "n/a")
            metric_lines.append(f"- {name}: {value}")

        log_lines = []
        for l in logs[-50:]:
            msg = l.get("message", "")
            level = l.get("level", "info")
            log_lines.append(f"- [{level}] {msg}")

        question = "\n".join(
            [
                "Analyze the following metrics and logs with available docs/config context.",
                "Metrics:",
                *metric_lines,
                "Logs:",
                *log_lines,
            ]
        )

        result = self._executor.invoke({"input": question})
        output = result.get("output", "")

        parsed = None
        try:
            parsed = json.loads(output)
        except Exception:
            parsed = None

        if isinstance(parsed, dict):
            return AnalysisResult(
                issue_detected=bool(parsed.get("issue_detected", False)),
                issue_type=str(parsed.get("issue_type", "")),
                severity=str(parsed.get("severity", "info")),
                confidence=float(parsed.get("confidence", 0.0)),
                root_cause=str(parsed.get("root_cause", "")),
                remediation=str(parsed.get("remediation", "")),
                metadata={"notes": parsed.get("notes", ""), "provider": self._provider, "model": self._model},
            )

        return AnalysisResult(
            issue_detected=False,
            issue_type="react_rag",
            severity="info",
            confidence=0.2,
            root_cause=output,
            remediation="Review RAG analysis output.",
            metadata={"provider": self._provider, "model": self._model},
        )
