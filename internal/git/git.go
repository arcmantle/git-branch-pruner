package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mattn/go-isatty"
)

// Branch represents a git branch with metadata.
type Branch struct {
	Name        string
	IsRemote    bool
	LastCommit  time.Time
	AgeDays     int
	RelativeAge string
	MergedInto  string // the branch this was found merged into; used for deletion verification
	SHA         string // full 40-char tip commit hash
	Author      string // author of the first commit unique to this branch (optional, requires --authors)
}

// SortField controls how branches are sorted.
type SortField string

const (
	SortByAge  SortField = "age"
	SortByName SortField = "name"
)

// runGit executes a git command in the given directory and returns trimmed stdout.
// Stderr is captured separately so it never bleeds into parsed output.
// The command is cancelled when ctx is done, killing the child process.
func runGit(ctx context.Context, repoPath string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if repoPath != "" {
		cmd.Dir = repoPath
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			return "", fmt.Errorf("git %s: %w", args[0], err)
		}
		return "", fmt.Errorf("%s: %w", msg, err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// ValidateRepo checks that repoPath (or the cwd if empty) is inside a git repository.
func ValidateRepo(ctx context.Context, repoPath string) error {
	_, err := runGit(ctx, repoPath, "rev-parse", "--git-dir")
	if err != nil {
		dir := repoPath
		if dir == "" {
			dir = "current directory"
		}
		return fmt.Errorf("%s is not a git repository", dir)
	}
	return nil
}

// CloneForAnalysis does a treeless bare clone of url into destDir.
// --filter=tree:0 downloads only commit objects (no trees, no blobs) which is
// sufficient for all ancestry queries this tool performs (branch --contains,
// merge-base --is-ancestor, log --format). Much faster than blobless on large repos.
func CloneForAnalysis(ctx context.Context, url, destDir string) error {
	cmd := exec.CommandContext(ctx, "git", "clone", "--bare", "--filter=tree:0", "--progress", "--", url, destDir)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// RemoteURL returns the fetch URL for the origin remote, or "" if not set.
func RemoteURL(ctx context.Context, repoPath string) string {
	out, err := runGit(ctx, repoPath, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return out
}

// CurrentBranch returns the name of the currently checked-out branch.
func CurrentBranch(ctx context.Context, repoPath string) string {
	out, err := runGit(ctx, repoPath, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return out
}

// MergedBranches returns branches that have been merged into targetBranch.
// Each returned branch has MergedInto set to targetBranch.
func MergedBranches(ctx context.Context, repoPath, targetBranch string, includeRemote bool, sortBy SortField) ([]Branch, error) {
	var branches []Branch

	// Local merged branches
	// Use --merged=<value> (not --merged <value>) so a "-"-prefixed target
	// cannot be misinterpreted as a git flag.
	localOut, err := runGit(ctx, repoPath, "branch", "--merged="+targetBranch, "--format=%(refname:short)")
	if err != nil {
		return nil, fmt.Errorf("listing local merged branches: %w", err)
	}
	for _, name := range splitLines(localOut) {
		if name == "" || name == targetBranch {
			continue
		}
		branches = append(branches, Branch{Name: name, IsRemote: false, MergedInto: targetBranch})
	}

	// Remote merged branches
	if includeRemote {
		remoteOut, err := runGit(ctx, repoPath, "branch", "-r", "--merged="+targetBranch, "--format=%(refname:short)")
		if err != nil {
			return nil, fmt.Errorf("listing remote merged branches: %w", err)
		}
		for _, name := range splitLines(remoteOut) {
			if name == "" || name == "origin/HEAD" {
				continue
			}
			shortName := strings.TrimPrefix(name, "origin/")
			if shortName == targetBranch {
				continue
			}
			branches = append(branches, Branch{Name: shortName, IsRemote: true, MergedInto: targetBranch})
		}
	}

	for i := range branches {
		enrichAge(ctx, repoPath, &branches[i])
	}
	sortBranches(branches, sortBy)
	return branches, nil
}

// MergedBranchesAnywhere returns branches whose tip commit is reachable from
// at least one OTHER existing branch, regardless of which branch that is.
// This correctly handles repos with multiple long-lived branches (hotfix/*, release/*, etc.)
// where feature branches may be merged back into any of them.
func MergedBranchesAnywhere(ctx context.Context, repoPath string, includeRemote bool, sortBy SortField) ([]Branch, error) {
	type ref struct {
		idx      int
		name     string
		tip      string
		isRemote bool
	}

	var allRefs []ref

	localOut, err := runGit(ctx, repoPath, "branch", "--format=%(refname:short) %(objectname)")
	if err != nil {
		return nil, err
	}
	for _, line := range splitLines(localOut) {
		parts := strings.Fields(line)
		if len(parts) == 2 {
			allRefs = append(allRefs, ref{idx: len(allRefs), name: parts[0], tip: parts[1], isRemote: false})
		}
	}

	if includeRemote {
		remoteOut, err := runGit(ctx, repoPath, "branch", "-r", "--format=%(refname:short) %(objectname)")
		if err != nil {
			return nil, err
		}
		for _, line := range splitLines(remoteOut) {
			parts := strings.Fields(line)
			if len(parts) == 2 {
				if !strings.Contains(parts[0], "/") {
					continue
				}
				name := strings.TrimPrefix(parts[0], "origin/")
				if name == "HEAD" {
					continue
				}
				allRefs = append(allRefs, ref{idx: len(allRefs), name: name, tip: parts[1], isRemote: true})
			}
		}
	}

	total := len(allRefs)
	fpc := newFirstParentCache()

	type result struct {
		branch Branch
		found  bool
	}
	results := make([]result, total)

	workers := runtime.NumCPU()
	if workers > 8 {
		workers = 8
	}

	// Fixed worker pool: exactly `workers` goroutines pull jobs from a buffered channel.
	// Much cheaper than spawning one goroutine per branch (which would park most of them
	// waiting on a semaphore for large repos).
	jobs := make(chan ref, workers*2)
	var wg sync.WaitGroup
	var progress atomic.Int32
	var progressMu sync.Mutex
	isTTY := isatty.IsTerminal(os.Stderr.Fd()) || isatty.IsCygwinTerminal(os.Stderr.Fd())
	// Pre-compute max line length so \r + space-padding can fully overwrite
	// previous output without relying on ANSI \x1b[2K (unsupported on some
	// Windows terminals).
	maxProgressLen := len(fmt.Sprintf("Analysing branches... %d/%d", total, total))

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := range jobs {
				mergedInto := findContainer(ctx, repoPath, r.name, r.tip, false, fpc)
				if mergedInto == "" && includeRemote {
					mergedInto = findContainer(ctx, repoPath, r.name, r.tip, true, fpc)
				}
				if mergedInto != "" {
					results[r.idx] = result{
						branch: Branch{Name: r.name, IsRemote: r.isRemote, MergedInto: mergedInto},
						found:  true,
					}
				}
				if isTTY {
					n := progress.Add(1)
					progressMu.Lock()
					fmt.Fprintf(os.Stderr, "\r%-*s", maxProgressLen, fmt.Sprintf("Analysing branches... %d/%d", n, total))
					progressMu.Unlock()
				} else {
					progress.Add(1)
				}
			}
		}()
	}

	for _, r := range allRefs {
		jobs <- r
	}
	close(jobs)
	wg.Wait()
	if isTTY {
		fmt.Fprintln(os.Stderr)
	}

	var candidates []Branch
	for _, res := range results {
		if res.found {
			candidates = append(candidates, res.branch)
		}
	}

	// Enrich age with the same fixed worker pool pattern.
	enrichJobs := make(chan int, workers*2)
	var enrichWg sync.WaitGroup
	for range workers {
		enrichWg.Add(1)
		go func() {
			defer enrichWg.Done()
			for i := range enrichJobs {
				enrichAge(ctx, repoPath, &candidates[i])
			}
		}()
	}
	for i := range candidates {
		enrichJobs <- i
	}
	close(enrichJobs)
	enrichWg.Wait()

	sortBranches(candidates, sortBy)
	return candidates, nil
}

// firstParentEntry holds the cached result and a sync.Once to ensure the
// expensive rev-list is computed exactly once per container branch.
type firstParentEntry struct {
	once sync.Once
	shas map[string]bool
}

// firstParentCache caches the set of first-parent commit SHAs per container branch.
// Computing this per container once (rather than per candidate) is a major speedup:
// a container with 100k commits would otherwise be rev-listed once per candidate.
type firstParentCache struct {
	mu    sync.Mutex
	cache map[string]*firstParentEntry
}

func newFirstParentCache() *firstParentCache {
	return &firstParentCache{cache: make(map[string]*firstParentEntry)}
}

// FirstParentCache is an opaque cache for first-parent ancestry queries.
// Create one with NewFirstParentCache and pass it to VerifyMerged
// to amortise expensive rev-list calls across many deletions.
type FirstParentCache = firstParentCache

// NewFirstParentCache returns an empty, ready-to-use FirstParentCache.
func NewFirstParentCache() *FirstParentCache {
	return newFirstParentCache()
}

// isTrivialAncestor returns true if tip is on the first-parent path of container.
// The first-parent list is computed exactly once per container via sync.Once,
// preventing redundant rev-list calls when multiple goroutines query the same container.
func (c *firstParentCache) isTrivialAncestor(ctx context.Context, repoPath, tip, container string) bool {
	c.mu.Lock()
	entry, ok := c.cache[container]
	if !ok {
		entry = &firstParentEntry{}
		c.cache[container] = entry
	}
	c.mu.Unlock()

	entry.once.Do(func() {
		out, err := runGit(ctx, repoPath, "rev-list", "--first-parent", container)
		entry.shas = make(map[string]bool)
		if err == nil {
			for _, line := range splitLines(out) {
				if line != "" {
					entry.shas[line] = true
				}
			}
		}
	})
	return entry.shas[tip]
}

// preferredBases are checked first when picking which branch to show in "merged into".
// Longer-lived / more significant branches are preferred over feature branches.
var preferredBases = []string{"main", "master", "develop", "development", "trunk"}

// findContainer returns the most meaningful branch (other than branchName itself)
// that contains the given tip commit via a real merge (not trivial ancestry).
// Prefers well-known base branches over incidental containers.
// Returns "" if no valid container is found.
func findContainer(ctx context.Context, repoPath, branchName, tip string, remote bool, fpc *firstParentCache) string {
	var args []string
	if remote {
		args = []string{"branch", "-r", "--contains", tip, "--format=%(refname:short)"}
	} else {
		args = []string{"branch", "--contains", tip, "--format=%(refname:short)"}
	}
	out, err := runGit(ctx, repoPath, args...)
	if err != nil {
		return ""
	}

	var containers []string
	for _, c := range splitLines(out) {
		shortC := strings.TrimPrefix(c, "origin/")
		if shortC == "" || shortC == branchName {
			continue
		}
		// Skip this container if the candidate's tip is on its first-parent path —
		// that means container descended from candidate, not that candidate was merged in.
		// Use the full ref for remote containers so rev-list resolves the correct branch
		// and the cache key doesn't collide with local branches of the same short name.
		ancestorRef := shortC
		if remote {
			ancestorRef = "origin/" + shortC
		}
		if fpc.isTrivialAncestor(ctx, repoPath, tip, ancestorRef) {
			continue
		}
		containers = append(containers, shortC)
	}
	if len(containers) == 0 {
		return ""
	}

	// Prefer well-known base branches
	for _, preferred := range preferredBases {
		for _, c := range containers {
			if c == preferred {
				return c
			}
		}
	}
	// Prefer long-lived branch patterns (release/*, hotfix/*, support/*)
	for _, c := range containers {
		lower := strings.ToLower(c)
		if strings.HasPrefix(lower, "release/") ||
			strings.HasPrefix(lower, "hotfix/") ||
			strings.HasPrefix(lower, "support/") ||
			strings.HasPrefix(lower, "maintenance/") {
			return c
		}
	}
	return containers[0]
}

// enrichAge fills in LastCommit, AgeDays, RelativeAge, and SHA for a branch.
func enrichAge(ctx context.Context, repoPath string, b *Branch) {
	ref := b.Name
	if b.IsRemote {
		ref = "origin/" + b.Name
	}
	out, err := runGit(ctx, repoPath, "log", "-1", "--format=%ct %H", ref)
	if err != nil || out == "" {
		return
	}
	fields := strings.Fields(out)
	if len(fields) >= 1 {
		ts, err := strconv.ParseInt(fields[0], 10, 64)
		if err == nil {
			commitTime := time.Unix(ts, 0)
			b.LastCommit = commitTime
			b.AgeDays = int(time.Since(commitTime).Hours() / 24)
			b.RelativeAge = relativeTime(commitTime)
		}
	}
	if len(fields) >= 2 {
		b.SHA = fields[1]
	}
}

// EnrichAuthors populates the Author field for each branch using the first commit
// that is unique to that branch (via --ancestry-path from the merge-base with MergedInto).
// Runs in parallel using the same worker pool pattern as MergedBranchesAnywhere.
func EnrichAuthors(ctx context.Context, repoPath string, branches []Branch) {
	workers := runtime.NumCPU()
	if workers > 8 {
		workers = 8
	}
	jobs := make(chan int, workers*2)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				branches[i].Author = firstBranchAuthor(ctx, repoPath, &branches[i])
			}
		}()
	}
	for i := range branches {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
}

// firstBranchAuthor returns the author name of the first commit unique to this branch.
//
// For squash/rebase merges the branch tip is NOT in the target's history, so we can
// compute a real merge-base and walk --ancestry-path to find the oldest branch-unique
// commit.
//
// For regular merge commits the tip IS already reachable from the target branch
// (merge-base == tip), making the log range empty.  In that case we fall back to the
// tip commit's author, which is the most recent person to work on the branch and a
// reliable proxy for ownership.
func firstBranchAuthor(ctx context.Context, repoPath string, b *Branch) string {
	if b.SHA == "" {
		return ""
	}
	base := b.MergedInto
	if base == "" {
		base = "HEAD"
	}

	mergeBase, err := runGit(ctx, repoPath, "merge-base", b.SHA, base)
	if err == nil && mergeBase != "" {
		if mergeBase != b.SHA {
			// Tip is not directly in the target's history (squash / rebase merge).
			// Walk the branch-unique commits in chronological order and take the first.
			out, logErr := runGit(ctx, repoPath, "log", "--ancestry-path", "--reverse",
				"--format=%an", mergeBase+".."+b.SHA)
			if logErr == nil && out != "" {
				return strings.SplitN(out, "\n", 2)[0]
			}
		}
	}

	// Fallback (regular merge commit, or any error above): author of the tip commit.
	out, err := runGit(ctx, repoPath, "log", "-1", "--format=%an", b.SHA)
	if err != nil {
		return ""
	}
	return out
}

// relativeTime returns a human-readable relative time string (e.g. "3 months ago").
func relativeTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		return plural(m, "minute") + " ago"
	case d < 24*time.Hour:
		h := int(d.Hours())
		return plural(h, "hour") + " ago"
	case d < 30*24*time.Hour:
		days := int(d.Hours() / 24)
		return plural(days, "day") + " ago"
	case d < 365*24*time.Hour:
		months := int(d.Hours() / 24 / 30)
		return plural(months, "month") + " ago"
	default:
		years := int(d.Hours() / 24 / 365)
		return plural(years, "year") + " ago"
	}
}

