# Release Checklist

Status: required for every release candidate

Last reviewed: 2026-08-24

Use one copy of this checklist per release candidate. Store completed evidence
in the private release record or CI system, never in the repository when it
contains credentials, pairing codes, browser profiles, local paths, or account
details. An unchecked required item blocks publication.

## 1. Candidate Record

- [ ] Version: `________________`
- [ ] Full source commit: `________________________________________`
- [ ] Release owner: `________________`
- [ ] Test date (UTC): `________________`
- [ ] Previous production version used for upgrade: `________________`
- [ ] Chrome Stable version and OS: `________________`
- [ ] Edge Stable version and OS: `________________`
- [ ] CI run/evidence URI: `________________`
- [ ] Manual evidence location: `________________`

The candidate commit must be immutable and the working tree clean. Versions in
the extension manifest, package metadata, release manifest, archive name, and
server metadata must agree.

## 2. Automated Release Gate

Install the pinned dependencies and release/security tools described in CI,
then run from the candidate commit:

```bash
npm ci --prefix chrome-extension
make release-readiness
```

`make release-readiness` runs formatting, lint, race tests, extension tests,
coverage, builds, the generated tool-reference check, vulnerability/license/
secret scans, two-profile Chromium E2E, two deterministic release builds, and
the static readiness checks. Record the complete successful log.

- [ ] Go vet, lint, race tests, and coverage gate pass.
- [ ] Extension format, lint, license, unit, and contract tests pass.
- [ ] Two-profile Chromium E2E passes without cross-routing.
- [ ] Vulnerability, dependency audit, license, workflow, and gitleaks checks
      pass or have a documented owner-approved exception.
- [ ] Repeated release builds have identical `SHA256SUMS`.
- [ ] Every release artifact passes `sha256sum -c SHA256SUMS`.
- [ ] Server binaries report the candidate version, commit, and UTC build date.
- [ ] Extension manifest/package/lock versions match and the ZIP contains only
      production files.
- [ ] CycloneDX SBOM, release manifest, release notes, and checksums are present.
- [ ] No Cyrillic appears outside `AGENTS.md`, `TASKS.md`, and `PRD.md`.
- [ ] Generated tool documentation matches the registered MCP tools.
- [ ] Latency NFRs and the short reconnect/event soak pass.
- [ ] The tracked working tree is clean after all checks.

Before publication, run the eight-hour qualification from the candidate commit
and retain both `SOAK_REPORT` records as described in
[`performance-soak.md`](performance-soak.md).

- [ ] The eight-hour reconnect success rate is at least 99.5%.
- [ ] Transport event loss is zero and every intentional CDP queue drop is
      accounted for.
- [ ] Retained heap and goroutine growth stay within the documented budgets.

## 3. PRD MVP Acceptance

Validate the eleven MVP criteria from `PRD.md` against the candidate, not a
development build.

- [ ] **MVP-1:** Two simultaneously connected Chromium profiles have stable,
      distinct `browserId` values.
- [ ] **MVP-2:** One MCP client lists both browsers and can select each one.
- [ ] **MVP-3:** An untargeted command with two connected browsers returns
      `AMBIGUOUS_BROWSER`.
- [ ] **MVP-4:** Commands never execute in the unselected browser; use a visible
      marker in each profile and retain the routing evidence.
- [ ] **MVP-5:** For each selected browser, verify tab list/activate/create/
      close, navigation, snapshot/HTML, click, fill, wait, and viewport
      screenshot.
- [ ] **MVP-6:** Parallel requests correlate with their own responses and show
      no duplicate, lost, or cross-browser completion.
- [ ] **MVP-7:** Terminating the MV3 service worker causes automatic reconnect
      with the same persistent browser identity.
- [ ] **MVP-8:** STDIO and Streamable HTTP integration scenarios pass.
- [ ] **MVP-9:** Default listeners are loopback-only and pairing/authentication
      is required.
- [ ] **MVP-10:** Go race tests, extension tests, and two-browser E2E pass.
- [ ] **MVP-11:** README installation, pairing, browser selection, examples,
      security defaults, and troubleshooting match the candidate.

## 4. Real Chrome and Edge Matrix

Automated Chromium profiles do not replace this gate. Use clean, non-personal
profiles in current desktop Chrome Stable and Edge Stable. Microsoft recommends
sideloading to test an Edge extension before store submission; Chrome documents
manual update testing and its Extension Update Testing Tool in the sources at
the end of this checklist.

For **each** browser:

- [ ] Fresh-install the exact release extension directory/ZIP in a new profile.
- [ ] Confirm the extension starts without service-worker, manifest, CSP, or
      runtime errors.
- [ ] Pair with the candidate server, reconnect, restart the browser, and
      confirm the same `browserId` returns.
- [ ] Grant Observe for one test origin; verify allowed and denied origins.
- [ ] Grant Debug; run one PDF/accessibility or network diagnostic; remove
      Debug and confirm the capability disappears and CDP detaches.
