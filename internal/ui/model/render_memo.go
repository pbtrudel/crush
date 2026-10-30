package model

import (
	"cmp"
	"math"

	"github.com/charmbracelet/crush/internal/session"
)

// This file holds the memoization keys for the per-frame sidebar and
// pills rebuilds. Each key captures every input the corresponding
// builder reads; the builder is skipped while the key is unchanged.
//
// Slice and map inputs are captured by identity (pointer + length) or
// by a version counter bumped where the state is replaced wholesale.
// Config edits that rename a provider or change the MCP/skill lists
// without any state refresh are not captured; they apply on the next
// refresh.

// modelInfoKey captures the model inputs modelInfo renders from.
type modelInfoKey struct {
	name            string
	provider        string
	contextWindow   int64
	canReason       bool
	reasoningLevels int
	reasoningEffort string
	think           bool
}

// sidebarCacheKey captures every input updateSidebarScrollState reads.
type sidebarCacheKey struct {
	width, height int
	isCompact     bool
	session       *session.Session
	gitBranch     string
	workingDir    string
	agentReady    bool
	model         modelInfoKey
	lspVer        uint64
	mcpVer        uint64
	skillsVer     uint64
	filesLen      int
	filesPtr      *SessionFile
	logo          string
	hyper         bool
	hasCredits    bool
	credits       int
	tpsLive       int
	tpsAverage    int
}

// pillsCacheKey captures every input renderPills reads.
type pillsCacheKey struct {
	width        int
	session      *session.Session
	queue        int
	queueLen     int
	queuePtr     *string
	expanded     bool
	spinning     bool
	focusedTodos bool
	focusedQueue bool
	hasInline    bool
}

func (m *UI) sidebarCacheKey() sidebarCacheKey {
	k := sidebarCacheKey{
		width:      m.layout.sidebar.Dx(),
		height:     m.layout.sidebar.Dy(),
		isCompact:  m.isCompact,
		session:    m.session,
		gitBranch:  m.gitBranch,
		workingDir: m.com.Workspace.WorkingDir(),
		agentReady: m.agentReady,
		model:      m.modelInfoKey(),
		lspVer:     m.lspVer,
		mcpVer:     m.mcpVer,
		skillsVer:  m.skillsVer,
		filesLen:   len(m.sessionFiles),
		logo:       m.sidebarLogo,
		hyper:      m.com.IsHyper(),
	}
	liveTPS, averageTPS := m.tpsStats()
	// Key on tenths to match the sidebar's one-decimal display without
	// rebuilding on every fractional change while streaming.
	k.tpsLive = int(math.Round(liveTPS * 10))
	k.tpsAverage = int(math.Round(averageTPS * 10))
	if k.filesLen > 0 {
		k.filesPtr = &m.sessionFiles[0]
	}
	if m.hyperCredits != nil {
		k.hasCredits = true
		k.credits = *m.hyperCredits
	}
	return k
}

func (m *UI) modelInfoKey() modelInfoKey {
	model := m.selectedLargeModel()
	if model == nil {
		return modelInfoKey{}
	}
	k := modelInfoKey{
		name:            model.CatwalkCfg.Name,
		contextWindow:   model.CatwalkCfg.ContextWindow,
		canReason:       model.CatwalkCfg.CanReason(),
		reasoningLevels: len(model.CatwalkCfg.ReasoningEffortLevels()),
		reasoningEffort: cmp.Or(model.ModelCfg.ReasoningEffort, model.CatwalkCfg.Reasoning.DefaultEffortLevel),
		think:           model.ModelCfg.Think,
	}
	if providerConfig, ok := m.com.Config().Providers.Get(model.ModelCfg.Provider); ok {
		k.provider = providerConfig.Name
	}
	return k
}

func (m *UI) pillsCacheKey() pillsCacheKey {
	k := pillsCacheKey{
		width:     m.layout.pills.Dx(),
		session:   m.session,
		queue:     m.promptQueue,
		queueLen:  len(m.promptQueueItems),
		expanded:  m.pillsExpanded,
		spinning:  m.todoIsSpinning,
		hasInline: m.activeInline != nil,
	}
	if k.queueLen > 0 {
		k.queuePtr = &m.promptQueueItems[0]
	}
	if m.session != nil {
		focused := m.effectiveFocusedSection()
		k.focusedTodos = focused == pillSectionTodos
		k.focusedQueue = focused == pillSectionQueue
	}
	return k
}