func plural(n int, unit string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", unit)
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// ValidateSortField returns an error if s is not a recognised SortField.
func ValidateSortField(s string) error {
	switch SortField(s) {
	case SortByAge, SortByName:
		return nil
	default:
		return fmt.Errorf("invalid --sort value %q: must be \"age\" or \"name\"", s)
	}
}

func sortBranches(branches []Branch, by SortField) {
	sort.Slice(branches, func(i, j int) bool {
		if by == SortByName {
			return branches[i].Name < branches[j].Name
		}
		return branches[i].AgeDays > branches[j].AgeDays
	})
}

// FilterByAge returns only branches older than the given number of days.
func FilterByAge(branches []Branch, olderThanDays int) []Branch {
	if olderThanDays <= 0 {
		return branches
	}
	var filtered []Branch
	for _, b := range branches {
		if b.AgeDays >= olderThanDays {
			filtered = append(filtered, b)
		}
	}
	return filtered
}

// FilterByExcludeMergedInto removes branches whose MergedInto field matches
// any of the given regex patterns. Returns an error if any pattern is invalid.
// Example patterns: "release/26\.0\.0", "release/.*", "hotfix/.*"
func FilterByExcludeMergedInto(branches []Branch, patterns []string) ([]Branch, error) {
	if len(patterns) == 0 {
		return branches, nil
	}
	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		// Validate the pattern compiles on its own before wrapping with anchors.
		// This prevents patterns like "a)|(b" which are invalid standalone but
		// become valid (with broken anchoring) when wrapped in "^(?:...)$".
		if _, err := regexp.Compile(p); err != nil {
			return nil, fmt.Errorf("invalid --exclude-merged-into pattern %q: %w", p, err)
		}
		// Auto-anchor so that "main" doesn't match "domain" or "mainline".
		re, err := regexp.Compile("^(?:" + p + ")$")
		if err != nil {
			return nil, fmt.Errorf("invalid --exclude-merged-into pattern %q: %w", p, err)
		}
		compiled = append(compiled, re)
	}
	var filtered []Branch
	for _, b := range branches {
		excluded := false
		for _, re := range compiled {
			if re.MatchString(b.MergedInto) {
				excluded = true
				break
			}
		}
		if !excluded {
			filtered = append(filtered, b)
		}
	}
	return filtered, nil
}

