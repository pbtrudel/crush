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

// Every gateway runs the same binary: `go run` does not forward SIGTERM to
// the program it starts, which would orphan webfake, so the fixture builds
// once per worker and launches the binary directly. playwright.config.ts
// runs one worker, so that is once per run, and a shard is a run.
const webfakeTests = base.extend<{}, { webfakeBinary: string }>({
  webfakeBinary: [
    async ({}, use) => {
      const dir = mkdtempSync(path.join(tmpdir(), 'webfake-'));
      const bin = path.join(dir, 'webfake');
      try {
        // Every go command in this repo shares one cache, and a fresh shell
        // has no GOCACHE: set the shared one unless the caller already did.
        const goEnv = { ...process.env, GOCACHE: process.env.GOCACHE ?? '/tmp/crush-go-build' };
        execFileSync('go', ['build', '-o', bin, './internal/web/cmd/webfake'], { cwd: repoRoot, env: goEnv, stdio: 'inherit' });
        await use(bin);
      } finally {
        rmSync(dir, { recursive: true, force: true });
      }
    },
    { scope: 'worker', timeout: 180_000 },
  ],
});

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

// The HUD labels the tab with the basename of the gateway's working
// directory, and the committed baselines were captured in a checkout named
// web-mode-m2. A fixed root keeps every run independent of where this repo
// is checked out: webfake creates it empty, and the sessions, files and
// diffs a test sees come from the seed, not from the disk.
const PROJECT_DIR = path.join(tmpdir(), 'web-mode-m2');

