#!/usr/bin/env python3
"""Exercise the actual, unmodified Bifrost image against a local fake upstream.

No real credentials or inference calls. --network=host is test-only and intended
for a Linux GitHub-hosted runner. PLUGIN_URL optionally checks public HTTPS loading.
"""
from __future__ import annotations

import copy
import hashlib
import http.server
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
TARGET = dict(line.split("=", 1) for line in (ROOT / "target.env").read_text().splitlines()
              if line and not line.startswith("#"))
PLUGIN = ROOT / "dist/bifrost-cliproxyapi-metadata.so"
TOKEN = "not-a-real-credential"  # Local fixture only, never an actual API key.
MODE_LOCK = threading.Lock()
MODE = "ok"
COUNTS = {"rich": 0, "standard": 0}
RICH = json.loads((ROOT / "testdata/catalog.json").read_text())
RICH["models"].append({"slug": "rich-only-never-add", "context_window": 100})
STANDARD_IDS = [m["slug"] for m in RICH["models"][:-1]] + ["bifrost-only-keep"]


def run(*args: str, capture: bool = False) -> str:
    result = subprocess.run(args, cwd=ROOT, check=True, text=True,
                            stdout=subprocess.PIPE if capture else None)
    return result.stdout.strip() if capture else ""


def free_port() -> int:
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


class Upstream(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_: object) -> None:
        pass  # Never log request headers.

    def do_GET(self) -> None:
        parsed = urllib.parse.urlsplit(self.path)
        if parsed.path == "/other/v1/models":
            value = {"data": [{"id": "other-model", "object": "model", "owned_by": "other"}]}
        elif parsed.path == "/v1/models":
            rich = urllib.parse.parse_qs(parsed.query).get("client_version") == ["pi"]
            with MODE_LOCK:
                COUNTS["rich" if rich else "standard"] += 1
                mode = MODE
            if rich:
                if self.headers.get("Authorization") != "Bearer " + TOKEN:
                    self.send_error(401)
                    return
                if mode == "500":
                    self.send_error(500)
                    return
                if mode == "invalid":
                    self.send_response(200)
                    self.end_headers()
                    self.wfile.write(b"{invalid-json")
                    return
                value = {"models": []} if mode == "empty" else RICH
            else:
                value = {"data": [{"id": model, "object": "model", "created": 1783616400,
                                   "owned_by": "fixture"} for model in STANDARD_IDS]}
        else:
            self.send_error(404)
            return
        data = json.dumps(value).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)


def parse_buildinfo(text: str) -> tuple[str, dict[str, str], dict[str, tuple[str, str]]]:
    lines = text.splitlines()
    version = lines[0].rsplit(" ", 1)[-1]
    settings: dict[str, str] = {}
    modules: dict[str, tuple[str, str]] = {}
    for line in lines[1:]:
        parts = line.strip().split("\t")
        if parts[0] == "build" and len(parts) > 1 and "=" in parts[1]:
            key, value = parts[1].split("=", 1)
            settings[key] = value.strip('"')
        elif parts[0] == "dep" and len(parts) >= 4:
            modules[parts[1]] = (parts[2], parts[3])
        elif parts[0] == "=>":
            raise RuntimeError("Host/plugin contains replacement modules; re-audit compatibility")
    return version, settings, modules


