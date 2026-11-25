import type { Snapshot } from '../api/types';
import { emptyTeam, type State } from './reducer';

// applyBrowse shows another session's snapshot without touching the live
// global state: prompts, sign-in, the AWS card, the plan and the notices
// stay as the frames last left them, because a browse fetch can be older
// than the frame that just arrived. The team and agent views are
// workspace-wide, so the snapshot's copy is current.
export function applyBrowse(s: State, snap: Snapshot): State {
  return {
    ...s,
    browsing: snap.current_session_id,
    sessionId: snap.current_session_id,
    session: snap.session ?? null,
    messages: snap.messages ?? [],
    files: snap.files ?? [],
    filesStale: false,
    team: snap.team ?? emptyTeam(),
    needsReload: null,
    // Shell cards, the inspector and the activity line belong to the
    // session that was on screen, not to the one being browsed.
    inspected: null,
    shells: {},
    droppedShells: {},
    activity: {},
  };
}
