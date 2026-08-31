# Rhizome Cross-Agent Testing

This dot-folder is the home for cloud-agent driven testing of Rhizome.

## For agents

Read `.rhizome-tests/SKILL.md` and follow the operating-system-specific instructions. When testing concludes, write a Markdown and a JSON report to `.rhizome-tests/reports/` using the template and schema in this folder.

## For humans

- `.github/workflows/pr.yml` contains the automated PR test matrix.
- `.rhizome-tests/reports/` holds per-run reports from cloud agents.
- `.rhizome-tests/SKILL.md` is the single source of truth for what an agent should run.
