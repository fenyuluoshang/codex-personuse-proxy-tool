import importlib.util
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch


ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("verify_codex", ROOT / "scripts" / "verify-codex.py")
VERIFY = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(VERIFY)


class IsolationTests(unittest.TestCase):
    def test_child_environment_drops_existing_auth_and_config(self):
        inherited = {
            "PATH": "test-path", "SYSTEMROOT": "test-system",
            "CODEX_HOME": "existing-user-home", "OPENAI_API_KEY": "must-not-inherit",
            "CODEX_API_KEY": "must-not-inherit", "HTTPS_PROXY": "http://user:secret@example:1",
            "USERPROFILE": "existing-user-profile", "HOME": "existing-user-home",
        }
        with patch.dict(os.environ, inherited, clear=True):
            child = VERIFY.synthetic_environment(Path("isolated-home"))
            self.assertEqual(os.environ["CODEX_HOME"], "existing-user-home")
        self.assertEqual(child["CODEX_HOME"], "isolated-home")
        self.assertEqual(child["CODEX_ISOLATED_TEST_KEY"], VERIFY.FAKE_KEY)
        self.assertEqual(child["PATH"], "test-path")
        self.assertFalse(set(inherited) & set(child) - {"PATH", "SYSTEMROOT", "CODEX_HOME"})

    def test_port_reservation_uses_loopback(self):
        port = VERIFY.reserve_port()
        self.assertGreater(port, 0)
        self.assertLessEqual(port, 65535)


if __name__ == "__main__":
    unittest.main()
