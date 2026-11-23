package config

import (
	"context"
	"log/slog"
	"slices"

	"github.com/charmbracelet/crush/internal/skills"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

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

// SkillConfigDisabled reports whether the merged config disables the
// named skill.
func (s *ConfigStore) SkillConfigDisabled(name string) bool {
	opts := s.Config().Options
	return opts != nil && slices.Contains(opts.DisabledSkills, name)
}

// SetSkillDisabledConfig toggles a skill's entry in the given scope's
// options.disabled_skills list. Skills are opt-out — an enabled skill is
// simply absent from the list — so disabling appends and enabling
// removes. Only the scope's own file is read and rewritten, so entries
// that come from other config files never leak into it. The dialog only
// offers discovered skills, so unknown names cannot arrive from the UI.
func (s *ConfigStore) SetSkillDisabledConfig(scope Scope, name string, disabled bool) error {
	const key = "options.disabled_skills"
	err := s.atomicWrite(scope, func(data []byte) ([]byte, error) {
		list := []string{}
		for _, existing := range gjson.GetBytes(data, key).Array() {
			if existing.String() != name {
				list = append(list, existing.String())
			}
		}
		if disabled {
			list = append(list, name)
		}
		slices.Sort(list)
		return sjson.SetBytes(data, key, list)
	})
	if err != nil {
		return err
	}
	if err := s.autoReload(context.Background()); err != nil {
		slog.Warn("Config file updated but failed to reload in-memory state", "error", err)
	}
	return nil
}
