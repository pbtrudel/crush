# Routed Copilot Fusion Context Budget Design

## Goal

Use the actual Copilot model limits for each Fusion turn when deciding whether to compact, instead of treating every Fusion tier as a fixed 200,000-token model. Preserve existing overflow recovery when limits cannot be established.

## Current constraints

Catwalk v3.0.1 parses Copilot `max_prompt_tokens`, `max_context_window_tokens`, and `max_output_tokens`, but its model conversion keeps only the latter two values (context and output). Crush must not treat a total context window as an input prompt limit. Fusion routing is lazy, while model-switch compaction runs before the first routed request. A Fusion plan can include generation, review, and repair phases; review has a separate task-and-draft prompt, and repair adds a handoff.

## Design

1. Add Crush-owned Copilot limit metadata containing optional prompt, total-context, and output limits. Obtain it from the authenticated Copilot `/models` response using the configured Copilot endpoint, resolved API key, and Copilot headers. Keep it runtime-only and keyed by the base model ID; do not extend or patch Catwalk, persist credentials, or change the selected long-context tier. Cache the response for the process with a bounded lifetime; failed or incomplete lookups remain unknown and do not fabricate an input limit.
2. Extend the Fusion runner with an explicit preparation step that resolves and caches its plan before compaction. The generation and repair steps consume conversation history, so determine their usable input ceiling from the smallest known `max_prompt_tokens` and `max_context_window_tokens - reserved_output` across those steps. The review step is bounded separately against its distinct prompt. Never subtract output twice from an input-only prompt limit. Preserve routed model IDs and factory errors.
3. Before the turn is streamed, estimate the assembled prompt from stored history, the current user context, system prompt, and tools, then apply existing compaction behavior against the prepared generation/repair budget. Re-read state after compaction and re-evaluate. If limits are unknown, retain existing model-window and provider-overflow recovery behavior. Do not promise that estimates prevent every provider rejection.
4. Compact before adopting a smaller routed model. After each successful Fusion turn, retain the concrete generation model and its usable input budget, keyed by session ID so one session's route never summarizes another. On the next routed turn of that session, when the new route's budget is smaller and the persisted history estimate reaches the compaction threshold of the new budget, summarize with the retained previous concrete model before the new primary runs, then reload history and recheck the prompt against the new budget. Ordinary configured model switches keep their existing previous-model compaction and rollback.
5. Base Fusion pre-request compaction decisions on the estimated persisted history and assembled prompt, not on the session's last-request `PromptTokens`/`CompletionTokens` snapshot, which can be stale after a route change or a prior compaction.
6. Output reservation per step uses the catalog `DefaultMaxTokens` when positive, falls back to the API `max_output_tokens`, and is raised to the turn's requested output when that is larger.
7. Keep model usage metadata separate from Catwalk's total `ContextWindow`. Use explicit output reservations where available. Explicit `[long]` selections retain their current semantics; a base model selected by Fusion is evaluated against the default tier limits only.

## Failure behavior

A routing error or unavailable routed model remains an error. A failed limits lookup does not fail an otherwise valid Fusion turn. Invalid or absent limits are treated as unknown. Compaction errors continue to use existing error handling; history is not discarded if compaction fails. Existing overflow recovery and retry behavior remain enabled. If summarizing with the retained previous route fails, the turn fails with the prompt persisted and history and summary pointers unchanged; the smaller route is not called. Retained routes are process memory only: after a restart no previous route is known, so the first turn falls back to preflight compaction with the new route, and a prompt still estimated over the new budget after compaction is rejected rather than sent.

## Validation

- Parse Copilot model response limits, including missing and zero-valued fields.
- Calculate usable input ceilings from prompt-only, total-plus-output, and mixed known metadata without double-subtracting output.
- Ensure Fusion plans are resolved once before pre-request compaction and reused by execution.
- Verify differing phase limits, separate review prompt limits, repair handoff allowance, unknown metadata, explicitly selected `[long]` entries, and disabled auto-compaction.
- Verify consecutive Fusion turns compact with the previous concrete route before a smaller route, retained routes do not leak across sessions, summary failure preserves history, and stale session usage alone does not trigger compaction.
- Run focused tests, `go test ./internal/config/ ./internal/agent/ -count=1`, and `go build ./...`.
