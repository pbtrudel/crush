# Web mode

Web mode serves a running Crush session to a browser tab. The terminal stays
open next to the tab, and either side can drive the session: prompts,
permission requests, questions, the input mode and the files are shared.

## Starting web mode

To start with a tab:

```sh
crush --web
```

To make it the default, set the option in your config:

```sh
# crushrc
option web true
```

```json
{ "options": { "web": true } }
```

An explicit `--web` wins over the option, so `crush --web=false` turns off a
default you set.

In a running session, type `/web`. Crush starts the gateway if it is not
running, shows the link in a notification, copies it to the clipboard, and
opens a tab. The notification stays for 30 seconds, so a slow or missing
browser never hides the link.

Each Crush process serves its own gateway on `127.0.0.1` at its own random
port, so two terminals give two independent tabs, one per process. While the
gateway runs, the status bar carries a `WEB` badge.

## How the tab signs in

Each gateway run generates a random token. It is the only credential the tab
gets; there is no cookie.

The browser opener never receives the link, because another local user can
read a process's arguments. It receives a private redirect file instead:

- The file is `launch.html` in a `crush-web-*` directory created with `0700`
  permissions, under `$XDG_RUNTIME_DIR` when that directory exists and under
  the temp directory otherwise.
- The file itself is `0600` and holds an HTML redirect to the link.
- Crush removes it when the first tab signs in, two minutes after writing it
  when no tab has signed in, and when the process exits.

The link appears in the terminal notification and in the clipboard. Opening
it in a new tab checks the token with `POST /api/auth`, keeps it in the tab's
`sessionStorage`, and strips it from the address bar. A reload keeps the tab
signed in.

`sessionStorage` belongs to the tab. A tab you duplicate, and a window the
tab opens itself, inherit it and are signed in too. Any other new tab starts
empty and shows a page titled "This tab is not linked to a crush session":

> This tab has no link to a crush session: the run has ended, or the tab was
> opened without its link. In the crush terminal, run `/web` to open a working
> link. Each new tab needs its own link from `/web`.

The link is the same for the whole run. It stops working when Crush exits,
and a token that a tab stored from an earlier run is dropped when the gateway
refuses it.

## WSL

Under WSL the launch file is a Linux path, which a Windows browser cannot
read. Crush maps it with `wslpath -w` and opens the result, so the tab loads
through a `file://wsl.localhost/<distro>/...` address. On older Windows
builds `wslpath -w` answers with a `\\wsl$\<distro>\...` path instead, and
Crush opens either form. `wslpath` ships with WSL.

Crush detects WSL through the `WSL_DISTRO_NAME` environment variable only. A
shell reached through `ssh` or `su` inside WSL may not have it. Crush then
treats the machine as plain Linux and hands the opener a plain
`file:///run/user/...` or `file:///tmp/...` address that the Windows browser
cannot read: either no tab opens, or the browser shows an error page. The link
still appears in the terminal. Paste it into a browser on the Windows side, or
start Crush from a normal WSL terminal where `WSL_DISTRO_NAME` is set.

When Crush does detect WSL but `wslpath` is missing or fails, the open fails
outright: Crush does not fall back to the link. The terminal then shows "Web
mode is running. Open <link>" with the link, for 30 seconds, so copy it into a
browser on the Windows side.

## Signing in to providers

Open the dialog with **Sign in to a provider** in the command palette, or with
**Sign in to another provider** at the bottom of the model selector. When a
provider login expires, the toast "Your <provider> login expired. Sign in
again." carries a **Sign in** button too. The dialog is titled **Sign in to a
provider** and groups providers under **Signed in** and **Available**.

With an API key:

- Type the key into the **API key** field and press **Sign in**.
- Crush checks the key with the provider before saving it. A rejected key
  answers "The provider did not accept this key."
- An accepted key is saved to the global config, with the same scope and file
  the terminal's key dialog writes.
- A key that contains a shell expression (`$` or a backtick) is refused,
  because only the terminal expands those.

With an account:

- **GitHub Copilot** and **Hyper** show a device code. **Copy code** copies
  it, and the provider page behind the link takes it.
- When Copilot is not set up yet, Crush first tries to reuse a VS Code login,
  as the terminal does. When one is found, the sign-in finishes without a
  device code.
- **ChatGPT** and **Grok** show a link to the provider's consent page, worded
  **Open the ChatGPT sign-in page** or **Open the Grok sign-in page**. Grok
  also accepts the code from that page in **Code from the sign-in page**. When
  Grok's loopback listener cannot start, it falls back to a device code.
  ChatGPT has no code fallback.
- The link, the status line and the done sentence name the provider the way
  its own sign-in does, so the provider the model selector calls OpenAI shows
  as **ChatGPT** here. The status line reads **Waiting for <provider>** while
  the flow runs, then **Signed in to <provider>.** **Choose a model** opens
  the model selector.