// FilterProtected removes protected branch names from the list.
// Supports glob patterns (e.g. "release/*") for consistency with --tier.
func FilterProtected(branches []Branch, protected []string) []Branch {
	trimmed := make([]string, 0, len(protected))
	for _, p := range protected {
		if t := strings.TrimSpace(p); t != "" {
			trimmed = append(trimmed, t)
		}
	}
	var filtered []Branch
	for _, b := range branches {
		matched := false
		for _, p := range trimmed {
			if globMatch(p, b.Name) {
				matched = true
				break
			}
		}
		if !matched {
			filtered = append(filtered, b)
		}
	}
	return filtered
}

// matchTier returns the tier index (0-based) of the first tier whose patterns
// match name, or -1 if no tier matches. Each tier is a slice of glob patterns.
func matchTier(name string, tiers [][]string) int {
	for i, patterns := range tiers {
		for _, p := range patterns {
			if globMatch(p, name) {
				return i
			}
		}
	}
	return -1
}

// globMatch reports whether name matches the glob pattern p.
// Supports * (any sequence within a segment) and ? (single non-/ char).
// The special suffix /* matches the exact prefix and any sub-path beneath it
// (e.g. "release/*" matches "release/1.0" and "release/1.0/patch").
// Falls back to exact match if the pattern contains unsupported syntax.
func globMatch(p, name string) bool {
	// path.Match treats / specially; use simple prefix match for "prefix/*" patterns
	if strings.HasSuffix(p, "/*") {
		prefix := strings.TrimSuffix(p, "/*")
		return strings.HasPrefix(name, prefix+"/")
	}
	matched, err := matchGlob(p, name)
	if err != nil {
		return p == name
	}
	return matched
}

