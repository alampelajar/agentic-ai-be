# Agentic AI backend

The Website Builder (a person describes a website, the AI builds and publishes it)
is planned in one file that covers both repositories. It lives in the frontend
repository, which is expected to be checked out next to this one. Read it before any
Builder work, and follow its decisions, security rules and stages:

@../agentic-ai-fe/WEBSITE-BUILDER.md

Existing architecture this work builds on:

- `docs/phase6-cloud-workspace.md`: workspaces, the execution worker and the sandbox.
- `docs/phase7-public-readiness.md`: what must be true before strangers can run code.
