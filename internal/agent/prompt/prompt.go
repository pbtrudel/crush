package prompt

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"text/template"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/filepathext"
	"github.com/charmbracelet/crush/internal/gitutil"
	"github.com/charmbracelet/crush/internal/home"
	"github.com/charmbracelet/crush/internal/shell"
	"github.com/charmbracelet/crush/internal/skills"
)

// Prompt represents a template-based prompt generator.
type Prompt struct {
	name       string
	template   string
	now        func() time.Time
	platform   string
	workingDir string
}

type PromptDat struct {
	Provider            string
	Model               string
	Config              config.Config
	WorkingDir          string
	IsGitRepo           bool
	Platform            string
	Date                string
	ContextFiles        []ContextFile
	GlobalContextFiles  []ContextFile
	ContextBudget       *ContextBudget
	GlobalContextBudget *ContextBudget
	AvailSkillXML       string
}

type ContextFile struct {
	Path    string
	Content string
}

// ContextBudget describes what a context-chain byte budget dropped or
// truncated. It is rendered into the prompt so the model knows the
// instructions it sees are incomplete.
type ContextBudget struct {
	LimitBytes int
	Omitted    []string
	Truncated  []string
}

// Notice renders the budget summary embedded in the prompt. It returns an
// empty string when nothing was changed.
func (b ContextBudget) Notice() string {
	if b.LimitBytes <= 0 || len(b.Omitted) == 0 && len(b.Truncated) == 0 {
		return ""
	}
	var parts []string
	if len(b.Omitted) > 0 {
		parts = append(parts, "omitted "+strings.Join(b.Omitted, ", "))
	}
	if len(b.Truncated) > 0 {
		parts = append(parts, "truncated "+strings.Join(b.Truncated, ", "))
	}
	return fmt.Sprintf("Context budget %d bytes: %s.", b.LimitBytes, strings.Join(parts, "; "))
}

type Option func(*Prompt)

func WithTimeFunc(fn func() time.Time) Option {
	return func(p *Prompt) {
		p.now = fn
	}
}

func WithPlatform(platform string) Option {
	return func(p *Prompt) {
		p.platform = platform
	}
}

func WithWorkingDir(workingDir string) Option {
	return func(p *Prompt) {
		p.workingDir = workingDir
	}
}

func NewPrompt(name, promptTemplate string, opts ...Option) (*Prompt, error) {
	p := &Prompt{
		name:     name,
		template: promptTemplate,
		now:      time.Now,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p, nil
}

func (p *Prompt) Build(ctx context.Context, provider, model string, store *config.ConfigStore) (string, error) {
	t, err := template.New(p.name).Parse(p.template)
	if err != nil {
		return "", fmt.Errorf("parsing template: %w", err)
	}
	var sb strings.Builder
	d, err := p.promptData(ctx, provider, model, store)
	if err != nil {
		return "", err
	}
	if err := t.Execute(&sb, d); err != nil {
		return "", fmt.Errorf("executing template: %w", err)
	}

	return sb.String(), nil
}

func processFile(filePath string) *ContextFile {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	return &ContextFile{
		Path:    filePath,
		Content: string(content),
	}
}

func processContextPath(p string, store *config.ConfigStore) []ContextFile {
	var contexts []ContextFile
	fullPath := filepathext.SmartJoin(store.WorkingDir(), p)
	info, err := os.Stat(fullPath)
	if err != nil {
		return contexts
	}
	if info.IsDir() {
		filepath.WalkDir(fullPath, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				if result := processFile(path); result != nil {
					contexts = append(contexts, *result)
				}
			}
			return nil
		})
	} else {
		result := processFile(fullPath)
		if result != nil {
			contexts = append(contexts, *result)
		}
	}
	return contexts
}

// expandPath expands ~ and environment variables in file paths
func expandPath(path string, store *config.ConfigStore) string {
	path = home.Long(path)
	// Handle environment variable expansion using the same pattern as config
	if strings.HasPrefix(path, "$") {
		if expanded, err := store.Resolver().ResolveValue(path); err == nil {
			path = expanded
		}
	}

	return path
}

