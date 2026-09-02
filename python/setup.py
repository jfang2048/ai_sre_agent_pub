"""Setup configuration for SRE Agent Python package."""

from setuptools import setup, find_packages

setup(
    name="sre-agent",
    version="0.95.0",
    description="AI-powered SRE agent for infrastructure monitoring and remediation",
    author="SRE Agent Team",
    license="GPL-3.0-only",
    packages=find_packages(),
    install_requires=[
        "haystack-ai>=2.9.0,<3.0.0",
        "openai>=1.0.0",
        "anthropic>=0.18.0",
        "numpy>=1.24.0",
        "scipy>=1.10.0",
        "grpcio>=1.50.0",
        "grpcio-tools>=1.50.0",
        "protobuf>=4.21.0",
    ],
    extras_require={
        "dev": [
            "pytest>=7.0.0",
            "pytest-cov>=4.0.0",
            "black>=23.0.0",
            "mypy>=1.0.0",
            "ruff>=0.1.0",
        ],
    },
    python_requires=">=3.10",
    entry_points={
        "console_scripts": [
            "sre-agent-python=sre_agent.cli:main",
        ],
    },
)
