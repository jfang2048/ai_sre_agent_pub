"""Issue classification and explanation primitives for SRE telemetry."""

import json
import logging
import os
import stat
import tempfile
from dataclasses import dataclass
from enum import Enum
from pathlib import Path
from typing import Any, Dict, List, Optional, Tuple

import numpy as np

# Optional model backends; rule-based classification still works without them.
try:
    from sklearn.cluster import DBSCAN
    from sklearn.ensemble import IsolationForest, RandomForestClassifier
    from sklearn.preprocessing import StandardScaler

    HAS_SKLEARN = True
except ImportError:
    HAS_SKLEARN = False
    logging.warning("scikit-learn not available, using rule-based fallback")

try:
    import tensorflow as tf

    HAS_TF = True
except ImportError:
    HAS_TF = False
    logging.warning("TensorFlow not available, using simpler models")


logger = logging.getLogger(__name__)


class IssueCategory(Enum):
    """Categories of issues."""

    UNKNOWN = "unknown"
    CPU_SATURATION = "cpu_saturation"
    MEMORY_PRESSURE = "memory_pressure"
    DISK_IO_BOTTLENECK = "disk_io_bottleneck"
    NETWORK_SATURATION = "network_saturation"
    APPLICATION_ERROR = "application_error"
    RESOURCE_LEAK = "resource_leak"
    CASCADING_FAILURE = "cascading_failure"
    CAPACITY_ISSUE = "capacity_issue"
    CONFIGURATION_ERROR = "configuration_error"
    EXTERNAL_DEPENDENCY = "external_dependency"


class Severity(Enum):
    """Severity levels."""

    INFO = "info"
    WARNING = "warning"
    ERROR = "error"
    CRITICAL = "critical"


@dataclass
class MetricData:
    """A single metric measurement."""

    name: str
    value: float
    timestamp: str
    labels: Dict[str, str] = None


@dataclass
class LogEntry:
    """A log entry."""

    message: str
    level: str
    timestamp: str
    source: str = ""
    labels: Dict[str, str] = None


@dataclass
class Classification:
    """Issue classification result."""

    category: IssueCategory
    severity: Severity
    confidence: float
    description: str
    factors: List[str]
    related_metrics: List[str]
    method: str  # "rules", "ml", "hybrid"


@dataclass
class Suggestion:
    """Remediation suggestion."""

    type: str
    title: str
    description: str
    confidence: float
    risk_level: str
    steps: List[Dict[str, Any]]
    reasoning: str


@dataclass
class Explanation:
    """Human-readable explanation."""

    summary: str
    what_happened: str
    why_happened: str
    impact: str
    next_steps: str


