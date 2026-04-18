# Code Audit Log

All items discovered across audits #1–#8 (2026-04-18), reorganised into implemented fixes and open issues.

---

## Implemented Fixes

### Security / Argument Injection

| # | Severity | File(s) | Summary |
|---|----------|---------|---------|
| 15 | Medium | `git.go` — `DeleteLocalBranch`, `BatchDeleteRemoteBranches`, `assertMergedInto` | Added `--` end-of-options separator before user-controlled ref arguments to prevent flag injection from crafted `--input` files. |
| 16 | Medium | `prune.go` — `loadBranchCSV`, `loadBranchJSON` | Reject branch names and `merged_into` values starting with `-` on load. 4 test cases added. |
| 22 | Low | `git.go` — `MergedBranches` | Changed `--merged <target>` to `--merged=<target>` joined form to prevent flag injection via `--target`. |
| 44 | Low | `git.go` — `FilterByExcludeMergedInto` | Validate regex compiles standalone before wrapping in `^(?:...)$` to prevent group-breaking patterns from bypassing auto-anchoring. 1 test case added. |

### Correctness

| # | Severity | File(s) | Summary |
|---|----------|---------|---------|
| 11 | Medium | `list.go` — `writeJSON` | Added missing `isBareClone` check so bare-clone branches export as `"remote"` in JSON (was already correct in CSV and table). |
| 20 | Medium | `git.go` — `MergedBranches`, `MergedBranchesAnywhere` | Changed `strings.Contains(name, "HEAD")` to exact match (`"origin/HEAD"` / `"HEAD"`) so legitimate branches containing "HEAD" aren't silently excluded. |
| 21 | Medium | `git.go` — `findContainer` | Pass full `"origin/" + shortC` ref for remote containers to `isTrivialAncestor`, fixing wrong rev-list resolution and cache-key collision with local branches. |
| 43 | Medium | `git.go` — `assertMergedAnywhereWithCache` | Always check remote containers for all branches (not just remote ones), matching the detection logic in `MergedBranchesAnywhere`. |
| 26 | Low | `prune.go` — batch delete fallback | Treat `"remote ref does not exist"` / `"could not find remote ref"` in retry loop as success (branch already deleted in batch). Fixes #7. |

### Windows / Darwin Compatibility

| # | Severity | File(s) | Summary |
|---|----------|---------|---------|
| 1 | Medium | `git.go` — `MergedBranchesAnywhere` | Replaced `\x1b[2K` ANSI escape in progress line with `\r` + fixed-width space padding (Windows cmd.exe compat). |
| 2 | Medium | `root.go` — `printTable` | Changed `fmt.Print`/`Println` to `fmt.Fprint(color.Output, ...)` so `go-colorable` translates ANSI on Windows. |
| 3 | Medium | `prune.go` — 6 sites | Changed `os.Stderr` to `color.Error` for colored stderr writes (Windows `go-colorable` compat). |
| 31 | Medium | `prune.go` — `loadBranchCSV` | Strip UTF-8 BOM from first line so Windows-generated CSV files parse correctly. 2 test cases added. |
| 17 | Low | `list.go`, `prune.go` | Case-insensitive file extension matching for `--output` / `--input` format auto-detection (`.JSON`, `.CSV`). |

### UX / Robustness

