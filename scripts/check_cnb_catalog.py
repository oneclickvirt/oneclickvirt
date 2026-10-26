#!/usr/bin/env python3
"""Check CNB discovery and document retrieval for the explicit public catalog."""
import argparse
import concurrent.futures
import json
import os
from pathlib import Path
import time
import urllib.error
import urllib.parse
import urllib.request


def api(path):
    request = urllib.request.Request("https://api.cnb.cool/" + path, headers={
        "Authorization": "Bearer " + os.environ["CNB_PUSH_TOKEN"],
        "Accept": "application/vnd.cnb.api+json",
    })
    for attempt in range(4):
        try:
            with urllib.request.urlopen(request, timeout=45) as response:
                return json.load(response)
        except urllib.error.HTTPError as exc:
            if exc.code not in (429, 500, 502, 503, 504) or attempt == 3:
                raise
        except (TimeoutError, urllib.error.URLError):
            if attempt == 3:
                raise
        time.sleep(2 ** attempt)


def flagged(flag, allowed):
    result = set()
    for page in range(1, 20):
        data = api("oneclickvirt/-/repos?" + urllib.parse.urlencode({
            "flags": flag, "filter_type": "public", "page_size": 100, "page": page,
        }))
        if not isinstance(data, list):
            raise ValueError("Unexpected CNB repository list")
        for item in data:
            # Never print unrelated organization or private repository metadata.
            repo = item.get("path", "")
            if repo in allowed and item.get("visibility_level") == "Public":
                result.add(repo)
        if len(data) < 100:
            return result
    raise ValueError("CNB repository pagination did not terminate")


def inspect(repo):
    metadata = api(repo)
    if metadata.get("visibility_level") != "Public":
        raise ValueError("Destination must already be public: " + repo)
    result = {"repository": repo, "public": True,
              "default_branch": metadata.get("default_branch")}
    try:
        knowledge = api(repo + "/-/knowledge/base")
    except urllib.error.HTTPError as exc:
        if exc.code != 404:
            raise
        result["knowledge"] = None
        return result
    result["knowledge"] = {key: knowledge.get(key) for key in (
        "include", "exclude", "issue_sync_enabled", "last_commit_sha", "statistics")}
    query = "Skill MCP installation source version" if not repo.endswith(".github.io") else "PVE 容器 安装 CNB"
    hits = api(repo + "/-/knowledge/base/query?" + urllib.parse.urlencode({
        "query": query, "top_k": 5, "score_threshold": 0,
    }))
    if not isinstance(hits, list):
        raise ValueError("Unexpected knowledge query result: " + repo)
    result["query"] = query
    result["hits"] = [{"score": hit.get("score"), "metadata": hit.get("metadata"),
                       "chunk_bytes": len(hit.get("chunk", "").encode())} for hit in hits]
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--only", nargs="*")
    parser.add_argument("--observe", action="store_true")
    parser.add_argument("--wait-seconds", type=int, default=0)
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    catalog = json.loads((root / "integrations/catalog.json").read_text())
    allowed = {profile["repository"] for profile in catalog}
    allowed.update(("oneclickvirt/oneclickvirt", "oneclickvirt/oneclickvirt.github.io"))
    selected = allowed if not args.only else {"oneclickvirt/" + name for name in args.only}
    if not selected <= allowed:
        raise ValueError("Repository is outside the explicit public catalog")
    if not 0 <= args.wait_seconds <= 1200:
        raise ValueError("Wait must be between zero and 1200 seconds")
    deadline = time.monotonic() + args.wait_seconds
    while True:
        skills = flagged("Skills", allowed)
        bases = flagged("KnowledgeBase", allowed)
        with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
            results = list(pool.map(inspect, sorted(selected)))
        failed = []
        for result in results:
            repo = result["repository"]
            result["skill_discovered"] = repo in skills
            result["knowledge_discovered"] = repo in bases
            knowledge = result["knowledge"]
            if ((not repo.endswith(".github.io") and repo not in skills)
                    or repo not in bases or not knowledge
                    or knowledge.get("issue_sync_enabled") is not False
                    or not (knowledge.get("statistics") or {}).get("count")
                    or not result.get("hits")):
                failed.append(repo)
        report = {"checked": len(results), "skills": sorted(skills),
                  "knowledge_bases": sorted(bases), "repositories": results,
                  "failed": failed}
        if args.observe or not failed or time.monotonic() >= deadline:
            print(json.dumps(report, ensure_ascii=False, indent=2), flush=True)
            if failed and not args.observe:
                raise SystemExit(1)
            return
        print("Waiting for CNB indexing: " + ", ".join(failed), flush=True)
        time.sleep(min(20, max(0, deadline - time.monotonic())))


if __name__ == "__main__":
    main()
