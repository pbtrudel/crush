package agent

import (
	"context"
	"embed"
	"slices"
	"strings"

	"github.com/charmbracelet/crush/internal/agent/prompt"
	"github.com/charmbracelet/crush/internal/config"
)

//go:embed templates/coder.md.tpl
var coderPromptTmpl []byte

// leanPromptTmpl is the shared base of the lean profile: the rules that keep
// the agent safe and correct, the environment, the skills, the context files
// and the team policy. Tool guidance lives in the tool descriptions instead
// of here.
//
//go:embed templates/lean.md.tpl
var leanPromptTmpl []byte

// leanFamilyTemplates holds the lean profile's per-family add-ons. A model
// family without an add-on renders the shared base alone.
//
//go:embed templates/family/*.md.tpl
var leanFamilyTemplates embed.FS

//go:embed templates/task.md.tpl
var taskPromptTmpl []byte

//go:embed templates/plan.md.tpl
var planPromptTmpl []byte

//go:embed templates/initialize.md.tpl
var initializePromptTmpl []byte

// sideQuestionPromptTmpl replaces the coding-agent prompt for side questions.
// That prompt is written for a model holding tools, so a model given it with
// no tools answers with a promise to go look at files (or nothing at all)
// instead of answering the question.
//
//go:embed templates/side_question.md
var sideQuestionPromptTmpl []byte

// sideQuestionPrompt returns the system prompt used for side questions.
func sideQuestionPrompt() string {
	return string(sideQuestionPromptTmpl)
}

func coderPrompt(opts ...prompt.Option) (*prompt.Prompt, error) {
	opts = append([]prompt.Option{prompt.WithVariant(coderPromptVariant)}, opts...)
	systemPrompt, err := prompt.NewPrompt("coder", string(coderPromptTmpl), opts...)
	if err != nil {
		return nil, err
	}
	return systemPrompt, nil
}

// coderPromptVariant renders the lean prompt profile. The full profile keeps
// the base template, so its text stays exactly as it is.
func coderPromptVariant(profile, provider, model string) (string, bool) {
	if profile != config.PromptProfileLean {
		return "", false
	}
	return string(leanPromptTmpl) + leanFamilyAddon(promptFamily(provider, model)), true
}

// Prompt families group the models that need the same lean-profile add-on.
const (
	familyAnthropic = "anthropic"
	familyGPT       = "gpt"
	familyDefault   = "default"
)

// promptFamilyRules names each family by the model ID markers that identify
// it and the provider IDs that serve only that family.
var promptFamilyRules = []struct {
	family    string
	providers []string
	markers   []string
}{
	{
		family:    familyAnthropic,
		providers: []string{"anthropic", "claude-agent"},
		markers:   []string{"claude", "sonnet", "opus", "haiku"},
	},
	{
		family:    familyGPT,
		providers: []string{"openai", "azure", "azure-openai", "copilot", "github-copilot"},
		markers:   []string{"gpt", "codex", "chatgpt", "o1-", "o3-", "o4-"},
	},
}

// promptFamily names the lean-profile add-on family a model belongs to. The
// model ID decides first, because a gateway such as OpenRouter or Vercel
// serves several families behind one provider ID. The provider ID decides
// next, and anything else gets the default add-on: more guidance than an
// Anthropic model needs, less than a GPT model gets.
func promptFamily(provider, model string) string {
	model = strings.ToLower(model)
	for _, rule := range promptFamilyRules {
		if slices.ContainsFunc(rule.markers, func(marker string) bool {
			return strings.Contains(model, marker)
		}) {
			return rule.family
		}
	}
	provider = strings.ToLower(provider)
	for _, rule := range promptFamilyRules {
		if slices.Contains(rule.providers, provider) {
			return rule.family
		}
	}
	return familyDefault
}

// leanFamilyAddon returns the add-on for one family. A family whose template
// is missing renders the shared base alone rather than failing the prompt.
func leanFamilyAddon(family string) string {
	text, err := leanFamilyTemplates.ReadFile("templates/family/" + family + ".md.tpl")
	if err != nil {
		return ""
	}
	return string(text)
}

func taskPrompt(opts ...prompt.Option) (*prompt.Prompt, error) {
	systemPrompt, err := prompt.NewPrompt("task", string(taskPromptTmpl), opts...)
	if err != nil {
		return nil, err
	}
	return systemPrompt, nil
}

func planPrompt(opts ...prompt.Option) (*prompt.Prompt, error) {
	systemPrompt, err := prompt.NewPrompt("plan", string(planPromptTmpl), opts...)
	if err != nil {
		return nil, err
	}
	return systemPrompt, nil
}

func InitializePrompt(cfg *config.ConfigStore) (string, error) {
	systemPrompt, err := prompt.NewPrompt("initialize", string(initializePromptTmpl))
	if err != nil {
		return "", err
	}
	return systemPrompt.Build(context.Background(), "", "", cfg)
}