// loadContextFiles loads and deduplicates context files from a list of
// paths, preserving the order of the input paths. The order has to be
// deterministic because the result is embedded in the system prompt: a
// reordering between builds rewrites the prompt prefix and invalidates the
// provider's prompt cache.
//
// When maxBytes is positive, the chain is bounded: broader files are omitted
// before the most specific file is truncated, so a large project chain
// cannot evict the user's global instructions.
func loadContextFiles(paths []string, store *config.ConfigStore, maxBytes int) ([]ContextFile, *ContextBudget) {
	var files []ContextFile
	seen := make(map[string]bool, len(paths))
	for _, pth := range paths {
		expanded := expandPath(pth, store)
		pathKey := strings.ToLower(expanded)
		if seen[pathKey] {
			continue
		}
		seen[pathKey] = true
		files = append(files, processContextPath(expanded, store)...)
	}
	return budgetContextFiles(files, maxBytes)
}

// budgetContextFiles applies a byte budget to one context chain. Files are
// ranked by specificity: deeper paths and the canonical instruction names
// survive longest. The returned slice keeps the original file order.
func budgetContextFiles(files []ContextFile, maxBytes int) ([]ContextFile, *ContextBudget) {
	if maxBytes <= 0 || len(files) == 0 {
		return files, nil
	}
	var total int
	for _, f := range files {
		total += len(f.Content)
	}
	if total <= maxBytes {
		return files, nil
	}

	order := make([]int, len(files))
	for i := range files {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		return contextFilePriority(files[order[i]]) < contextFilePriority(files[order[j]])
	})

	dropped := make([]bool, len(files))
	var omitted []string
	for _, idx := range order {
		if total <= maxBytes || len(files)-len(omitted) <= 1 {
			break
		}
		total -= len(files[idx].Content)
		dropped[idx] = true
		omitted = append(omitted, files[idx].Path)
	}

	kept := make([]ContextFile, 0, len(files)-len(omitted))
	for i, f := range files {
		if !dropped[i] {
			kept = append(kept, f)
		}
	}

	var truncated []string
	if total > maxBytes && len(kept) > 0 {
		truncIdx := 0
		for i := range kept {
			if contextFilePriority(kept[i]) > contextFilePriority(kept[truncIdx]) {
				truncIdx = i
			}
		}
		available := maxBytes - (total - len(kept[truncIdx].Content))
		kept[truncIdx].Content = truncateContextContent(kept[truncIdx].Content, available)
		truncated = append(truncated, kept[truncIdx].Path)
	}

	return kept, &ContextBudget{
		LimitBytes: maxBytes,
		Omitted:    omitted,
		Truncated:  truncated,
	}
}

// contextFilePriority ranks a context file for budget retention. Higher
// values survive longer. The canonical instruction file names outrank
// incidental project files at the same depth.
func contextFilePriority(f ContextFile) int {
	slashPath := filepath.ToSlash(f.Path)
	depth := strings.Count(slashPath, "/")
	name := strings.ToLower(filepath.Base(slashPath))
	var nameScore int
	switch name {
	case "agents.md":
		nameScore = 100
	case "crush.md":
		nameScore = 90
	case "claude.md":
		nameScore = 80
	case "gemini.md":
		nameScore = 70
	case "agents.local.md":
		nameScore = 60
	case "crush.local.md":
		nameScore = 50
	case "claude.local.md":
		nameScore = 40
	}
	return depth*1000 + nameScore + len(slashPath)
}

// truncateContextContent keeps the head and tail of a context file within
// maxBytes, inserting a marker in the middle. It never splits a UTF-8 rune.
func truncateContextContent(content string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(content) <= maxBytes {
		return content
	}
	const marker = "\n\n[... context truncated to fit the context budget ...]\n\n"
	if maxBytes <= len(marker) {
		return runePrefix(marker, maxBytes)
	}
	keep := maxBytes - len(marker)
	headBudget := keep * 2 / 3
	tailBudget := keep - headBudget
	head := runePrefix(content, headBudget)
	tail := runeSuffix(content, tailBudget)
	return head + marker + tail
}

// runePrefix returns the longest prefix of s whose UTF-8 byte length does
// not exceed maxBytes.
func runePrefix(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	cut := 0
	for idx := range s {
		if idx > maxBytes {
			break
		}
		cut = idx
	}
	if cut == 0 {
		return ""
	}
	return s[:cut]
}

