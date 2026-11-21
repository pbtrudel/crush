import { test as base } from '@playwright/test';
import { execFileSync, spawn, type ChildProcess } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import readline from 'node:readline';

const repoRoot = fileURLToPath(new URL('../..', import.meta.url));

// The launch line carries the session token. It is handed to the page and
// never logged, so every diagnostic below is redacted first.
const LAUNCH_LINE = /^http:\/\/127\.0\.0\.1:\d+\/#t=\S+$/;
const LAUNCH_TIMEOUT_MS = 60_000;
const EXIT_TIMEOUT_MS = 5_000;

function redact(text: string): string {
  return text.replace(/#t=\S+/g, '#t=<redacted>').trim();
}

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

export const test = base.extend<{}, { gateway: { launch: string; origin: string } }>({
  gateway: [async ({}, use) => {
    // `go run` does not forward SIGTERM to the program it starts, which
    // would orphan webfake. Build once and run the binary directly.
    const dir = mkdtempSync(path.join(tmpdir(), 'webfake-'));
    const bin = path.join(dir, 'webfake');
    let proc: ChildProcess | undefined;
    try {
      execFileSync('go', ['build', '-o', bin, './internal/web/cmd/webfake'], { cwd: repoRoot, stdio: 'inherit' });
      // stderr is piped rather than inherited: webfake repeats the token in
      // its dev-server hint and test output must never carry it.
      proc = spawn(bin, [], { cwd: repoRoot, stdio: ['ignore', 'pipe', 'pipe'] });
      let stderrText = '';
      proc.stderr!.on('data', (chunk: Buffer) => {
        stderrText = (stderrText + chunk.toString()).slice(-4096);
      });
      const launch = await waitForLaunch(proc, () => stderrText);
      await use({ launch, origin: launch.split('/#')[0] });
    } finally {
      if (proc) await stop(proc);
      rmSync(dir, { recursive: true, force: true });
    }
  }, { scope: 'worker', timeout: 180_000 }],
});

export { expect } from '@playwright/test';
