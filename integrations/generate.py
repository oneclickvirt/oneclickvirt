#!/usr/bin/env python3
"""Generate the self-contained agent integrations from the reviewed public catalog."""
import argparse
import hashlib
import json
from pathlib import Path

ROOT=Path(__file__).resolve().parent

def skill(profile, zh=False):
 name=profile['repository'].split('/')[1]
 heading=profile['skill_name']
 purpose=profile['purpose_zh' if zh else 'purpose']
 body='---\nname: '+heading+'\ndescription: '+json.dumps(purpose,ensure_ascii=False)+'\nmetadata:\n  repository: '+profile['repository']+'\n  integration-version: "1.0.0"\n---\n\n# '+name+'\n\n'
 body+=('## 使用范围\n\n' if zh else '## Scope\n\n')+purpose+'\n\n'
 body+='\n'.join('- '+n for n in profile['notes_zh' if zh else 'notes'])+'\n\n'
 body+=('## 安装和更新\n\n' if zh else '## Install and update\n\n')
 body+='```bash\ncurl -fL --retry 3 https://raw.githubusercontent.com/'+profile['repository']+'/'+profile['branch']+'/scripts/install_agent.py -o install_agent.py\npython3 install_agent.py --client codex\n```\n\n'
 body+=('可将下载地址改为 `https://cnb.cool/'+profile['repository']+'/-/git/raw/'+profile['branch']+'/scripts/install_agent.py`，并使用 `--source cnb` 从 CNB 的固定提交安装；此模式另需 Git。\n\n' if zh else 'The installer is also mirrored at `https://cnb.cool/'+profile['repository']+'/-/git/raw/'+profile['branch']+'/scripts/install_agent.py`; add `--source cnb` to install from an immutable CNB snapshot (requires Git).\n\n')
 body+=('用 `--client claude` 或 `--client openclaw` 选择其他客户端。重新下载并执行可更新；`--ref` 固定源码标签或提交。安装前校验全部 SHA-256，保留额外本地文件及已有客户端设置，`install.json` 记录来源提交。\n\n' if zh else 'Use `--client claude` or `--client openclaw` for another client. Download and run again to update; `--ref` pins a source tag/commit. The installer verifies all SHA-256 checksums before replacing its files, preserves extra local files/client settings, and records the source commit in `install.json`.\n\n')
 if profile['mcp']:
  body+=('## MCP\n\n运行需要 Python 3.9 以上版本；MCP 使用本机已有程序及其权限，不自动安装虚拟化环境。将安装目录内 `mcp.example.json` 合并到客户端配置。远端宿主机应通过无交互 SSH 运行该主机上的适配器。\n\n' if zh else '## MCP\n\nRequires Python 3.9+. The adapter uses existing local commands and their existing permissions; it does not install a virtualization runtime. Merge the installed `mcp.example.json` into the client configuration. For a remote host, run its adapter over noninteractive SSH.\n\n')
  if profile.get('asset_prefix'):
   body+='```bash\npython3 install_agent.py --client codex --install-binary\n```\n\n'
   body+=('`--version` 可指定检测程序的正式 Release 标签，`--ref` 则指定 Skill/MCP 源码版本，两者独立。默认从 GitHub 解析一次最新正式版，选择本机系统/架构的真实产物并校验 SHA-256；没有匹配产物时停止。已有程序可用 `--binary /完整路径/程序` 复用。\n\n' if zh else '`--version` selects the probe release tag; `--ref` selects Skill/MCP source independently. The default resolves the latest stable GitHub release once, selects a real asset for this OS/architecture, and verifies SHA-256. Missing assets fail without guessing. Reuse an existing executable with `--binary /absolute/path/to/program`.\n\n')
  body+=('先调用 `integration_info` 查看能力和运行要求。' if zh else 'Call `integration_info` first for capabilities and prerequisites.')+'\n\n'
  if profile.get('presets'):
   body+=('可用检测预设：\n\n' if zh else 'Available probe presets:\n\n')
   for preset,spec in profile['presets'].items():
    body+='- `'+preset+'`: `'+profile['binary']+' '+' '.join(spec['args'])+(' '+spec['target']+' <target>' if spec.get('target') else '')+'`\n'
   body+='\n'
  if profile.get('releases'):
   body+=('`release_assets` 返回实际版本、文件名、大小、摘要及下载地址；需要固定版本时传完整标签。更新产物后应重新安装并验证帮助和实际调用，不能只因文件存在便判定可用。\n\n' if zh else '`release_assets` returns the actual version, filenames, sizes, digests and download URLs. Pass an exact tag for a pinned release. After a product update, verify help and an actual call rather than treating file presence as a successful test.\n\n')
 else:
  body+=('此仓库发布 Skill，不安装 MCP 服务端；按原有脚本或开发接口操作。\n\n' if zh else 'This repository distributes a Skill without an MCP server; use its existing scripts or development APIs.\n\n')
 body+=('## 对应源码与说明\n\n' if zh else '## Source and documentation\n\n')
 for path in profile['source_files']:
  body+='- [`'+path+'`](https://github.com/'+profile['repository']+'/blob/'+profile['branch']+'/'+path+')\n'
 body+='\n- [集成安装说明 / Integration guide]('+profile['guide']+')\n'
 if profile.get('platform_guide'):body+='- [平台准备与安装 / Platform prerequisites]('+profile['platform_guide']+')\n'
 body+='\n'+('给出操作命令前核对这些源码文件及当前文档，保留现有版本固定、数据路径和宿主机标识。报告测试时区分真实程序执行、隔离测试及未验证的环境。\n' if zh else 'Check the relevant source and current documentation before giving commands. Preserve requested version pins, data paths and native host identifiers. Distinguish actual program execution, isolated tests and unverified environments.\n')
 return body

