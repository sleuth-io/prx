package skills

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/sleuth-io/prx/internal/skills/builtins"
)

func TestScanFS_findsSkills(t *testing.T) {
	fsys := fstest.MapFS{
		"skill-a/SKILL.md": &fstest.MapFile{
			Data: []byte("---\nname: skill-a\ndescription: \"A test skill\"\n---\n\nBody of skill A."),
		},
		"skill-b/SKILL.md": &fstest.MapFile{
			Data: []byte("---\nname: skill-b\ndescription: Second skill\n---\n\nBody of skill B."),
		},
	}

	skills := ScanFS(fsys, "test")
	if len(skills) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(skills))
	}

	found := map[string]Skill{}
	for _, s := range skills {
		found[s.Name] = s
	}

	if s, ok := found["skill-a"]; !ok {
		t.Fatal("skill-a not found")
	} else {
		if s.Description != "A test skill" {
			t.Errorf("skill-a description = %q, want %q", s.Description, "A test skill")
		}
		if s.Body != "Body of skill A." {
			t.Errorf("skill-a body = %q, want %q", s.Body, "Body of skill A.")
		}
		if s.Source != "test" {
			t.Errorf("skill-a source = %q, want %q", s.Source, "test")
		}
	}

	if s, ok := found["skill-b"]; !ok {
		t.Fatal("skill-b not found")
	} else if s.Description != "Second skill" {
		t.Errorf("skill-b description = %q, want %q", s.Description, "Second skill")
	}
}

func TestScanFS_usesDirectoryNameWhenNameMissing(t *testing.T) {
	fsys := fstest.MapFS{
		"my-skill/SKILL.md": &fstest.MapFile{
			Data: []byte("---\ndescription: A skill without name field\n---\n\nBody here."),
		},
	}

	skills := ScanFS(fsys, "test")
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	if skills[0].Name != "my-skill" {
		t.Errorf("expected name %q from directory, got %q", "my-skill", skills[0].Name)
	}
}

func TestScanFS_skipsWhenDescriptionMissing(t *testing.T) {
	fsys := fstest.MapFS{
		"bad-skill/SKILL.md": &fstest.MapFile{
			Data: []byte("---\nname: bad-skill\n---\n\nNo description."),
		},
	}

	skills := ScanFS(fsys, "test")
	if len(skills) != 0 {
		t.Fatalf("expected 0 skills (missing description), got %d", len(skills))
	}
}

func TestScanFS_skipsMalformedYAML(t *testing.T) {
	fsys := fstest.MapFS{
		"broken/SKILL.md": &fstest.MapFile{
			Data: []byte("no frontmatter here\njust text"),
		},
	}

	skills := ScanFS(fsys, "test")
	if len(skills) != 0 {
		t.Fatalf("expected 0 skills (no frontmatter), got %d", len(skills))
	}
}

func TestScanFS_skipsNoClosingFrontmatter(t *testing.T) {
	fsys := fstest.MapFS{
		"broken/SKILL.md": &fstest.MapFile{
			Data: []byte("---\nname: broken\ndescription: never closed"),
		},
	}

	skills := ScanFS(fsys, "test")
	if len(skills) != 0 {
		t.Fatalf("expected 0 skills (unclosed frontmatter), got %d", len(skills))
	}
}

func TestScanFS_handlesUnquotedColons(t *testing.T) {
	fsys := fstest.MapFS{
		"colon/SKILL.md": &fstest.MapFile{
			Data: []byte("---\nname: colon-skill\ndescription: \"Handles colons: like this one\"\n---\n\nBody."),
		},
	}

	skills := ScanFS(fsys, "test")
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	if skills[0].Description != "Handles colons: like this one" {
		t.Errorf("description = %q", skills[0].Description)
	}
}

func TestMerge_userOverridesBuiltin(t *testing.T) {
	builtinSkills := []Skill{
		{Name: "user-guide", Description: "Built-in guide", Body: "Built-in body", Source: "built-in"},
		{Name: "other", Description: "Other skill", Body: "Other body", Source: "built-in"},
	}
	userSkills := []Skill{
		{Name: "user-guide", Description: "User custom guide", Body: "User body", Source: "/home/user/.config/prx/skills"},
	}

	result := merge(builtinSkills, userSkills)
	if len(result) != 2 {
		t.Fatalf("expected 2 skills after merge, got %d", len(result))
	}

	found := map[string]Skill{}
	for _, s := range result {
		found[s.Name] = s
	}

	if s := found["user-guide"]; s.Source != "/home/user/.config/prx/skills" {
		t.Errorf("user-guide should be overridden by user skill, got source=%q", s.Source)
	}
	if s := found["other"]; s.Source != "built-in" {
		t.Errorf("other should remain built-in, got source=%q", s.Source)
	}
}

