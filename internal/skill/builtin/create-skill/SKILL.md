---
name: create-skill
description: Create and edit Agent Skills (SKILL.md directories with YAML frontmatter). Use when the user wants to create, write, or modify an Agent Skill or SKILL.md file.
---

# Create an Agent Skill

Guide the user to add or edit a skill that follows the Agent Skills format. A skill is a directory containing `SKILL.md` (YAML frontmatter plus Markdown instructions) and optional supporting files.

## When to apply

Use this skill when the user wants to create a new skill, draft or rewrite `SKILL.md`, or fix an existing skill so it matches the spec.

## Directory layout

```text
skill-name/
├── SKILL.md       # required: frontmatter + instructions
├── scripts/       # optional: executable helpers
├── references/    # optional: docs loaded on demand
└── assets/        # optional: templates, images, data files
```

Keep the install layout flat: one skill directory per name, no nested skill roots.

## Frontmatter

`SKILL.md` must start with YAML frontmatter delimited by `---` lines.

Required fields:

- `name`: 1–64 characters. Lowercase letters, digits, and hyphens only. Must not start or end with a hyphen. Must not contain consecutive hyphens. Must match the parent directory name.
- `description`: 1–1024 characters, non-empty. State **what the skill does** and **when to use it**. Include concrete keywords an agent can match.

Optional fields (include only when useful; unknown fields are ignored by this harness):

- `license`
- `compatibility` (≤500 characters; environment or product requirements)
- `metadata` (string-to-string map)
- `allowed-tools` (experimental; space-separated tool names)

Minimal example:

```markdown
---
name: pdf-processing
description: Extract text and tables from PDFs, fill forms, and merge files. Use when the user works with PDF documents, forms, or document extraction.
---
```

## Body

Write imperative instructions for the agent. Keep `SKILL.md` under 500 lines. Prefer progressive disclosure: put the essential procedure in the body and move long reference material into `references/`, loaded only when needed.

Keep file references one level deep from `SKILL.md` (for example `references/API.md`, `scripts/extract.py`). Avoid nested chains of includes.

## Supporting directories

- `scripts/`: self-contained executables with clear dependencies and error messages.
- `references/`: focused documents the agent can read on demand.
- `assets/`: templates, images, and other static files. Do not rely on the agent memorizing their contents.

When the skill directory is available on disk, resolve these paths relative to that directory.

## Install locations

- Project: `<workspace>/.agents/skills/<skill-name>/SKILL.md`
- User (global): `~/.agents/skills/<skill-name>/SKILL.md`

## Editing checklist

1. Create or open `<skill-name>/SKILL.md`.
2. Set `name` equal to the directory name and within the character rules.
3. Write a description that covers both what and when.
4. Keep the body concise; split bulky content into `references/`.
5. Add `scripts/` or `assets/` only when they are actually needed.
