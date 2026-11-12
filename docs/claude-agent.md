# Claude Agent provider

The Claude Agent provider runs Claude models in Crush through the user's own
`claude` CLI, so a Claude subscription works without an API key. It is the same
approach the Claude Agent SDK takes: Crush starts the CLI as a child process and
speaks its streaming protocol over stdio.

The provider ID is `claude-agent`, and it shows up in the model picker as
**Claude Agent**.

## Requirements

- Claude Code 2.1.248 or newer on `PATH`, or pointed at with
  `CRUSH_CLAUDE_PATH`.
- A login. Run `claude auth login` once in a terminal. Any login works: a
  subscription, an API key, or a cloud account.

Crush never reads Claude Code's saved credentials. The CLI owns the login; Crush
only starts it.

## What Claude sees

- **System prompt**: Claude Code's own system prompt, with the Crush agent prompt
  and your `AGENTS.md` files appended. Crush's `<env>` block is dropped because
  Claude Code writes its own date and environment.
- **Tools**: only Crush's tools. Claude Code's built-in tools are off, so every
  tool call runs through Crush's normal execution and permission path. If the
  tool bridge cannot be set up, the request fails rather than falling back to
  Claude Code's tools.
- **History**: Crush's conversation is the source of truth. Claude's replies are
  saved like any other model's, so switching between Claude and another model
  works. When Claude has not seen part of the conversation, that part is sent
  once, wrapped in `<conversation-checkpoint>`.

Claude Code settings files, `CLAUDE.md`, hooks, MCP servers and skills are all
turned off: Crush has its own versions of each, and only one of them should run.

## Caching and token use

The provider is built around Claude's prompt cache:

- **One process per session.** The conversation lives in Claude's own session, so
  after a tool call Crush sends only the tool result, not the whole history.
- **Stable prompt prefix.** Tools are sorted, the system prompt is byte-identical
  between turns, and the date and environment are left to Claude Code. A stable
  prefix is what makes the cache hit.
- **Resume across restarts.** When a session goes idle for five minutes the
  process stops; the next message resumes the saved Claude session, so the cache
  survives the gap.
- **No double counting.** The CLI reports a response's usage several times while
  streaming. Crush keeps the highest count per response and uses the last
  response's input as the context size, so compaction is not triggered early.
- **Standalone tasks stay standalone.** Titles and compaction summaries run in a
  throwaway process with their own system prompt and no transcript, and never
  touch the chat's session.

A measured example: a two-step turn (one tool call, then the answer) read 2,891
cached tokens on the second step and wrote 85 new ones.

## Configuration

The provider works with no configuration once the CLI is installed and logged in.
To point it at a gateway or a different account, set the usual provider fields:

```json
{
  "providers": {
    "claude-agent": {
      "base_url": "https://your-gateway.example.com",
      "api_key": "$ANTHROPIC_API_KEY",
      "extra_headers": { "x-tenant": "example" },
      "extra_body": { "some_field": true }
    }
  }
}
```

| Crush setting                 | Claude Code setting             |
| ----------------------------- | ------------------------------- |
| `base_url`                    | `ANTHROPIC_BASE_URL`            |
| `api_key`                     | `ANTHROPIC_API_KEY`             |
| `provider_options.auth_token` | `ANTHROPIC_AUTH_TOKEN`          |
| `provider_options.header_timeout` | `API_TIMEOUT_MS`            |
| `provider_options.chunk_timeout`  | `CLAUDE_STREAM_IDLE_TIMEOUT_MS` |
| `extra_headers`               | `ANTHROPIC_CUSTOM_HEADERS`      |
| `extra_body`                  | `CLAUDE_CODE_EXTRA_BODY`        |
| `reasoning_effort`            | the CLI's `--effort`            |

A subscription login is marked flat-rate, so sessions show no cost. An API key
login is metered and shows Anthropic's prices.

## Model list

The models come from the CLI, so the list matches what your login can actually
run. It is read once (the CLI is started without sending a prompt, which costs no
tokens), cached under the Crush data directory, and refreshed whenever a new
process starts.

## Limitations

- `claude` must be installed and signed in on the same machine.
- Temperature, top-p, top-k, penalties and stop sequences are not supported:
  Claude Code owns the request.
- Claude Code's own subagents, plan mode, auto memory, away summaries and the
  advisor tool are turned off. Crush's versions of those features are used
  instead.
- A conversation that is edited or rewound starts a new Claude session and sends
  the whole history once, because Claude cannot rewind its own transcript without
  a fork.

## Troubleshooting

- **"Claude Code is not installed"**: install it from
  <https://code.claude.com/docs/en/setup>, then run `claude auth login`.
- **"Claude Code is not logged in"**: run `claude auth login` in a terminal.
- **"Claude Code X is too old"**: run `claude update`.
- **"subscription limit reached"**: the plan's window is exhausted; the message
  includes the reset time.
