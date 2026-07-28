// Package assets carries the files `succubus setup` copies into a project.
//
// They are embedded from their real locations rather than duplicated here, so
// editing the plugin or the skill updates what gets installed — there is no
// second copy to forget about.
package assets

import (
	"embed"
	"strings"
)

//go:embed all:opencode
var openCode embed.FS

//go:embed all:skill
var skill embed.FS

//go:embed all:contract
var contract embed.FS

// PathPlaceholder is what the docs and the shipped config snippets contain in
// place of a real binary path.
const PathPlaceholder = "/ABSOLUTE/PATH/TO/succubus"

// OpenCodePlugin returns the OpenCode plugin source.
func OpenCodePlugin() string {
	b, err := openCode.ReadFile("opencode/succubus.ts")
	if err != nil {
		return ""
	}
	return string(b)
}

// Skill returns the Agent Skill markdown.
func Skill() string {
	b, err := skill.ReadFile("skill/SKILL.md")
	if err != nil {
		return ""
	}
	return string(b)
}

// AgentsBlock returns the contract appended to a project's AGENTS.md, between
// the succubus:begin and succubus:end markers.
func AgentsBlock() string {
	b, err := contract.ReadFile("contract/AGENTS.md.block")
	if err != nil {
		return ""
	}
	return string(b)
}

// ResolvePaths swaps the documentation's placeholder for this machine's actual
// binary path, so anything shown in the dashboard or handed to an agent is
// ready to paste rather than ready to edit.
func ResolvePaths(text, binPath string) string {
	if binPath == "" {
		return text
	}
	return strings.ReplaceAll(text, PathPlaceholder, binPath)
}
