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

function SessionsDialog({ inert }: { inert: boolean }) {
  const [sessions, setSessions] = useState<Session[]>([]);
  const [query, setQuery] = useState('');
  const [draft, setDraft] = useState('');
  const [active, setActive] = useState(0);
  const [loadError, setLoadError] = useState<unknown | null>(null);
  const [actionError, setActionError] = useState<unknown | null>(null);
  const dialog = useRef<HTMLDivElement>(null);
  const renameInput = useRef<HTMLInputElement>(null);
  const confirmButton = useRef<HTMLButtonElement>(null);
  const alive = useRef(true);
  const generation = useRef(0);
  const renaming = sessionRenameId.value;
  const deleting = sessionDeleteId.value;

  useDialogFocus(dialog, 'sessions');

  // An answer can arrive after the dialog closed; the flag keeps it from
  // reaching an unmounted tree, and the counter drops a slower older one.
  useEffect(() => {
    alive.current = true;
    return () => { alive.current = false; };
  }, []);

  const load = async () => {
    const gen = ++generation.current;
    try {
      const list = await api.sessions();
      if (!alive.current || gen !== generation.current) return;
      setSessions(list);
      setLoadError(null);
    } catch (err) {
      if (!alive.current || gen !== generation.current) return;
      setLoadError(err);
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

  const needle = query.trim().toLowerCase();
  const visible = needle === '' ? sessions : sessions.filter((s) => s.title.toLowerCase().includes(needle));
  const current = visible.length === 0 ? 0 : Math.min(active, visible.length - 1);

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
    setActionError(null);
    if (sess.id === state.value.sessionId) {
      closeSessions();
      return;
    }
    try {
      await api.follow(sess.id);
      if (!alive.current) return;
      closeSessions();
    } catch (err) {
      if (!alive.current) return;
      setActionError(err);
    }
  };

  const startRename = (sess: Session) => {
    setActionError(null);
    sessionDeleteId.value = null;
    setDraft(sess.title);
    sessionRenameId.value = sess.id;
  };

  const submitRename = async (sess: Session) => {
    setActionError(null);
    try {
      await api.renameSession(sess.id, draft.trim());
      if (!alive.current) return;
      sessionRenameId.value = null;
      await load();
    } catch (err) {
      if (!alive.current) return;
      setActionError(err);
    }
  };

  const askDelete = (sess: Session) => {
    setActionError(null);
    sessionRenameId.value = null;
    sessionDeleteId.value = sess.id;
  };

  const deleteSession = async (sess: Session) => {
    setActionError(null);
    try {
      await api.deleteSession(sess.id);
      if (!alive.current) return;
      sessionDeleteId.value = null;
      await load();
    } catch (err) {
      if (!alive.current) return;
      sessionDeleteId.value = null;
      setActionError(err);
    }
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
                      class="sessions__action"
                      aria-label={`Rename ${sess.title}`}
                      onClick={() => startRename(sess)}
                    >Rename</button>
                    <button
                      type="button"
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
            <button type="button" class="link-button" onClick={() => void load()}>Retry</button>
          </div>
        )}
      </div>
    </div>
  );
}
