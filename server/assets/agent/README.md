Optional controller-local agent assets live in this directory.

Supported filenames:
- install_agent.sh
- oneclickvirt-agent-linux-amd64.tar.gz
- oneclickvirt-agent-linux-arm64.tar.gz

Behavior by build mode:
- CI release builds stage the installer and both agent archives here before building the controller.
- Docker builds always inject install_agent.sh from the repository root and will also embed any pre-staged agent archives already present here.
- Source builds can leave this directory without release archives; the controller will still compile and public download endpoints will fall back to GitHub-hosted assets when a requested file is absent.