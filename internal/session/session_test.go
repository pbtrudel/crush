package session

import (
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/db"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/stretchr/testify/require"
)

func TestEstimatedUsageStateSurvivesFetchModifySave(t *testing.T) {
	dataDir := t.TempDir()
	t.Cleanup(func() {
		// Release closes this test's pooled connection. Do not call
		// db.ResetPool here: these tests run in parallel against a shared
		// global pool, and a global reset closes other tests' live
		// connections ("sql: database is closed").
		require.NoError(t, db.Release(dataDir))
	})

	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)

	sessions := NewService(db.New(conn), conn)

	created, err := sessions.Create(t.Context(), "test")
	require.NoError(t, err)
	created.PromptTokens = 100
	created.CompletionTokens = 50
	created.EstimatedUsage = true

	saved, err := sessions.Save(t.Context(), created)
	require.NoError(t, err)
	require.True(t, saved.EstimatedUsage)

	fetched, err := sessions.Get(t.Context(), created.ID)
	require.NoError(t, err)
	require.True(t, fetched.EstimatedUsage)

	fetched.Todos = []Todo{{
		Content:    "Check estimate state",
		Status:     TodoStatusInProgress,
		ActiveForm: "Checking estimate state",
	}}

	updated, err := sessions.Save(t.Context(), fetched)
	require.NoError(t, err)
	require.True(t, updated.EstimatedUsage)

	refetched, err := sessions.Get(t.Context(), created.ID)
	require.NoError(t, err)
	require.True(t, refetched.EstimatedUsage)
}

func TestCacheTokenTotalsRoundTrip(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	t.Cleanup(func() {
		// Release closes this test's pooled connection. Do not call
		// db.ResetPool here: these tests run in parallel against a shared
		// global pool, and a global reset closes other tests' live
		// connections ("sql: database is closed").
		require.NoError(t, db.Release(dataDir))
	})

	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)
	sessions := NewService(db.New(conn), conn)

	created, err := sessions.Create(t.Context(), "cache")
	require.NoError(t, err)
	created.TotalCacheReadTokens = 700
	created.TotalContextTokens = 900

	_, err = sessions.Save(t.Context(), created)
	require.NoError(t, err)

	fetched, err := sessions.Get(t.Context(), created.ID)
	require.NoError(t, err)
	require.Equal(t, int64(700), fetched.TotalCacheReadTokens)
	require.Equal(t, int64(900), fetched.TotalContextTokens)

	// Usage updates accumulate into the cache totals.
	err = sessions.AddSessionUsage(t.Context(), created.ID, 100, 1100, 0.5)
	require.NoError(t, err)

	fetched, err = sessions.Get(t.Context(), created.ID)
	require.NoError(t, err)
	require.Equal(t, int64(800), fetched.TotalCacheReadTokens)
	require.Equal(t, int64(2000), fetched.TotalContextTokens)
	require.InDelta(t, 0.5, fetched.Cost, 1e-9)
}

func TestUpdateTodosLeavesUsageUntouched(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	t.Cleanup(func() {
		// Release closes this test's pooled connection. Do not call
		// db.ResetPool here: these tests run in parallel against a shared
		// global pool, and a global reset closes other tests' live
		// connections ("sql: database is closed").
		require.NoError(t, db.Release(dataDir))
	})

	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)
	sessions := NewService(db.New(conn), conn)

	created, err := sessions.Create(t.Context(), "todos")
	require.NoError(t, err)
	created.TotalCacheReadTokens = 700
	created.TotalContextTokens = 900
	created.Cost = 0.25
	_, err = sessions.Save(t.Context(), created)
	require.NoError(t, err)

	todos := []Todo{{Content: "write tests", Status: TodoStatusPending, ActiveForm: "Writing tests"}}
	require.NoError(t, sessions.UpdateTodos(t.Context(), created.ID, todos))

	fetched, err := sessions.Get(t.Context(), created.ID)
	require.NoError(t, err)
	require.Equal(t, todos, fetched.Todos)
	// Usage columns are untouched.
	require.Equal(t, int64(700), fetched.TotalCacheReadTokens)
	require.Equal(t, int64(900), fetched.TotalContextTokens)
	require.InDelta(t, 0.25, fetched.Cost, 1e-9)

	// Clearing works too.
	require.NoError(t, sessions.UpdateTodos(t.Context(), created.ID, nil))
	fetched, err = sessions.Get(t.Context(), created.ID)
	require.NoError(t, err)
	require.Empty(t, fetched.Todos)
}

