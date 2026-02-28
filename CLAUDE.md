# Gypsum Release Process

## Version scheme
`vMAJOR.MINOR.PATCH` — bump **minor** for new features, **patch** for bug fixes/small changes.

## Step-by-step

### 1. Commit the feature/fix changes first
```bash
git add <files>
git commit -m "Short description of change"
```

### 2. Check current version
```bash
git tag --sort=-v:refname | head -5
```
Latest tag = current version. Determine next version based on change type.

### 3. Update CHANGELOG.md
Add a new section **at the top** (below the intro line), before the previous latest version:
```markdown
## vX.Y.Z

### Added
- **Feature name** — description of what was added.

### Changed
- **Thing** — description of what changed.

### Fixed
- **Bug** — description of the fix.
```
Only include the headings that apply.

### 4. Update README.md
- In the **Features** list: update or add a bullet for the new capability
- In the relevant **Usage** section (e.g. ### Images): add/update docs
- Keep it concise, match the existing style

### 5. Commit changelog + readme together with the feature commit
Preferred: combine all changed files into a single commit:
```bash
git add CHANGELOG.md README.md <code files>
git commit -m "Add <feature> (vX.Y.Z)"
```

### 6. Tag and push
```bash
git tag vX.Y.Z
git push origin main
git push origin vX.Y.Z
```

## Notes
- Files modified per release: `CHANGELOG.md`, `README.md`, plus the actual code files
- No version string in Go source — version is tracked only via git tags
- GitHub Actions may trigger on version tags (Helm chart packaging via `.github/workflows/helm.yml`)
- Commit message convention: `Add <feature> (vX.Y.Z)` or `Fix <thing> (vX.Y.Z)`
