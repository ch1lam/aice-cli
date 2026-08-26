package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/skill"
	"github.com/ch1lam/aice-cli/internal/tool"
	"github.com/ch1lam/aice-cli/internal/trust"
)

// skillDiscovery is the merged catalog plus every diagnostic from scanning
// and same-name shadowing. /skills reads these from the run environment.
type skillDiscovery struct {
	catalog skill.Catalog
	diags   []skill.Diagnostic
}

// discoverSkills loads builtin skills, then scans userRoot and (when trusted)
// projectRoot. Roots are skills directories, not home/workspace. A missing
// root is an empty source. Scan errors become diagnostics and that source is
// treated as empty so startup still succeeds.
func discoverSkills(userRoot, projectRoot string, trusted bool) skillDiscovery {
	diags := make([]skill.Diagnostic, 0)
	groups := make([][]skill.Skill, 0, 3)

	builtinSkills, builtinDiags, err := skill.Builtin()
	if err != nil {
		diags = append(diags, skill.Diagnostic{
			Level:   skill.LevelError,
			Message: fmt.Sprintf("skip builtin skills: %v", err),
		})
	} else {
		diags = append(diags, builtinDiags...)
		groups = append(groups, builtinSkills)
	}

	userSkills, userDiags := scanDiskSkills(userRoot, skill.SourceUser)
	diags = append(diags, userDiags...)
	groups = append(groups, userSkills)

	if trusted {
		projectSkills, projectDiags := scanDiskSkills(projectRoot, skill.SourceProject)
		diags = append(diags, projectDiags...)
		groups = append(groups, projectSkills)
	}

	catalog, mergeDiags := skill.Merge(groups...)
	diags = append(diags, mergeDiags...)
	return skillDiscovery{catalog: catalog, diags: diags}
}

func scanDiskSkills(root string, source skill.Source) ([]skill.Skill, []skill.Diagnostic) {
	if root == "" {
		return []skill.Skill{}, nil
	}
	skills, diags, err := skill.Scan(os.DirFS(root), source, root)
	if err != nil {
		return []skill.Skill{}, []skill.Diagnostic{{
			Level:   skill.LevelError,
			Dir:     root,
			Message: err.Error(),
		}}
	}
	return skills, diags
}

func (a *application) discoverRunSkills(workspace string, trusted bool) skillDiscovery {
	userRoot := ""
	diags := make([]skill.Diagnostic, 0)
	home, err := a.userHome()
	switch {
	case err != nil:
		diags = append(diags, skill.Diagnostic{
			Level:   skill.LevelError,
			Message: fmt.Sprintf("skip user skills: %v", err),
		})
	case strings.TrimSpace(home) == "":
		diags = append(diags, skill.Diagnostic{
			Level:   skill.LevelError,
			Message: "skip user skills: home directory is empty",
		})
	default:
		userRoot = filepath.Join(home, filepath.FromSlash(trust.SkillsDir))
	}

	discovery := discoverSkills(
		userRoot,
		filepath.Join(workspace, filepath.FromSlash(trust.SkillsDir)),
		trusted,
	)
	if len(diags) == 0 {
		return discovery
	}
	discovery.diags = append(diags, discovery.diags...)
	return discovery
}

func (a *application) userHome() (string, error) {
	if a != nil && a.dependencies.userHomeDir != nil {
		return a.dependencies.userHomeDir()
	}
	return os.UserHomeDir()
}

func appendSkillTool(tools []agent.Tool, catalog skill.Catalog) []agent.Tool {
	skills := catalog.Skills()
	if len(skills) == 0 {
		return tools
	}
	entries := make([]tool.SkillEntry, len(skills))
	for i, item := range skills {
		entries[i] = tool.SkillEntry{
			Name: item.Name,
			Dir:  item.Dir,
			Body: item.Body,
		}
	}
	return append(tools, tool.NewSkill(entries))
}

func skillReadOnlyRoots(catalog skill.Catalog) []string {
	skills := catalog.Skills()
	roots := make([]string, 0, len(skills))
	for _, item := range skills {
		if item.Dir == "" {
			continue
		}
		roots = append(roots, item.Dir)
	}
	return roots
}

func appendSkillsPrompt(base string, catalog skill.Catalog) string {
	section := formatSkillsPrompt(catalog)
	if section == "" {
		return base
	}
	return base + "\n\n" + section
}

func formatSkillsPrompt(catalog skill.Catalog) string {
	skills := catalog.Skills()
	if len(skills) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("Available Agent Skills:\n")
	builder.WriteString("The following skills provide specialized instructions for specific tasks.\n")
	builder.WriteString("When a task matches a skill's description, call the skill tool with the\n")
	builder.WriteString("skill's name to load its full instructions.\n")
	builder.WriteString("<available_skills>\n")
	for _, item := range skills {
		builder.WriteString("- ")
		builder.WriteString(item.Name)
		builder.WriteString(": ")
		builder.WriteString(item.Description)
		builder.WriteByte('\n')
	}
	builder.WriteString("</available_skills>")
	return builder.String()
}
