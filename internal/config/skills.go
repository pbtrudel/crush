package config

import "github.com/charmbracelet/crush/internal/skills"

// SkillsDiscoveryConfig builds the skills discovery inputs from the
// store's current options and working directory. Workspace setup and
// skill reloads both use it so discovery stays consistent across call
// sites.
func SkillsDiscoveryConfig(store *ConfigStore) skills.DiscoveryConfig {
	opts := store.Config().Options
	var paths, disabled []string
	if opts != nil {
		paths = opts.SkillsPaths
		disabled = opts.DisabledSkills
	}
	var resolver func(string) (string, error)
	if r := store.Resolver(); r != nil {
		resolver = r.ResolveValue
	}
	return skills.DiscoveryConfig{
		SkillsPaths:    paths,
		DisabledSkills: disabled,
		WorkingDir:     store.WorkingDir(),
		Resolver:       resolver,
	}
}
