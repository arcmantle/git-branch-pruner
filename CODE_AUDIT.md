# Code Audit Log

## Audit #1 — 2026-04-18

### Issues Fixed

#### 1. Raw ANSI escape `\x1b[2K` in progress line (Windows incompatibility)
- **File:** `internal/git/git.go` — `MergedBranchesAnywhere`
- **Severity:** Medium
- **Problem:** The progress line used `\x1b[2K\r` (CSI "Erase in Line") to clear and redraw the counter. This escape is not supported on older Windows terminals (cmd.exe without Virtual Terminal Processing enabled), producing garbled output.
- **Fix:** Replaced with `\r` + `%-*s` (space-padding to the maximum possible line length). Since worker goroutines can report progress out-of-order, shorter strings could leave remnants; the fixed-width padding eliminates that without relying on any ANSI sequence.

#### 2. `printTable` writes colored output to raw `os.Stdout` (Windows color broken)
- **File:** `cmd/root.go` — `printTable`
- **Severity:** Medium
- **Problem:** Cells containing ANSI color codes (via `headerColor.Sprint`, `remoteColor.Sprint`, `dimColor.Sprint`) were written through `fmt.Print`/`fmt.Println`, which target raw `os.Stdout`. On Windows without VTP, `go-colorable` cannot intercept these writes to translate ANSI to console API calls, producing escape-code garbage in the output.
- **Fix:** Changed `fmt.Print`/`fmt.Println` to `fmt.Fprint(color.Output, ...)`/`fmt.Fprintln(color.Output, ...)`. `color.Output` is `colorable.NewColorableStdout()`, which transparently translates ANSI codes on Windows.

#### 3. Colored stderr writes bypass `go-colorable` (Windows color broken)
- **File:** `cmd/prune.go` — 6 occurrences
- **Severity:** Medium
- **Problem:** `warnColor.Fprintf(os.Stderr, ...)` and `errorColor.Fprintf(os.Stderr, ...)` write ANSI-colored strings to raw `os.Stderr`. Same root cause as #2 — on Windows without VTP, the ANSI codes render as visible garbage.
- **Fix:** Changed `os.Stderr` to `color.Error` (`colorable.NewColorableStderr()`) in all six call sites.

---

### Issues Identified (Not Yet Fixed)

#### 4. `resolveOutput` truncates target file eagerly
- **File:** `cmd/list.go` — `resolveOutput`
- **Severity:** Low
- **Problem:** `os.Create(path)` truncates the file immediately. If the command later fails, the user's original file is lost. Standard CLI behavior (e.g. `ls > file`) but could be safer.
- **Recommendation:** Write to a temp file in the same directory and atomically rename on success. Low priority since this matches common CLI conventions.

#### 5. No timeout on `exec.Command` git calls
- **File:** `internal/git/git.go` — `runGit`, `CloneForAnalysis`
- **Severity:** Low
- **Problem:** All git subprocesses are spawned without a context timeout. A hung git process (e.g. waiting for a credential helper, network issue) blocks the tool indefinitely.
- **Recommendation:** Use `exec.CommandContext` with a generous timeout (e.g. 5 minutes for normal commands). `CloneForAnalysis` intentionally streams progress and may need a longer or no timeout.

#### 6. `syscall.SIGTERM` is a no-op on Windows
- **File:** `cmd/root.go` — `Execute`
- **Severity:** Info
- **Problem:** `signal.Notify(sig, os.Interrupt, syscall.SIGTERM)` — `SIGTERM` is defined but never delivered on Windows. The `os.Interrupt` handler still works for Ctrl+C, so temp-dir cleanup functions correctly on Windows. No action needed; noted for documentation.

#### 7. Batch remote delete retry may double-report success
- **File:** `cmd/prune.go` — batch delete fallback
- **Severity:** Low
- **Problem:** If `BatchDeleteRemoteBranches` partially succeeds (some branches deleted, some not), the individual retry loop re-attempts all branches. Already-deleted branches will fail with "remote ref does not exist", which is reported as a failure even though the branch was successfully removed.
- **Recommendation:** In the retry loop, distinguish "remote ref does not exist" errors from genuine failures, or check existence before retrying.

