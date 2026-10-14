Ask the user a structured question and wait for their response. Use this
when you need clarification, confirmation, or a choice before proceeding.

## How it works

Always provide a `questions` array with at least one item. A single item
renders as a plain question; multiple items render as a tabbed form with
a confirmation screen at the end.

Every question MUST include:
- `type` — `yes_no`, `single_choice`, `multi_choice`, or `free_text`
- `question` — a short, direct question (one line)
- `description` — markdown context shown below the question with details,
  tradeoffs, or examples. **Always required.** Omitting it causes an error.

## Hard limits

These are enforced. Violations return an error and waste a round trip.

- **Max 5 choices** per question; **max 5 questions** per batch (split into
  multiple batches and tell the user there will be follow-up questions).
- **Choices required** for `single_choice` and `multi_choice`.
- **Descriptions required** on every question (under 300 chars) and on
  every choice (under 100 chars).

## Question types

- `yes_no` — confirmation only: the question must be a proposition the
  user affirms or rejects (e.g. "Proceed with deletion?"). Never use it
  for A-vs-B or preference questions where both answers are valid; use
  `single_choice` with two choices instead — even when there are exactly
  two options.
- `single_choice` — pick one from `choices` (always provide at least 2).
  Use for any selection between named alternatives, including binary ones
  like "TypeScript or Go?".
- `multi_choice` — pick one or more from `choices`.
- `free_text` — open-ended text input for narrative answers. No choices
  needed. Do not use `yes_no` for open-ended questions.

Single and multi choice questions automatically include a free-text
fill-in option; do not add an "Other", "Something else", or "Custom"
choice manually.

## Multiple questions

With multiple questions, each item can include an optional `label` (3
words max) used as the tab header; it defaults to the first 3 words of
`question`. The batch always ends with a confirmation screen summarizing
the answers, so provide:

- `confirm_title`: a short question like "Ready to go?" or "Sound good?"
- `confirm_description`: summarize what will happen based on the expected
  answers, written as if you already know what they will pick.

## When to use

- Confirm destructive or ambiguous actions
- The request has multiple valid interpretations
- The user must pick from options or answer several related questions

## When NOT to use

- Questions answerable by reading code or docs, or by using other tools
- Asking permission (use the permission system)

## Example

```json
{
  "questions": [
    {"label": "Database", "type": "single_choice", "question": "Which database?", "description": "PostgreSQL for relational data, MongoDB for documents.", "choices": [{"id": "pg", "label": "PostgreSQL"}, {"id": "mongo", "label": "MongoDB"}]},
    {"label": "Caching", "type": "yes_no", "question": "Enable caching?", "description": "Reduces latency for repeated queries but adds invalidation complexity."}
  ],
  "confirm_title": "Ready to configure?",
  "confirm_description": "We will set up the database with caching enabled."
}
```
