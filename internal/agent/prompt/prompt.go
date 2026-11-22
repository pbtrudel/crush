package prompt

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"text/template"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/crush/internal/agentteam"
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
	canWrite   bool
	toolNames  []string
	variant    VariantFunc
}

// VariantFunc returns the template text to render instead of the base one
// for a prompt profile, the model's provider and its model ID. ok false
// means keep the base template.
//
// The profile decides the text, so a profile that is not implemented for a
// prompt leaves it exactly as it was.
type VariantFunc func(profile, provider, model string) (text string, ok bool)

type PromptDat struct {
	Provider            string
	Model               string
	Config              config.Config
	WorkingDir          string
	IsGitRepo           bool
	Platform            string
	Date                string
	GitStatus           string
	ContextFiles        []ContextFile
	GlobalContextFiles  []ContextFile
	ContextBudget       *ContextBudget
	GlobalContextBudget *ContextBudget
	AvailSkillXML       string
	CanWrite            bool
	// ToolNames are the tools the agent is actually given, which is not
	// always its whole configured set: some tools depend on the run mode.
	ToolNames []string
	// TeamPolicy carries the Agent Teams coordination policy when the
	// experimental feature is enabled.
	TeamPolicy string
	// AgentTeams is the Agent Teams setting. With it off the prompt
	// carries no delegation instructions either.
	AgentTeams bool
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

// WithCanWrite marks the prompt as belonging to an agent that can edit files.
func WithCanWrite(canWrite bool) Option {
	return func(p *Prompt) {
		p.canWrite = canWrite
	}
}

// WithToolNames sets the tools the agent is actually given, so a prompt that
// advertises its tools cannot promise one that was never attached.
func WithToolNames(names []string) Option {
	return func(p *Prompt) {
		p.toolNames = names
	}
}

func WithWorkingDir(workingDir string) Option {
	return func(p *Prompt) {
		p.workingDir = workingDir
	}
}

// WithVariant registers the template a prompt renders for a profile other
// than the full one. Leaving it unset renders the base template for every
// profile, which is what a prompt with a single template set wants.
func WithVariant(fn VariantFunc) Option {
	return func(p *Prompt) {
		p.variant = fn
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
	t, err := template.New(p.name).Parse(p.templateText(store, provider, model))
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

// templateText returns the template the configured prompt profile renders.
// The base template is the full prompt, so it is what any profile without a
// variant of its own gets.
func (p *Prompt) templateText(store *config.ConfigStore, provider, model string) string {
	if p.variant == nil {
		return p.template
	}
	if text, ok := p.variant(store.Config().Options.GetPromptProfile(), provider, model); ok {
		return text
	}
	return p.template
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

// processContextPath loads the context files a context path names. A
// relative path is resolved in every directory from the project root down to
// the working directory, broadest first: a session started in a subdirectory
// still reads the instructions kept at the repository root. An absolute path
// names one file or directory and is read there only.
func processContextPath(p, workingDir string) []ContextFile {
	var contexts []ContextFile
	seen := make(map[string]bool)
	for _, dir := range contextSearchDirs(workingDir, filepathext.SmartIsAbs(p)) {
		for _, found := range processContextPathAt(filepathext.SmartJoin(dir, p)) {
			// Two files whose names differ only in case are two files
			// on a case-sensitive filesystem, so the key keeps the case.
			key := filepath.Clean(found.Path)
			if seen[key] {
				continue
			}
			seen[key] = true
			contexts = append(contexts, found)
		}
	}
	return contexts
}

// contextSearchDirs returns the directories a relative context path is
// resolved against, broadest first: the project root, then each directory
// leading down to the working directory. Outside a repository the project
// root is the working directory, so nothing above it is read. An absolute
// path is resolved against the working directory alone.
func contextSearchDirs(workingDir string, absolute bool) []string {
	if absolute {
		return []string{workingDir}
	}
	root, inRepo := config.ProjectRoot(workingDir)
	if !inRepo {
		return []string{workingDir}
	}
	abs, err := filepath.Abs(workingDir)
	if err != nil {
		return []string{workingDir}
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || !filepath.IsLocal(rel) {
		// The root and the working directory do not describe the same
		// tree, which a case-insensitive filesystem can do. Stay inside
		// the working directory rather than reading above it.
		return []string{workingDir}
	}
	if rel == "." {
		return []string{workingDir}
	}
	dirs := []string{root}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		dirs = append(dirs, current)
	}
	return dirs
}

// processContextPathAt loads one context file, or every file under one
// directory, at an already resolved path.
func processContextPathAt(fullPath string) []ContextFile {
	var contexts []ContextFile
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
//
// Relative paths resolve against workingDir, the directory the prompt names,
// so a prompt built for a directory other than the session's reads that
// directory's instructions.
func loadContextFiles(paths []string, workingDir string, store *config.ConfigStore, maxBytes int) ([]ContextFile, *ContextBudget) {
	var files []ContextFile
	seen := make(map[string]bool, len(paths))
	for _, pth := range paths {
		expanded := expandPath(pth, store)
		pathKey := strings.ToLower(expanded)
		if seen[pathKey] {
			continue
		}
		seen[pathKey] = true
		files = append(files, processContextPath(expanded, workingDir)...)
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
	contextFiles, contextBudget := loadContextFiles(cfg.Options.ContextPaths, workingDir, store, cfg.Options.GetContextMaxBytes())
	globalContextFiles, globalContextBudget := loadContextFiles(cfg.Options.GlobalContextPaths, workingDir, store, cfg.Options.GetContextMaxBytes())

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

	// A session started in a subdirectory of a repository is still in that
	// repository; the project root decides, as it does for config loading.
	// The check follows the directory the prompt names, so the rendered flag
	// and the rendered path cannot describe two different places.
	inRepo := isGitRepo(workingDir)
	data := PromptDat{
		Provider:      provider,
		Model:         model,
		Config:        *cfg,
		WorkingDir:    filepath.ToSlash(workingDir),
		IsGitRepo:     inRepo,
		Platform:      platform,
		Date:          p.now().Format("1/2/2006"),
		AvailSkillXML: availSkillXML,
		CanWrite:      p.canWrite,
		ToolNames:     p.toolNames,
	}
	data.AgentTeams = cfg.Options.GetAgentTeams()
	if data.AgentTeams && hasTeamTools(p.toolNames) {
		data.TeamPolicy = agentteam.Policy
	}

	data.ContextFiles = contextFiles
	data.GlobalContextFiles = globalContextFiles
	data.ContextBudget = contextBudget
	data.GlobalContextBudget = globalContextBudget
	return data, nil
}

// hasTeamTools reports whether the palette holds at least one Team tool. An
// empty list means the caller did not say which tools the agent has, so the
// policy is kept: dropping it on a missing hint would silently remove rules
// the agent needs.
func hasTeamTools(toolNames []string) bool {
	if len(toolNames) == 0 {
		return true
	}
	for _, name := range toolNames {
		if slices.Contains(agentteam.ToolNames, name) {
			return true
		}
	}
	return false
}

// isGitRepo reports whether dir is inside a working tree. The repository
// root decides, so a session started in a subdirectory still counts. A
// directory that holds a .git entry also counts when git cannot read it: a
// broken or dubious repository is still one, and the git commands report
// their own failure. Only the root search needs a root git found.
func isGitRepo(dir string) bool {
	if _, inRepo := config.ProjectRoot(dir); inRepo {
		return true
	}
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
