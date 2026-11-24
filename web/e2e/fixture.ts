import AxeBuilder from '@axe-core/playwright';
import { test as base, expect, type Page } from '@playwright/test';
import { execFileSync, spawn, type ChildProcess } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import readline from 'node:readline';
import { redact, stderrBuffer } from './stderr';

const repoRoot = fileURLToPath(new URL('../..', import.meta.url));

// The launch line carries the session token. It is handed to the page and
// never logged, so every diagnostic below is redacted first.
const LAUNCH_LINE = /^http:\/\/127\.0\.0\.1:\d+\/#t=\S+$/;
const LAUNCH_TIMEOUT_MS = 60_000;
const EXIT_TIMEOUT_MS = 5_000;

// waitForLaunch resolves with the first stdout line webfake prints and
// rejects when the process goes away before printing one.
function waitForLaunch(proc: ChildProcess, stderrText: () => string): Promise<string> {
  return new Promise((resolve, reject) => {
    const rl = readline.createInterface({ input: proc.stdout!, crlfDelay: Infinity });
    const timer = setTimeout(() => {
      cleanup();
      reject(new Error(`webfake did not print a launch URL within ${LAUNCH_TIMEOUT_MS / 1000}s`));
    }, LAUNCH_TIMEOUT_MS);

    function cleanup() {
      clearTimeout(timer);
      rl.off('line', onLine);
      proc.off('close', onClose);
      proc.off('error', onError);
      rl.close();
    }

    function onLine(line: string) {
      const launch = line.trim();
      if (!LAUNCH_LINE.test(launch)) {
        // A malformed first line is usually a startup message; do not echo
        // it, it may carry the token.
        cleanup();
        reject(new Error('webfake printed an unexpected first line'));
        return;
      }
      cleanup();
      resolve(launch);
    }

    // 'close' fires after the stdio streams are drained, so a URL already
    // written to the pipe is delivered as a line before this runs.
    function onClose(code: number | null, signal: NodeJS.Signals | null) {
      cleanup();
      const detail = redact(stderrText());
      reject(new Error(
        `webfake exited before printing a launch URL (code ${String(code)}, signal ${String(signal)})` +
        (detail ? `: ${detail}` : ''),
      ));
    }

    function onError(err: Error) {
      cleanup();
      reject(err);
    }

    rl.on('line', onLine);
    proc.once('close', onClose);
    proc.once('error', onError);
  });
}

function waitForExit(proc: ChildProcess, timeout: number): Promise<boolean> {
  return new Promise((resolve) => {
    if (proc.exitCode !== null || proc.signalCode !== null) {
      resolve(true);
      return;
    }
    const timer = setTimeout(() => resolve(false), timeout);
    proc.once('exit', () => {
      clearTimeout(timer);
      resolve(true);
    });
  });
}

// stop asks webfake to shut down, and only escalates to SIGKILL when it does
// not exit in time. The gateway closes its listener and serves Shutdown on
// SIGTERM.
async function stop(proc: ChildProcess): Promise<void> {
  proc.kill('SIGTERM');
  if (await waitForExit(proc, EXIT_TIMEOUT_MS)) return;
  proc.kill('SIGKILL');
  await waitForExit(proc, EXIT_TIMEOUT_MS);
}

export interface Gateway {
  launch: string;
  origin: string;
}

async function launchGateway(env: NodeJS.ProcessEnv, use: (gateway: Gateway) => Promise<void>): Promise<void> {
  // `go run` does not forward SIGTERM to the program it starts, which
  // would orphan webfake. Build once and run the binary directly.
  const dir = mkdtempSync(path.join(tmpdir(), 'webfake-'));
  const bin = path.join(dir, 'webfake');
  let proc: ChildProcess | undefined;
  try {
    // Every go command in this repo shares one cache, and a fresh shell has
    // no GOCACHE: set the shared one unless the caller already did.
    const goEnv = { ...process.env, GOCACHE: process.env.GOCACHE ?? '/tmp/crush-go-build' };
    execFileSync('go', ['build', '-o', bin, './internal/web/cmd/webfake'], { cwd: repoRoot, env: goEnv, stdio: 'inherit' });
    // stderr is piped rather than inherited: webfake repeats the token in
    // its dev-server hint and test output must never carry it.
    proc = spawn(bin, [], { cwd: repoRoot, env: { ...process.env, ...env }, stdio: ['ignore', 'pipe', 'pipe'] });
    const stderr = stderrBuffer();
    proc.stderr!.on('data', (chunk: Buffer) => stderr.push(chunk.toString()));
    const launch = await waitForLaunch(proc, () => stderr.text());
    await use({ launch, origin: launch.split('/#')[0] });
  } finally {
    if (proc) await stop(proc);
    rmSync(dir, { recursive: true, force: true });
  }
}

export const test = base.extend<{}, { gateway: Gateway }>({
  gateway: [async ({}, use) => launchGateway({}, use), { scope: 'worker', timeout: 180_000 }],
});