#### 8. Package-level mutable state makes tests fragile
- **File:** `cmd/root.go`, `cmd/list.go`, `cmd/prune.go`
- **Severity:** Low
- **Problem:** Global variables (`repoPath`, `isBareClone`, `protectedBranches`, `excludeMergedInto`, `tierPatterns`, `tiersShorthand`) are mutated at runtime and in tests. Tests that modify these must manually reset them, which is fragile. Not an issue for a CLI tool with single-execution semantics, but could cause test races if `t.Parallel()` is ever used.
- **Recommendation:** Consider passing configuration through a struct or function parameters. Low priority.

#### 9. `--no-color` does not suppress the progress counter `\r` output
- **File:** `internal/git/git.go` — `MergedBranchesAnywhere`
- **Severity:** Info
- **Problem:** When `--no-color` is set, `color.NoColor` is true and `fatih/color` suppresses ANSI color codes. However, the progress counter still emits `\r` carriage returns to stderr. This is not strictly a "color" issue, but some users setting `--no-color` might also expect no terminal control characters. The progress is already gated on `isTTY`, so piped output is unaffected.
- **Recommendation:** Optionally gate progress output on `!color.NoColor` as well. Very low priority since `isTTY` is the primary guard.

#### 10. Hardcoded `origin` remote name
- **File:** `internal/git/git.go`, `cmd/root.go` (documented in README)
- **Severity:** Info
- **Problem:** Remote branch operations (`branch -r`, `push origin --delete`, `remote get-url origin`) assume the remote is named `origin`. Repos using a different primary remote name are unsupported.
- **Recommendation:** Already documented in README. Could be made configurable via a `--remote-name` flag in the future.

---

## Audit #2 — 2026-04-18

### Issues Fixed

#### 11. `writeJSON` missing `isBareClone` check (branch type mismatch in JSON export)
- **File:** `cmd/list.go` — `writeJSON`
- **Severity:** Medium
- **Problem:** `writeCSV` and `buildRows` both use `b.IsRemote || isBareClone` to determine branch type, correctly marking branches from bare clones (remote URL analysis) as `"remote"`. However, `writeJSON` only checked `b.IsRemote`, causing branches to be written as `"local"` in JSON exports from bare clones. If a user ran `list <url> --format json --output branches.json` and later imported with `prune --input branches.json`, the branches would be incorrectly treated as local, causing deletion failures or targeting wrong refs.
- **Fix:** Changed `if b.IsRemote` to `if b.IsRemote || isBareClone` in `writeJSON`, matching the logic in `writeCSV` and `buildRows`.

---

### Issues Identified (Not Yet Fixed)

#### 12. `cleanupFns` slice not protected by synchronization
- **File:** `cmd/root.go` — `Execute`, `PersistentPreRunE`
- **Severity:** Low
- **Problem:** The `cleanupFns` slice is appended to in `PersistentPreRunE` (main goroutine) and read in the signal-handler goroutine. While in practice the append completes before the signal handler could fire (the user would have to press Ctrl+C during the sub-millisecond append window), there is no formal happens-before edge between the two goroutines under Go's memory model. `go vet -race` would flag this.
- **Recommendation:** Protect `cleanupFns` with a `sync.Mutex`, or restructure so the signal handler reads the slice only after it is fully built (e.g. start the signal goroutine after `PersistentPreRunE` returns).

#### 13. Signal handler does not kill child git processes
- **File:** `cmd/root.go` — `Execute`
- **Severity:** Low
- **Problem:** When the user presses Ctrl+C, the signal handler runs cleanup functions and calls `os.Exit(130)`, but does not explicitly terminate any in-flight `exec.Command` git processes. On POSIX systems the child processes typically receive SIGINT from the terminal's process group and terminate. On Windows, child processes spawned via `exec.Command` may not receive the console Ctrl+C event, potentially leaving orphaned `git` processes. This is related to audit item #5 (no context/timeout).
- **Recommendation:** Use `exec.CommandContext` with a cancellable context. Cancel the context in the signal handler before running cleanup. This would address both this issue and #5 in one change.

#### 14. Unnecessary `progress.Add(1)` in non-TTY branch
- **File:** `internal/git/git.go` — `MergedBranchesAnywhere`
- **Severity:** Info
- **Problem:** When `isTTY` is false, the worker goroutine still calls `progress.Add(1)` even though the counter value is never read or displayed. This is harmless but adds an unnecessary atomic operation per branch.
- **Recommendation:** Remove the `else` branch, or keep it as-is for defensive consistency. Very low priority.

