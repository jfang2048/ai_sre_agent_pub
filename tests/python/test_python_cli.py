import unittest
from contextlib import redirect_stdout
from io import StringIO

from sre_agent import __version__
from sre_agent.cli import build_parser, main


class TestPythonCLI(unittest.TestCase):
    def test_version_flag(self) -> None:
        out = StringIO()
        with redirect_stdout(out):
            rc = main(["--version"])
        self.assertEqual(0, rc)
        self.assertEqual("0.5.0", out.getvalue().strip())

    def test_serve_command_parse(self) -> None:
        args = build_parser().parse_args(
            ["serve-ai", "--port", "50052", "--log-level", "INFO"]
        )
        self.assertEqual("serve-ai", args.command)
        self.assertEqual(50052, args.port)

    def test_version_semver(self) -> None:
        self.assertEqual("0.5.0", __version__)

    def test_no_command_prints_help(self) -> None:
        out = StringIO()
        with redirect_stdout(out):
            rc = main([])
        self.assertEqual(0, rc)
        self.assertIn("serve-ai", out.getvalue())


if __name__ == "__main__":
    unittest.main()
