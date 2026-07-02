#!/bin/bash
# Hermes Agent build script — runs in pixi builder stage.
# Builder image provides: gcc, nodejs, npm, python3-devel, libffi-devel, etc.
set -euo pipefail

# 1. Clone hermes-agent repo
cd /tmp
git clone --depth 1 https://github.com/NousResearch/hermes-agent.git
cd hermes-agent

# 2. Install hermes-agent (non-editable) into pixi env
"$HOME/.pixi/envs/default/bin/pip" install --no-deps .

# 3. Install project-level npm deps (agent-browser, camoufox-browser)
# These must be in the project node_modules, not global — hermes expects them locally
npm install --prefer-offline --no-audit

# 4. Install WhatsApp bridge npm deps
cd scripts/whatsapp-bridge
npm install --prefer-offline --no-audit
cd ../..

# 5. Stage hermes-agent source under $HOME for COPY
# Needed at runtime for: .env.example, cli-config.yaml.example,
# docker/SOUL.md, skills/, tools/skills_sync.py, node_modules/, whatsapp-bridge
cp -r /tmp/hermes-agent "$HOME/hermes-agent"

# 6. Cleanup
cd /
rm -rf /tmp/hermes-agent