class IssueClassifier:
    """Classify telemetry with rules and optional trained models."""

    FEATURE_NAMES = [
        "cpu_usage",
        "memory_usage",
        "disk_usage",
        "disk_io_util",
        "load_1m",
        "network_rx_util",
        "network_tx_util",
        "swap_usage",
        "fd_usage",
        "iowait",
    ]

    THRESHOLDS = {
        "cpu_usage": {"warning": 70, "error": 85, "critical": 95},
        "memory_usage": {"warning": 75, "error": 90, "critical": 97},
        "disk_usage": {"warning": 80, "error": 90, "critical": 95},
        "disk_io_util": {"warning": 70, "error": 85, "critical": 95},
        "load_1m": {"warning": 4, "error": 8, "critical": 16},
        "network_rx_util": {"warning": 70, "error": 85, "critical": 95},
        "network_tx_util": {"warning": 70, "error": 85, "critical": 95},
    }

    def __init__(self, model_path: Optional[str] = None):
        """Initialize model state and load a saved classifier when provided."""
        self.scaler = None
        self.anomaly_detector = None
        self.classifier = None
        self.is_trained = False

        if HAS_SKLEARN:
            self.scaler = StandardScaler()
            self.anomaly_detector = IsolationForest(
                contamination=0.1, random_state=42, n_estimators=100
            )
            self.classifier = RandomForestClassifier(n_estimators=100, random_state=42)

        if model_path and os.path.exists(model_path):
            self._load_model(model_path)

    def classify(
        self, metrics: List[MetricData], logs: List[LogEntry] = None
    ) -> List[Classification]:
        """Classify current metric and log evidence."""
        classifications = []

        metric_dict = {m.name: m.value for m in metrics}

        rule_classifications = self._classify_by_rules(metric_dict)
        classifications.extend(rule_classifications)

        if logs:
            log_classifications = self._classify_by_logs(logs)
            classifications.extend(log_classifications)

        if HAS_SKLEARN and self.is_trained:
            ml_classifications = self._classify_by_ml(metric_dict)
            classifications = self._merge_classifications(classifications, ml_classifications)

        if HAS_SKLEARN:
            self._detect_anomalies(metric_dict, classifications)

        return classifications

    def _classify_by_rules(self, metrics: Dict[str, float]) -> List[Classification]:
        """Apply rule-based classification."""
        classifications = []

        # Normalize metric names
        m = self._normalize_metrics(metrics)

        # CPU saturation
        cpu = m.get("cpu_usage", 0)
        load = m.get("load_1m", 0)
        if cpu > 90:
            classifications.append(
                Classification(
                    category=IssueCategory.CPU_SATURATION,
                    severity=Severity.CRITICAL,
                    confidence=0.90,
                    description=f"Critical CPU saturation at {cpu:.1f}%",
                    factors=["CPU usage exceeds 90%"],
                    related_metrics=["system.cpu.usage"],
                    method="rules",
                )
            )
        elif cpu > 80 and load > 4:
            classifications.append(
                Classification(
                    category=IssueCategory.CPU_SATURATION,
                    severity=Severity.WARNING,
                    confidence=0.75,
                    description=f"High CPU at {cpu:.1f}% with load {load:.2f}",
                    factors=["High CPU with elevated load"],
                    related_metrics=["system.cpu.usage", "system.load.1m"],
                    method="rules",
                )
            )

        # Memory pressure
        mem = m.get("memory_usage", 0)
        swap = m.get("swap_usage", 0)
        if mem > 95:
            classifications.append(
                Classification(
                    category=IssueCategory.MEMORY_PRESSURE,
                    severity=Severity.CRITICAL,
                    confidence=0.92,
                    description=f"Critical memory pressure at {mem:.1f}%",
                    factors=["Memory usage exceeds 95%"],
                    related_metrics=["system.memory.usage"],
                    method="rules",
                )
            )
        elif mem > 85 and swap > 50:
            classifications.append(
                Classification(
                    category=IssueCategory.MEMORY_PRESSURE,
                    severity=Severity.ERROR,
                    confidence=0.80,
                    description=f"Memory pressure with swap: mem={mem:.1f}%, swap={swap:.1f}%",
                    factors=["High memory with significant swap"],
                    related_metrics=["system.memory.usage", "system.swap.usage"],
                    method="rules",
                )
            )

        # Disk I/O
        disk_io = m.get("disk_io_util", 0)
        iowait = m.get("iowait", 0)
        disk_usage = m.get("disk_usage", 0)
        if disk_io > 90 or iowait > 30:
            classifications.append(
                Classification(
                    category=IssueCategory.DISK_IO_BOTTLENECK,
                    severity=Severity.CRITICAL,
                    confidence=0.85,
                    description=f"Disk I/O bottleneck: util={disk_io:.1f}%, iowait={iowait:.1f}%",
                    factors=["Extreme disk utilization or I/O wait"],
                    related_metrics=["system.disk.io.utilization", "system.cpu.iowait"],
                    method="rules",
                )
            )
        if disk_usage > 90:
            classifications.append(
                Classification(
                    category=IssueCategory.CAPACITY_ISSUE,
                    severity=Severity.CRITICAL,
                    confidence=0.90,
                    description=f"Disk capacity critical at {disk_usage:.1f}%",
                    factors=["Disk usage exceeds 90%"],
                    related_metrics=["system.disk.usage"],
                    method="rules",
                )
            )

        # Network
        net_rx = m.get("network_rx_util", 0)
        net_tx = m.get("network_tx_util", 0)
        if net_rx > 90 or net_tx > 90:
            classifications.append(
                Classification(
                    category=IssueCategory.NETWORK_SATURATION,
                    severity=Severity.CRITICAL,
                    confidence=0.85,
                    description=f"Network saturation: rx={net_rx:.1f}%, tx={net_tx:.1f}%",
                    factors=["Network utilization extremely high"],
                    related_metrics=["system.network.rx.utilization"],
                    method="rules",
                )
            )

        return classifications

    def _classify_by_logs(self, logs: List[LogEntry]) -> List[Classification]:
        """Classify based on log patterns."""
        classifications = []

        for log in logs:
            msg = log.message.lower()

            if "oomkilled" in msg or "out of memory" in msg:
                classifications.append(
                    Classification(
                        category=IssueCategory.MEMORY_PRESSURE,
                        severity=Severity.CRITICAL,
                        confidence=0.95,
                        description="Process OOMKilled",
                        factors=["OOMKilled event in logs"],
                        related_metrics=["system.memory.usage"],
                        method="rules",
                    )
                )

            if "no space left" in msg or "disk full" in msg:
                classifications.append(
                    Classification(
                        category=IssueCategory.CAPACITY_ISSUE,
                        severity=Severity.CRITICAL,
                        confidence=0.95,
                        description="Disk space exhausted",
                        factors=["Disk full error in logs"],
                        related_metrics=["system.disk.usage"],
                        method="rules",
                    )
                )

            if "connection refused" in msg or "connection timed out" in msg:
                classifications.append(
                    Classification(
                        category=IssueCategory.EXTERNAL_DEPENDENCY,
                        severity=Severity.ERROR,
                        confidence=0.85,
                        description="External service connectivity issue",
                        factors=["Connection error in logs"],
                        related_metrics=[],
                        method="rules",
                    )
                )

            if "segmentation fault" in msg or "core dumped" in msg:
                classifications.append(
                    Classification(
                        category=IssueCategory.APPLICATION_ERROR,
                        severity=Severity.CRITICAL,
                        confidence=0.95,
                        description="Application crash detected",
                        factors=["Segfault or core dump in logs"],
                        related_metrics=[],
                        method="rules",
                    )
                )

        return classifications

    def _classify_by_ml(self, metrics: Dict[str, float]) -> List[Classification]:
        """Use ML model for classification."""
        if not self.is_trained:
            return []

        features = self._extract_features(metrics)
        X = np.array([features])

        X_scaled = self.scaler.transform(X)

        predictions = self.classifier.predict(X_scaled)
        probabilities = self.classifier.predict_proba(X_scaled)

        classifications = []
        for pred, probs in zip(predictions, probabilities):
            category = IssueCategory(pred)
            confidence = float(max(probs))

            if confidence > 0.5:
                classifications.append(
                    Classification(
                        category=category,
                        severity=self._infer_severity(metrics, category),
                        confidence=confidence,
                        description=f"ML model predicted {category.value}",
                        factors=["ML classification"],
                        related_metrics=[],
                        method="ml",
                    )
                )

        return classifications

    def _detect_anomalies(self, metrics: Dict[str, float], classifications: List[Classification]):
        """Detect anomalies using Isolation Forest."""
        if not HAS_SKLEARN:
            return

        features = self._extract_features(metrics)
        X = np.array([features])

        prediction = self.anomaly_detector.fit_predict(X)

        if prediction[0] == -1:
            existing_categories = {c.category for c in classifications}

            if IssueCategory.UNKNOWN not in existing_categories and not classifications:
                classifications.append(
                    Classification(
                        category=IssueCategory.UNKNOWN,
                        severity=Severity.WARNING,
                        confidence=0.60,
                        description="Anomalous metric pattern detected",
                        factors=["Isolation Forest anomaly detection"],
                        related_metrics=list(metrics.keys())[:5],
                        method="ml",
                    )
                )

    def train(self, training_data: List[Tuple[Dict[str, float], IssueCategory]]):
        """Train the classifier on historical data.

        Args:
            training_data: List of (metrics_dict, category) tuples
        """
        if not HAS_SKLEARN:
            logger.error("scikit-learn not available, cannot train")
            return

        X = []
        y = []

        for metrics, category in training_data:
            features = self._extract_features(metrics)
            X.append(features)
            y.append(category.value)

        X = np.array(X)
        y = np.array(y)

        self.scaler.fit(X)
        X_scaled = self.scaler.transform(X)

        self.classifier.fit(X_scaled, y)
        self.is_trained = True

        logger.info(f"Classifier trained on {len(training_data)} samples")

    def _normalize_metrics(self, metrics: Dict[str, float]) -> Dict[str, float]:
        """Normalize metric names to standard format."""
        mapping = {
            "system.cpu.usage": "cpu_usage",
            "system.memory.usage": "memory_usage",
            "system.disk.usage": "disk_usage",
            "system.disk.io.utilization": "disk_io_util",
            "system.load.1m": "load_1m",
            "system.network.rx.utilization": "network_rx_util",
            "system.network.tx.utilization": "network_tx_util",
            "system.swap.usage": "swap_usage",
            "system.cpu.iowait": "iowait",
        }

        result = {}
        for key, value in metrics.items():
            normalized = mapping.get(key, key.replace(".", "_"))
            result[normalized] = value

        return result

    def _extract_features(self, metrics: Dict[str, float]) -> List[float]:
        """Extract feature vector from metrics."""
        m = self._normalize_metrics(metrics)
        return [m.get(name, 0.0) for name in self.FEATURE_NAMES]

    def _infer_severity(self, metrics: Dict[str, float], category: IssueCategory) -> Severity:
        """Infer severity from metrics."""
        m = self._normalize_metrics(metrics)

        if category == IssueCategory.CPU_SATURATION:
            cpu = m.get("cpu_usage", 0)
            if cpu > 95:
                return Severity.CRITICAL
            elif cpu > 85:
                return Severity.ERROR
            return Severity.WARNING

        elif category == IssueCategory.MEMORY_PRESSURE:
            mem = m.get("memory_usage", 0)
            if mem > 95:
                return Severity.CRITICAL
            elif mem > 90:
                return Severity.ERROR
            return Severity.WARNING

        return Severity.WARNING

    def _merge_classifications(
        self, rules: List[Classification], ml: List[Classification]
    ) -> List[Classification]:
        """Merge rule-based and ML classifications."""
        by_category = {c.category: c for c in rules}

        for c in ml:
            if c.category in by_category:
                existing = by_category[c.category]
                if c.confidence > existing.confidence:
                    c.method = "hybrid"
                    by_category[c.category] = c
            else:
                by_category[c.category] = c

        return list(by_category.values())

    def _load_model(self, path: str):
        """Load a model from an owner-controlled, non-writable file."""
        import pickle

        model_path = Path(path).expanduser()
        with self._open_trusted_model(model_path) as f:
            # Pickle is retained for sklearn compatibility, but only after the
            # opened descriptor has passed ownership, type, and mode checks.
            data = pickle.load(f)  # nosec B301
            self.scaler = data.get("scaler", self.scaler)
            self.classifier = data.get("classifier", self.classifier)
            self.is_trained = data.get("is_trained", False)
        logger.info("Model loaded from %s", model_path)

    def save_model(self, path: str):
        """Atomically save the trained model with owner-only permissions."""
        import pickle

        candidate = Path(path).expanduser().absolute()
        candidate.parent.mkdir(parents=True, exist_ok=True)
        destination = candidate.parent.resolve(strict=True) / candidate.name
        if destination.is_symlink():
            raise ValueError("model path must not be a symbolic link")
        fd, temporary_path = tempfile.mkstemp(
            prefix=f".{destination.name}.", dir=destination.parent
        )
        try:
            os.fchmod(fd, 0o600)
            with os.fdopen(fd, "wb") as f:
                fd = -1
                pickle.dump(
                    {
                        "scaler": self.scaler,
                        "classifier": self.classifier,
                        "is_trained": self.is_trained,
                    },
                    f,
                )
                f.flush()
                os.fsync(f.fileno())
            os.replace(temporary_path, destination)
        finally:
            if fd >= 0:
                os.close(fd)
            try:
                os.unlink(temporary_path)
            except FileNotFoundError:
                pass
        logger.info("Model saved to %s", destination)

    @staticmethod
    def _open_trusted_model(path: Path):
        flags = os.O_RDONLY | getattr(os, "O_CLOEXEC", 0) | getattr(os, "O_NOFOLLOW", 0)
        try:
            fd = os.open(path, flags)
        except OSError as exc:
            if path.is_symlink():
                raise ValueError("model path must not be a symbolic link") from exc
            raise
        try:
            file_stat = os.fstat(fd)
            if not stat.S_ISREG(file_stat.st_mode):
                raise ValueError("model path must be a regular file")
            if file_stat.st_mode & 0o022:
                raise PermissionError(
                    "model file must not be writable by group or others"
                )
            if hasattr(os, "getuid") and file_stat.st_uid != os.getuid():
                raise PermissionError("model file must be owned by the current user")
            stream = os.fdopen(fd, "rb")
            fd = -1
            return stream
        finally:
            if fd >= 0:
                os.close(fd)