func TestSetTitleIfUnchanged(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	t.Cleanup(func() {
		// Release closes this test's pooled connection. Do not call
		// db.ResetPool here: these tests run in parallel against a shared
		// global pool, and a global reset closes other tests' live
		// connections ("sql: database is closed").
		require.NoError(t, db.Release(dataDir))
	})

	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)
	sessions := NewService(db.New(conn), conn)

	created, err := sessions.Create(t.Context(), "original")
	require.NoError(t, err)

	// Matching current title: applied.
	require.NoError(t, sessions.SetTitleIfUnchanged(t.Context(), created.ID, "original", "generated"))
	fetched, err := sessions.Get(t.Context(), created.ID)
	require.NoError(t, err)
	require.Equal(t, "generated", fetched.Title)

	// Stale expectation: not applied.
	require.NoError(t, sessions.SetTitleIfUnchanged(t.Context(), created.ID, "original", "clobbered"))
	fetched, err = sessions.Get(t.Context(), created.ID)
	require.NoError(t, err)
	require.Equal(t, "generated", fetched.Title)
}

func TestSessionChannelPersists(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	t.Cleanup(func() {
		// Release closes this test's pooled connection. Do not call
		// db.ResetPool here: these tests run in parallel against a shared
		// global pool, and a global reset closes other tests' live
		// connections ("sql: database is closed").
		require.NoError(t, db.Release(dataDir))
	})

	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)
	sessions := NewService(db.New(conn), conn)

	created, err := sessions.Create(t.Context(), "channel")
	require.NoError(t, err)
	updated, err := sessions.SetChannel(t.Context(), created.ID, "signal")
	require.NoError(t, err)
	require.Equal(t, "signal", updated.Channel)

	fetched, err := sessions.Get(t.Context(), created.ID)
	require.NoError(t, err)
	require.Equal(t, "signal", fetched.Channel)
}

func TestMCPServerDisabledRoundTrip(t *testing.T) {
	dataDir := t.TempDir()
	t.Cleanup(func() {
		// Release closes this test's pooled connection. Do not call
		// db.ResetPool here: these tests run in parallel against a shared
		// global pool, and a global reset closes other tests' live
		// connections ("sql: database is closed").
		require.NoError(t, db.Release(dataDir))
	})

	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)

	sessions := NewService(db.New(conn), conn)

	disabled, err := sessions.MCPDisabledServers(t.Context())
	require.NoError(t, err)
	require.Empty(t, disabled, "a new repository must default to the config")

	require.NoError(t, sessions.SetMCPServerDisabled(t.Context(), "docker", true))
	require.NoError(t, sessions.SetMCPServerDisabled(t.Context(), "serena", true))
	require.NoError(t, sessions.SetMCPServerDisabled(t.Context(), "docker", true), "disabling twice must be idempotent")

	disabled, err = sessions.MCPDisabledServers(t.Context())
	require.NoError(t, err)
	require.Equal(t, []string{"docker", "serena"}, disabled)

	require.NoError(t, sessions.SetMCPServerDisabled(t.Context(), "docker", false))
	disabled, err = sessions.MCPDisabledServers(t.Context())
	require.NoError(t, err)
	require.Equal(t, []string{"serena"}, disabled)

	// Enabling records an enabled override so a config-disabled server
	// stays enabled across restarts; disabling removes it again.
	enabled, err := sessions.MCPServersEnabled(t.Context())
	require.NoError(t, err)
	require.Equal(t, []string{"docker"}, enabled)

	require.NoError(t, sessions.SetMCPServerDisabled(t.Context(), "docker", true))
	enabled, err = sessions.MCPServersEnabled(t.Context())
	require.NoError(t, err)
	require.Empty(t, enabled)
}