def generate(profile, target):
 target=Path(target)
 skilldir=target/'skills'/profile['skill_name']
 skilldir.mkdir(parents=True,exist_ok=True)
 (target/'agent').mkdir(parents=True,exist_ok=True)
 (target/'scripts').mkdir(parents=True,exist_ok=True)
 (skilldir/'SKILL.md').write_text(skill(profile),encoding='utf-8')
 (skilldir/'SKILL_ZH.md').write_text(skill(profile,True),encoding='utf-8')
 (target/'agent/profile.json').write_text(json.dumps(profile,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
 if profile['mcp']:(target/'agent/server.py').write_bytes((ROOT/'templates/server.py').read_bytes())
 installer=(ROOT/'templates/install_agent.py').read_text().replace('__REPOSITORY__',profile['repository']).replace('__DEFAULT_REF__',profile['branch']).replace('__SKILL_NAME__',profile['skill_name'])
 (target/'scripts/install_agent.py').write_text(installer,encoding='utf-8')
 paths=['agent/profile.json','skills/'+profile['skill_name']+'/SKILL.md','skills/'+profile['skill_name']+'/SKILL_ZH.md']
 if profile['mcp']:paths.append('agent/server.py')
 manifest={'repository':profile['repository'],'skill_name':profile['skill_name'],'mcp':profile['mcp'],'files':{p:hashlib.sha256((target/p).read_bytes()).hexdigest() for p in paths}}
 (target/'agent/manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
 if profile['repository'].endswith('/ClawChimera'):(target/'SKILL.md').write_text(skill(profile),encoding='utf-8')
 return target

def main():
 parser=argparse.ArgumentParser(description=__doc__)
 parser.add_argument('--output',type=Path,required=True)
 parser.add_argument('--repository')
 args=parser.parse_args()
 for profile in json.loads((ROOT/'catalog.json').read_text()):
  if not args.repository or profile['repository']==args.repository:
   generate(profile,args.output/profile['repository'].split('/')[1])

if __name__=='__main__':main()
