import { useEffect, useRef, useState } from 'preact/hooks';
import type { Session } from '../../api/types';
import { api } from '../../api/http';
import { describeFailure } from '../../state/failure';
import { state } from '../../state/store';
import {
  closeSessions, sessionDeleteId, sessionRenameId, sessionsOpen,
} from '../../state/palette';
import { useDialogFocus } from '../interrupt/focus';

// The server answers newest first and the switcher keeps that order (I16).
export function SessionSwitcher({ inert }: { inert: boolean }) {
  if (!sessionsOpen.value) return null;
  return <SessionsDialog inert={inert} />;
}

// A focus request outlives the render that removes the editor, because the
// element to focus only exists once the row is back to its buttons.
type FocusRequest =
  | { kind: 'rename'; id: string }
  | { kind: 'delete'; id: string }
  | { kind: 'deleted'; id: string; index: number }
  | { kind: 'filter' };

function SessionsDialog({ inert }: { inert: boolean }) {
  const [sessions, setSessions] = useState<Session[]>([]);
  const [query, setQuery] = useState('');
  const [draft, setDraft] = useState('');
  const [active, setActive] = useState(0);
  const [loadError, setLoadError] = useState<unknown | null>(null);
  const [actionError, setActionError] = useState<unknown | null>(null);
  const dialog = useRef<HTMLDivElement>(null);
  const filterInput = useRef<HTMLInputElement>(null);
  const renameInput = useRef<HTMLInputElement>(null);
  const confirmButton = useRef<HTMLButtonElement>(null);
  const alive = useRef(true);
  const generation = useRef(0);
  const focusRequest = useRef<FocusRequest | null>(null);
  // The editor effects below close a rename or a confirm from outside the
  // component (Escape), so they watch the signals and hand focus back.
  const lastRenaming = useRef<string | null>(null);
  const lastDeleting = useRef<string | null>(null);
  const deletedIndex = useRef<number | null>(null);
  // A deleted row is gone on the server even when its reload failed, so the
  // Retry line becomes the focus target instead of a stale row.
  const reloadFailed = useRef(false);
  // A close that carries a fresh server answer must keep its message line.
  const keepsError = useRef(false);
  // One request at a time, even when a double click or a double Enter arrives.
  const following = useRef(false);
  const renamingBusy = useRef(false);
  const deletingBusy = useRef(false);
  const renaming = sessionRenameId.value;
  const deleting = sessionDeleteId.value;

  useDialogFocus(dialog, 'sessions');

  // An answer can arrive after the dialog closed; the flag keeps it from
  // reaching an unmounted tree, and the counter drops a slower older one.
  useEffect(() => {
    alive.current = true;
    return () => { alive.current = false; };
  }, []);

  const load = async (): Promise<boolean> => {
    const gen = ++generation.current;
    try {
      const list = await api.sessions();
      if (!alive.current || gen !== generation.current) return false;
      setSessions(list);
      setLoadError(null);
      // A fresh list means the failure it showed is no longer true.
      setActionError(null);
      return true;
    } catch (err) {
      if (!alive.current || gen !== generation.current) return false;
      setLoadError(err);
      return false;
    }
  };

  useEffect(() => { void load(); }, []);

  useEffect(() => { setActive(0); }, [query]);

  // A row that turns into an input or a confirm takes the focus, so a
  // keyboard user lands on the control the action revealed.
  useEffect(() => {
    if (renaming !== null) renameInput.current?.focus();
  }, [renaming]);
  useEffect(() => {
    if (deleting !== null) confirmButton.current?.focus();
  }, [deleting]);

  // Escape closes the editors from the App's capture listener, so the
  // transition back to null is where focus returns to the row and a stale
  // failure line goes away. A close that carries an answer keeps the line.
  // Starting the other editor in the same batch closes this one, and that
  // editor takes the focus instead, so no request is made for the row.
  useEffect(() => {
    const prev = lastRenaming.current;
    lastRenaming.current = renaming;
    if (prev === null || renaming !== null) return;
    if (deleting === null) focusRequest.current = { kind: 'rename', id: prev };
    if (!keepsError.current) setActionError(null);
    keepsError.current = false;
  }, [renaming]);
  useEffect(() => {
    const prev = lastDeleting.current;
    lastDeleting.current = deleting;
    if (prev === null || deleting !== null) return;
    const index = deletedIndex.current;
    deletedIndex.current = null;
    if (renaming === null) {
      focusRequest.current = index === null
        ? { kind: 'delete', id: prev }
        : { kind: 'deleted', id: prev, index };
    }
    if (!keepsError.current) setActionError(null);
    keepsError.current = false;
  }, [deleting]);

  const needle = query.trim().toLowerCase();
  const visible = needle === '' ? sessions : sessions.filter((s) => s.title.toLowerCase().includes(needle));
  const current = visible.length === 0 ? 0 : Math.min(active, visible.length - 1);

  // Apply the pending focus after every render, so a row that just came back
  // is in the DOM. A deleted row waits for the reload that drops it, then
  // hands focus to the row that took its place, or to the filter when empty.
  useEffect(() => {
    const request = focusRequest.current;
    if (request === null) return;
    let target: HTMLElement | null;
    if (request.kind === 'deleted') {
      if (reloadFailed.current) {
        // The failure line lands a render later; wait for its button.
        const retry = document.getElementById('session-retry');
        if (!retry) return;
        reloadFailed.current = false;
        focusRequest.current = null;
        retry.focus();
        return;
      }
      if (visible.some((s) => s.id === request.id)) return;
      const to = visible.length === 0 ? -1 : Math.min(request.index, visible.length - 1);
      focusRequest.current = null;
      if (to < 0) {
        filterInput.current?.focus();
        return;
      }
      if (to !== active) setActive(to);
      target = document.getElementById(`session-open-${visible[to].id}`);
    } else if (request.kind === 'filter') {
      focusRequest.current = null;
      filterInput.current?.focus();
      return;
    } else {
      focusRequest.current = null;
      target = document.getElementById(
        request.kind === 'rename' ? `session-rename-${request.id}` : `session-delete-${request.id}`,
      );
    }
    target?.focus();
    target?.scrollIntoView?.({ block: 'nearest' });
  });

  const focusRow = (index: number) => {
    if (visible.length === 0) return;
    const to = ((index % visible.length) + visible.length) % visible.length;
    setActive(to);
    // The list scrolls under the HUD, so bring the whole row into view.
    // jsdom has no scrollIntoView, hence the optional call.
    const el = document.getElementById(`session-open-${visible[to].id}`);
    el?.focus();
    el?.scrollIntoView?.({ block: 'nearest' });
  };

  const open = async (sess: Session) => {
    if (following.current) return;
    setActionError(null);
    if (sess.id === state.value.sessionId) {
      closeSessions();
      return;
    }
    following.current = true;
    try {
      await api.follow(sess.id);
    } catch (err) {
      if (!alive.current) return;
      setActionError(err);
      return;
    } finally {
      following.current = false;
    }
    if (!alive.current) return;
    closeSessions();
  };

  const startRename = (sess: Session) => {
    setActionError(null);
    sessionDeleteId.value = null;
    setDraft(sess.title);
    sessionRenameId.value = sess.id;
  };

  const submitRename = async (sess: Session) => {
    if (renamingBusy.current) return;
    renamingBusy.current = true;
    setActionError(null);
    let failure: unknown | null = null;
    try {
      await api.renameSession(sess.id, draft.trim());
    } catch (err) {
      failure = err;
    } finally {
      renamingBusy.current = false;
    }
    if (!alive.current) return;
    if (failure !== null) {
      setActionError(failure);
      return;
    }
    sessionRenameId.value = null;
    await load();
  };

  const askDelete = (sess: Session) => {
    setActionError(null);
    sessionRenameId.value = null;
    sessionDeleteId.value = sess.id;
  };

  const deleteSession = async (sess: Session) => {
    if (deletingBusy.current) return;
    deletingBusy.current = true;
    setActionError(null);
    const index = visible.findIndex((s) => s.id === sess.id);
    let failure: unknown | null = null;
    try {
      await api.deleteSession(sess.id);
    } catch (err) {
      failure = err;
    } finally {
      deletingBusy.current = false;
    }
    if (!alive.current) return;
    if (failure !== null) {
      // The confirm closes and the row stays: focus goes back to its Delete
      // button, and this message is newer than the close.
      keepsError.current = true;
      sessionDeleteId.value = null;
      setActionError(failure);
      return;
    }
    deletedIndex.current = Math.max(index, 0);
    reloadFailed.current = false;
    sessionDeleteId.value = null;
    const loaded = await load();
    if (!alive.current) return;
    if (!loaded) reloadFailed.current = true;
  };

  const shownAction = actionError === null ? null : describeFailure(actionError);
  const shownLoad = loadError === null ? null : describeFailure(loadError);

  return (
    <div class="sessions-layer" inert={inert}>
      <div
        class="sessions glass scanlines enter"
        role="dialog"
        aria-modal="true"
        aria-label="Sessions"
        ref={dialog}
        tabIndex={-1}
      >
        <input
          ref={filterInput}
          class="sessions__input"
          aria-label="Filter sessions"
          placeholder="Filter sessions"
          value={query}
          onInput={(e) => setQuery((e.currentTarget as HTMLInputElement).value)}
          onKeyDown={(e) => {
            if (e.key !== 'ArrowDown' || e.altKey || e.ctrlKey || e.metaKey) return;
            e.preventDefault();
            focusRow(0);
          }}
        />
        <ul class="sessions__list">
          {visible.map((sess, i) => {
            const isCurrent = sess.id === state.value.sessionId;
            const reasonId = `session-reason-${sess.id}`;
            return (
              <li key={sess.id} class="sessions__row" data-current={isCurrent ? 'true' : undefined}>
                {renaming === sess.id ? (
                  <input
                    ref={renameInput}
                    class="sessions__rename"
                    aria-label={`Rename ${sess.title}`}
                    value={draft}
                    onInput={(e) => setDraft((e.currentTarget as HTMLInputElement).value)}
                    onKeyDown={(e) => {
                      if (e.key !== 'Enter') return;
                      e.preventDefault();
                      void submitRename(sess);
                    }}
                  />
                ) : (
                  <>
                    <button
                      type="button"
                      id={`session-open-${sess.id}`}
                      class="sessions__title"
                      tabIndex={i === current ? 0 : -1}
                      aria-current={isCurrent ? 'true' : undefined}
                      aria-label={`Open ${sess.title} in the terminal`}
                      onClick={() => void open(sess)}
                      onKeyDown={(e) => {
                        if (e.altKey || e.ctrlKey || e.metaKey) return;
                        const step = e.key === 'ArrowDown' ? 1 : e.key === 'ArrowUp' ? -1 : 0;
                        if (step === 0) return;
                        e.preventDefault();
                        focusRow(i + step);
                      }}
                    >{sess.title}</button>
                    {isCurrent && <span class="sessions__tag hud-label">current</span>}
                    <button
                      type="button"
                      id={`session-rename-${sess.id}`}
                      class="sessions__action"
                      aria-label={`Rename ${sess.title}`}
                      onClick={() => startRename(sess)}
                    >Rename</button>
                    <button
                      type="button"
                      id={`session-delete-${sess.id}`}
                      class="sessions__action sessions__action--danger"
                      aria-label={`Delete ${sess.title}`}
                      disabled={isCurrent}
                      aria-describedby={isCurrent ? reasonId : undefined}
                      onClick={() => askDelete(sess)}
                    >Delete</button>
                    {isCurrent && (
                      <span id={reasonId} class="sessions__reason">
                        The terminal is showing this session
                      </span>
                    )}
                  </>
                )}
                {deleting === sess.id && (
                  <div class="sessions__confirm" role="alertdialog" aria-label={`Delete “${sess.title}”?`}>
                    <span class="sessions__confirm-text">Delete “{sess.title}”?</span>
                    <button
                      ref={confirmButton}
                      type="button"
                      class="sessions__action sessions__action--danger"
                      onClick={() => void deleteSession(sess)}
                    >Delete</button>
                    <button
                      type="button"
                      class="sessions__action"
                      onClick={() => { sessionDeleteId.value = null; }}
                    >Cancel</button>
                  </div>
                )}
              </li>
            );
          })}
          {visible.length === 0 && <li class="sessions__empty">No sessions match</li>}
        </ul>
        {shownAction && (
          <div class="sessions__status" role={shownAction.tone === 'starting' ? 'status' : 'alert'} data-tone={shownAction.tone}>
            <span>{shownAction.text}</span>
          </div>
        )}
        {shownLoad && (
          <div class="sessions__status" role={shownLoad.tone === 'starting' ? 'status' : 'alert'} data-tone={shownLoad.tone}>
            <span>{shownLoad.text}</span>
            <button type="button" id="session-retry" class="link-button" onClick={() => void load()}>Retry</button>
          </div>
        )}
      </div>
    </div>
  );
}
