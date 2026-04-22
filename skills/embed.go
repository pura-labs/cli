// Package skills embeds the SKILL.md files bundled with the CLI.
//
// At build time, //go:embed pulls every file in pura/ into the binary so
// `pura skill install` works from anywhere a user runs the CLI — no need
// to keep the source tree around. That matters for users who `curl |
// install.sh` or grab a release archive.
//
// The on-disk layout is preserved so the embed tree and a checked-out
// repo tree are interchangeable (helpful for local development).
package skills

import "embed"

// FS is the read-only filesystem rooted at cli/skills/. Callers reach for
// `pura/SKILL.md` or the sibling skill directories.
//
//go:embed pura/SKILL.md pura-slides/SKILL.md pura-slides/starter-pitch.md pura-slides/starter-weekly.md pura-slides/starter-research.md
var FS embed.FS

// PuraSkillPath is the canonical path inside FS. Exposed as a constant so
// callers don't repeat the magic string.
const PuraSkillPath = "pura/SKILL.md"

// PuraSlidesSkillPath points at the slides-authoring skill — a companion
// to PuraSkillPath with the full md format spec, theme class vocab, and
// tool-sequence recipes for common JTBDs.
const PuraSlidesSkillPath = "pura-slides/SKILL.md"
