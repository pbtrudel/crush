package model

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/crush/internal/agentrun"
	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/ui/chat"
	"github.com/charmbracelet/crush/internal/ui/dialog"
)

// agentDescriptors returns every agent visible from the current session:
// its Team members, its dispatched subagents, and the live registry runs
// scoped to its session tree. Running entries come first, oldest first;
// settled entries follow, newest first.
func (m *UI) agentDescriptors() []dialog.AgentDescriptor {
	rootID := m.currentSessionID()
	if rootID == "" {
		return nil
	}
	now := time.Now()
	descriptors := make([]dialog.AgentDescriptor, 0)

	sessions := stateSessionTree(m.teamState, rootID)
	for _, member := range stateSessionMembers(m.teamState, sessions) {
		if member.Status != agentteam.StatusRunning && member.Status != agentteam.StatusProvisioning {
			continue
		}
		descriptors = append(descriptors, dialog.AgentDescriptor{
			SessionID:       member.SessionID,
			ParentSessionID: member.ParentID,
			Name:            member.Name,
			Kind:            agentrun.KindTeammate,
			Status:          agentRunStatusFromTeam(member.Status),
		})
	}

	if m.com != nil && m.com.Workspace != nil {
		for i := range m.chat.Len() {
			item, ok := m.chat.MessageItemAt(i).(*chat.AgentToolMessageItem)
			if !ok {
				continue
			}
			status, ok := item.DispatchStatus()
			if !ok || !subAgentStatusRetained(status, now) {
				continue
			}
			descriptors = append(descriptors, dialog.AgentDescriptor{
				SessionID:       m.com.Workspace.CreateAgentToolSessionID(item.MessageID(), item.ToolCall().ID),
				ParentSessionID: rootID,
				Name:            status.Label,
				Kind:            agentrun.KindSubagent,
				Status:          agentRunStatusFromDispatch(status),
				StartedAt:       status.Started,
				FinishedAt:      status.Finished,
			})
		}
	}

	for _, run := range freshestAgentRuns(sessionAgentRuns(m.agentRuns, rootID)) {
		src := dialog.AgentDescriptor{
			SessionID:       run.SessionID,
			ParentSessionID: run.ParentSessionID,
			Name:            run.Name,
			Kind:            run.Kind,
			Status:          run.Status,
			StartedAt:       run.StartedAt,
			FinishedAt:      run.FinishedAt,
		}
		merged := false
		for i := range descriptors {
			if descriptors[i].SessionID == src.SessionID {
				descriptors[i] = mergeAgentDescriptor(descriptors[i], src)
				merged = true
				break
			}
		}
		if !merged {
			descriptors = append(descriptors, src)
		}
	}

	descriptors = slices.DeleteFunc(descriptors, func(d dialog.AgentDescriptor) bool {
		return !retainedAgentID(d.SessionID)
	})
	sortAgentDescriptors(descriptors)
	return descriptors
}

// freshestAgentRuns deduplicates reopened child sessions before registry
// records are merged. Registry display ordering is not freshness ordering: a
// new running record sorts ahead of the older terminal record for the same
// child, so last-record-wins would resurrect the old lifecycle.
func freshestAgentRuns(runs []agentrun.Run) []agentrun.Run {
	latest := make(map[string]agentrun.Run, len(runs))
	order := make([]string, 0, len(runs))
	for _, run := range runs {
		current, exists := latest[run.SessionID]
		if !exists {
			order = append(order, run.SessionID)
		}
		if !exists || compareAgentRunFreshness(run, current) > 0 {
			latest[run.SessionID] = run
		}
	}
	freshest := make([]agentrun.Run, 0, len(latest))
	for _, sessionID := range order {
		freshest = append(freshest, latest[sessionID])
	}
	return freshest
}

// compareAgentRunFreshness returns a positive value when a is fresher than b.
// Start time is the primary freshness signal. Running wins a same-start tie
// against a terminal record; the remaining fields provide a total, deterministic
// tie-break independent of registry ordering.
func compareAgentRunFreshness(a, b agentrun.Run) int {
	if c := a.StartedAt.Compare(b.StartedAt); c != 0 {
		return c
	}
	aRunning := a.Status == agentrun.StatusRunning
	bRunning := b.Status == agentrun.StatusRunning
	if aRunning != bRunning {
		if aRunning {
			return 1
		}
		return -1
	}
	if c := a.FinishedAt.Compare(b.FinishedAt); c != 0 {
		return c
	}
	// Reverse string comparisons so the lexicographically smallest record
	// wins, matching the descriptor sort's deterministic ID tie-break.
	return cmp.Or(
		cmp.Compare(b.ID, a.ID),
		cmp.Compare(b.ParentSessionID, a.ParentSessionID),
		cmp.Compare(b.Kind, a.Kind),
		cmp.Compare(b.Name, a.Name),
		cmp.Compare(b.Status, a.Status),
	)
}

// mergeAgentDescriptor layers a registry run's lifecycle over an existing
// descriptor without discarding a more useful dispatch or Team name.
func mergeAgentDescriptor(dst, src dialog.AgentDescriptor) dialog.AgentDescriptor {
	if dst.Name != "" {
		src.Name = dst.Name
	}
	if src.StartedAt.IsZero() {
		src.StartedAt = dst.StartedAt
	}
	if src.FinishedAt.IsZero() {
		src.FinishedAt = dst.FinishedAt
	}
	return src
}

func agentRunStatusFromTeam(status agentteam.Status) agentrun.Status {
	switch status {
	case agentteam.StatusRunning, agentteam.StatusProvisioning:
		return agentrun.StatusRunning
	case agentteam.StatusFailed:
		return agentrun.StatusFailed
	default:
		return agentrun.StatusDone
	}
}

func agentRunStatusFromDispatch(status chat.DispatchStatus) agentrun.Status {
	if status.Running {
		return agentrun.StatusRunning
	}
	return agentrun.StatusDone
}

// retainedAgentID drops sessions that are not agents: empty IDs and the
// helper sessions used to generate titles or other background work.
func retainedAgentID(id string) bool {
	if id == "" {
		return false
	}
	return !strings.HasPrefix(id, "title-") && !strings.HasPrefix(id, "helper-")
}

func sortAgentDescriptors(descriptors []dialog.AgentDescriptor) {
	slices.SortStableFunc(descriptors, func(a, b dialog.AgentDescriptor) int {
		aRunning := a.Status == agentrun.StatusRunning
		bRunning := b.Status == agentrun.StatusRunning
		switch {
		case aRunning && !bRunning:
			return -1
		case !aRunning && bRunning:
			return 1
		case aRunning:
			if a.StartedAt.IsZero() != b.StartedAt.IsZero() {
				if a.StartedAt.IsZero() {
					return 1
				}
				return -1
			}
			if c := a.StartedAt.Compare(b.StartedAt); c != 0 {
				return c
			}
		default:
			if c := b.FinishedAt.Compare(a.FinishedAt); c != 0 {
				return c
			}
		}
		return cmp.Compare(a.SessionID, b.SessionID)
	})
}