The gateway runs one sign-in at a time, and starting another one stops the
running flow. **Cancel sign-in** stops a flow from the tab and reports
"Sign-in cancelled."

ChatGPT's browser flow uses the fixed loopback ports 1455 and 1457, which
Codex also uses. Another program or another ChatGPT sign-in can hold one or
both, and when both are busy a ChatGPT sign-in from the tab answers "Another
sign-in is using the callback port. Finish or cancel it first." Free the ports
and start again.

On a first run there is no provider and no agent. A sign-in from the tab
saves the credential, but choosing the first model and starting the agent
still happens in the terminal's onboarding. A refused model switch says why,
for example "Finish setting up Crush in the terminal, then choose a model."

## AWS SSO

When a Bedrock request needs refreshed AWS credentials, Crush runs the
provider's refresh command, such as `aws sso login`. The tab shows an **AWS
sign-in** card with the command, a link to **Open the AWS sign-in page** once
the command prints a URL, and the status **Waiting for the AWS CLI to
finish**.

The command runs where Crush runs, next to the model calls. In client/server
mode that is the machine with the server. The terminal shows its own dialog
for the same refresh, and the tab's card follows it: it appears when the
refresh starts, fills in the URL when the command prints one, and clears when
the refresh succeeds. A failed refresh stays on the card as the error with a
**Dismiss** button.

## GPG and SSH prompts

When a GPG operation or an SSH connection needs typed input or a
confirmation, Crush shows the prompt in the terminal and in the tab at the
same time, with the same message and key details. The tab labels it **Secure
prompt** and names the field **Passphrase**, **PIN** or **Password**;
**Unlock** sends it and **Cancel** dismisses it. A confirmation prompt shows
**Confirm** and **Decline**.

The first answer wins. When the terminal answers first, the tab closes the
dialog and toasts **Answered in the terminal**. When the tab answers first,
the terminal dialog closes. A late answer from the tab is refused with "The
prompt was already answered or has expired." and shows as a toast.

Security key touches work differently. The touch on the key is the approval,
and the terminal answers the prompt by itself. The tab shows no dialog and no
notice for one; it shows a warning toast carrying the prompt's message. An
answer or cancel from the tab is refused.

The tab sends the secret in the request body, clears the field when you
submit, and keeps no copy.

## Browsing other sessions

The tab follows the terminal's session by default. When the workspace has
more than one top-level session, the pin control in the HUD, labelled
**Following the terminal**, opens the session switcher. Every row other than
the terminal's offers **Browse here**. Browsing shows that session's
transcript, edited files and agents in the tab without moving the terminal;
the HUD says **Browsing** and names the session.

A browsed session is read-only. The message box explains why: prompts go to
the terminal's session. **Return to the terminal's session** goes back to
whatever the terminal shows now, including a session it switched to while
you were browsing. **Open this session in the terminal** asks the terminal
to show the browsed session; the tab follows it and the message box unlocks.

Permission, question and secure prompt dialogs stay global: they open
wherever they belong while you browse.

## Display settings

The **Display** control in the HUD, and **Display settings** in the command
palette, open the tab's display settings. They are stored in the global
config under `options.web_display`, so they follow you across runs and every
tab of the same Crush reads them:

```json
{
  "options": {
    "web_display": {
      "theme": "solid",
      "motion": "reduced",
      "low_power": false,
      "cinematic_hint": true
    }
  }
}
```

The settings are:

- **Theme**: **Holodeck** is the translucent glass with scan lines.
  **Solid glass** gives every panel the opaque glass, with no blur and no
  scan lines.
- **Motion**: **Follow the system** follows the browser's reduced motion
  setting and forgets the tab's earlier choice. **Full**, **Reduced** and
  **Off** override it.
- **Effects**: **Low power (2D constellation)** draws the flat constellation
  in place of the 3D scene. When the scene or the frame-time guard steps the
  effects down, **Restore effects** brings them back.
- **Cinematic view**: **Show the orbit hint in cinematic view** turns the bar
  naming the orbit keys on or off.
- **Reset display settings** removes the keys, so every setting returns to
  its default.

A change from one tab reaches the other tabs at once.

## Stopping web mode

The bottom of the display settings dialog has **Stop web mode**. It asks for
a confirmation with **Stop web mode now**; the tab disconnects and every tab
of that gateway shows "Web mode was stopped. Run /web in the terminal for a
new link." The message box stays disabled for that reason. The terminal keeps
running and the session is untouched.

Run `/web` in the terminal to start a new gateway; the new link works in the
same tab. If the terminal exits instead of stopping web mode, the tab says
the session ended and points at the same `/web` command.

