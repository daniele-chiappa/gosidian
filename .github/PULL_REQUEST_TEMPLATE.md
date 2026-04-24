## Summary

<!-- One or two sentences: what this PR changes and why. -->

## Changes

<!-- Bulleted list of the concrete changes. Mention new packages,
removed code, behaviour shifts. -->

-

## Test plan

<!-- How did you verify this works? What did you run? -->

- [ ] `go vet ./...` passes
- [ ] `go test -race -count=1 ./...` passes
- [ ] (UI changes) Manually verified in browser at `http://localhost:8080`
- [ ] (MCP changes) Manually verified via `claude mcp` or equivalent

## Checklist

- [ ] CHANGELOG.md updated under `## [Unreleased]` (if user-visible)
- [ ] Docs updated (`README.md` / `docs/**/*.md`) if behaviour or
      configuration changed
- [ ] No new dependencies, or new dependencies justified in PR
      description
- [ ] No `Co-Authored-By` trailer or other private convention markers
      in commits
- [ ] Backward compatibility preserved, or breaking change explicitly
      flagged with rationale + migration notes

## Linked issues

<!-- e.g. "Closes #123" or "Refs #456" -->
