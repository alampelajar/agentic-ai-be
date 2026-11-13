# Incident response

What to do when something has gone wrong with security: an account taken over, data
seen by someone it does not belong to, a leaked key, a server that is attacked. Written
for the people who run Agentic AI, using the tools the app has today.

## Roles

| Role | Who | Does |
|---|---|---|
| Incident lead | *(fill in name)* | Decides, keeps the timeline, says when it is over |
| Technical | *(fill in name)* | Investigates and makes the changes |
| Communication | *(fill in name)* | Tells users, the team and, when required, the authorities |

One person may hold several roles. The lead is whoever notices first until someone
takes it over.

## Severity

| Level | Example | Start within |
|---|---|---|
| Critical | Admin account taken over, database or `.env` leaked, data of many users exposed | Immediately |
| High | One user's account taken over, an API key or GitHub token leaked, a working exploit reported | Same day |
| Medium | A vulnerability reported but not used, repeated sign-in attacks that the limits stop | Within 3 working days |
| Low | A hardening idea, a finding with no realistic use | Next planned work |

## Where the signs show up

| Sign | Where |
|---|---|
| A refresh token used again after it was replaced (a stolen session) | Admin console, Logs: `security.refresh_token_reused` |
| Admin console sign-ins, users created or changed, maintenance mode, model and feedback changes | Admin console, Logs (`admin.login`, `user.*_by_admin`, `settings.maintenance_changed`, `model.*`, `feedback.*`) |
| Failed and locked sign-ins | API server log: lines `[security] event=login_failed` and `event=login_locked`, with the address block and a short hash of the account |
| A report from someone outside | The process in `SECURITY.md` |

Nothing watches these automatically yet; see "Still to do".

## Steps

1. **Record.** Open a private note: time noticed, who noticed, what was seen. Keep every
   step and its time from here on.
2. **Contain.** Stop it from getting worse, with the smallest change that does it:
   - Many users at risk: turn on **maintenance mode** (Admin console, Settings).
   - One account taken over: delete or change its password from the admin console, or
     the user changes it; a password change ends the account's other sessions.
   - Every session must end (a JWT key may have leaked): replace `JWT_ACCESS_SECRET`
     and `JWT_REFRESH_SECRET` with new random values and restart the API. Everyone signs
     in again.
   - An AI provider key leaked: revoke it at the provider, then enter the new one in the
     admin console (Models).
   - A GitHub token leaked: the user disconnects GitHub in Settings, Integrations, and
     revokes the app in GitHub's own settings.
   - `AI_ENCRYPTION_KEY` leaked together with a database copy: treat every stored API key
     and GitHub token as leaked and replace them; do not change the key itself before
     the stored values have been re-entered, or they can no longer be read.
3. **Keep evidence.** Before cleaning up, make a backup (`deploy/backup.sh`) and copy the
   API logs of the period. Do not delete audit log rows.
4. **Find the cause.** Use the audit log, the server log and the code history. Note which
   accounts and data were affected and from when.
5. **Fix and recover.** Fix the cause in code, with a test that fails before the fix.
   If data was changed or deleted, restore it (`docs/backup-restore.md`). Turn maintenance
   mode off only after checking the fix.
6. **Tell people.** Users whose data or accounts were affected are told what happened,
   what was done and what they should do (for example, change a password used
   elsewhere). Personal data breaches may have to be reported to the authorities within
   a legal deadline; the incident lead checks this on day one.
7. **Learn.** Within a week: a short written review (timeline, cause, what worked, what
   to change), without blame. Turn each change into a task.

## Still to do

- Alerts: send `security.refresh_token_reused` and bursts of `login_locked` to the team
  (chat or e-mail) instead of waiting for someone to read the logs.
- A way to suspend an account without deleting it.
- Rehearse this once with a made-up incident (Phase 11 in `phase7-public-readiness.md`).