| # | Severity | File(s) | Summary |
|---|----------|---------|---------|
| 37 | Medium | `prune.go` — `loadBranchCSV`, `loadBranchJSON` | Parse `age_days`, `last_commit`, `relative_age`, `author` from input files so prune preview shows real data instead of "unknown". 2 test cases added. |
| 18 | Low | `git.go` — `runGit` | Return `fmt.Errorf("git %s: %w", args[0], err)` instead of empty string when git exits non-zero with no output. |
| 32 | Low | `prune.go` — `loadBranchCSV` | Error on missing `"name"` column instead of silently returning zero branches. 1 test case added. |
| 33 | Low | `prune.go` — cobra definition | Changed `Use` from `"prune [url]"` to `"prune"` since URLs are always rejected. |
| 34 | Low | `prune.go` — `RunE` | Compare input file's `remote_url` metadata against current repo and warn on mismatch. |
| 38 | Low | `list.go` — zero-branches path | Write header row for table format when no branches found (JSON/CSV already did this). |
| 39 | Low | `prune.go` — input-loading path | Warn when `FilterProtected` silently removes branches from `--input` file. |
| 45 | Low | `prune.go` — `RunE` | Reject non-URL positional arguments with helpful error suggesting `--target` instead of silently ignoring them. |
| 47 | Low | `list.go` — `RunE` | Reject non-URL positional arguments with helpful error (matching prune's #45 behaviour). |
| 23 | Low | `list.go` — `resolveOutput` | Create parent directories with `os.MkdirAll` before `os.Create` so `--output path/to/file.json` works without pre-existing dirs. |
| 24 | Info | `list.go` — `writeCSV` | Strip `\n` and `\r` from remote URL before writing CSV comment line to prevent line injection. |
| 27 | Info | `git.go` — `DefaultBranch` | Removed dead exported function that was never called anywhere in the project. |
| 29 | Low | `git.go` — `runGit` | Preserve `exec.ExitError` in error chain via `%w` when stderr/stdout has content, so callers can use `errors.As` to inspect exit codes. |
| 46 | Info | `prune.go` — `printBranchTable` | Conditionally show AUTHOR column when any branch has author data (e.g. loaded from `--input` files produced by `list --authors`). |

### Process Lifecycle / Context

| # | Severity | File(s) | Summary |
|---|----------|---------|---------|
| 5 | Low | `git.go` — `runGit`, `CloneForAnalysis` | All git subprocesses now use `exec.CommandContext` with a cancellable context. Cancelled on SIGINT/SIGTERM, killing child processes immediately. |
| 12 | Low | `root.go` — `Execute` | `cleanupFns` slice is now protected by `sync.Mutex`. Signal handler copies the slice under lock before iterating. |
| 13 | Low | `root.go` — `Execute` | Signal handler now cancels the shared context before running cleanup, terminating in-flight git processes. On Windows this releases file locks before temp-dir removal. |
| 25 | Low | `root.go` — signal handler | Temp dir cleanup now succeeds on Windows because child git processes are killed via context cancellation before `os.RemoveAll` runs. |

---

## Dismissed Issues

| # | Severity | Reason |
|---|----------|--------|
| 4 | Low | `resolveOutput` eager file truncation matches standard CLI convention (`ls > file` does the same). |
| 6 | Info | `syscall.SIGTERM` no-op on Windows is harmless; `os.Interrupt` handles Ctrl+C. |
| 8 | Low | Global mutable state is standard for single-execution CLI tools; major refactor for marginal test benefit. |
| 9 | Info | `\r` progress counter not suppressed by `--no-color` — already gated on `isTTY`, which is the correct guard. |
| 10 | Info | Hardcoded `origin` remote name is a known limitation, documented in README. Feature request, not a bug. |
| 14 | Info | Extra `progress.Add(1)` in non-TTY path is a single harmless atomic op. |
| 19 | Info | `rev-parse` without `--` — attack vector blocked by input validation (#16). |
| 28 | Low | `matchGlob` exponential backtracking is unreachable with real-world glob patterns from CLI flags. |
| 30 | Info | `enrichAge`/`firstBranchAuthor` without `--` — refs come from git's own output; attack vector blocked by validation. |
| 35 | Info | UTF-8 symbols on legacy Windows code pages — affected configurations are increasingly rare. |
| 36 | Info | `CloneForAnalysis` ANSI progress — git's own terminal detection responsibility. |
| 40 | Info | `loadBranchJSON` confusing fallback error — current error message is sufficient to diagnose the problem. |
| 41 | Info | Future commit timestamps produce negative `AgeDays` — safety behavior (excluded from deletion) is correct. |
| 48 | Info | No force-exit on second SIGINT during cleanup — child processes are already killed via context cancellation; `os.RemoveAll` on a temp dir is fast. |
