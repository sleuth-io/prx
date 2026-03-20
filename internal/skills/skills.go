package skills

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/sleuth-io/prx/internal/logger"
	"github.com/sleuth-io/prx/internal/skills/builtins"
)

// Skill represents a discovered skill with its metadata and content.
type Skill struct {
	Name        string
	Description string
	Body        string   // markdown content after frontmatter
	Source      string   // "built-in", or filesystem path
	Resources   []string // relative paths to resource files (e.g. "reference/guide.md")
	fsys        fs.FS    // the filesystem this skill was discovered from
	dir         string   // skill directory name within fsys
}

// ReadResource reads a resource file from the skill's directory.
func (s *Skill) ReadResource(path string) (string, error) {
	if s.fsys == nil {
		return "", fmt.Errorf("skill %q has no filesystem", s.Name)
	}
	// Prevent path traversal
	if strings.Contains(path, "..") {
		return "", fmt.Errorf("invalid resource path: %s", path)
	}
	fullPath := s.dir + "/" + path
	data, err := fs.ReadFile(s.fsys, fullPath)
	if err != nil {
		return "", fmt.Errorf("reading resource %s: %w", path, err)
	}
	return string(data), nil
}

// ScanFS walks any fs.FS looking for */SKILL.md files, parses each, and returns skills.
func ScanFS(fsys fs.FS, source string) []Skill {
	var result []Skill
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		logger.Debug("skills: cannot read dir for source %s: %v", source, err)
		return nil
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillPath := entry.Name() + "/SKILL.md"
		data, err := fs.ReadFile(fsys, skillPath)
		if err != nil {
			continue
		}
		skill, ok := parseSkillMD(string(data), source)
		if !ok {
			logger.Debug("skills: skipping %s/%s: missing description or unparseable frontmatter", source, entry.Name())
			continue
		}
		if skill.Name == "" {
			skill.Name = entry.Name()
		}
		skill.fsys = fsys
		skill.dir = entry.Name()
		skill.Resources = discoverResources(fsys, entry.Name())
		result = append(result, skill)
	}
	return result
}

// discoverResources finds all non-SKILL.md files in a skill directory.
func discoverResources(fsys fs.FS, dir string) []string {
	var resources []string
	err := fs.WalkDir(fsys, dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		// Skip SKILL.md itself
		if d.Name() == "SKILL.md" {
			return nil
		}
		// Store path relative to skill directory
		rel := strings.TrimPrefix(path, dir+"/")
		resources = append(resources, rel)
		return nil
	})
	if err != nil {
		logger.Debug("skills: error walking resources in %s: %v", dir, err)
	}
	return resources
}

// parseSkillMD parses a SKILL.md file with YAML-like frontmatter.
// Returns the skill and true if parsing succeeded, false if it should be skipped.
func parseSkillMD(content, source string) (Skill, bool) {
	lines := strings.Split(content, "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != "---" {
		return Skill{}, false
	}

	// Find closing ---
	endIdx := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			endIdx = i
			break
		}
	}
	if endIdx < 0 {
		return Skill{}, false
	}

	// Parse frontmatter key: value pairs
	skill := Skill{Source: source}
	for _, line := range lines[1:endIdx] {
		key, value, ok := parseKV(line)
		if !ok {
			continue
		}
		switch key {
		case "name":
			skill.Name = value
		case "description":
			skill.Description = value
		}
	}

	if skill.Description == "" {
		return Skill{}, false
	}

	// Body is everything after the closing ---
	bodyLines := lines[endIdx+1:]
	skill.Body = strings.TrimSpace(strings.Join(bodyLines, "\n"))

	return skill, true
}

// parseKV parses a simple "key: value" line, stripping surrounding quotes from the value.
func parseKV(line string) (string, string, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	idx := strings.IndexByte(line, ':')
	if idx < 0 {
		return "", "", false
	}
	key := strings.TrimSpace(line[:idx])
	value := strings.TrimSpace(line[idx+1:])
	// Strip surrounding quotes
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') ||
			(value[0] == '\'' && value[len(value)-1] == '\'') {
			value = value[1 : len(value)-1]
		}
	}
	return key, value, true
}

// Discover merges all skill sources: built-in embedded FS + user skills directory.
// User skills override built-ins with the same name.
func Discover() []Skill {
	result := ScanFS(builtins.FS, "built-in")

	if userDir := userSkillsDir(); userDir != "" {
		if info, err := os.Stat(userDir); err == nil && info.IsDir() {
			userSkills := ScanFS(os.DirFS(userDir), userDir)
			result = merge(result, userSkills)
		}
	}

	return result
}

// userSkillsDir returns the path to ~/.config/prx/skills/.
func userSkillsDir() string {
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		xdg = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(xdg, "prx", "skills")
}

// merge combines two skill slices, with later entries overriding earlier ones by name.
func merge(base, override []Skill) []Skill {
	byName := make(map[string]int, len(base))
	result := make([]Skill, len(base))
	copy(result, base)
	for i, s := range result {
		byName[s.Name] = i
	}
	for _, s := range override {
		if idx, ok := byName[s.Name]; ok {
			result[idx] = s
		} else {
			byName[s.Name] = len(result)
			result = append(result, s)
		}
	}
	return result
}

// CatalogPrompt renders the skill catalog for injection into a chat system prompt.
func CatalogPrompt(skills []Skill) string {
	if len(skills) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n## Available Skills\n")
	sb.WriteString("The following skills provide specialized instructions for specific tasks.\n")
	sb.WriteString("When a task matches a skill's description, call the activate_skill tool\n")
	sb.WriteString("with the skill's name to load its full instructions.\n")
	sb.WriteString("Skills are also available as slash commands in the chat: type /skill-name to activate.\n\n")
	for _, s := range skills {
		fmt.Fprintf(&sb, "- %s: %s\n", s.Name, s.Description)
	}
	return sb.String()
}