## Palette commands

`Ctrl+K` (`Cmd+K` on macOS) opens the command palette. Besides the sessions,
models, custom commands and skills, it offers these web mode actions:

- **Export session as Markdown** writes the current session to
  `crush-session-<session>.md` in the working directory, the same file the
  terminal writes, and shows the path in a toast.
- **Set goal** asks for an objective and starts it; **Goal status** and
  **Clear goal** read and clear it. **Start loop** asks for a task, and
  **Loop status** and **Clear loops** read and clear the loops.
- **Agent Teams: On** or **Agent Teams: Off** toggles Agent Teams and writes
  the setting to the global config, the same switch the terminal has.
- **Enable Docker MCP catalog** and **Disable Docker MCP catalog** add or
  remove the Docker MCP server. The enable item is absent while Docker MCP is
  unavailable on the machine.
- **Channels** opens the systems panel on its MCP list filtered to the
  servers that are, or could be, channels.
- **Initialize project** starts a new session with the project onboarding
  prompt, the same run the terminal starts on a fresh project.
- **Stop web mode** opens the display settings dialog at its confirmation
  step; **Stop web mode now** disconnects every tab of this gateway.
- **Systems**, **Sign in to a provider** and **Display settings** open the
  same dialogs the HUD controls open.

Goals and loops belong to the terminal: the scheduler runs in the Crush
process, so these commands go through it and need a terminal attached and
showing the same session. With no terminal attached, the command fails and
the palette shows the gateway's error.

The palette does not carry every terminal command. Notification settings and
Remote Control stay terminal-only.

## Client/server mode

With `CRUSH_CLIENT_SERVER=1`, Crush runs as a client of a Crush server. Web
mode works the same way: `crush --web` and `/web` start a gateway in the
client process, and the tab mirrors that client's TUI.

Sign-in flows and their loopback callback listeners run in the client
process, and the credential is saved through the server, so it lands in the
config of the machine that runs the server.

GPG and SSH prompts come from the server's tools. The server publishes the
prompt, and the client shows it in the tab and answers through the server, so
the first answer still wins.

Everything else matches in-process mode: the same routes, the same token, the
same dialog, and the same session the terminal is showing.

## Security model

- The gateway binds `127.0.0.1` on a random port and is not reachable from
  another machine.
- `Host` must name the gateway's own address and port, either
  `127.0.0.1:<port>` or `localhost:<port>`. Anything else answers 403.
- Every request that changes state must carry an `Origin` for that same
  address. A missing `Origin` is accepted only on GET and HEAD. A cross-origin
  request answers 403 "cross-origin request rejected".
- Every `/api` request except `POST /api/auth` carries
  `Authorization: Bearer <token>`. A WebSocket upgrade cannot set that header,
  so the stream sends the token in the `crush.auth.<token>` subprotocol and
  the gateway selects `crush.v1`. Crush sets no cookie.
- The token travels in the URL fragment, which the browser never sends to the
  server, and lives in the tab's `sessionStorage`, which is isolated per
  origin including the port. Crush does not log it, and a secret-bearing
  request that puts its secret in a query string is refused with "secrets are
  accepted in the request body only".
- The launch file keeps the token out of the browser opener's process
  arguments, where other local users could read it. It is `0600` in a `0700`
  directory, and it is removed as described above.
- The link stays in the terminal notification, in the clipboard and in any
  clipboard history until Crush exits.
- Secrets typed in the tab, such as API keys, passphrases and SSH answers, are
  sent only in request bodies, never echoed in a response or an event, and
  never written to the log. The field is cleared when you submit.
- Web mode assumes a single-user host. It does not defend against another
  process running as your user.

## Troubleshooting

- **The tab says "This tab is not linked to a crush session".** The tab has no
  valid token. Run `/web` in the terminal and open the link it shows. A tab
  left over from an earlier run cannot sign back in; close it.
- **No tab opens on WSL, or the browser shows a file error.** The shell that
  started Crush has no `WSL_DISTRO_NAME`, which is common through `ssh` or
  `su`, so Crush used the plain Linux path. Paste the link from the terminal
  into a Windows browser, or restart Crush from a normal WSL terminal. When
  Crush did detect WSL but `wslpath` is missing or fails, the open fails and
  the terminal shows "Web mode is running. Open <link>"; open that link in a
  browser on the Windows side.
- **"Another sign-in is using the callback port. Finish or cancel it first."**
  Both fixed ports 1455 and 1457 are in use, by another ChatGPT sign-in or by
  another program that holds them, for example Codex. Free the ports, then
  start again.
- **"The prompt was already answered or has expired."** The GPG or SSH prompt
  was answered elsewhere, or the command that asked has ended. The dialog
  closes; nothing else is needed.
