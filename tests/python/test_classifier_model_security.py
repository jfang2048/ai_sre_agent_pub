import os
import tempfile
import unittest
from pathlib import Path

from sre_agent.ai.classifier import IssueClassifier


class TestClassifierModelSecurity(unittest.TestCase):
    def test_saved_model_is_private_and_reloadable(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            model_path = Path(directory) / "classifier.pkl"
            classifier = IssueClassifier()

            classifier.save_model(str(model_path))

            self.assertEqual(0o600, model_path.stat().st_mode & 0o777)
            loaded = IssueClassifier(str(model_path))
            self.assertEqual(classifier.is_trained, loaded.is_trained)

    def test_loader_rejects_group_writable_model(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            model_path = Path(directory) / "classifier.pkl"
            classifier = IssueClassifier()
            classifier.save_model(str(model_path))
            model_path.chmod(0o620)

            with self.assertRaises(PermissionError):
                IssueClassifier(str(model_path))

    def test_loader_rejects_symbolic_link(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            model_path = Path(directory) / "classifier.pkl"
            link_path = Path(directory) / "classifier-link.pkl"
            classifier = IssueClassifier()
            classifier.save_model(str(model_path))
            os.symlink(model_path, link_path)

            with self.assertRaises(ValueError):
                IssueClassifier(str(link_path))


if __name__ == "__main__":
    unittest.main()