func TestEstimatedUsageStateCanBeClearedByExplicitSave(t *testing.T) {
	dataDir := t.TempDir()
	t.Cleanup(func() {
		// Release closes this test's pooled connection. Do not call
		// db.ResetPool here: these tests run in parallel against a shared
		// global pool, and a global reset closes other tests' live
		// connections ("sql: database is closed").
		require.NoError(t, db.Release(dataDir))
	})

	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)

	sessions := NewService(db.New(conn), conn)

	created, err := sessions.Create(t.Context(), "test")
	require.NoError(t, err)
	created.PromptTokens = 100
	created.CompletionTokens = 50
	created.EstimatedUsage = true

	saved, err := sessions.Save(t.Context(), created)
	require.NoError(t, err)
	require.True(t, saved.EstimatedUsage)

	saved.EstimatedUsage = false
	updated, err := sessions.Save(t.Context(), saved)
	require.NoError(t, err)
	require.False(t, updated.EstimatedUsage)

	refetched, err := sessions.Get(t.Context(), created.ID)
	require.NoError(t, err)
	require.False(t, refetched.EstimatedUsage)
}

// AddStepUsage is the per-agent-step usage write: snapshot counters are
// replaced, accumulating ones increment, all in one statement so parallel
// sub-agents cannot lose each other's totals.
func TestAddStepUsage(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	t.Cleanup(func() { require.NoError(t, db.Release(dataDir)) })

	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)
	svc := NewService(db.New(conn), conn)
	ctx := t.Context()

	sess, err := svc.Create(ctx, "usage")
	require.NoError(t, err)

	sub := svc.Subscribe(ctx)
	var seen []Session
	done := make(chan struct{})
	go func() {
		for ev := range sub {
			if ev.Type == pubsub.UpdatedEvent {
				seen = append(seen, ev.Payload)
			}
			if len(seen) >= 2 {
				close(done)
				return
			}
		}
	}()

	first, err := svc.AddStepUsage(ctx, sess.ID, StepUsage{
		PromptTokens: 100, CompletionTokens: 40, CacheReadTokens: 1000, ContextTokens: 500, Cost: 1.5, Estimated: true,
	})
	require.NoError(t, err)
	require.Equal(t, int64(100), first.PromptTokens)
	require.Equal(t, int64(40), first.CompletionTokens)
	require.Equal(t, int64(1000), first.TotalCacheReadTokens)
	require.Equal(t, int64(500), first.TotalContextTokens)
	require.InDelta(t, 1.5, first.Cost, 1e-9)
	require.True(t, first.EstimatedUsage, "the estimated flag reaches the returned session")

	second, err := svc.AddStepUsage(ctx, sess.ID, StepUsage{
		PromptTokens: 200, CompletionTokens: 60, CacheReadTokens: 100, ContextTokens: 300, Cost: 0.5,
	})
	require.NoError(t, err)
	require.Equal(t, int64(200), second.PromptTokens, "prompt tokens are a snapshot, not a sum")
	require.Equal(t, int64(60), second.CompletionTokens)
	require.Equal(t, int64(1100), second.TotalCacheReadTokens, "cache reads accumulate")
	require.Equal(t, int64(800), second.TotalContextTokens, "context tokens accumulate")
	require.InDelta(t, 2.0, second.Cost, 1e-9, "cost accumulates")

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("usage updates were not published")
	}
	require.Len(t, seen, 2)
}