// Visual tests get their own webfake: the worker's shared session keeps every
// message an earlier test sent, and the log and token counts in a screenshot
// must not depend on which tests ran first.
export const visualTest = base.extend<{ gateway: Gateway }>({
  gateway: async ({}, use) => launchGateway({ WEBFAKE_CLOCK: '2026-11-22T12:00:00Z' }, use),
});

// A test that writes history (an edited file gets a new version) gets its own
// webfake too: the worker's shared session carries every version, and the
// smoke specs assert on the seeded three.
export const freshTest = base.extend<{ gateway: Gateway }>({
  gateway: async ({}, use) => launchGateway({}, use),
});

// The M3 surfaces live in their own scenario, so the default seed and every
// baseline taken against it stay exactly as they were.
const M3 = { WEBFAKE_SCENARIO: 'm3' };

export const m3Test = base.extend<{}, { gateway: Gateway }>({
  gateway: [async ({}, use) => launchGateway(M3, use), { scope: 'worker', timeout: 180_000 }],
});

export const m3FreshTest = base.extend<{ gateway: Gateway }>({
  gateway: async ({}, use) => launchGateway(M3, use),
});

export const m3VisualTest = base.extend<{ gateway: Gateway }>({
  gateway: async ({}, use) => launchGateway({ ...M3, WEBFAKE_CLOCK: '2026-11-22T12:00:00Z' }, use),
});

// The M4 surfaces (secure prompts, provider sign-in and the AWS SSO card)
// have their own scenario on top of the M3 seeds, so every earlier scenario
// and baseline stays exactly as it was.
const M4 = { WEBFAKE_SCENARIO: 'm4' };

// The read-only M4 tests share one gateway: nothing they do saves a key,
// finishes a sign-in, leaves a flow running or answers a prompt.
export const m4Test = base.extend<{}, { gateway: Gateway }>({
  gateway: [async ({}, use) => launchGateway(M4, use), { scope: 'worker', timeout: 180_000 }],
});

// An M4 test that changes the gateway's state, or takes a baseline, gets its
// own webfake.
export const m4FreshTest = base.extend<{ gateway: Gateway }>({
  gateway: async ({}, use) => launchGateway(M4, use),
});

export const m4VisualTest = base.extend<{ gateway: Gateway }>({
  gateway: async ({}, use) => launchGateway({ ...M4, WEBFAKE_CLOCK: '2026-11-22T12:00:00Z' }, use),
});

// Each secure prompt test scripts one prompt kind on a gateway of its own:
// the prompt arrives shortly after the gateway starts.
export const promptTest = base.extend<{ gateway: Gateway }>({
  gateway: async ({}, use) => launchGateway({ ...M4, WEBFAKE_SECURE_PROMPT: 'pinentry' }, use),
});

// The terminal answers the prompt itself once the delay has passed.
export const promptElsewhereTest = base.extend<{ gateway: Gateway }>({
  gateway: async ({}, use) => launchGateway({ ...M4, WEBFAKE_SECURE_PROMPT: 'pinentry', WEBFAKE_PROMPT_ELSEWHERE_MS: '1500' }, use),
});

export const confirmTest = base.extend<{ gateway: Gateway }>({
  gateway: async ({}, use) => launchGateway({ ...M4, WEBFAKE_SECURE_PROMPT: 'confirm' }, use),
});

export const touchTest = base.extend<{ gateway: Gateway }>({
  gateway: async ({}, use) => launchGateway({ ...M4, WEBFAKE_SECURE_PROMPT: 'touch' }, use),
});

export const promptVisualTest = base.extend<{ gateway: Gateway }>({
  gateway: async ({}, use) => launchGateway({ ...M4, WEBFAKE_SECURE_PROMPT: 'pinentry', WEBFAKE_CLOCK: '2026-11-22T12:00:00Z' }, use),
});

// A device flow finishes on its own after 800 ms, the way a user approving
// it in the provider's page would.
export const signinDoneTest = base.extend<{ gateway: Gateway }>({
  gateway: async ({}, use) => launchGateway({ ...M4, WEBFAKE_SIGNIN_MS: '800' }, use),
});

// One AWS SSO refresh: the command first, then its verification URL.
export const awsTest = base.extend<{ gateway: Gateway }>({
  gateway: async ({}, use) => launchGateway({ ...M4, WEBFAKE_AWS_SSO: '1' }, use),
});

// A starting agent answers 503 agent_starting on every route that needs it.
export const startingTest = base.extend<{ gateway: Gateway }>({
  gateway: async ({}, use) => launchGateway({ ...M3, WEBFAKE_AGENT_STARTING: '1' }, use),
});

// A ready plan waits for the browser, the way the terminal leaves it in plan
// mode.
export const planTest = base.extend<{ gateway: Gateway }>({
  gateway: async ({}, use) => launchGateway({ ...M3, WEBFAKE_PLAN_READY: '1' }, use),
});

// axeScan is the M1 and M2 accessibility gate: serious and critical
// violations fail.
export async function axeScan(page: Page): Promise<void> {
  const { violations } = await new AxeBuilder({ page }).analyze();
  expect(violations.filter((v) => v.impact === 'serious' || v.impact === 'critical')).toEqual([]);
}

export { expect };
