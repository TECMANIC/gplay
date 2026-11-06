# Agents

This is `github.com/TECMANIC/gplay`. It is not affiliated with Google.

## Step 0

Run `gplay version`. The line must start with `gplay (github.com/TECMANIC/gplay)`. If it does not, stop. Another project also installs a binary named `gplay`.

Then run `gplay schema` and use that document, not memory, for flags and exit codes.

## Rules that are easy to get wrong

- Do not point this CLI at production unless the human asked for that write, `.gplay/policy.json` has `allowProduction: true`, and the command passes `--allow-production`. The lock is a mistake-guard, not a security boundary. The agent service account must not have permission to release to production.
- `beta` is not locked. It is still a public test track.
- `upload --dry-run` uploads the bundle and then deletes the edit. It is not a no-network flag.
- A commit sends `changesInReviewBehavior=ERROR_IF_IN_REVIEW` unless `--cancel-in-review` is set, and that flag also requires `allowCancelInReview` in the policy file. Do not cancel a review to make a command succeed.
- Do not retry a commit. Exit 5, code `changes_in_review`, means wait or ask the human.
- Opening an edit invalidates any other edit held by the same service account.
- Read stderr's last JSON line. stdout is empty when the exit code is not 0. Do not look in logs for the key, the token, or the request body. `--debug` does not print them.
- Credential variables are `GPLAY_CREDENTIALS` and `GPLAY_CREDENTIALS_JSON`. Do not set PollyGlot's `GPLAY_SERVICE_ACCOUNT`.

The release procedure is `skills/gplay-release/SKILL.md`. The contract is `SPEC.md`.