// runeSuffix returns the longest suffix of s whose UTF-8 byte length does
// not exceed maxBytes, starting on a rune boundary.
func runeSuffix(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	start := len(s) - maxBytes
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:]
}

func (p *Prompt) promptData(ctx context.Context, provider, model string, store *config.ConfigStore) (PromptDat, error) {
	workingDir := cmp.Or(p.workingDir, store.WorkingDir())
	platform := cmp.Or(p.platform, runtime.GOOS)

	cfg := store.Config()
	contextFiles, contextBudget := loadContextFiles(cfg.Options.ContextPaths, store, cfg.Options.GetContextMaxBytes())
	globalContextFiles, globalContextBudget := loadContextFiles(cfg.Options.GlobalContextPaths, store, cfg.Options.GetContextMaxBytes())

	// Discover and load skills metadata.
	var availSkillXML string

	// Start with builtin skills.
	allSkills := skills.DiscoverBuiltin()
	builtinNames := make(map[string]bool, len(allSkills))
	for _, s := range allSkills {
		builtinNames[s.Name] = true
	}

	// Discover user skills from configured paths.
	if len(cfg.Options.SkillsPaths) > 0 {
		expandedPaths := make([]string, 0, len(cfg.Options.SkillsPaths))
		for _, pth := range cfg.Options.SkillsPaths {
			expandedPaths = append(expandedPaths, expandPath(pth, store))
		}
		for _, userSkill := range skills.Discover(expandedPaths) {
			if builtinNames[userSkill.Name] {
				slog.Warn("User skill overrides builtin skill", "name", userSkill.Name)
			}
			allSkills = append(allSkills, userSkill)
		}
	}

	// Deduplicate: user skills override builtins with the same name.
	allSkills = skills.Deduplicate(allSkills)

	// Filter out disabled skills.
	allSkills = skills.Filter(allSkills, cfg.Options.DisabledSkills)

	if len(allSkills) > 0 {
		availSkillXML = skills.ToPromptXML(allSkills)
	}

	isGit := isGitRepo(store.WorkingDir())
	data := PromptDat{
		Provider:      provider,
		Model:         model,
		Config:        *cfg,
		WorkingDir:    filepath.ToSlash(workingDir),
		IsGitRepo:     isGit,
		Platform:      platform,
		Date:          p.now().Format("1/2/2006"),
		AvailSkillXML: availSkillXML,
	}

	data.ContextFiles = contextFiles
	data.GlobalContextFiles = globalContextFiles
	data.ContextBudget = contextBudget
	data.GlobalContextBudget = globalContextBudget
	return data, nil
}

func isGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

func getGitStatus(ctx context.Context, dir string) (string, error) {
	sh := shell.NewShell(&shell.Options{
		WorkingDir: dir,
	})
	branch := getGitBranch(dir)
	status, err := getGitStatusSummary(ctx, sh)
	if err != nil {
		return "", err
	}
	commits, err := getGitRecentCommits(ctx, sh)
	if err != nil {
		return "", err
	}
	return branch + status + commits, nil
}

// getGitBranch reads the checked-out branch straight from the repository.
// It stays off the shell: a detail of prompt assembly has no business
// passing through command blocking and permission policy.
func getGitBranch(dir string) string {
	branch := gitutil.CurrentBranch(dir)
	if branch == "" {
		return ""
	}
	return fmt.Sprintf("Current branch: %s\n", branch)
}

func getGitStatusSummary(ctx context.Context, sh *shell.Shell) (string, error) {
	out, _, err := sh.Exec(ctx, "git status --short 2>/dev/null | head -20")
	if err != nil {
		return "", nil
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return "Status: clean\n", nil
	}
	return fmt.Sprintf("Status:\n%s\n", out), nil
}

func getGitRecentCommits(ctx context.Context, sh *shell.Shell) (string, error) {
	out, _, err := sh.Exec(ctx, "git log --oneline -n 3 2>/dev/null")
	if err != nil || out == "" {
		return "", nil
	}
	out = strings.TrimSpace(out)
	return fmt.Sprintf("Recent commits:\n%s\n", out), nil
}

func (p *Prompt) Name() string {
	return p.name
}
