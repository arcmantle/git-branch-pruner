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

---

## Audit #4 — 2026-04-18

### Issues Fixed

#### 20. `strings.Contains(name, "HEAD")` incorrectly filters branches with "HEAD" in their name
- **File:** `internal/git/git.go` — `MergedBranches`, `MergedBranchesAnywhere`
- **Severity:** Medium
- **Problem:** The remote-branch filtering in both `MergedBranches` and `MergedBranchesAnywhere` used `strings.Contains(name, "HEAD")` to skip the `origin/HEAD` symbolic ref. However, this also silently excluded any legitimate branch containing "HEAD" as a substring — e.g. `feature/HEAD-refactor`, `fix/myHEADer`, `origin/update-HEAD-handling`. These branches would never appear in merged-branch results, preventing them from being listed or pruned.
- **Fix:** Changed to exact match: `name == "origin/HEAD"` in `MergedBranches` (before prefix trimming) and `name == "HEAD"` in `MergedBranchesAnywhere` (after prefix trimming).

#### 21. `findContainer` uses short name for remote container `isTrivialAncestor` check (cache key collision + wrong ref)
- **File:** `internal/git/git.go` — `findContainer`
- **Severity:** Medium
- **Problem:** When `findContainer` checks remote containers (`remote=true`), it trims `origin/` from container names (e.g. `origin/release/1.0` → `release/1.0`) and passes the short name to `isTrivialAncestor`. This caused two problems:
  1. **Wrong ref for rev-list:** `rev-list --first-parent release/1.0` resolves to the *local* `release/1.0` branch (if it exists), not `origin/release/1.0`. When local and remote refs are at different commits, the first-parent chain is computed from the wrong branch.
  2. **Cache poisoning:** The `firstParentCache` uses the ref name as key. If a remote-only branch `origin/foo` was checked first (failing with an empty SHA set because local `foo` doesn't exist), the cache entry for `"foo"` is permanently set to empty via `sync.Once`. Any subsequent local-branch check for `foo` would hit the poisoned cache entry and skip the trivial-ancestor filter entirely, potentially reporting incorrect merge relationships.
- **Fix:** When `remote=true`, pass `"origin/" + shortC` as the ref to `isTrivialAncestor` instead of `shortC`. This ensures `rev-list` resolves the correct remote-tracking ref and the cache key is distinct from the local branch entry.

#### 22. `MergedBranches` passes `targetBranch` as separate argument to `--merged` (flag injection via `--target`)
- **File:** `internal/git/git.go` — `MergedBranches`
- **Severity:** Low (Security)
- **Problem:** `runGit(repoPath, "branch", "--merged", targetBranch, ...)` passes `targetBranch` as a separate argument. If a user passed `--target "--delete"`, git would interpret `--delete` as a flag to `git branch` rather than as the `--merged` argument. The `targetBranch` comes from the user's own `--target` CLI flag (not from untrusted files), so exploitation requires the user to attack themselves, but it's still inconsistent with the hardening in audit items #15/#16.
- **Fix:** Changed both local and remote invocations to use the `--merged=<value>` joined form (e.g. `"--merged=" + targetBranch`), which git always parses as a single option+value token regardless of the value's content.

---

### Issues Identified (Not Yet Fixed)

#### 23. `resolveOutput` does not create parent directories
- **File:** `cmd/list.go` — `resolveOutput`
- **Severity:** Low
- **Problem:** `os.Create(path)` fails with a confusing "no such file or directory" error if the parent directory doesn't exist. Users running `list --output path/to/branches.json` where `path/to/` doesn't exist get an unhelpful error.
- **Recommendation:** Either call `os.MkdirAll` on the parent directory before creating the file, or wrap the error with a hint about the missing directory.

#### 24. `writeCSV` metadata comment does not sanitize newlines in remote URL
- **File:** `cmd/list.go` — `writeCSV`
- **Severity:** Info
- **Problem:** `fmt.Fprintf(w, "# remote_url: %s\n", r)` writes the remote URL into a CSV comment line. If the URL somehow contained a newline (unusual but possible with crafted git configs), it could inject additional lines before the CSV header row, potentially confusing `loadBranchCSV` on re-import.
- **Recommendation:** Strip or replace newlines in the remote URL meta value. Very low priority since `git remote get-url` won't normally return newlines.

#### 25. Temp directory cleanup may fail on Windows when child git processes hold file locks
- **Files:** `cmd/root.go` — signal handler, `PersistentPreRunE`
- **Severity:** Low (Windows-specific)
- **Problem:** When analysing a remote URL, the tool clones into a temp directory and cleans up via `os.RemoveAll`. On Windows, files held open by child `git` processes cannot be deleted. Combined with audit item #13 (signal handler does not kill child git processes), pressing Ctrl+C during a clone or analysis may leave orphaned git processes that lock the temp directory, causing cleanup to fail silently. On Darwin/Linux, `unlink` succeeds even while files are open.
- **Recommendation:** Address audit item #13 (use `exec.CommandContext` with a cancellable context) which would fix both the orphaned process issue and the Windows cleanup issue together.

---

## Audit #5 — 2026-04-18

### Issues Fixed

#### 26. Batch remote delete retry reports already-deleted branches as failures (implements fix for #7)
- **File:** `cmd/prune.go` — batch delete fallback loop
- **Severity:** Low
- **Problem:** When `BatchDeleteRemoteBranches` partially succeeds (some branches deleted, some rejected), the individual retry loop re-attempts **all** branches including those already deleted. Git responds with `"remote ref does not exist"` for already-deleted branches, which was reported to the user as `✗ Failed to delete ...` — making it appear that more branches failed than actually did. The `deleted` counter would undercount and the error count would overcount.
- **Fix:** In the retry loop, check if the error message contains `"remote ref does not exist"` or `"could not find remote ref"`. If so, the branch was successfully removed in the initial batch push, and it is counted as a successful deletion rather than a failure.

---

### Issues Identified (Not Yet Fixed)

#### 27. `DefaultBranch` function is exported but never called (dead code)
- **File:** `internal/git/git.go` — `DefaultBranch`
- **Severity:** Info
- **Problem:** The `DefaultBranch` function is exported and implemented (tries `symbolic-ref refs/remotes/origin/HEAD`, then falls back to checking for `main`/`master`) but is never called anywhere in the project. No command or internal function references it. It appears to have been written for future use or was used in an earlier version and left behind.
- **Recommendation:** Either remove the function to reduce maintenance surface, or document its intent for external consumers importing the `git` package. If kept, it should also check remote-tracking refs (`origin/main`, `origin/master`) as a fallback for bare clones and detached HEAD states.

#### 28. `matchGlob` recursive backtracking has exponential worst-case complexity
- **File:** `internal/git/git.go` — `matchGlob`
- **Severity:** Low
- **Problem:** The `matchGlob` function uses recursive backtracking to match `*` wildcards. For pathological patterns with multiple consecutive wildcards (e.g. `a*a*a*a*b` matched against `aaaa...a`), the time complexity is O(2^n) where n is the input length. Since glob patterns come from user-controlled `--protected` and `--tier` CLI flags (not from untrusted input files), this is not a security issue, but a user could inadvertently cause a hang with an unusual pattern.
- **Recommendation:** Replace with an iterative two-pointer algorithm or use Go's `path.Match` (which has O(n·m) complexity) with a custom `/` handling wrapper. Very low priority since typical patterns like `release/*` are matched in linear time.

#### 29. `runGit` discards typed `exec.ExitError` when stderr/stdout has content
- **File:** `internal/git/git.go` — `runGit`
- **Severity:** Low
- **Problem:** When a git command fails and produces stderr or stdout output, `runGit` returns `fmt.Errorf("%s", msg)` — a plain string error that discards the original `exec.ExitError`. Only when both outputs are empty does it wrap with `%w` (preserving the exit code). This means callers cannot use `errors.As` to inspect the exit code. For example, `merge-base --is-ancestor` exits 1 for "not an ancestor" and 128 for "ref doesn't exist", but both are reported identically as the stderr text. Audit item #18 fixed the empty-output case but did not address this broader issue.
- **Recommendation:** Change the non-empty case to `fmt.Errorf("%s: %w", msg, err)` so the original `exec.ExitError` is preserved in the error chain. This would append `": exit status N"` to error messages shown to users, which is acceptable and useful for debugging. Callers that need to distinguish failure modes could then use `errors.As`.

#### 30. `enrichAge` and `firstBranchAuthor` git commands have no end-of-options protection before ref arguments
- **Files:** `internal/git/git.go` — `enrichAge`, `firstBranchAuthor`
- **Severity:** Info
- **Problem:** `enrichAge` passes a ref name to `git log -1 --format=... <ref>` and `firstBranchAuthor` passes refs/SHAs to `merge-base` and `log` without end-of-options markers. Unlike `git branch -d` or `git push --delete` (hardened in audit #15), `git log` interprets `--` as a revision/path separator — adding `--` before a ref would cause it to be treated as a file path, changing semantics entirely. These functions are not vulnerable because: (a) branch names always originate from git's own `branch --format=%(refname:short)` output (never from `--input` files), and (b) the input validation from audit #16 blocks `-`-prefixed names before they could reach these code paths. However, the lack of protection is inconsistent with the hardening philosophy applied elsewhere.
- **Recommendation:** Use fully-qualified ref paths (`refs/heads/NAME` for local, `refs/remotes/origin/NAME` for remote) instead of short names. This eliminates any ambiguity regardless of the ref content, without relying on `--` which has different semantics per git subcommand. Low priority since the attack vector is blocked by input validation.

---

## Audit #6 — 2026-04-18

### Issues Fixed

#### 31. UTF-8 BOM in CSV files corrupts header parsing (Windows incompatibility)
- **Files:** `cmd/prune.go` — `loadBranchCSV`
- **Severity:** Medium
- **Problem:** CSV files created on Windows by tools like Excel, PowerShell `Export-Csv`, or other editors often include a UTF-8 BOM (bytes `0xEF 0xBB 0xBF`) at the start. The BOM was not stripped, causing two failures depending on file structure:
  1. If the file starts with a `#` comment line, the BOM prepends to `#`, so `strings.HasPrefix(line, "#")` still matches and the BOM is discarded with the comment. No issue in this case.
  2. If the file starts directly with the header row, the BOM prepends to the first column name, turning `"name"` into `"\xef\xbb\xbfname"`. The column index lookup `idx["name"]` then fails, and every row's name resolves to `""`, causing all rows to be silently skipped. The user sees "No merged branches found." with no indication the CSV was misread.
- **Fix:** Added `stripBOM()` helper that removes a UTF-8 BOM from the first line read by the scanner. Applied to the first line unconditionally before comment/header processing. Added 2 test cases (BOM before comment line, BOM before header row).

#### 32. `loadBranchCSV` silently returns zero branches when `name` column is missing
- **Files:** `cmd/prune.go` — `loadBranchCSV`
- **Severity:** Low
- **Problem:** If a CSV file uses different column names (e.g. `"branch"` instead of `"name"`), the `col(row, "name")` helper returns `""` for every row, and every row is skipped via the `name == ""` continue guard. The user receives "No merged branches found." with no indication that the CSV format was wrong. This is confusing when the file clearly contains data.
- **Fix:** After building the column index from the header row, explicitly check that the `"name"` key exists. If missing, return a clear error: `CSV header missing required "name" column (got: ...)`. Added 1 test case.

#### 33. `prune` command `Use` string says `prune [url]` but URLs are always rejected
- **Files:** `cmd/prune.go` — cobra command definition
- **Severity:** Low (UX)
- **Problem:** The `Use` field was `"prune [url]"`, which cobra displays in `--help` output and auto-generated docs. Since the prune command immediately rejects URL arguments (both in `PersistentPreRunE` and in `RunE`), the `[url]` placeholder is misleading — users see it in help and attempt to use it, only to get an error.
- **Fix:** Changed `Use` to `"prune"`. The `Long` description already explains that prune does not support URLs and directs users to `list <url>`.

#### 34. `--input` file may have been generated for a different repository (no URL verification)
- **Files:** `cmd/prune.go` — prune `RunE`
- **Severity:** Low (Safety)
- **Problem:** When using `prune --input`, the file's `remote_url` metadata (present in both CSV comments and JSON `meta` object) was loaded but never compared to the current repository. If a user accidentally ran `prune --input branches.json` in the wrong repository, and that repo happened to have identically-named branches that are also merged, those branches would be silently deleted. The `VerifyMerged` check would pass (the branches are genuinely merged in the current repo), but the user's intent was to prune a different repo.
- **Fix:** `loadBranchFile`, `loadBranchCSV`, and `loadBranchJSON` now return the parsed metadata alongside branches. After loading, the prune command compares the file's `remote_url` against `git.RemoteURL(repoPath)`. If both are non-empty and differ, a warning is printed to stderr: `⚠ Input file was generated for <file-url> but current repo remote is <current-url>`. This is a warning (not an error) to allow intentional cross-repo use while alerting accidental misuse. Added meta extraction from CSV `#` comment lines and JSON `meta` object. Existing tests updated for new return signatures; meta value assertions added to `TestLoadBranchCSV` and `TestLoadBranchJSON_Wrapper`.

---

### Issues Identified (Not Yet Fixed)

#### 35. UTF-8 emoji characters (⚠, ✓, ✗) may not render on older Windows terminals
- **Files:** `cmd/prune.go` — success/error/warning messages
- **Severity:** Info (Windows-specific)
- **Problem:** The prune command uses Unicode emoji for status indicators: `✓` (U+2713), `✗` (U+2717), `⚠` (U+26A0). On Windows with `cmd.exe` using legacy code pages (not UTF-8 code page 65001), these characters render as `?` or garbage. Modern Windows Terminal (default since Windows 11) and `cmd.exe` on Windows 10 1903+ with UTF-8 enabled handle them correctly. On macOS (Darwin), UTF-8 is the default and these always render correctly.
- **Recommendation:** Either detect the console code page on Windows and fall back to ASCII equivalents (`[OK]`, `[FAIL]`, `[WARN]`), or document the UTF-8 requirement. Very low priority since the affected Windows configurations are increasingly rare.

#### 36. `CloneForAnalysis` sends `--progress` output through raw `os.Stderr` on Windows
- **Files:** `internal/git/git.go` — `CloneForAnalysis`
- **Severity:** Info (Windows-specific)
- **Problem:** `cmd.Stderr = os.Stderr` with `--progress` flag means git's clone progress (which uses `\r` and ANSI codes for line rewriting) goes directly to the terminal. On Windows `cmd.exe` without VTP, git's own ANSI codes in progress output could produce garbled text. Git usually auto-detects terminal capabilities, but `--progress` forces output regardless.
- **Recommendation:** Could pipe through `color.Error` (`go-colorable`) or let git auto-detect by removing `--progress` and relying on git's isatty check. Very low priority since git's own progress rendering is git's responsibility, and the tool explicitly requires git to be installed.