---

## Audit #3 — 2026-04-18

### Issues Fixed

#### 15. Missing `--` separator in git commands that accept user-controlled refs (argument injection)
- **Files:** `internal/git/git.go` — `DeleteLocalBranch`, `BatchDeleteRemoteBranches`, `assertMergedInto`
- **Severity:** Medium (Security)
- **Problem:** Branch names loaded via `--input` (CSV/JSON files) are passed directly as arguments to git subcommands (`git branch -d`, `git push origin --delete`, `git merge-base --is-ancestor`) without a `--` end-of-options separator. A crafted input file containing "branch names" starting with `-` (e.g. `--all`, `--mirror`, `--force`) could be interpreted as git flags rather than ref names. While git itself rejects `-`-prefixed branch names, the `--input` path loads arbitrary strings from external files, bypassing git's own naming rules.
- **Fix:** Added `"--"` separator before user-controlled ref arguments in `DeleteLocalBranch`, `BatchDeleteRemoteBranches`, and `assertMergedInto`. This ensures all subsequent arguments are treated as literal ref names regardless of their content.

#### 16. No validation of branch names from `--input` files
- **Files:** `cmd/prune.go` — `loadBranchCSV`, `loadBranchJSON`
- **Severity:** Medium (Security)
- **Problem:** Branch names and `merged_into` values loaded from user-provided CSV/JSON files were used without validation. Since git branch names cannot start with `-`, any such value in an input file is either malformed or malicious. Combined with issue #15, this was the primary entry point for argument injection.
- **Fix:** Added validation in both `loadBranchCSV` and `loadBranchJSON` that rejects branch names and `merged_into` values starting with `-`, returning a clear error message. Added 4 corresponding test cases.

#### 17. Case-sensitive file extension detection (Windows/Darwin incompatibility)
- **Files:** `cmd/list.go` — format auto-detection; `cmd/prune.go` — `loadBranchFile`
- **Severity:** Low
- **Problem:** Format auto-detection for `--output` (in `list`) and format detection for `--input` (in `prune`) used case-sensitive `strings.HasSuffix` checks (`.json`, `.csv`). On Windows, file extensions like `.JSON` and `.CSV` are common, and on macOS, users may encounter case-varied extensions from other tools. These would silently fall through to the default behavior (table format for `--output`, "unsupported format" error for `--input`).
- **Fix:** Changed `list` auto-detection to use `strings.ToLower(filepath.Ext(...))` and `loadBranchFile` to use `strings.ToLower(path)` before suffix matching.

#### 18. `runGit` returns empty error message when git exits non-zero with no output
- **File:** `internal/git/git.go` — `runGit`
- **Severity:** Low
- **Problem:** When a git command exits with a non-zero status but produces no stderr or stdout output, `runGit` returned `fmt.Errorf("%s", "")` — a non-nil error with an empty string. Callers wrapping this with `%w` would produce unhelpful messages like `"listing local merged branches: "` with no indication of what went wrong.
- **Fix:** Added a fallback that wraps the original `exec.ExitError` with the git subcommand name when both stderr and stdout are empty: `fmt.Errorf("git %s: %w", args[0], err)`. This preserves the exit code information and produces messages like `"git branch: exit status 128"`.

---

### Issues Identified (Not Yet Fixed)

#### 19. `rev-parse` in `assertMergedAnywhereWithCache` has no `--` protection
- **File:** `internal/git/git.go` — `assertMergedAnywhereWithCache`
- **Severity:** Low
- **Problem:** `runGit(repoPath, "rev-parse", ref)` passes the ref directly. Unlike other git subcommands, `rev-parse` treats `--` as a revision/path separator rather than end-of-options, so adding `--` would change the semantics (resolving as a path instead of a ref). The branch name validation added in issue #16 prevents `-`-prefixed names from reaching this code path, so it is protected indirectly.
- **Recommendation:** If additional hardening is desired, use fully-qualified ref paths (`refs/heads/NAME` for local, `refs/remotes/origin/NAME` for remote) instead of short names. Low priority since the loader validation blocks the attack vector.
