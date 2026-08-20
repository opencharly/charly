---
description: Load skills matching the dispatcher trigger keywords
argument-hint: "<trigger1, trigger2, ...>"
---
# Load Skills: $@

1. Read the AGENTS.md skill dispatcher table to identify matching triggers
2. Call `charly_load_skills` with triggers: [$@]
3. Review the returned SKILL.md content before proceeding
4. If the task involves git operations, `charly_load_skills` automatically loads the git-workflow skill