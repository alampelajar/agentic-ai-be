# Phase 5B: Agent Delegation

## Goal
A user working with one agent can hand a piece of work to another of their
agents and get the answer back in the same conversation. Phase 5A gave agents
memory; 5B lets agents work together, one step at a time, under the user's
control.

## Scope
In scope:
- The user starts a delegation explicitly (a button and a dialog in AI Chat).
  Nothing is delegated automatically.
- One hop and one agent at a time. The delegated agent answers in text and
  cannot delegate again.
- The delegated run goes through the same `Chat` pipeline as a normal message,
  so model choice, ownership checks, skill instructions and Persistent Memory
  (when the user turned it on) behave the same.
- A record of each delegation, listed and deletable by its owner.

Not in scope (later phases): an agent deciding to delegate on its own,
several agents running in parallel, embeddings or a vector database.

## Design
- `POST /api/delegations` with `to_agent_id`, `instruction`, and optionally
  `from_agent_id`, `task_id` and `context` (an excerpt of the conversation).
- The handler builds a private request for `Chat` and marks it with a context
  value that only server code can set. In a delegated run `Chat`:
  - sends the model no tools and offers no tool protocol,
  - tells the model it is a delegated sub-agent that answers in text only.
- Tool markers in the answer (`<AGENT_TOOL>`, `<LOCAL_AGENT_TOOL>`) are removed.
  The answer is data for the conversation; nothing in it is executed.
- `GET /api/delegations` (filter by `task_id` or `agent_id`) and
  `DELETE /api/delegations/:id` give the owner the history and a way to erase it.

## Safety
- Everything is scoped by the session's `user_id`. The target agent, the source
  agent and the task must be accessible to the caller.
- The target must differ from the source agent.
- Limits: instruction 4,000 characters, context 6,000, stored result 8,000;
  10 delegations per user per minute.
- Instruction and result are redacted for credentials before they are stored,
  and failures are recorded without the provider's raw error.
- Deleting an account deletes its delegations.

## Tests
Integration tests drive the real `Chat` handler with a fake AI provider and
check what the provider receives: no tools, the delegation notice, memory of
the target agent only, and that answers lose their tool markers. Ownership,
validation, rate limit, failure, redaction and account deletion are covered.