def audit_binary(image_id: str) -> None:
    (ROOT / ".build").mkdir(exist_ok=True)
    cid = run("docker", "create", image_id, capture=True)
    try:
        run("docker", "cp", f"{cid}:/app/main", str(ROOT / ".build/bifrost-host"))
    finally:
        run("docker", "rm", "-v", cid, capture=True)
    builder = f"bifrost-cliproxyapi-builder:{TARGET['GO_VERSION']}-{TARGET['GOARCH']}"
    command = ["docker", "run", "--rm", "--platform", "linux/amd64", "--mount",
               f"type=bind,src={ROOT},dst=/workspace,readonly", builder]
    host_text = run(*command, "go", "version", "-m", "/workspace/.build/bifrost-host", capture=True)
    (ROOT / "dist/host-buildinfo.txt").write_text(host_text + "\n")
    plugin_text = (ROOT / "dist/plugin-buildinfo.txt").read_text()
    host_go, host_settings, host_modules = parse_buildinfo(host_text)
    plugin_go, plugin_settings, plugin_modules = parse_buildinfo(plugin_text)
    expected = "go" + TARGET["GO_VERSION"]
    if host_go != expected or plugin_go != expected:
        raise RuntimeError(f"Exact toolchain mismatch: host={host_go}, plugin={plugin_go}, expected={expected}")
    for key, expected_value in {"GOOS": "linux", "GOARCH": "amd64", "GOAMD64": TARGET["GOAMD64"],
                                "CGO_ENABLED": "1", "-trimpath": "true", "-tags": TARGET["BUILD_TAGS"]}.items():
        if host_settings.get(key) != expected_value or plugin_settings.get(key) != expected_value:
            raise RuntimeError(f"Incompatible {key}: host={host_settings.get(key)}, plugin={plugin_settings.get(key)}")
    if host_settings.get("-race") == "true":
        raise RuntimeError("A race-enabled host is not this release target")
    revision = host_settings.get("vcs.revision")
    if revision and revision != TARGET["BIFROST_SOURCE"]:
        raise RuntimeError("Published host revision differs from the pinned source commit")
    for module in host_modules.keys() & plugin_modules.keys():
        if host_modules[module] != plugin_modules[module]:
            raise RuntimeError(f"Shared module mismatch: {module}")
    core = "github.com/maximhq/bifrost/core"
    if host_modules.get(core, (None,))[0] != TARGET["CORE_VERSION"]:
        raise RuntimeError("Host core version does not match target.env")
    elf = run(*command, "readelf", "-l", "/workspace/.build/bifrost-host", capture=True)
    (ROOT / "dist/host-elf.txt").write_text(elf + "\n")
    if "INTERP" not in elf:
        print("WARNING: official host has no ELF interpreter; static musl normally cannot dlopen plugins.", flush=True)
        # Still exercise the real host so the release gate observes the actual
        # result rather than inferring success/failure solely from build flags.


def fetch_json(port: int, path: str) -> dict:
    request = urllib.request.Request(f"http://127.0.0.1:{port}{path}")
    with urllib.request.urlopen(request, timeout=12) as response:
        return json.load(response)


def verify_models(response: dict, enriched: bool) -> None:
    models = {model["id"]: model for model in response["data"]}
    expected = {"cliproxyapi/" + model for model in STANDARD_IDS} | {"other-provider/other-model"}
    if set(models) != expected or len(response["data"]) != len(expected):
        raise RuntimeError(f"Availability was changed: {sorted(models)}")
    other = models["other-provider/other-model"]
    if other.get("context_length") is not None or other.get("reasoning") is not None:
        raise RuntimeError("Other provider was incorrectly enriched")
    if not enriched:
        return  # Fail-open must preserve listing availability, even with no cache.
    gpt = models["cliproxyapi/gpt-5.6-sol"]
    if (gpt.get("context_length"), gpt.get("max_output_tokens"), gpt.get("name")) != (272000, 128000, "GPT 5.6 Sol"):
        raise RuntimeError("Plugin did not enrich the actual native /v1/models response; inspect smoke logs")
    if gpt.get("architecture", {}).get("input_modalities") != ["text", "image"]:
        raise RuntimeError("Incorrect modalities at HTTP boundary")
    reasoning = gpt.get("reasoning") or {}
    if reasoning.get("supported_efforts") != ["low", "medium", "high", "xhigh", "max", "ultra"] or reasoning.get("default_effort") != "low":
        raise RuntimeError("Incorrect reasoning at HTTP boundary")
    if (models["cliproxyapi/claude-opus-4-6-thinking"].get("reasoning") or {}).get("supported_efforts"):
        raise RuntimeError("Invented reasoning for Claude")
    if models["cliproxyapi/gpt-oss-120b-medium"].get("architecture", {}).get("input_modalities") != ["text"]:
        raise RuntimeError("Text-only mapping failed")
    if models["cliproxyapi/gemini-pro-agent"].get("context_length") != 1048576:
        raise RuntimeError("Alias-style slug mapping failed")