- [ ] Exercise popup/settings status, diagnostics, copy-safe fields, profile
      state, revoke, identity reset, and uninstall instructions.
- [ ] Run the MVP-5 smoke flow and verify results target the chosen tab/document.
- [ ] Check the browser's extension error page after the flow; no new error is
      acceptable.

Then connect Chrome and Edge simultaneously to the same server:

- [ ] Both appear with the recorded product/version and distinct identities.
- [ ] Selection is MCP-session-scoped.
- [ ] Ten alternating visible actions produce zero cross-routing.
- [ ] Disconnecting/revoking one browser does not interrupt the other.

## 5. Fresh Install, Upgrade, and Permission Warnings

Test upgrade from the latest production package without uninstalling it. Keep
the same signing identity/store listing; never commit or attach a private `.pem`
key to evidence.

- [ ] Fresh install shows only the expected Core warnings.
- [ ] Observe, Debug, and Personal data remain optional and are requested only
      after a user action in settings.
- [ ] Before accepting each optional prompt, cancel once and verify no grant or
      hidden command execution occurred.
- [ ] After accepting, the profile and advertised capabilities update without
      reinstalling the extension.
- [ ] Removing each optional grant removes the corresponding capabilities and
      leaves unrelated grants intact.
- [ ] Upgrade preserves extension identity, browser identity, settings, and the
      stored credential unless the release notes explicitly announce a reset.
- [ ] Upgrade does not silently add required permissions. If a warning-triggering
      required permission changed, verify the browser disable/re-enable flow and
      publish the exact user-facing migration warning.
- [ ] The new service worker becomes active after reload/idle/restart, reconnects,
      and reports the new version.
- [ ] Downgrade/rollback instructions were tested or the release notes explicitly
      state why rollback is unsafe.

## 6. Pairing, Revocation, and Security

- [ ] A pairing code is single-use, expires, and is absent from logs/evidence.
- [ ] Invalid, expired, and reused codes fail with safe structured errors.
- [ ] Revoking one browser invalidates its stored credential and requires new
      pairing on reconnect.
- [ ] Revocation does not change another browser's credential or MCP selection.
- [ ] Host/Origin denial, restricted URLs, stale documents, cancellation,
      timeout, oversized results, and rate limits fail closed.
- [ ] Sample secrets in URLs, headers, cookies, password fields, console output,
      network bodies, and local paths do not appear in normal results or logs.
- [ ] Debug logs are off by default and contain no unredacted test secrets when
      enabled for the negative test.
- [ ] No credential, token, key, `.pem`, private browser profile, artifact body,
      or local path is included in the release bundle or public evidence.

## 7. Documentation and Published Limitations

- [ ] `README.md`, `chrome-extension/INSTALL.md`, `docs/tool-reference.md`, CLI
      `--help`/`--version`, popup, and settings use the same names, ports,
      permission profiles, defaults, and version.
- [ ] Every registered tool is documented with its profile, target, limits,
      result shape, errors, and at least one valid example.
- [ ] [`known-limitations.md`](known-limitations.md) matches the candidate and is
      linked from README and release notes.
- [ ] Browser support and CDP caveats match
      [`browser-support.md`](browser-support.md).
- [ ] Security boundaries match [`security-review.md`](security-review.md).
- [ ] Release notes call out permission changes, migrations, security-impacting
      changes, deprecations, limitations added/removed, and rollback concerns.

## 8. Publication and Rollback

- [ ] Store listing descriptions, privacy links, screenshots, and support links
      match the release.
- [ ] Chrome and Edge packages were produced from the recorded commit and their
      extracted contents match the verified extension ZIP.
- [ ] Checksums, SBOM, release manifest, release notes, and server binaries are
      attached together.
- [ ] Store draft validation/review has no unresolved warning.
- [ ] Rollout scope and stop conditions are recorded; use a staged rollout when
      the store supports it.
- [ ] Previous artifacts and rollback instructions remain available.
- [ ] Post-publication owner and monitoring window are assigned.

## 9. Sign-Off

| Role | Name | Decision | UTC timestamp | Evidence |
| --- | --- | --- | --- | --- |
| Release owner |  | approve / reject |  |  |
| Security reviewer |  | approve / reject |  |  |
| Chrome tester |  | approve / reject |  |  |
| Edge tester |  | approve / reject |  |  |

Publication requires four approvals or an explicit, recorded exception from the
project owner. Any candidate change after sign-off invalidates the checklist.

## Official Browser References

- Chrome: [Permission warning guidelines](https://developer.chrome.com/docs/extensions/develop/concepts/permission-warnings)
- Chrome: [Extension update lifecycle](https://developer.chrome.com/docs/extensions/develop/concepts/extensions-update-lifecycle)
- Microsoft Edge: [Extension overview and cross-browser testing](https://learn.microsoft.com/en-us/microsoft-edge/extensions/)
- Microsoft Edge: [Sideload and reload an extension](https://learn.microsoft.com/en-us/microsoft-edge/extensions/getting-started/extension-sideloading)