func TestMerge_addsNewUserSkills(t *testing.T) {
	builtinSkills := []Skill{
		{Name: "builtin-a", Description: "A"},
	}
	userSkills := []Skill{
		{Name: "user-b", Description: "B"},
	}

	result := merge(builtinSkills, userSkills)
	if len(result) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(result))
	}
}

func TestDiscoverBuiltins(t *testing.T) {
	// Verify that the embedded built-in skills are discovered
	skills := ScanFS(builtins.FS, "built-in")
	if len(skills) == 0 {
		t.Fatal("expected at least 1 built-in skill (user-guide)")
	}

	found := false
	for _, s := range skills {
		if s.Name == "user-guide" {
			found = true
			if s.Description == "" {
				t.Error("user-guide skill has empty description")
			}
			if s.Body == "" {
				t.Error("user-guide skill has empty body")
			}
			// Verify resources are discovered
			if len(s.Resources) == 0 {
				t.Error("user-guide skill should have resources (reference/guide.md)")
			}
			break
		}
	}
	if !found {
		t.Error("user-guide skill not found in built-ins")
	}
}

func TestReadResource(t *testing.T) {
	skills := ScanFS(builtins.FS, "built-in")
	var guide *Skill
	for i := range skills {
		if skills[i].Name == "user-guide" {
			guide = &skills[i]
			break
		}
	}
	if guide == nil {
		t.Fatal("user-guide not found")
	}

	content, err := guide.ReadResource("reference/guide.md")
	if err != nil {
		t.Fatalf("ReadResource failed: %v", err)
	}
	if !contains(content, "Keyboard Shortcuts") {
		t.Error("reference/guide.md should contain keyboard shortcuts section")
	}
}

func TestReadResource_pathTraversal(t *testing.T) {
	skills := ScanFS(builtins.FS, "built-in")
	for _, s := range skills {
		if s.Name == "user-guide" {
			_, err := s.ReadResource("../embed.go")
			if err == nil {
				t.Error("expected error for path traversal, got nil")
			}
			return
		}
	}
	t.Fatal("user-guide not found")
}

func TestResourceDiscovery(t *testing.T) {
	fsys := fstest.MapFS{
		"my-skill/SKILL.md": &fstest.MapFile{
			Data: []byte("---\nname: my-skill\ndescription: Skill with resources\n---\n\nBody."),
		},
		"my-skill/reference/doc.md": &fstest.MapFile{
			Data: []byte("# Documentation"),
		},
		"my-skill/scripts/run.sh": &fstest.MapFile{
			Data: []byte("#!/bin/bash"),
		},
	}

	skills := ScanFS(fsys, "test")
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	if len(skills[0].Resources) != 2 {
		t.Fatalf("expected 2 resources, got %d: %v", len(skills[0].Resources), skills[0].Resources)
	}

	// Verify we can read them
	content, err := skills[0].ReadResource("reference/doc.md")
	if err != nil {
		t.Fatalf("ReadResource failed: %v", err)
	}
	if content != "# Documentation" {
		t.Errorf("resource content = %q, want %q", content, "# Documentation")
	}
}

func TestScanFS_emptyDirectory(t *testing.T) {
	dir := t.TempDir()
	skills := ScanFS(os.DirFS(dir), dir)
	if len(skills) != 0 {
		t.Fatalf("expected 0 skills from empty dir, got %d", len(skills))
	}
}

func TestScanFS_realFilesystem(t *testing.T) {
	dir := t.TempDir()

	// Create a skill directory with SKILL.md
	skillDir := filepath.Join(dir, "test-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: test-skill\ndescription: A filesystem skill\n---\n\nFilesystem body."
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	skills := ScanFS(os.DirFS(dir), dir)
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	if skills[0].Name != "test-skill" {
		t.Errorf("name = %q, want %q", skills[0].Name, "test-skill")
	}
}

func TestCatalogPrompt_empty(t *testing.T) {
	result := CatalogPrompt(nil)
	if result != "" {
		t.Errorf("expected empty string for nil skills, got %q", result)
	}
}

func TestCatalogPrompt_withSkills(t *testing.T) {
	skills := []Skill{
		{Name: "user-guide", Description: "Guide to prx"},
		{Name: "custom", Description: "Custom skill"},
	}
	result := CatalogPrompt(skills)
	if result == "" {
		t.Fatal("expected non-empty catalog prompt")
	}
	if !contains(result, "user-guide") || !contains(result, "custom") {
		t.Errorf("catalog prompt missing skill names: %s", result)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