class ExplanationGenerator:
    """Generate issue explanations with an optional LLM path."""

    def __init__(self, llm_client=None):
        """Initialize the optional LLM client."""
        self.llm_client = llm_client

    def explain(
        self, classification: Classification, metrics: Dict[str, float], audience: str = "operator"
    ) -> Explanation:
        """Explain one classification for the requested audience."""
        if self.llm_client:
            try:
                return self._explain_with_llm(classification, metrics, audience)
            except Exception as e:
                logger.warning(f"LLM explanation failed: {e}")

        return self._explain_with_template(classification, metrics, audience)

    def _explain_with_template(
        self, c: Classification, metrics: Dict[str, float], audience: str
    ) -> Explanation:
        """Generate explanation using templates."""
        templates = {
            IssueCategory.MEMORY_PRESSURE: {
                "summary": "Memory at {mem:.0f}% - system under memory pressure",
                "what": "Memory usage has reached {mem:.1f}%, exceeding safe thresholds.",
                "why": "Possible causes: memory leak, insufficient allocation, or workload spike.",
                "impact": "Leads to OOM kills, crashes, and degraded performance.",
                "next": "Identify top consumers with 'top', kill idle tasks, or scale pods.",
            },
            IssueCategory.CPU_SATURATION: {
                "summary": "CPU at {cpu:.0f}% with load {load:.1f} - compute saturation",
                "what": "CPU usage is {cpu:.1f}% with system load at {load:.2f}.",
                "why": "Possible causes: runaway process, insufficient capacity, or inefficient code.",
                "impact": "Causes slow response, timeouts, and degraded user experience.",
                "next": "Check with 'top -o %CPU', look for runaway processes, consider scaling.",
            },
            IssueCategory.DISK_IO_BOTTLENECK: {
                "summary": "Disk I/O bottleneck - utilization {disk_io:.0f}%",
                "what": "Disk I/O is saturated at {disk_io:.1f}% with {iowait:.1f}% iowait.",
                "why": "Possible causes: heavy I/O, slow storage, or insufficient capacity.",
                "impact": "Slow application response, database timeouts, log delays.",
                "next": "Use 'iostat -x 1' to identify busy disks, 'iotop' for processes.",
            },
            IssueCategory.NETWORK_SATURATION: {
                "summary": "Network saturated - bandwidth limit reached",
                "what": "Network utilization has exceeded normal levels.",
                "why": "Possible causes: traffic spike, DDoS, or excessive application traffic.",
                "impact": "Packet loss, connection timeouts, degraded service.",
                "next": "Check with 'iftop' or 'nethogs', identify top consumers.",
            },
        }

        template = templates.get(
            c.category,
            {
                "summary": f"Issue: {c.description}",
                "what": c.description,
                "why": "; ".join(c.factors) if c.factors else "Unknown cause",
                "impact": "Impact depends on severity.",
                "next": "Investigate related metrics and logs.",
            },
        )

        m = {
            "cpu": metrics.get("system.cpu.usage", metrics.get("cpu_usage", 0)),
            "mem": metrics.get("system.memory.usage", metrics.get("memory_usage", 0)),
            "load": metrics.get("system.load.1m", metrics.get("load_1m", 0)),
            "disk_io": metrics.get("system.disk.io.utilization", metrics.get("disk_io_util", 0)),
            "iowait": metrics.get("system.cpu.iowait", metrics.get("iowait", 0)),
        }

        return Explanation(
            summary=template["summary"].format(**m),
            what_happened=template["what"].format(**m),
            why_happened=template["why"].format(**m),
            impact=(
                template["impact"].format(**m) if "{" in template["impact"] else template["impact"]
            ),
            next_steps=(
                template["next"].format(**m) if "{" in template["next"] else template["next"]
            ),
        )

    def _explain_with_llm(
        self, c: Classification, metrics: Dict[str, float], audience: str
    ) -> Explanation:
        """Generate explanation using LLM."""
        prompt = f"""You are an expert SRE explaining a system issue.

Issue: {c.category.value}
Severity: {c.severity.value}
Description: {c.description}
Contributing factors: {', '.join(c.factors)}

Current metrics:
{json.dumps(metrics, indent=2)}

Target audience: {audience}

Provide a clear, actionable explanation with:
1. One-line summary
2. What happened
3. Why it happened
4. Impact
5. Next steps

Keep it concise and practical."""

        response = self.llm_client.chat(prompt)

        lines = response.strip().split("\n")

        return Explanation(
            summary=lines[0] if lines else c.description,
            what_happened=lines[1] if len(lines) > 1 else c.description,
            why_happened=lines[2] if len(lines) > 2 else "",
            impact=lines[3] if len(lines) > 3 else "",
            next_steps=lines[4] if len(lines) > 4 else "",
        )