// waitForSessionUpdate reports whether an update for sessionID arrives before
// the timeout, draining every other session's updates on the way.
func waitForSessionUpdate(t *testing.T, updates <-chan Session, sessionID string) bool {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case s := <-updates:
			if s.ID == sessionID {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

func newRollupService(t *testing.T) Service {
	t.Helper()
	dataDir := t.TempDir()
	t.Cleanup(func() { require.NoError(t, db.Release(dataDir)) })
	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)
	return NewService(db.New(conn), conn)
}

// A child agent's usage must reach the parent as it happens: the sidebar and
// the Discord bridge read only the parent's totals, so a rollup deferred to
// the end of the sub-agent leaves the cache-hit percentage stale for the whole
// run.
func TestAddStepUsageRollsUpToParent(t *testing.T) {
	t.Parallel()

	svc := newRollupService(t)
	ctx := t.Context()

	parent, err := svc.Create(ctx, "parent")
	require.NoError(t, err)
	child, err := svc.CreateTaskSession(ctx, "rollup-step-child", parent.ID, "child")
	require.NoError(t, err)

	updates := make(chan Session, 16)
	sub := svc.Subscribe(ctx)
	go func() {
		for ev := range sub {
			if ev.Type == pubsub.UpdatedEvent {
				updates <- ev.Payload
			}
		}
	}()

	_, err = svc.AddStepUsage(ctx, child.ID, StepUsage{
		PromptTokens: 100, CompletionTokens: 40, CacheReadTokens: 1000, ContextTokens: 500, Cost: 1.5,
	})
	require.NoError(t, err)

	parentAfter, err := svc.Get(ctx, parent.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1000), parentAfter.TotalCacheReadTokens)
	require.Equal(t, int64(500), parentAfter.TotalContextTokens)
	require.InDelta(t, 1.5, parentAfter.Cost, 1e-9)

	childAfter, err := svc.Get(ctx, child.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1000), childAfter.TotalCacheReadTokens, "the child keeps its own totals")

	require.True(t, waitForSessionUpdate(t, updates, parent.ID), "the parent's update must be published live")
}

func TestAddSessionUsageRollsUpToParent(t *testing.T) {
	t.Parallel()

	svc := newRollupService(t)
	ctx := t.Context()

	parent, err := svc.Create(ctx, "parent")
	require.NoError(t, err)
	child, err := svc.CreateTitleSession(ctx, parent.ID)
	require.NoError(t, err)

	updates := make(chan Session, 16)
	sub := svc.Subscribe(ctx)
	go func() {
		for ev := range sub {
			if ev.Type == pubsub.UpdatedEvent {
				updates <- ev.Payload
			}
		}
	}()

	require.NoError(t, svc.AddSessionUsage(ctx, child.ID, 700, 900, 0.25))

	parentAfter, err := svc.Get(ctx, parent.ID)
	require.NoError(t, err)
	require.Equal(t, int64(700), parentAfter.TotalCacheReadTokens)
	require.Equal(t, int64(900), parentAfter.TotalContextTokens)
	require.InDelta(t, 0.25, parentAfter.Cost, 1e-9)

	require.True(t, waitForSessionUpdate(t, updates, parent.ID), "the parent's update must be published live")
}

// A grandchild's usage must reach the root, not stop at the middle session:
// only the root is listed and rendered, so a chain that halts after one hop
// leaves the sidebar and Discord under-reporting every deeper agent. The hop
// goes through AddSessionUsage on the parent, which rolls up again, and each
// level is written exactly once.
func TestUsageRollupReachesTheRootThroughTheChain(t *testing.T) {
	t.Parallel()

	svc := newRollupService(t)
	ctx := t.Context()

	root, err := svc.Create(ctx, "root")
	require.NoError(t, err)
	middle, err := svc.CreateTaskSession(ctx, "rollup-middle", root.ID, "middle")
	require.NoError(t, err)
	leaf, err := svc.CreateTaskSession(ctx, "rollup-leaf", middle.ID, "leaf")
	require.NoError(t, err)

	_, err = svc.AddStepUsage(ctx, leaf.ID, StepUsage{CacheReadTokens: 100, ContextTokens: 200, Cost: 1})
	require.NoError(t, err)

	middleAfter, err := svc.Get(ctx, middle.ID)
	require.NoError(t, err)
	require.Equal(t, int64(100), middleAfter.TotalCacheReadTokens)
	require.Equal(t, int64(200), middleAfter.TotalContextTokens)
	require.InDelta(t, 1, middleAfter.Cost, 1e-9)

	rootAfter, err := svc.Get(ctx, root.ID)
	require.NoError(t, err)
	require.Equal(t, int64(100), rootAfter.TotalCacheReadTokens, "one call must carry the leaf's usage to the root exactly once")
	require.Equal(t, int64(200), rootAfter.TotalContextTokens)
	require.InDelta(t, 1, rootAfter.Cost, 1e-9)

	_, err = svc.AddStepUsage(ctx, middle.ID, StepUsage{CacheReadTokens: 10, ContextTokens: 20, Cost: 0.5})
	require.NoError(t, err)

	middleAfter, err = svc.Get(ctx, middle.ID)
	require.NoError(t, err)
	require.Equal(t, int64(110), middleAfter.TotalCacheReadTokens)
	require.Equal(t, int64(220), middleAfter.TotalContextTokens)
	require.InDelta(t, 1.5, middleAfter.Cost, 1e-9)

	rootAfter, err = svc.Get(ctx, root.ID)
	require.NoError(t, err)
	require.Equal(t, int64(110), rootAfter.TotalCacheReadTokens, "the middle session's own step rolls up on top of the leaf's")
	require.Equal(t, int64(220), rootAfter.TotalContextTokens)
	require.InDelta(t, 1.5, rootAfter.Cost, 1e-9)
}

