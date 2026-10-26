#!/usr/bin/env python3
"""Provision public CNB mirrors only for the explicit, reviewed integration list."""
import json
import os
from pathlib import Path
import urllib.error
import urllib.request


def main():
    root = Path(__file__).resolve().parents[1]
    catalog = json.loads((root / "integrations/catalog.json").read_text())
    repos = [p["repository"] for p in catalog] + ["oneclickvirt/oneclickvirt.github.io"]
    token = os.environ["CNB_PUSH_TOKEN"]
    failed = []
    for repo in repos:
        owner, name = repo.split("/")
        if owner != "oneclickvirt":
            raise ValueError("Repository is outside the authorized organization")
        # Check without a token first; private destinations must never be
        # changed into public repositories or populated by this helper.
        probe = urllib.request.Request("https://api.cnb.cool/" + repo, headers={"Accept": "application/vnd.cnb.api+json"})
        try:
            with urllib.request.urlopen(probe, timeout=30) as response:
                response.read()
            print("Public mirror exists:", repo, flush=True)
            continue
        except urllib.error.HTTPError as exc:
            if exc.code != 404:
                failed.append({"repository": repo, "status": exc.code})
                continue
        body = json.dumps({"name": name, "visibility": "public",
                           "description": "Public mirror of https://github.com/" + repo}).encode()
        request = urllib.request.Request("https://api.cnb.cool/oneclickvirt/-/repos", data=body, method="POST",
            headers={"Authorization": "Bearer " + token, "Content-Type": "application/json", "Accept": "application/vnd.cnb.api+json"})
        try:
            with urllib.request.urlopen(request, timeout=30) as response:
                response.read()
                print("Provisioned public mirror:", repo, response.status, flush=True)
        except urllib.error.HTTPError as exc:
            # Do not print response bodies or request headers: they may contain
            # account diagnostics. A permission failure requires another route,
            # never widening an existing credential's scope automatically.
            failed.append({"repository": repo, "status": exc.code})
            print("Provisioning failed:", repo, exc.code, flush=True)
            if exc.code in (401, 403):
                break
    if failed:
        raise SystemExit("CNB repositories could not be provisioned: " + json.dumps(failed))


if __name__ == "__main__":
    main()