# ============================================================================
# Service Entry Point
# ============================================================================


def create_ai_service(
    model_path: Optional[str] = None, llm_client=None
) -> Tuple[IssueClassifier, ExplanationGenerator]:
    """Create classifier and explanation components."""
    classifier = IssueClassifier(model_path)
    explainer = ExplanationGenerator(llm_client)
    return classifier, explainer


if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO)

    classifier, explainer = create_ai_service()

    test_metrics = [
        MetricData(name="system.cpu.usage", value=92.5, timestamp="2024-02-01T12:00:00Z"),
        MetricData(name="system.memory.usage", value=78.0, timestamp="2024-02-01T12:00:00Z"),
        MetricData(name="system.load.1m", value=6.5, timestamp="2024-02-01T12:00:00Z"),
    ]

    classifications = classifier.classify(test_metrics)
    for c in classifications:
        print(f"Category: {c.category.value}")
        print(f"Severity: {c.severity.value}")
        print(f"Confidence: {c.confidence:.2f}")
        print(f"Description: {c.description}")
        print()

        metrics_dict = {m.name: m.value for m in test_metrics}
        explanation = explainer.explain(c, metrics_dict)
        print(f"Summary: {explanation.summary}")
        print(f"What: {explanation.what_happened}")
        print(f"Why: {explanation.why_happened}")
        print(f"Next: {explanation.next_steps}")
        print("---")
