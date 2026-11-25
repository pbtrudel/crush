import type { TaskStatus, TeamTask } from '../../api/types';
import { scopedTasks } from '../../state/agents';
import { actions, state } from '../../state/store';

const LANES: Array<[TaskStatus, string]> = [['pending', 'Pending'], ['in_progress', 'In progress'], ['completed', 'Completed']];

export function TaskBoard() {
  const s = state.value;
  const tasks = scopedTasks(s.sessionId, s.team, s.runs);
  if (tasks.length === 0) return null;
  const ownerSession = (t: TeamTask) => s.team.members.find((m) => m.id === t.owner_id)?.session_id;
  return (
    <section class="taskboard holo scanlines" aria-labelledby="taskboard-title">
      <h2 id="taskboard-title" class="hud-label">Team tasks</h2>
      <div class="taskboard__lanes">
        {LANES.map(([status, title]) => {
          const lane = tasks.filter((t) => t.status === status);
          return (
            <section key={status} class="taskboard__lane" data-status={status} aria-labelledby={`lane-${status}`}>
              <h3 id={`lane-${status}`} class="hud-label">{title} <span class="taskboard__count">{lane.length}</span></h3>
              <ul>
                {lane.map((t) => {
                  const sid = ownerSession(t);
                  return (
                    <li key={t.id} class="taskcard" data-ready={t.ready}>
                      <span class="taskcard__id hud-label">{t.id}</span>
                      <span class="taskcard__subject">{t.subject}</span>
                      {t.status === 'pending' && !t.ready && t.blocked_by.length > 0 && (
                        <span class="taskcard__blocked">Blocked by {t.blocked_by.join(', ')}</span>
                      )}
                      {t.owner_name && (sid
                        ? (
                          <button type="button" class="chip taskcard__owner" aria-label={`Inspect ${t.owner_name}`}
                            onClick={() => void actions.inspect(sid)}>{t.owner_name}</button>
                        )
                        : <span class="chip">{t.owner_name}</span>)}
                    </li>
                  );
                })}
              </ul>
            </section>
          );
        })}
      </div>
    </section>
  );
}