def main() -> None:
    global MODE
    if not shutil.which("docker") or not PLUGIN.exists():
        raise RuntimeError("Docker and make build output are required for the real-host smoke test")
    if os.uname().sysname != "Linux":
        raise RuntimeError("The actual-image smoke test requires native Linux/amd64")
    url = os.environ.get("PLUGIN_URL")
    if url and not url.startswith("https://"):
        raise RuntimeError("PLUGIN_URL must be a public immutable HTTPS release-asset URL")
    image = f"{TARGET['BIFROST_IMAGE_REPO']}:{TARGET['BIFROST_VERSION']}"
    run("docker", "pull", "--platform", "linux/amd64", image)
    inspection = json.loads(run("docker", "image", "inspect", image, capture=True))[0]
    image_id = inspection["Id"]  # Freeze the pulled image for this entire validation run.
    audit_binary(image_id)
    upstream = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Upstream)
    worker = threading.Thread(target=upstream.serve_forever, daemon=True)
    worker.start()
    upstream_port = upstream.server_address[1]
    logs: list[str] = []
    record = ROOT / "dist" / ("url-smoke.json" if url else "host-smoke.json")
    log_path = ROOT / "dist" / ("url-smoke.log" if url else "host-smoke.log")
    record.unlink(missing_ok=True)
    try:
        with tempfile.TemporaryDirectory(prefix="bifrost-plugin-smoke-") as directory:
            app = Path(directory)
            app.chmod(0o777)  # Only disposable, test-only SQLite/config directory.
            provider = {
                "custom_provider_config": {
                    "base_provider_type": "openai",
                    "is_key_less": True,
                },
                "network_config": {
                    "base_url": f"http://127.0.0.1:{upstream_port}",
                    "max_retries": 0,
                },
            }

            other = copy.deepcopy(provider)
            other["network_config"]["base_url"] = (
                f"http://127.0.0.1:{upstream_port}/other"
            )
            config = {"providers": {"cliproxyapi": provider, "other-provider": other}, "plugins": [{
                "name": "cliproxyapi-model-metadata", "enabled": True,
                "path": url or "/plugin/metadata.so",
                "config": {"base_url": f"http://127.0.0.1:{upstream_port}", "timeout_ms": 1000}}]}
            (app / "config.json").write_text(json.dumps(config))
            for cache_present in (True, False):
                with MODE_LOCK:
                    MODE = "ok" if cache_present else "500"
                port = free_port()
                args = ["docker", "run", "-d", "--network", "host", "--platform", "linux/amd64",
                        "--mount", f"type=bind,src={app},dst=/app/data", "-e", f"APP_PORT={port}",
                        "-e", "APP_HOST=127.0.0.1", "-e", "APP_DIR=/app/data", "-e", "LOG_LEVEL=debug",
                        "-e", f"CLIPROXYAPI_API_KEY={TOKEN}"]
                if not url:
                    args += ["--mount", f"type=bind,src={PLUGIN},dst=/plugin/metadata.so,readonly"]
                cid = run(*args, image_id, capture=True)
                try:
                    for _ in range(120):
                        try:
                            with urllib.request.urlopen(f"http://127.0.0.1:{port}/health", timeout=1):
                                break
                        except (OSError, urllib.error.URLError):
                            if run("docker", "inspect", "--format", "{{.State.Running}}", cid, capture=True) != "true":
                                raise RuntimeError("Bifrost exited during startup/plugin initialization")
                            time.sleep(0.5)
                    else:
                        raise RuntimeError("Bifrost did not become healthy")
                    modes = ("ok", "500", "invalid", "empty") if cache_present else ("500",)
                    for mode in modes:
                        with MODE_LOCK:
                            MODE = mode
                            before = COUNTS["rich"]
                        verify_models(fetch_json(port, "/v1/models"), cache_present)
                        with MODE_LOCK:
                            if COUNTS["rich"] <= before:
                                raise RuntimeError("Actual client operation did not invoke metadata refresh")
                finally:
                    # Capture loader errors even when Bifrost keeps serving after
                    # refusing an incompatible plugin. Health alone is not proof.
                    captured = subprocess.run(["docker", "logs", cid], cwd=ROOT, text=True, capture_output=True, check=False)
                    logs.append(captured.stdout + captured.stderr)
                    run("docker", "rm", "-f", "-v", cid, capture=True)
        record.write_text(json.dumps({"status": "passed", "image": image,
                                     "image_id": image_id, "repo_digests": inspection.get("RepoDigests", []),
                                     "plugin_sha256": hashlib.sha256(PLUGIN.read_bytes()).hexdigest(),
                                     "plugin_url": url, "target": TARGET,
                                     "checks": ["native-catalog", "availability", "other-provider", "reasoning",
                                                "cached-failure", "no-cache-fail-open"]}, indent=2) + "\n")
        print(f"PASS: real Bifrost image loaded plugin and served enriched catalog ({record.name})")
    finally:
        upstream.shutdown()
        upstream.server_close()
        worker.join()
        log_path.write_text("\n".join(logs).replace(TOKEN, "[TEST-TOKEN]"))


if __name__ == "__main__":
    main()
