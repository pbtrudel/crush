import { test as base, expect } from '@playwright/test'
import { spawn } from 'node:child_process'
import { mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

export interface Webfake {
  origin: string
  launchUrl: string
}

// PendingWebfake is the fake whose M1 permission waits for a file the spec
// writes, instead of the worker gateway's own one-second timer.
export interface PendingWebfake extends Webfake {
  pendingFile: string
}

// StreamWebfake is an M2 fake whose streamed reply waits for the file the
// spec writes.
export interface StreamWebfake extends Webfake {
  streamFile: string
}

const root = fileURLToPath(new URL('../../..', import.meta.url))
const binary = join(tmpdir(), `webfake-e2e-${process.pid}`)
const startTimeoutMs = 120_000

// A running fake is one process; stop terminates it and waits for the exit so
// a later fake cannot find the old server still holding a port.
interface RunningWebfake {
  app: Webfake
  // pendingFile is the file the fake waits for, or null when its own timer
  // opens the M1 permission or starts the M2 stream.
  pendingFile: string | null
  stop: () => Promise<void>
}

interface StartOptions {
  // scenario picks the seed; m1 is the default the shell specs use.
  scenario?: 'm1' | 'm2'
  // pendingOnFile asks the fake to hold the M1 permission until the spec
  // writes the file, so the change never races a slow page load.
  pendingOnFile?: boolean
  // streamOnFile asks the M2 fake to start streaming its reply once the spec
  // writes the file.
  streamOnFile?: boolean
}

// buildWebfake compiles the fake gateway once per test process. It has to be a
// binary and not `go run`: a signal to the go tool would leave the server
// running, and the port would leak between workers. One binary is shared by
// every fake in the process because a second `go build -o` onto a path that is
// already executing fails with "text file busy" on Linux.
let built: Promise<void> | null = null
function buildWebfake(): Promise<void> {
  if (built === null) {
    built = new Promise<void>((resolve, reject) => {
      const child = spawn('go', ['build', '-o', binary, './internal/web/cmd/webfake'], {
        cwd: root,
      })
      let output = ''
      child.stderr.on('data', (chunk: Buffer) => {
        output += chunk.toString()
      })
      child.on('error', reject)
      child.on('exit', (code) => {
        if (code === 0) {
          resolve()
          return
        }
        reject(new Error(`webfake build exited with ${code}\n${output}`))
      })
    })
    // A failed build must be retried by the next fake in this process, not
    // answered from the memo.
    built.catch(() => {
      built = null
    })
  }
  return built
}

// startWebfake runs the binary with the scenario's seed on a random loopback
// port. The gateway prints its launch URL on stdout; the URL carries the token
// in the fragment, so it is handed to the spec and never logged or put in an
// error.
async function startWebfake(options: StartOptions = {}): Promise<RunningWebfake> {
  await buildWebfake()
  const waits = options.pendingOnFile === true || options.streamOnFile === true
  const pendingDir = waits ? mkdtempSync(join(tmpdir(), 'webfake-pending-')) : null
  const pendingFile = pendingDir === null ? null : join(pendingDir, 'open')
  const env: NodeJS.ProcessEnv = {
    ...process.env,
    WEBFAKE_SCENARIO: options.scenario ?? 'm1',
    WEBFAKE_PROJECT_DIR: join(tmpdir(), 'crush-e2e-project'),
  }
  if (pendingFile !== null) {
    if (options.streamOnFile) {
      env.WEBFAKE_M2_STREAM_ON_FILE = pendingFile
    } else {
      env.WEBFAKE_M1_PENDING_ON_FILE = pendingFile
    }
  }
  const child = spawn(binary, [], {
    cwd: root,
    env,
    stdio: ['ignore', 'pipe', 'pipe'],
  })
  // The dev hint on stderr is drained and dropped: an unread pipe would block
  // the server, and its text is not part of a test failure.
  child.stderr.resume()

  const launchUrl = await new Promise<string>((resolve, reject) => {
    const timer = setTimeout(() => {
      child.kill('SIGKILL')
      reject(new Error('webfake did not print a launch URL'))
    }, startTimeoutMs)
    let buffered = ''
    child.stdout.on('data', (chunk: Buffer) => {
      buffered += chunk.toString()
      // A chunk can split the line, so it is only read once it has ended.
      if (!buffered.includes('\n')) {
        return
      }
      const line = buffered.split('\n')[0]?.trim()
      if (line && line.startsWith('http://127.0.0.1:')) {
        clearTimeout(timer)
        resolve(line)
      }
    })
    child.on('error', (cause) => {
      clearTimeout(timer)
      reject(new Error(`webfake failed to start: ${cause.message}`))
    })
    child.on('exit', (code) => {
      clearTimeout(timer)
      reject(new Error(`webfake exited with ${code}`))
    })
  })

  const stop = async () => {
    if (pendingDir !== null) {
      rmSync(pendingDir, { recursive: true, force: true })
    }
    if (child.exitCode !== null || child.signalCode !== null) {
      return
    }
    await new Promise<void>((resolve) => {
      // A server that ignores SIGTERM must not hold the run open forever.
      const force = setTimeout(() => {
        child.kill('SIGKILL')
      }, 10_000)
      child.once('exit', () => {
        clearTimeout(force)
        resolve()
      })
      child.kill('SIGTERM')
    })
  }

  return { app: { origin: launchUrl.split('/#t=')[0], launchUrl }, pendingFile, stop }
}

// The binary is removed when the process ends, not when the first fake stops:
// another fake may still be running the shared file.
process.once('exit', () => {
  rmSync(binary, { force: true })
})

export const test = base.extend<
  { freshApp: PendingWebfake; streamApp: StreamWebfake },
  { app: Webfake; chatApp: Webfake }
>({
  // app is the worker's gateway, shared by every spec that just needs a seeded
  // shell. It is already old by the second spec: webfake publishes the M1
  // permission one second after start.
  app: [
    async ({}, use) => {
      const running = await startWebfake()
      await use(running.app)
      await running.stop()
    },
    // A cold `go build` of the fake on a CI runner can take minutes, and this
    // fixture pays for it on the first spec.
    { scope: 'worker', timeout: 300_000 },
  ],
  // chatApp is the worker's M2 gateway, for the specs that only read the
  // seeded chat.
  chatApp: [
    async ({}, use) => {
      const running = await startWebfake({ scenario: 'm2' })
      await use(running.app)
      await running.stop()
    },
    { scope: 'worker', timeout: 300_000 },
  ],
  // streamApp is a fresh M2 gateway per test: its reply streams once, when
  // the spec writes streamFile.
  streamApp: async ({}, use) => {
    const running = await startWebfake({ scenario: 'm2', streamOnFile: true })
    const streamFile = running.pendingFile
    if (streamFile === null) {
      await running.stop()
      throw new Error('webfake did not get a stream file')
    }
    await use({ ...running.app, streamFile })
    await running.stop()
  },
  // freshApp is a gateway started for one test, for a spec that has to see a
  // change happen on a page that is already open. Its M1 permission waits for
  // the file the spec writes once its page has loaded.
  freshApp: async ({}, use) => {
    const running = await startWebfake({ pendingOnFile: true })
    const pendingFile = running.pendingFile
    if (pendingFile === null) {
      await running.stop()
      throw new Error('webfake did not get a pending file')
    }
    await use({ ...running.app, pendingFile })
    await running.stop()
  },
})

export { expect }
