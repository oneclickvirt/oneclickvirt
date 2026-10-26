#!/usr/bin/env python3
"""Add document-only CNB indexing to a public catalog checkout (requires PyYAML)."""
import argparse
import copy
import json
from pathlib import Path

DOCS = "oneclickvirt/oneclickvirt.github.io"
DOC_PATHS = [
    "docs/index.md", "docs/en/index.md", "docs/guide/**/*.md",
    "docs/en/guide/**/*.md", "docs/case/**/*.md", "docs/en/case/**/*.md",
    "docs/developer/**/*.md", "docs/en/developer/**/*.md",
]


def includes(repository, profile=None):
    if repository == DOCS:
        return DOC_PATHS[:]
    paths = ["README*.md"]
    if repository == "oneclickvirt/oneclickvirt":
        paths += ["skills/SKILL.md", "skills/SKILL_ZH.md"]
    else:
        paths += ["skills/" + profile["skill_name"] + "/SKILL.md",
                  "skills/" + profile["skill_name"] + "/SKILL_ZH.md"]
    if repository == "oneclickvirt/ecs-gui":
        paths += ["docs/**/*.md"]
    return paths


def configure(target, repository, profile=None):
    import yaml

    target = Path(target)
    path = target / ".cnb.yml"
    old = yaml.safe_load(path.read_text()) if path.exists() else {}
    config = copy.deepcopy(old) or {}
    main = config.setdefault("main", {})
    job = {"name": "index-public-documentation", "stages": [{
        "name": "Index public documentation", "type": "knowledge:update",
        "options": {"include": includes(repository, profile),
                    "issueSyncEnabled": False, "ignoreProcessFailures": False},
    }]}
    if repository == DOCS:
        # Keep installation commands close to their registry/version context
        # when long, multi-topic guide pages are split for retrieval.
        job["stages"][0]["options"].update(chunkSize=900, chunkOverlap=150)
    push = main.get("push", [])
    if not isinstance(push, list):
        raise ValueError("Refusing to replace an unsupported main.push configuration")
    # Copy the list: existing scheduled/image-publishing aliases keep their jobs.
    main["push"] = [item for item in push if item.get("name") != job["name"]
                    and not any(stage.get("type") == "knowledge:update"
                                for stage in item.get("stages", []))] + [job]
    if "api_trigger_knowledge" in main and any(
            item.get("name") != job["name"] for item in main["api_trigger_knowledge"]):
        raise ValueError("Refusing to replace an unrelated manual event")
    main["api_trigger_knowledge"] = [job]
    path.write_text(yaml.safe_dump(config, sort_keys=False, allow_unicode=True), encoding="utf-8")
    settings_path = target / ".cnb/settings.yml"
    settings = yaml.safe_load(settings_path.read_text()) if settings_path.exists() else {}
    settings = settings or {}
    npc = {
        "roles": [{"name": "使用说明", "prompt": (
            "依据当前仓库与已引入的公开文档知识库回答安装、使用和版本问题，附上文档链接。"
            "核对文档对应的版本与架构；没有依据时说明缺少的信息。"
            "仅提供说明和操作建议，不执行宿主机操作，也不请求或展示密钥及私有仓库内容。"
        )}],
        "defaultRepo": repository, "defaultRole": "使用说明",
        "button": {"name": "使用说明", "description": "查询公开的安装和使用文档"},
    }
    if repository != DOCS:
        npc["imports"] = {"list": [DOCS]}
    if "npc" in settings and settings["npc"] != npc:
        raise ValueError("Existing NPC settings need an explicit merge")
    settings["npc"] = npc
    settings_path.parent.mkdir(parents=True, exist_ok=True)
    settings_path.write_text(yaml.safe_dump(settings, sort_keys=False, allow_unicode=True), encoding="utf-8")
    return old, config


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repository", required=True)
    parser.add_argument("--root", required=True, type=Path)
    args = parser.parse_args()
    catalog = json.loads((Path(__file__).resolve().parent / "catalog.json").read_text())
    profiles = {profile["repository"]: profile for profile in catalog}
    if args.repository not in profiles and args.repository not in (DOCS, "oneclickvirt/oneclickvirt"):
        raise ValueError("Repository is outside the explicit public catalog")
    configure(args.root, args.repository, profiles.get(args.repository))


if __name__ == "__main__":
    main()
