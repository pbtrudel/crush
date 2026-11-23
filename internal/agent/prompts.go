package agent

import (
	"context"
	"embed"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/charmbracelet/crush/internal/agent/prompt"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/filepathext"
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

// coderPrompt builds the built-in coder prompt, with the lean profile
// variant attached.
func coderPrompt(opts ...prompt.Option) (*prompt.Prompt, error) {
	return prompt.NewPrompt("coder", string(coderPromptTmpl), withPromptVariant("coder", string(coderPromptTmpl), opts)...)
}

// withPromptVariant attaches the lean profile variant to the coder prompt
// only while it is the built-in one: a prompt the user replaced through
// options.prompts renders as written for every profile.
func withPromptVariant(name, text string, opts []prompt.Option) []prompt.Option {
	if name != "coder" || text != string(coderPromptTmpl) {
		return opts
	}
	return append([]prompt.Option{prompt.WithVariant(coderPromptVariant)}, opts...)
}

//go:embed templates/title.md
var titlePromptTmpl []byte

//go:embed templates/summary.md
var summaryPromptTmpl []byte

// builtinPrompts holds every prompt a user may replace via options.prompts.
// Its keys must cover config.PromptNames, which TestBuiltinPromptsCoverNames
// enforces.
var builtinPrompts = map[string][]byte{
	"coder":      coderPromptTmpl,
	"task":       taskPromptTmpl,
	"plan":       planPromptTmpl,
	"initialize": initializePromptTmpl,
	"title":      titlePromptTmpl,
	"summary":    summaryPromptTmpl,
}

// promptText returns a built-in prompt, or the file that replaces it.
func promptText(store *config.ConfigStore, name string) (string, error) {
	builtin, ok := builtinPrompts[name]
	if !ok {
		return "", fmt.Errorf("unknown prompt %q", name)
	}
	path := promptPath(store, name)
	if path == "" {
		return string(builtin), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading %s prompt: %w", name, err)
	}
	return string(data), nil
}

// promptPath returns the file replacing the named prompt, or "" for the
// built-in. A --prompt flag beats options.prompts. Config loading already
// anchors relative paths to the config file that set them; anything still
// relative (a $VAR that expands to one) resolves against the workspace.
func promptPath(store *config.ConfigStore, name string) string {
	if path := store.Overrides().Prompts[name]; path != "" {
		return path
	}
	opts := store.Config().Options
	if opts == nil || opts.Prompts[name] == "" {
		return ""
	}
	return filepathext.SmartJoin(store.WorkingDir(), prompt.ExpandPath(opts.Prompts[name], store))
}

// loadPromptTexts reads every prompt once, built-in or replaced, so a
// missing file or a broken template fails at startup, and a file moved or
// deleted mid-session cannot later break compaction, titles, or sub-agents.
func loadPromptTexts(store *config.ConfigStore) (map[string]string, error) {
	texts := make(map[string]string, len(config.PromptNames))
	for _, name := range config.PromptNames {
		text, err := promptText(store, name)
		if err != nil {
			return nil, err
		}
		if templatedPrompts[name] {
			if _, err := prompt.NewPrompt(name, text); err != nil {
				return nil, err
			}
		}
		texts[name] = text
	}
	return texts, nil
}

// promptOrBuiltin returns the named prompt from texts, as loaded by
// loadPromptTexts, or the built-in when texts lacks it, as it does for
// agents built by hand in tests.
func promptOrBuiltin(texts map[string]string, name string) string {
	if text, ok := texts[name]; ok {
		return text
	}
	return string(builtinPrompts[name])
}

// newPrompt builds a templated prompt from the text read at startup.
func (c *coordinator) newPrompt(name string, opts ...prompt.Option) (*prompt.Prompt, error) {
	text := promptOrBuiltin(c.promptTexts, name)
	return prompt.NewPrompt(name, text, withPromptVariant(name, text, opts)...)
}

// templatedPrompts are the prompts rendered as Go templates. Title and
// summary are sent verbatim, so a "{{" in them is just text.
var templatedPrompts = map[string]bool{
	"coder":      true,
	"task":       true,
	"plan":       true,
	"initialize": true,
}

// loadPrompt builds a templated prompt by name, honoring options.prompts.
func loadPrompt(store *config.ConfigStore, name string, opts ...prompt.Option) (*prompt.Prompt, error) {
	text, err := promptText(store, name)
	if err != nil {
		return nil, err
	}
	return prompt.NewPrompt(name, text, withPromptVariant(name, text, opts)...)
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

func InitializePrompt(store *config.ConfigStore) (string, error) {
	systemPrompt, err := loadPrompt(store, "initialize")
	if err != nil {
		return "", err
	}
	return systemPrompt.Build(context.Background(), "", "", store)
}
