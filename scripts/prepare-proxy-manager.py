"""Export and patch the pinned proxy-manager revision in an isolated directory."""
from pathlib import Path
import json
import shutil
import subprocess
import sys
import tarfile


def main(repo, output, expected):
    repo = Path(repo).resolve()
    output = Path(output).resolve()
    actual = subprocess.check_output(["git", "-C", str(repo), "rev-parse", "HEAD"], text=True).strip()
    if actual != expected:
        raise SystemExit("unexpected upstream commit: expected %s, got %s" % (expected, actual))
    if output.exists():
        shutil.rmtree(output)
    output.mkdir(parents=True)
    archive = subprocess.check_output(["git", "-C", str(repo), "archive", "--format=tar", expected])
    with tarfile.open(fileobj=__import__('io').BytesIO(archive), mode="r:") as tf:
        for member in tf.getmembers():
            target = (output / member.name).resolve()
            if output not in target.parents and target != output:
                raise SystemExit("unsafe archive member: " + member.name)
            if member.issym() or member.islnk():
                raise SystemExit("links are not allowed in an isolated source archive: " + member.name)
        tf.extractall(output)
    (output / ".codex-proxy-isolated-source.json").write_text(
        json.dumps({"commit": actual, "source": str(repo)}, indent=2), encoding="utf-8")
    patch = repo.parent.parent / "patches" / "proxy-manager" / "patch.py"
    subprocess.check_call([sys.executable, str(patch), str(output)])
    print(str(output))


if __name__ == "__main__":
    if len(sys.argv) != 4:
        raise SystemExit("usage: prepare-proxy-manager.py SUBMODULE OUTPUT EXPECTED_COMMIT")
    main(*sys.argv[1:])
