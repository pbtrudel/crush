Start a subagent that runs on its own and reports back later.

The call returns as soon as the subagent starts. It returns the subagent's name, which is your `label` with a first name in front (`date-impl` becomes something like `betty-date-impl`). Its result arrives as a `<subagent-report>` message under that name, once it finishes. Keep working on whatever does not depend on it. When nothing is left that does not, end your turn: the report wakes you with a new turn, so ending one here is not abandoning the task.

Use it to fan work out (a review, a search, an investigation across several scopes) or to hand off reading that would otherwise fill your context. For a quick lookup you need before doing anything else, use `agent` instead, which blocks and returns the answer.

`label` describes the subagent's scope: `auth-review`, `db-layer`. Use the returned name for `agent_send`, `agent_status`, and `agent_stop`. Names are unique per session.

`access` is `read` (default) for a subagent that inspects and runs things but cannot edit, or `write` for one that makes a change. Give parallel `write` subagents scopes that do not overlap.

`model` runs this subagent on a different model than the session default. Name a model ID from the `[models]` section of `crush_info`, which lists every provider you can use, the models each can serve, and in brackets the reasoning values each model accepts. Omit `provider` to use the parent agent's provider; `provider` requires `model`. `reasoning` sets how hard the subagent thinks, using one of the values listed for its model there; when omitted the model's default applies. Without any of these the subagent runs on the session's model. The override is remembered: `agent_send` follow-ups and later resumes of the subagent keep running on it.

The prompt is all the subagent sees. It cannot see this conversation or other subagents. Say what to examine, what to do, and what shape the answer should take.

Subagents keep running if the user interrupts your turn. After it reports, a subagent can still take follow-ups through `agent_send`.
