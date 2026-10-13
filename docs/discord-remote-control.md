# Discord Remote Control (`/rc`)

`/rc` binds a Discord thread to the live session running in your terminal.
Everything the agent does is mirrored into the thread, and anything typed
in the thread is injected back into the same session — from the prompt, to
tool calls, to permission prompts, to slash commands. The terminal and
Discord stay in sync the whole time: prompts typed remotely appear in the
TUI as normal user messages, and prompts typed locally are echoed to the
thread.

This feature only exists in this fork.

## Setup

1. Create a Discord application and bot at
   <https://discord.com/developers/applications>.
2. Under **Bot**, enable the **MESSAGE CONTENT INTENT** (without it the
   bot cannot read message text; the bridge will report the gateway
   error if it is missing).
3. Invite the bot to your server with the **Create Public Threads**,
   **Send Messages**, **Add Reactions**, and **Read Message History**
   permissions.
4. Export two environment variables (optionally a third):

   ```bash
   export DISCORD_BOT_TOKEN="..."      # bot token
   export DISCORD_CHANNEL_IDS="..."    # comma-separated channel ids; the first is used
   export DISCORD_ALLOWED_USER_IDS=""  # optional: comma-separated user ids allowed to drive the session
   ```

5. Start Crush, type `/rc` (or `/remote_control`, `/discord`) in the
   command palette and pick **Toggle Remote Control (Discord)**.

A thread named after the session appears in the configured channel with a
banner showing the model, mode, and YOLO state. Run `/rc` again — or
`/rc off` in the thread — to end remote control; the session itself keeps
running in the terminal.

## What gets mirrored

- Assistant text streams into a single live message that animates while
  the agent thinks, writes, and runs tools (spinner, status icon, elapsed
  timer), then freezes into the full transcript.
- Thinking blocks appear as a one-line summary (`🧠 thought for 12s`).
- Tool calls are one line each with their main parameter; tool results are
  posted as compact, fenced messages (long output is trimmed with a hidden
  line count).
- Prompts typed in the terminal are echoed with a `💻 (terminal)` marker.
- The session title renames the thread once it is generated.
- Token usage appears in `/status` and the completion line.

## Commands

Type these in the thread:

| Command | Action |
| --- | --- |
| `/help` | Command list, including custom commands |
| `/status` | Session title, model, mode, YOLO, queue, tokens |
| `/model <query>` | Switch the main model (fuzzy match, exact `provider/model` wins) |
| `/models [query]` | List available models for configured providers |
| `/plan`, `/code` | Switch input mode |
| `/yolo [on\|off]` | Toggle permission prompts |
| `/stop` | Cancel the running turn |
| `/summarize` | Compact the session |
| `/queue` | List queued prompts |
| `/tokens`, `/title` | Usage and session title |
| `/allow`, `/allow-session`, `/deny [tool]` | Resolve a pending permission |
| `/answer <text\|#n>` | Answer a pending question |
| `/rc off` | End remote control |
| `/<custom-command> [args]` | Run a custom command from `~/.config/crush/commands` |

If the bot's application has native slash commands registered (for
example by the commandcode bridge), invoking one in the thread works
too: it is acknowledged immediately and its output is posted in the
thread. `/compact` is treated as `/summarize`; registered commands with
no remote-control equivalent (such as `/clear` or `/effort`) reply with
a hint pointing at `/help`. Native commands invoked outside the control
thread are ignored.

Anything else is sent to the agent as a prompt. A prompt that starts with
a literal `/` can be escaped with a backslash: `\/hello`.

## Permission prompts

Tool permission requests are posted as cards with ✅ (allow), 🔁 (allow
for the session), and ❌ (deny) reactions — or resolve them with
`/allow`, `/allow-session`, and `/deny`. The card updates once either
side resolves it, and the terminal dialog closes when Discord answers
first (and vice versa).

## Notes and limits

- Remote control requires local mode: it does not work when
  `CRUSH_CLIENT_SERVER` is set.
- The bridge lives inside the running Crush process. Quitting Crush (or
  `/rc` again) posts a farewell in the thread and disconnects the bot;
  the thread itself is kept for history.
- Messages from bots are ignored, and `DISCORD_ALLOWED_USER_IDS` can
  restrict who may drive the session (empty means any member who can post
  in the thread).
- Images attached in Discord are forwarded to the agent as attachments
  (up to 8 MiB each).
