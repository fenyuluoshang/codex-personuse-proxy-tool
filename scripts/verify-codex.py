#!/usr/bin/env python3
"""Run the installed Codex CLI against a local fixture in a temporary home.

Only synthetic credentials and the fixture's constant response are used. No
existing Codex home, auth cache, daemon, or project configuration is read.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import urllib.request


ROOT = Path(__file__).resolve().parents[1]
FAKE_KEY = "codex-isolation-test-only-not-a-real-key"
OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def codex_command(explicit: str | None) -> list[str]:
    executable = explicit or shutil.which("codex")
    if not executable:
        raise RuntimeError("Codex CLI was not found; pass --codex-executable.")
    path = Path(executable).resolve()
    if os.name == "nt" and path.suffix.lower() in {".cmd", ".ps1"}:
        vendor = path.parent / "node_modules" / "@openai" / "codex"
        matches = sorted(vendor.glob("node_modules/@openai/codex-win32-*/vendor/*/bin/codex.exe"))
        if matches:
            return [str(matches[0])]
        script = vendor / "bin" / "codex.js"
        node = shutil.which("node")
        if script.is_file() and node:
            return [node, str(script)]
    return [str(path)]


def synthetic_environment(home: Path) -> dict[str, str]:
    # Inherit OS/runtime necessities, not API keys, proxy credentials, or Codex
    # overrides. CODEX_HOME is set only on this child process dictionary.
    allowed = {"PATH", "PATHEXT", "SYSTEMROOT", "WINDIR", "COMSPEC", "TEMP", "TMP", "LANG", "LC_ALL", "NUMBER_OF_PROCESSORS"}
    env = {key: value for key, value in os.environ.items() if key.upper() in allowed}
    env.update({"CODEX_HOME": str(home), "CODEX_ISOLATED_TEST_KEY": FAKE_KEY,
                "NO_PROXY": "127.0.0.1,localhost,::1", "RUST_LOG": "off"})
    return env


def fetch(url: str) -> dict:
    with OPENER.open(url, timeout=3) as response:
        return json.load(response)


def reserve_port() -> int:
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return listener.getsockname()[1]


def stop_owned(process: subprocess.Popen | None) -> None:
    if process is not None and process.poll() is None:
        process.terminate()
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=5)


def run_case(command: list[str], work: Path, home: Path, base: str,
             requires_auth: bool, timeout: int) -> dict:
    home.mkdir()
    env = synthetic_environment(home)
    overrides = {
        "model": "mock-model", "model_provider": "isolated_mock", "web_search": "disabled",
        "model_providers.isolated_mock.name": "Isolated local mock",
        "model_providers.isolated_mock.base_url": base + "/v1",
        "model_providers.isolated_mock.env_key": "CODEX_ISOLATED_TEST_KEY",
        "model_providers.isolated_mock.wire_api": "responses",
        "model_providers.isolated_mock.requires_openai_auth": requires_auth,
        "model_providers.isolated_mock.supports_websockets": False,
        "model_providers.isolated_mock.request_max_retries": 0,
        "model_providers.isolated_mock.stream_max_retries": 0,
        "model_providers.isolated_mock.stream_idle_timeout_ms": 15000,
        "analytics.enabled": False, "feedback.enabled": False,
    }
    args = command + ["--no-daemon", "exec", "--ephemeral", "--ignore-user-config", "--ignore-rules",
                      "--skip-git-repo-check", "--sandbox", "read-only", "--json", "--cd", str(work)]
    for key, value in overrides.items():
        args += ["-c", key + "=" + json.dumps(value)]
    args += ["Reply with the fixture response. Do not call tools."]
    requests_before = fetch(base + "/__mock/requests")["requests"]
    process = None
    try:
        process = subprocess.Popen(args, cwd=work, env=env, stdin=subprocess.DEVNULL,
                                   stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                   text=True, encoding="utf-8", errors="replace")
        stdout, stderr = process.communicate(timeout=timeout)
    finally:
        stop_owned(process)
    events = []
    for line in stdout.splitlines():
        try:
            events.append(json.loads(line))
        except json.JSONDecodeError:
            continue
    meta = fetch(base + "/__mock/requests")
    last = meta["last"]
    expected_hash = hashlib.sha256(("Bearer " + FAKE_KEY).encode()).hexdigest()
    messages = [e.get("item", {}).get("text", "") for e in events if e.get("type") == "item.completed"]
    result = {
        "requires_openai_auth": requires_auth,
        "exit_code": process.returncode,
        "turn_completed": any(e.get("type") == "turn.completed" for e in events),
        "fixture_response_received": "mock response" in messages,
        "model": last.get("model"), "path": last.get("path"), "request_count": meta["requests"] - requests_before,
        "synthetic_authorization_matched": last.get("authorization_sha256") == expected_hash,
        "cookie_absent": not last.get("cookie_present"),
        "auth_cache_created": (home / "auth.json").exists(),
    }
    # Never dump raw process stderr/stdout or arbitrary metadata. Even in this
    # synthetic test, keep reports in a small allowlisted shape.
    if process.returncode or not result["turn_completed"]:
        result["failure_category"] = "codex_turn_failed"
        result["stderr_present"] = bool(stderr)
    return result


def verify(executable: str | None, timeout: int) -> dict:
    command = codex_command(executable)
    with tempfile.TemporaryDirectory(prefix="codex-local-verification-") as directory:
        temporary = Path(directory)
        fixture = temporary / ("mock-provider.exe" if os.name == "nt" else "mock-provider")
        build = subprocess.run(["go", "build", "-o", str(fixture), "."],
                               cwd=ROOT / "fixtures" / "mock-provider", capture_output=True,
                               text=True, timeout=120)
        if build.returncode:
            raise RuntimeError("Mock provider build failed; run its Go tests for details.")
        port = reserve_port()
        base = f"http://127.0.0.1:{port}"
        provider = subprocess.Popen([str(fixture), "-addr", f"127.0.0.1:{port}"],
                                    cwd=temporary, stdin=subprocess.DEVNULL,
                                    stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        try:
            deadline = time.monotonic() + 10
            while True:
                try:
                    fetch(base + "/healthz")
                    break
                except OSError:
                    if time.monotonic() > deadline or provider.poll() is not None:
                        raise RuntimeError("Mock provider did not become healthy.") from None
                    time.sleep(0.1)
            version_env = synthetic_environment(temporary / "version-home")
            version = subprocess.run(command + ["--version"], cwd=temporary,
                                     env=version_env, capture_output=True, text=True, timeout=15)
            work = temporary / "work"
            work.mkdir()
            cases = [run_case(command, work, temporary / ("home-" + str(auth).lower()), base, auth, timeout)
                     for auth in (False, True)]
            return {"codex_version": version.stdout.strip(), "cases": cases,
                    "scope": "isolated CLI plus synthetic env_key; no existing login cache or real account validation"}
        finally:
            stop_owned(provider)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--codex-executable")
    parser.add_argument("--timeout", type=int, default=60)
    parser.add_argument("--report", type=Path, help="optional redacted JSON report")
    args = parser.parse_args()
    try:
        report = verify(args.codex_executable, args.timeout)
    except (RuntimeError, subprocess.TimeoutExpired, OSError) as error:
        print(json.dumps({"result": "failed", "category": type(error).__name__}, ensure_ascii=False))
        return 1
    output = json.dumps(report, ensure_ascii=False, indent=2)
    print(output)
    if args.report:
        args.report.parent.mkdir(parents=True, exist_ok=True)
        args.report.write_text(output + "\n", encoding="utf-8")
    return 0 if all(c["exit_code"] == 0 and c["turn_completed"] and c["fixture_response_received"]
                    and c["synthetic_authorization_matched"] and c["cookie_absent"]
                    and c["request_count"] == 1 and c["path"] == "/v1/responses"
                    and not c["auth_cache_created"] for c in report["cases"]) else 1


if __name__ == "__main__":
    sys.exit(main())
