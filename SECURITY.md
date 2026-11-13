# Security policy

Agentic AI is made of three repositories: the backend API (`agentic-ai-be`), the user
app (`agentic-ai-fe`) and the admin console (`admin-agentic-ai`). This policy covers all
three.

## Reporting a vulnerability

Please do not open a public issue, pull request or discussion for a security problem.

Report it privately through GitHub: open the repository's **Security** tab and choose
**Report a vulnerability**. If that button is missing, ask a maintainer for a private
channel first and send the details only there.

A useful report says:

- what is affected (repository, endpoint or page, version or commit),
- how to reproduce it, step by step,
- what an attacker could do with it,
- whether you have seen it used against real users.

Test only against your own local copy or an environment you were invited to test.
Do not access, change or delete other people's data, and do not run tests that could
take a shared server down.

## What happens next

| Step | Target |
|---|---|
| We confirm we received the report | 3 working days |
| We tell you whether we can reproduce it and how severe it is | 10 working days |
| Fix for a critical or high finding is released | 30 days |
| Fix for a medium or low finding is released | Next planned release |

We credit reporters in the fix's release notes unless they ask us not to.

## Supported versions

Only the latest commit on `main` receives security fixes.

## Keeping dependencies safe

Every pull request runs a check for known vulnerabilities: `govulncheck` for the backend
and `npm audit` (production dependencies, high and critical) for the two web apps. A new
advisory fails the check until the dependency is updated or the finding is reviewed.
