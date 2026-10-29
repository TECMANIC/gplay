# Manual end-to-end checklist

None of this has been run. There is no Play test app in this environment, and a green `go test` does not cover it.

Run the steps on the Tecmanic test app with the agent service account. That account must not have `CAN_MANAGE_PUBLIC_APKS`. No production write may succeed at any point. Do not tag `v0.1.0` as part of this checklist.

`cosign verify-blob` on a real release tag has not been run. The release workflow has not been executed, and `brew install tecmanic/tap/gplay` has not been tried.

If the test app has never been published, Play only accepts a `draft` release. Use `--status draft` on the internal create in that case, then let a human roll it out in Console.

## Ordered checks

- [ ] 1. `gplay auth doctor` reports both APIs enabled, the key file mode, clock skew, and app access.
- [ ] 2. `gplay apps list` and `gplay tracks list --package <pkg>` succeed. Record the real track names.
- [ ] 3. `gplay upload --dry-run` uploads the bundle and then discards the edit. `gplay upload --commit` leaves the bundle visible in App bundle explorer.
- [ ] 4. `gplay release create --track internal --version-code N --notes-file notes/` commits. `gplay release status` reaches `RELEASE_LIFECYCLE_STATE_PUBLISHED`. Install that build from the internal testing link on a device.
- [ ] 5. On a closed track: `release create --fraction 0.1`, then `rollout` to `0.5`, `halt`, `resume`, and `complete`. Confirm each state in Play Console.
- [ ] 6. `gplay release promote --from internal --to <closed track>`.
- [ ] 7. Negatives:
  - [ ] production without `--allow-production` exits 8 and sends no request
  - [ ] `--allow-production` without `allowProduction` in the policy file exits 8 and sends no request
  - [ ] with both set, Google returns exit 3 for the agent account (no `CAN_MANAGE_PUBLIC_APKS`)
  - [ ] a duplicate version code exits 6
  - [ ] a wrong key exits 3
  - [ ] `--track bogus` exits 4
- [ ] 8. With a closed-track release in review, `release create --track internal` exits 5. Confirm in Console that the review was not cancelled. Then one `--cancel-in-review`, with `allowCancelInReview` set in the policy, and confirm that this one does cancel the review.
- [ ] 9. Re-run steps 3–5 with `--debug`. Grep stdout and stderr for `PRIVATE KEY`, `ya29.`, and `private_key_id`. None of those strings may appear.
- [ ] 10. Security sign-off below. No production write succeeded.

## Security sign-off

- [ ] The first configured credential source is the only one used. A missing or invalid source exits 3 and does not fall through. Only `type` `service_account` with `token_uri` `https://oauth2.googleapis.com/token` is accepted. Application default credentials are used only when the value is `adc`. `auth status` prints `source` and does not print the secret.
- [ ] `GPLAY_CREDENTIALS_JSON` is capped at 64 KiB and is never echoed. It is still visible in the process environment, so CI should prefer a mode `0600` file and `GPLAY_CREDENTIALS`.
- [ ] `--debug` logs the method, the URL (query included), the status, the latency, and the request ids. It does not log request or response bodies. The grep in step 9 is clean.
- [ ] A key whose mode has any `0o077` bit warns on stderr and shows up as a doctor finding. The command still runs. `~/.config/gplay` is mode `0700` and `config.json` is mode `0600`. A notes directory only contains `<bcp47>.txt` files, each valid UTF-8 and at most 500 runes, and a symlink cannot escape that directory.
- [ ] Live calls only go to `androidpublisher.googleapis.com`, `playdeveloperreporting.googleapis.com`, and `oauth2.googleapis.com`.
- [ ] The agent service account has `CAN_VIEW_NON_FINANCIAL_DATA` and `CAN_MANAGE_TRACK_APKS` only. It does not have `CAN_MANAGE_PUBLIC_APKS` or `CAN_MANAGE_PERMISSIONS`. Record whether a view-only account can `edits.insert`. If it cannot, `tracks list` needs `CAN_MANAGE_TRACK_APKS`.
- [ ] A new grant that still returns 403 is propagation delay (up to 24 hours), not a reason to grant Admin.
- [ ] When a tag exists, `checksums.txt` verifies with the `cosign verify-blob` command in the README. That verification is still outstanding.
