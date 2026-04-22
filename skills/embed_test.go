package skills

import (
	"io/fs"
	"strings"
	"testing"
)

// TestPuraSlidesSkillEmbedded asserts the slides-authoring skill +
// its three starter decks ship inside the CLI binary.
func TestPuraSlidesSkillEmbedded(t *testing.T) {
	for _, path := range []string{
		PuraSlidesSkillPath,
		"pura-slides/starter-pitch.md",
		"pura-slides/starter-weekly.md",
		"pura-slides/starter-research.md",
	} {
		data, err := fs.ReadFile(FS, path)
		if err != nil {
			t.Fatalf("missing embedded skill file %q: %v", path, err)
		}
		if len(data) == 0 {
			t.Errorf("embedded %q is empty", path)
		}
	}
}

// TestPuraSlidesSkillMentionsAllTools — the skill SSOT must keep pace
// with the server's tool registry. If a new slides tool ships, add it
// here; the test flags any drop from the skill docs.
func TestPuraSlidesSkillMentionsAllTools(t *testing.T) {
	data, err := fs.ReadFile(FS, PuraSlidesSkillPath)
	if err != nil {
		t.Fatalf("read skill: %v", err)
	}
	body := string(data)
	for _, tool := range []string{
		"slides.read",
		"slides.outline",
		"slides.read_section",
		"slides.export",
		"slides.patch_section",
		"slides.append_section",
		"slides.set_meta",
		"slides.set_content",
		"slides.clone",
	} {
		if !strings.Contains(body, tool) {
			t.Errorf("skill does not mention %q", tool)
		}
	}
}

// TestPuraSlidesSkillMentionsChefThemes — when a theme ships or
// deprecates, update the skill here + in the file.
func TestPuraSlidesSkillMentionsChefThemes(t *testing.T) {
	data, err := fs.ReadFile(FS, PuraSlidesSkillPath)
	if err != nil {
		t.Fatalf("read skill: %v", err)
	}
	body := string(data)
	for _, theme := range []string{"mono", "paper", "magazine", "dark"} {
		if !strings.Contains(body, theme) {
			t.Errorf("skill does not mention theme %q", theme)
		}
	}
}
