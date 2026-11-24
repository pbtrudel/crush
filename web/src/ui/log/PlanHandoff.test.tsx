import { afterEach, describe, expect, it, vi } from 'vitest';
import { render } from 'preact';
import { act } from 'preact/test-utils';
import { ApiError, api } from '../../api/http';
import { STARTING_TEXT } from '../../state/failure';
import { initialState, needsAttention, needsYou } from '../../state/reducer';
import { actions, state } from '../../state/store';
import { PlanHandoff } from './PlanHandoff';

const root = document.createElement('div');
document.body.append(root);

function seed(over: Partial<typeof state.value> = {}) {
  state.value = {
    ...initialState(), conn: 'live', sessionId: 's1', planReady: 's1', inputMode: 'plan', ...over,
  };
}

async function mount() {
  await act(() => { render(<PlanHandoff />, root); });
}

async function update(next: typeof state.value) {
  await act(() => { state.value = next; });
}

async function flush() {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

const card = () => root.querySelector<HTMLElement>('.plan-handoff');
const action = (name: string) => root.querySelector<HTMLButtonElement>(`button[data-action="${name}"]`);

afterEach(async () => {
  await act(() => { render(null, root); });
  vi.restoreAllMocks();
});

describe('PlanHandoff', () => {
  it('shows the card only for the pending session in plan mode', async () => {
    seed();
    await mount();
    expect(card()!.textContent).toContain('The plan is ready');
    expect(card()!.textContent).toContain('Ready to start coding?');

    await update({ ...state.value, sessionId: 's2' });
    expect(card()).toBeNull();

    await update({ ...state.value, sessionId: 's1', inputMode: 'code' });
    expect(card()).toBeNull();
  });

  it('starts coding and stays busy until the plan clears', async () => {
    seed();
    const handoff = vi.spyOn(api, 'planHandoff').mockResolvedValue(undefined);
    await mount();

    await act(async () => { action('code')!.click(); });
    await flush();

    expect(handoff).toHaveBeenCalledWith('s1', 'code');
    expect(card()).not.toBeNull();
    expect(action('code')!.getAttribute('aria-disabled')).toBe('true');
    expect(action('code_yolo')!.getAttribute('aria-disabled')).toBe('true');
    // A focused control is never disabled; aria-disabled carries the state.
    expect(action('code')!.matches(':disabled')).toBe(false);

    await update({ ...state.value, planReady: '' });
    expect(card()).toBeNull();
  });

  it('starts coding in YOLO mode with its hint', async () => {
    seed();
    const handoff = vi.spyOn(api, 'planHandoff').mockResolvedValue(undefined);
    await mount();
    expect(action('code_yolo')!.textContent).toBe('Code with YOLO');
    expect(action('code_yolo')!.getAttribute('aria-describedby')).toBe('plan-yolo-hint');
    expect(root.querySelector('#plan-yolo-hint')!.textContent).toBe('YOLO mode skips permission prompts');

    await act(async () => { action('code_yolo')!.click(); });
    await flush();

    expect(handoff).toHaveBeenCalledWith('s1', 'code_yolo');
  });

  it('revises the plan through the normal prompt path', async () => {
    seed();
    const send = vi.spyOn(actions, 'sendPrompt').mockResolvedValue(null);
    await mount();

    await act(() => { action('revise')!.click(); });
    const box = root.querySelector<HTMLTextAreaElement>('#plan-feedback')!;
    expect(root.querySelector('label[for="plan-feedback"]')!.textContent).toBe('What should change?');
    await act(() => {
      box.value = 'make the diff smaller';
      box.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await act(async () => {
      root.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    });
    await flush();

    expect(send).toHaveBeenCalledWith('make the diff smaller', []);
    expect(root.querySelector('#plan-feedback')).toBeNull();
    expect(document.activeElement).toBe(action('revise'));
  });

  it('shows a starting state with a retry that repeats the action', async () => {
    seed();
    const handoff = vi.spyOn(api, 'planHandoff')
      .mockRejectedValueOnce(new ApiError(503, 'agent_starting', 'not ready'))
      .mockResolvedValueOnce(undefined);
    await mount();

    await act(async () => { action('code')!.click(); });
    await flush();

    const failure = root.querySelector<HTMLElement>('.plan-handoff__error')!;
    expect(failure.getAttribute('role')).toBe('status');
    expect(failure.getAttribute('data-tone')).toBe('starting');
    expect(failure.textContent).toContain(STARTING_TEXT);

    await act(async () => { action('retry')!.click(); });
    await flush();
    expect(handoff).toHaveBeenCalledTimes(2);
    expect(handoff).toHaveBeenLastCalledWith('s1', 'code');
  });

  it('shows a conflict as an alert without a retry', async () => {
    seed();
    vi.spyOn(api, 'planHandoff').mockRejectedValue(new ApiError(409, 'plan_not_ready', 'no ready plan to hand off'));
    await mount();

    await act(async () => { action('code')!.click(); });
    await flush();

    const failure = root.querySelector<HTMLElement>('.plan-handoff__error')!;
    expect(failure.getAttribute('role')).toBe('alert');
    expect(failure.textContent).toContain('no ready plan to hand off');
    expect(action('retry')).toBeNull();
  });

  it('starts a new plan unfettered after the old one clears', async () => {
    seed();
    vi.spyOn(api, 'planHandoff').mockResolvedValue(undefined);
    await mount();
    await act(async () => { action('code')!.click(); });
    await flush();
    expect(action('code')!.getAttribute('aria-disabled')).toBe('true');

    await update({ ...state.value, planReady: '' });
    await update({ ...state.value, planReady: 's1' });
    expect(action('code')!.getAttribute('aria-disabled')).toBeNull();
  });

  it('ignores an answer that lands after its plan cleared', async () => {
    seed();
    let reject!: (err: unknown) => void;
    const pending = new Promise<void>((_, rej) => { reject = rej; });
    vi.spyOn(api, 'planHandoff').mockReturnValueOnce(pending);
    await mount();
    await act(async () => { action('code')!.click(); });
    await flush();

    // The plan clears while the request is in flight, then a new one waits.
    await update({ ...state.value, planReady: '' });
    await update({ ...state.value, planReady: 's1' });
    await act(async () => { reject(new ApiError(503, 'agent_starting', 'not ready')); });
    await flush();

    expect(root.querySelector('.plan-handoff__error')).toBeNull();
    expect(action('code')!.getAttribute('aria-disabled')).toBeNull();
  });

  it('moves focus to the deck when the card leaves under it', async () => {
    const deck = document.createElement('textarea');
    deck.id = 'deck-input';
    document.body.append(deck);
    try {
      seed();
      await mount();
      action('code')!.focus();
      expect(document.activeElement).toBe(action('code'));

      await update({ ...state.value, planReady: '' });
      expect(card()).toBeNull();
      expect(document.activeElement).toBe(deck);
    } finally {
      deck.remove();
    }
  });

  it('marks the page as needing you while the plan waits', async () => {
    seed();
    expect(needsAttention(state.value)).toBe(false);
    expect(needsYou(state.value)).toBe(true);
  });
});
