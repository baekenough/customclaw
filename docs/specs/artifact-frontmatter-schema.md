# Artifact Frontmatter Schema

Defines the YAML frontmatter for skill-generated artifacts in `.claude/outputs/sessions/`.

## Required Fields

| Field | Type | Description |
|-------|------|-------------|
| `skill` | string | Skill that produced the artifact |
| `date` | string | ISO date (YYYY-MM-DD) or ISO-8601 timestamp |
| `query` | string | Original user query |

## Optional Fields

### `related` (object)

Enables cross-artifact discovery and relationship tracking.

| Sub-field | Type | Description |
|-----------|------|-------------|
| `issues` | number[] | GitHub issue numbers |
| `artifacts` | string[] | Artifact IDs (`{skill}-{HHmmss}`) |
| `commits` | string[] | Related commit SHAs |
| `relation` | string | `derived-from`, `supersedes`, or `related-to` |

All sub-fields are optional. An empty `related` block is valid.

### Relationship Types

| Type | Meaning | Example |
|------|---------|---------|
| `derived-from` | This artifact's source/basis | Research -> triage report |
| `supersedes` | Replaces an earlier artifact | Updated analysis |
| `related-to` | Loose association | Same topic, different scope |

### Artifact ID Convention

Format: `{skill-name}-{HHmmss}`

Example: `research-143000` (research skill, 14:30:00)

The ID can be derived from the artifact filename: `{skill-name}-{HHmmss}.md`

## Example

```yaml
---
skill: professor-triage
date: 2026-03-24
query: "triage issue #31"
related:
  issues: [31, 54]
  artifacts: [research-020000]
  commits: [abc1234]
  relation: derived-from
---
```
