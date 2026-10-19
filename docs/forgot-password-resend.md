# Forgot Password with Resend

The password-reset flow uses Resend transactional email. The Resend API key is read only by the Go backend and must never be placed in the frontend environment.

## Backend environment

Set these variables in the backend `.env` (do not commit real values):

```dotenv
RESEND_API_KEY=re_xxxxxxxxxxxxxxxxxxxxxxxxx
RESEND_FROM_EMAIL=Agentic AI <noreply@your-verified-domain.com>
PASSWORD_RESET_PEPPER=replace_with_a_random_secret_at_least_32_chars
```

Create an API key in the Resend dashboard and verify the sender domain before using this flow for users. The sender address must belong to a domain configured in Resend. Use a long, randomly generated `PASSWORD_RESET_PEPPER` and keep it stable; changing it invalidates outstanding OTP verifications.

## Public endpoints

- `POST /api/auth/forgot-password/request` — accepts `{ "email": "user@example.com" }` and sends a six-digit OTP if an account exists.
- `POST /api/auth/forgot-password/verify` — accepts `{ "email": "...", "otp": "123456" }` and returns a short-lived `reset_token` after verification.
- `POST /api/auth/forgot-password/reset` — accepts `{ "email": "...", "reset_token": "...", "new_password": "...", "confirm_password": "..." }`.

## Security behavior

- OTP expires after 5 minutes, is stored only as an HMAC hash, and can be used once.
- Resend requests are throttled to one per email per minute.
- OTP verification is limited to five attempts.
- The reset token is random, stored only as a SHA-256 hash, expires after 10 minutes, and can be used once.
- A successful reset revokes active refresh-token sessions and writes an audit-log event.
- The request endpoint returns a generic response so it does not disclose whether an email is registered. Resend delivery errors are logged without the recipient, OTP, provider response body, or API key.
- Passwords follow the existing bcrypt constraints: at least 8 characters and at most 72 bytes.

## Frontend

The FE uses `VITE_API_BASE_URL` (defaults to `http://localhost:8081`). Only the backend needs `RESEND_API_KEY` and `RESEND_FROM_EMAIL`.
