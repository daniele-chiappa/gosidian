# Contributing to gosidian

Thanks for taking the time to contribute. This file covers the minimum
you need to know to file a useful bug report or send a pull request.

## Ways to contribute

- **File an issue** if you hit a bug, find a confusing piece of
  documentation, or want to propose a feature. Include the gosidian
  version (`gosidian healthcheck` prints it) and enough repro steps that
  someone else can reach the same state.
- **Open a pull request** for small, focused changes. For anything
  architectural, please open an issue first — it's cheaper to align on
  direction than to rewrite a large diff.
- **Share how you use it.** gosidian's design is opinionated; real-world
  feedback shapes the next release.

## Development setup

```bash
git clone https://github.com/daniele-chiappa/gosidian.git
cd gosidian
go test ./...
go build -o gosidian ./cmd/gosidian
./gosidian --vault ./testdata/vault --mcp-addr 127.0.0.1:8765
```

Requirements: Go 1.27 or newer. No CGO required for the default build
(the SQLite driver is pure Go). Docker is optional but convenient for
end-to-end tests.

## Pull request checklist

- [ ] `go test ./...` is green
- [ ] `go vet ./...` is clean
- [ ] New behaviour is covered by at least one test
- [ ] Public-facing changes update `README.md` and/or the relevant
      docstrings
- [ ] Commit messages are in English and describe the *why*, not just
      the *what*
- [ ] No secrets, credentials, or internal hostnames in the diff

## Branch & commit conventions

- Branch from `main`; name branches `feat/…`, `fix/…`, `docs/…`,
  `chore/…` as appropriate
- Prefer small, self-contained commits over a single large one, but
  don't split a change across commits that can't compile on their own
- Use [conventional commits](https://www.conventionalcommits.org) style
  prefixes where they fit naturally; the project's existing history
  follows this loosely

## Code style

- Formatting is enforced by `gofmt` (run `gofmt -w .` before commit);
  CI fails when `gofmt -l .` lists a file
- The web UI (`web/`) must pass `npm run lint` and `npm run test:unit`,
  which CI runs too
- Prefer the standard library. New direct dependencies need a short
  justification in the PR description.
- Web UI handlers pass `map[string]any` to templates (not typed
  structs) by project convention — see the existing handlers for the
  pattern.

## Testing

- Unit tests live alongside the code in `_test.go` files
- Integration tests should use `testdata/` fixtures, not production
  vaults
- The MCP server can be exercised in-process via the tool handlers —
  see `internal/mcp/*_test.go` for examples

## Review and merge

- PRs are reviewed on a best-effort basis; expect a first response
  within a week
- Squash-merge is the default to keep `main` readable
- Breaking changes are released only at major version bumps

## Release cadence & tagging policy

gosidian follows [SemVer](https://semver.org/) **strictly**. The
lifecycle of a commit is:

1. **Land on `main`** with a clear English commit message. That's it
   — no tag required.
2. **A MINOR tag** (`v2.N.0`) is cut when a user-facing feature set
   is ready to publish: new MCP tool, new page, new config surface,
   i18n additions, visible behaviour change.
3. **A PATCH tag** (`v2.N.M`) exists *only* to fix a bug on a
   previously-published MINOR. If the bug is found before the first
   public release of the affected code, the fix goes into the next
   MINOR instead — no patch tag needed.
4. **A MAJOR tag** (`v2.0.0`, …) marks breaking changes to the MCP
   tool surface, the web URL contract, the vault layout convention,
   or the on-disk format of `.gosidian/*` files.

**Doc-only changes, refactors, and WIP never get their own tag.**
They ship as part of the next MINOR. This keeps the CHANGELOG
readable and Go module consumers' upgrade paths meaningful.

Contributors aren't expected to tag anything — maintainers cut
releases from `main` when the accumulated change justifies one.

## Translating gosidian

The web UI strings live in `internal/i18n/catalogs/`, one JSON file per
(scope, language) pair. The SPA bundles the `ui` catalogs
(`web/src/locales/index.ts`); `/api/v1/i18n` serves `ui` and `errors` to
other clients:

```
catalogs/
├── ui.en.json      errors.en.json
├── ui.it.json      errors.it.json
├── ui.es.json      errors.es.json    ← stub (v1.10)
├── ui.fr.json      errors.fr.json    ← stub (v1.10)
└── ui.de.json      errors.de.json    ← stub (v1.10)
```

**Current state**: English is the reference. Italian is complete. Spanish,
French, and German cover about three quarters of the `ui` keys (the
two-factor screens are among the missing ones); every missing key falls
back to the English catalog automatically (vue-i18n's `fallbackLocale` in
`web/src/locales/index.ts`). An operator who prefers to hide a partial
translation leaves it out of `i18n.enabled_langs`.

### How to contribute a translation

1. Pick a scope/lang pair you want to improve, e.g. `ui.es.json`.
2. Open the matching EN file (`ui.en.json`) alongside as reference.
3. Keep the **keys** identical. Translate only the **values**. The
   JSON tree shape must mirror the EN source so new keys added
   upstream don't silently disappear on merge.
4. Preserve any format verbs (`%s`, `%d`, …) and placeholders
   (`{name}`). They are filled in at runtime.
5. Remove the `"_stub"` header key once the file is materially
   complete — it's a contribution marker, not a translation.
6. Open a PR with a short summary ("complete ES ui catalog", "fix DE
   plural in tokens.confirm_revoke", …). No need to translate all
   both scopes at once; partial PRs are welcome.

### Adding a new language

1. Create `<scope>.<lang>.json` for both scopes, mirroring the EN
   structure. Start with an empty `{}` if you only have time for the
   topbar — missing keys fall back to EN without crashing.
2. Add the language to the SPA: import its `ui` catalog in
   `web/src/locales/index.ts`, add the code to `LocaleCode` and
   `VALID_LOCALES` in `web/src/stores/ui.ts`, and add an entry with its
   display label to `localeOptions` in `web/src/views/SettingsView.vue`.
   The server needs no code change: a language is available once its
   `ui` catalog exists. Add the code to the list `TestLanguages`
   (`internal/i18n`) expects.
3. Submit a PR with the 2 files + the SPA edits.

### AI-assisted translations

AI translation tools (Claude, DeepL, Google Translate, etc.) are
welcome for the initial pass — they're fast and produce good baseline
quality for most languages. However, **a human read-through is
expected before the PR is merged**, especially for:

- UI microcopy where tone matters (buttons, errors shown to end users)
- Format verb preservation (`%s` placeholders must stay exactly where
  the EN source has them)
- Idiomatic phrasing that literal translation misses

Mark the PR description with the method you used ("DeepL + manual
review", "fluent speaker", etc.) so reviewers calibrate their
scrutiny accordingly.

### Testing locally

After editing a catalog, rebuild and switch languages in Settings. A key
missing from the language shows the English text; a key missing from
English too shows its literal dotted path (e.g. `nav.projects`).

## Code of conduct

Be decent to each other. Disagreement about technical choices is
welcome and expected; personal attacks are not.