// matchGlob is a simple glob matcher supporting * and ? but not [].
func matchGlob(pattern, name string) (bool, error) {
	// Use Go's standard path.Match semantics (no OS separator issues)
	pi, ni := 0, 0
	for pi < len(pattern) && ni < len(name) {
		switch pattern[pi] {
		case '*':
			// match any sequence of non-/ chars
			pi++
			if pi == len(pattern) {
				return !strings.Contains(name[ni:], "/"), nil
			}
			for ni <= len(name) {
				if ok, _ := matchGlob(pattern[pi:], name[ni:]); ok {
					return true, nil
				}
				if ni < len(name) && name[ni] == '/' {
					return false, nil
				}
				ni++
			}
			return false, nil
		case '?':
			if name[ni] == '/' {
				return false, nil
			}
			pi++
			ni++
		default:
			if pattern[pi] != name[ni] {
				return false, nil
			}
			pi++
			ni++
		}
	}
	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}
	return pi == len(pattern) && ni == len(name), nil
}

// FilterByTierHierarchy enforces a branch protection hierarchy.
// Each element of tiers is a slice of glob patterns at that tier level (0 = highest).
//
//   - Tier 0 branches are never prunable.
//   - Tier N (N>0) branches are prunable only when merged into a branch at tier < N.
//   - Branches not in any tier are treated as an implicit lowest tier: prunable only
//     if merged into a branch that IS in an explicit tier (any tier level).
//     This prevents e.g. task/foo merged into task/bar from being prunable until
//     the work reaches a named tier branch like main or release/*.
func FilterByTierHierarchy(branches []Branch, tiers [][]string) []Branch {
	if len(tiers) == 0 {
		return branches
	}
	var filtered []Branch
	for _, b := range branches {
		tier := matchTier(b.Name, tiers)
		switch {
		case tier < 0:
			// Implicit lowest tier: prunable only if merged into an explicit tier branch.
			if matchTier(b.MergedInto, tiers) >= 0 {
				filtered = append(filtered, b)
			}
		case tier == 0:
			// Tier 0: never prunable.
		default:
			// Tier N: prunable only if merged into a branch at a lower tier number.
			containerTier := matchTier(b.MergedInto, tiers)
			if containerTier >= 0 && containerTier < tier {
				filtered = append(filtered, b)
			}
		}
	}
	return filtered
}

