# Agent skills for pdfspec

These files tell a coding agent to use `pdfspec` whenever it needs what's inside a PDF, and how to
read what it gives back. All three say the same thing. `TestSkillsAreOneSkill` keeps them in step,
and `TestSkillCommandsExist` checks that every command they show is one `pdfspec` accepts.

| agent | file | where it goes |
|---|---|---|
| Claude Code | `claude/pdfspec/SKILL.md` | `~/.claude/skills/pdfspec/SKILL.md`, or `.claude/skills/pdfspec/SKILL.md` in a project |
| Codex | `codex/pdfspec/SKILL.md` | `~/.codex/skills/pdfspec/SKILL.md` |
| any other agent | `generic/pdfspec.md` | paste it into the project's `AGENTS.md`, or the agent's system prompt |

On Linux or macOS, for Claude Code:

```sh
mkdir -p ~/.claude/skills/pdfspec
curl -fsSL -o ~/.claude/skills/pdfspec/SKILL.md \
  https://raw.githubusercontent.com/model-harness/pdftools/main/skills/claude/pdfspec/SKILL.md
```

For Codex, use `~/.codex/skills/pdfspec` and `skills/codex/pdfspec/SKILL.md` instead.

To change what the skill says, edit `generic/pdfspec.md`, then copy its text below the frontmatter
of the other two.
