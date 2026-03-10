"""Command-line entrypoint for the Python SRE runtime components."""

from __future__ import annotations

import argparse
from typing import Sequence

from . import __version__


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="sre-agent-python")
    parser.add_argument(
        "--version",
        action="store_true",
        help="Print version and exit",
    )

    subparsers = parser.add_subparsers(dest="command")
    subparsers.required = False

    serve_parser = subparsers.add_parser(
        "serve-ai",
        help="Run the Python AI service (JSON-RPC over HTTP).",
    )
    serve_parser.add_argument("--port", type=int, default=50052)
    serve_parser.add_argument("--model", type=str, default=None)
    serve_parser.add_argument("--log-level", type=str, default="INFO")
    return parser


def main(argv: Sequence[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)

    if args.version:
        print(__version__)
        return 0

    if args.command is None:
        parser.print_help()
        return 0

    if args.command == "serve-ai":
        import logging

        from .ai.service import serve

        log_level = getattr(args, "log_level", "INFO")
        port = getattr(args, "port", 50052)
        model = getattr(args, "model", None)
        logging.basicConfig(
            level=getattr(logging, str(log_level).upper()),
            format="%(asctime)s - %(name)s - %(levelname)s - %(message)s",
        )
        serve(port, model)
        return 0

    parser.error(f"unknown command: {args.command}")
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