async function launchGateway(bin: string, env: NodeJS.ProcessEnv, use: (gateway: Gateway) => Promise<void>): Promise<void> {
  let proc: ChildProcess | undefined;
  try {
    // stderr is piped rather than inherited: webfake repeats the token in
    // its dev-server hint and test output must never carry it.
    proc = spawn(bin, [], {
      cwd: repoRoot,
      env: { ...process.env, WEBFAKE_PROJECT_DIR: PROJECT_DIR, ...env },
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    const stderr = stderrBuffer();
    proc.stderr!.on('data', (chunk: Buffer) => stderr.push(chunk.toString()));
    const launch = await waitForLaunch(proc, () => stderr.text());
    await use({ launch, origin: launch.split('/#')[0] });
  } finally {
    if (proc) await stop(proc);
  }
}

export const test = webfakeTests.extend<{}, { gateway: Gateway }>({
  gateway: [async ({ webfakeBinary }, use) => launchGateway(webfakeBinary, {}, use), { scope: 'worker', timeout: 180_000 }],
});

// Visual tests get their own webfake: the worker's shared session keeps every
// message an earlier test sent, and the log and token counts in a screenshot
// must not depend on which tests ran first.
export const visualTest = webfakeTests.extend<{ gateway: Gateway }>({
  gateway: async ({ webfakeBinary }, use) => launchGateway(webfakeBinary, { WEBFAKE_CLOCK: '2026-11-22T12:00:00Z' }, use),
});

// A test that writes history (an edited file gets a new version) gets its own
// webfake too: the worker's shared session carries every version, and the
// smoke specs assert on the seeded three.
export const freshTest = webfakeTests.extend<{ gateway: Gateway }>({
  gateway: async ({ webfakeBinary }, use) => launchGateway(webfakeBinary, {}, use),
});

// The M3 surfaces live in their own scenario, so the default seed and every
// baseline taken against it stay exactly as they were.
const M3 = { WEBFAKE_SCENARIO: 'm3' };

export const m3Test = webfakeTests.extend<{}, { gateway: Gateway }>({
  gateway: [async ({ webfakeBinary }, use) => launchGateway(webfakeBinary, M3, use), { scope: 'worker', timeout: 180_000 }],
});

export const m3FreshTest = webfakeTests.extend<{ gateway: Gateway }>({
  gateway: async ({ webfakeBinary }, use) => launchGateway(webfakeBinary, M3, use),
});

export const m3VisualTest = webfakeTests.extend<{ gateway: Gateway }>({
  gateway: async ({ webfakeBinary }, use) => launchGateway(webfakeBinary, { ...M3, WEBFAKE_CLOCK: '2026-11-22T12:00:00Z' }, use),
});

// The M4 surfaces (secure prompts, provider sign-in and the AWS SSO card)
// have their own scenario on top of the M3 seeds, so every earlier scenario
// and baseline stays exactly as it was.
const M4 = { WEBFAKE_SCENARIO: 'm4' };

// The read-only M4 tests share one gateway: nothing they do saves a key,
// finishes a sign-in, leaves a flow running or answers a prompt.
export const m4Test = webfakeTests.extend<{}, { gateway: Gateway }>({
  gateway: [async ({ webfakeBinary }, use) => launchGateway(webfakeBinary, M4, use), { scope: 'worker', timeout: 180_000 }],
});

// An M4 test that changes the gateway's state, or takes a baseline, gets its
// own webfake.
export const m4FreshTest = webfakeTests.extend<{ gateway: Gateway }>({
  gateway: async ({ webfakeBinary }, use) => launchGateway(webfakeBinary, M4, use),
});

export const m4VisualTest = webfakeTests.extend<{ gateway: Gateway }>({
  gateway: async ({ webfakeBinary }, use) => launchGateway(webfakeBinary, { ...M4, WEBFAKE_CLOCK: '2026-11-22T12:00:00Z' }, use),
});

// Each secure prompt test scripts one prompt kind on a gateway of its own:
// the prompt arrives shortly after the gateway starts.
export const promptTest = webfakeTests.extend<{ gateway: Gateway }>({
  gateway: async ({ webfakeBinary }, use) => launchGateway(webfakeBinary, { ...M4, WEBFAKE_SECURE_PROMPT: 'pinentry' }, use),
});

// The terminal answers the prompt itself five seconds after the gateway
// starts, long after the prompt has reached the tab.
export const promptElsewhereTest = webfakeTests.extend<{ gateway: Gateway }>({
  gateway: async ({ webfakeBinary }, use) => launchGateway(webfakeBinary, { ...M4, WEBFAKE_SECURE_PROMPT: 'pinentry', WEBFAKE_PROMPT_ELSEWHERE_MS: '5000' }, use),
});

// promptOnFileTest scripts one prompt kind on a gateway of its own, and the
// prompt waits for a file the test writes instead of a clock from the
// gateway's start. A test that writes the file once the tab is listening
// sees a stream frame; one that raced a slow page load would only find the
// prompt in the snapshot, which carries no notice of its arrival.
function promptOnFileTest(kind: string) {
  return webfakeTests.extend<{ gateway: Gateway; promptTrigger: string }>({
    promptTrigger: async ({}, use) => {
      const dir = mkdtempSync(path.join(tmpdir(), 'prompt-trigger-'));
      try {
        await use(path.join(dir, 'open'));
      } finally {
        rmSync(dir, { recursive: true, force: true });
      }
    },
    gateway: async ({ webfakeBinary, promptTrigger }, use) =>
      launchGateway(webfakeBinary, { ...M4, WEBFAKE_SECURE_PROMPT: kind, WEBFAKE_PROMPT_ON_FILE: promptTrigger }, use),
  });
}

// The prompt waits for a file the test writes, so it lands once the test has
// put the focus where the prompt interrupts it rather than on a clock from
// the gateway's start. The panel is the layer the prompt closes over.
export const promptLateTest = promptOnFileTest('pinentry');

// A touch prompt is only announced when its stream frame arrives, so it too
// waits for the test's file instead of a clock that a slow page load can
// outrun.
export const touchLateTest = promptOnFileTest('touch');

export const confirmTest = webfakeTests.extend<{ gateway: Gateway }>({
  gateway: async ({ webfakeBinary }, use) => launchGateway(webfakeBinary, { ...M4, WEBFAKE_SECURE_PROMPT: 'confirm' }, use),
});

export const promptVisualTest = webfakeTests.extend<{ gateway: Gateway }>({
  gateway: async ({ webfakeBinary }, use) => launchGateway(webfakeBinary, { ...M4, WEBFAKE_SECURE_PROMPT: 'pinentry', WEBFAKE_CLOCK: '2026-11-22T12:00:00Z' }, use),
});

// A device flow finishes on its own three seconds after it starts, the way a
// user approving it in the provider's page would.
export const signinDoneTest = webfakeTests.extend<{ gateway: Gateway }>({
  gateway: async ({ webfakeBinary }, use) => launchGateway(webfakeBinary, { ...M4, WEBFAKE_SIGNIN_MS: '3000' }, use),
});

// One AWS SSO refresh: the command first, then its verification URL.
export const awsTest = webfakeTests.extend<{ gateway: Gateway }>({
  gateway: async ({ webfakeBinary }, use) => launchGateway(webfakeBinary, { ...M4, WEBFAKE_AWS_SSO: '1' }, use),
});

// A starting agent answers 503 agent_starting on every route that needs it.
export const startingTest = webfakeTests.extend<{ gateway: Gateway }>({
  gateway: async ({ webfakeBinary }, use) => launchGateway(webfakeBinary, { ...M3, WEBFAKE_AGENT_STARTING: '1' }, use),
});

// A ready plan waits for the browser, the way the terminal leaves it in plan
// mode.
export const planTest = webfakeTests.extend<{ gateway: Gateway }>({
  gateway: async ({ webfakeBinary }, use) => launchGateway(webfakeBinary, { ...M3, WEBFAKE_PLAN_READY: '1' }, use),
});

// seedDisplay writes one of the tab's display settings to a fresh gateway
// before a page loads, so a run can start on it instead of clicking through
// the dialog. It uses the same allowlisted route the dialog uses, with the
// launch token, and only a fresh gateway keeps the write from later tests.
export async function seedDisplay(page: Page, gateway: Gateway, key: string, value: unknown): Promise<void> {
  const token = gateway.launch.split('#t=')[1] ?? '';
  const rsp = await page.request.post(gateway.origin + '/api/config/set', {
    headers: { Authorization: `Bearer ${token}`, Origin: gateway.origin },
    data: { key, value },
  });
  expect(rsp.status()).toBe(200);
}

// axeScan is the M1 and M2 accessibility gate: serious and critical
// violations fail.
export async function axeScan(page: Page): Promise<void> {
  const { violations } = await new AxeBuilder({ page }).analyze();
  expect(violations.filter((v) => v.impact === 'serious' || v.impact === 'critical')).toEqual([]);
}

export { expect };

// The M4b surfaces (the display settings dialog) extend the M4 scenario with
// the tab's display block, so every earlier scenario and baseline stays as it
// was.
const M4B = { WEBFAKE_SCENARIO: 'm4b' };

// The read-only M4b tests share one gateway: nothing they do writes the
// display settings or detaches.
export const m4bTest = webfakeTests.extend<{}, { gateway: Gateway }>({
  gateway: [async ({ webfakeBinary }, use) => launchGateway(webfakeBinary, M4B, use), { scope: 'worker', timeout: 180_000 }],
});

// An M4b test that writes the display settings or stops web mode gets its own
// webfake: a settings write changes the gateway's config for later tests.
export const m4bFreshTest = webfakeTests.extend<{ gateway: Gateway }>({
  gateway: async ({ webfakeBinary }, use) => launchGateway(webfakeBinary, M4B, use),
});

// The m4b baselines share the M4 clock. The theme is a launch variable, so a
// baseline that needs the solid theme asks for it with test.use({ theme:
// 'solid' }) instead of a fixture of its own.
export const m4bVisualTest = webfakeTests.extend<{ gateway: Gateway; theme: string }>({
  theme: ['holodeck', { option: true }],
  gateway: async ({ webfakeBinary, theme }, use) =>
    launchGateway(webfakeBinary, { ...M4B, WEBFAKE_CLOCK: '2026-11-22T12:00:00Z', WEBFAKE_THEME: theme }, use),
});

// The AWS SSO card has to stay above the chronoline, so the case needs both
// surfaces on one gateway.
export const m4bAwsTest = webfakeTests.extend<{ gateway: Gateway }>({
  gateway: async ({ webfakeBinary }, use) => launchGateway(webfakeBinary, { ...M4B, WEBFAKE_AWS_SSO: '1' }, use),
});

// A long model name for the HUD truncation case. It is a launch variable so
// the shared m4b gateway keeps its usual name.
export const LONG_MODEL_NAME = 'An extremely long model name that will not fit in the HUD at all';
export const m4bLongModelTest = webfakeTests.extend<{ gateway: Gateway }>({
  gateway: async ({ webfakeBinary }, use) =>
    launchGateway(webfakeBinary, { ...M4B, WEBFAKE_MODEL_NAME: LONG_MODEL_NAME }, use),
});

// The layout case pads the session with hundreds of tool calls, so the
// chronoline's single row is far wider than the window. It is a launch
// variable, so the default seed and every baseline stay as they were.
export const MANY_TOOL_CALLS = 180;
export const overflowTest = webfakeTests.extend<{ gateway: Gateway }>({
  gateway: async ({ webfakeBinary }, use) =>
    launchGateway(webfakeBinary, { WEBFAKE_TOOL_CALLS: String(MANY_TOOL_CALLS) }, use),
});

// The log layout case needs one tool path far wider than a log column. It
// rides the default seeds behind an opt-in variable, so every baseline
// keeps its seeded log.
const LONG_PATH = { WEBFAKE_LONG_PATH: '1' };
export const longPathTest = webfakeTests.extend<{ gateway: Gateway }>({
  gateway: async ({ webfakeBinary }, use) => launchGateway(webfakeBinary, LONG_PATH, use),
});