// VerifyMerged checks that b is still merged (into b.MergedInto, or anywhere if empty)
// without deleting it. Use this to pre-validate remote branches before a batch push.
func VerifyMerged(ctx context.Context, repoPath string, b Branch, fpc *FirstParentCache) error {
	if b.MergedInto != "" {
		return assertMergedInto(ctx, repoPath, b, b.MergedInto)
	}
	return assertMergedAnywhereWithCache(ctx, repoPath, b, fpc)
}

// DeleteLocalBranch deletes a local branch using -d (safe delete).
// Git's own merge check provides a second safety net on top of VerifyMerged.
func DeleteLocalBranch(ctx context.Context, repoPath string, b Branch) error {
	_, err := runGit(ctx, repoPath, "branch", "-d", "--", b.Name)
	return err
}

// BatchDeleteRemoteBranches deletes all named remote branches in a single
// "git push origin --delete" call, which is much faster than one call per branch.
// Returns an error if the push fails; on failure the caller should fall back to
// individual deletions to identify which specific branches could not be removed.
func BatchDeleteRemoteBranches(ctx context.Context, repoPath string, names []string) error {
	if len(names) == 0 {
		return nil
	}
	args := []string{"push", "origin", "--delete", "--"}
	args = append(args, names...)
	_, err := runGit(ctx, repoPath, args...)
	return err
}

// assertMergedInto verifies the branch tip is reachable from target.
func assertMergedInto(ctx context.Context, repoPath string, b Branch, target string) error {
	ref := b.Name
	if b.IsRemote {
		ref = "origin/" + b.Name
	}
	_, err := runGit(ctx, repoPath, "merge-base", "--is-ancestor", "--", ref, target)
	if err != nil {
		return fmt.Errorf("branch %q is not fully merged into %s — refusing to delete", b.Name, target)
	}
	return nil
}

func assertMergedAnywhereWithCache(ctx context.Context, repoPath string, b Branch, fpc *firstParentCache) error {
	ref := b.Name
	if b.IsRemote {
		ref = "origin/" + b.Name
	}
	tip, err := runGit(ctx, repoPath, "rev-parse", ref)
	if err != nil {
		return fmt.Errorf("could not resolve %q: %w", b.Name, err)
	}
	if container := findContainer(ctx, repoPath, b.Name, tip, false, fpc); container != "" {
		return nil
	}
	if container := findContainer(ctx, repoPath, b.Name, tip, true, fpc); container != "" {
		return nil
	}
	return fmt.Errorf("branch %q is no longer merged into any other branch — refusing to delete", b.Name)
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