// A root session has no parent to roll into, so its totals stay exactly what
// its own steps recorded.
func TestUsageRollupLeavesRootSessionAlone(t *testing.T) {
	t.Parallel()

	svc := newRollupService(t)
	ctx := t.Context()

	root, err := svc.Create(ctx, "root")
	require.NoError(t, err)
	require.Empty(t, root.ParentSessionID)

	_, err = svc.AddStepUsage(ctx, root.ID, StepUsage{CacheReadTokens: 100, ContextTokens: 200, Cost: 1})
	require.NoError(t, err)
	require.NoError(t, svc.AddSessionUsage(ctx, root.ID, 10, 20, 0.5))

	after, err := svc.Get(ctx, root.ID)
	require.NoError(t, err)
	require.Equal(t, int64(110), after.TotalCacheReadTokens)
	require.Equal(t, int64(220), after.TotalContextTokens)
	require.InDelta(t, 1.5, after.Cost, 1e-9)
}

// A session whose parent is itself must not roll into itself, which would
// double every total.
func TestUsageRollupIgnoresSelfParent(t *testing.T) {
	t.Parallel()

	svc := newRollupService(t)
	ctx := t.Context()

	self, err := svc.CreateTaskSession(ctx, "rollup-self", "rollup-self", "self")
	require.NoError(t, err)
	require.Equal(t, self.ID, self.ParentSessionID)

	_, err = svc.AddStepUsage(ctx, self.ID, StepUsage{CacheReadTokens: 100, ContextTokens: 200, Cost: 1})
	require.NoError(t, err)
	require.NoError(t, svc.AddSessionUsage(ctx, self.ID, 10, 20, 0.5))

	after, err := svc.Get(ctx, self.ID)
	require.NoError(t, err)
	require.Equal(t, int64(110), after.TotalCacheReadTokens)
	require.Equal(t, int64(220), after.TotalContextTokens)
	require.InDelta(t, 1.5, after.Cost, 1e-9)
}

// A parent can be deleted while a child is still recording usage:
// parent_session_id has no foreign key, so the parent row can be gone. The
// rollup must not turn that into a failed write - the child's own totals still
// have to land.
func TestUsageRollupToleratesMissingParent(t *testing.T) {
	t.Parallel()

	svc := newRollupService(t)
	ctx := t.Context()

	orphan, err := svc.CreateTaskSession(ctx, "rollup-orphan", "deleted-parent", "orphan")
	require.NoError(t, err)

	_, err = svc.AddStepUsage(ctx, orphan.ID, StepUsage{CacheReadTokens: 100, ContextTokens: 200, Cost: 1})
	require.NoError(t, err)
	require.NoError(t, svc.AddSessionUsage(ctx, orphan.ID, 10, 20, 0.5))

	after, err := svc.Get(ctx, orphan.ID)
	require.NoError(t, err)
	require.Equal(t, int64(110), after.TotalCacheReadTokens)
	require.Equal(t, int64(220), after.TotalContextTokens)
	require.InDelta(t, 1.5, after.Cost, 1e-9)
}
