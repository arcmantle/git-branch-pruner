package git

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Branch represents a git branch with metadata.
type Branch struct {
	Name        string
	IsRemote    bool
	LastCommit  time.Time
	AgeDays     int
	RelativeAge string
	MergedInto  string // the branch this was found merged into; used for deletion verification
}

// SortField controls how branches are sorted.
type SortField string

const (
	SortByAge  SortField = "age"
	SortByName SortField = "name"
)

// runGit executes a git command in the given directory and returns trimmed stdout.
// Stderr is captured separately so it never bleeds into parsed output.
func runGit(repoPath string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
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
		return "", fmt.Errorf("%s", msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// ValidateRepo checks that repoPath (or the cwd if empty) is inside a git repository.
func ValidateRepo(repoPath string) error {
	_, err := runGit(repoPath, "rev-parse", "--git-dir")
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
func CloneForAnalysis(url, destDir string) error {
	cmd := exec.Command("git", "clone", "--bare", "--filter=tree:0", "--progress", "--", url, destDir)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// CurrentBranch returns the name of the currently checked-out branch.
func CurrentBranch(repoPath string) string {
	out, err := runGit(repoPath, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return out
}

// DefaultBranch returns the name of the default branch (main/master).
func DefaultBranch(repoPath string) (string, error) {
	// Try symbolic-ref for the remote HEAD
	out, err := runGit(repoPath, "symbolic-ref", "refs/remotes/origin/HEAD", "--short")
	if err == nil {
		parts := strings.SplitN(out, "/", 2)
		if len(parts) == 2 {
			return parts[1], nil
		}
		return out, nil
	}
	// Fallback: check if main or master exists
	for _, name := range []string{"main", "master"} {
		if _, err := runGit(repoPath, "rev-parse", "--verify", name); err == nil {
			return name, nil
		}
	}
	return "", fmt.Errorf("could not determine default branch")
}

// MergedBranches returns branches that have been merged into targetBranch.
// Each returned branch has MergedInto set to targetBranch.
func MergedBranches(repoPath, targetBranch string, includeRemote bool, sortBy SortField) ([]Branch, error) {
	var branches []Branch

	// Local merged branches
	localOut, err := runGit(repoPath, "branch", "--merged", targetBranch, "--format=%(refname:short)")
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
		remoteOut, err := runGit(repoPath, "branch", "-r", "--merged", targetBranch, "--format=%(refname:short)")
		if err != nil {
			return nil, fmt.Errorf("listing remote merged branches: %w", err)
		}
		for _, name := range splitLines(remoteOut) {
			if name == "" || strings.Contains(name, "HEAD") {
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
		enrichAge(repoPath, &branches[i])
	}
	sortBranches(branches, sortBy)
	return branches, nil
}

// MergedBranchesAnywhere returns branches whose tip commit is reachable from
// at least one OTHER existing branch, regardless of which branch that is.
// This correctly handles repos with multiple long-lived branches (hotfix/*, release/*, etc.)
// where feature branches may be merged back into any of them.
//
// Note: requires one git call per branch, so may be slower on very large repos.
func MergedBranchesAnywhere(repoPath string, includeRemote bool, sortBy SortField) ([]Branch, error) {
	type ref struct {
		name     string
		tip      string
		isRemote bool
	}

	var allRefs []ref

	// Collect local branches with their tip SHAs in one call
	localOut, err := runGit(repoPath, "branch", "--format=%(refname:short) %(objectname)")
	if err != nil {
		return nil, err
	}
	for _, line := range splitLines(localOut) {
		parts := strings.Fields(line)
		if len(parts) == 2 {
			allRefs = append(allRefs, ref{name: parts[0], tip: parts[1], isRemote: false})
		}
	}

	if includeRemote {
		remoteOut, err := runGit(repoPath, "branch", "-r", "--format=%(refname:short) %(objectname)")
		if err != nil {
			return nil, err
		}
		for _, line := range splitLines(remoteOut) {
			parts := strings.Fields(line)
			if len(parts) == 2 {
				// skip origin/HEAD (symref) — its refname:short is just "origin" (no slash)
				if !strings.Contains(parts[0], "/") {
					continue
				}
				name := strings.TrimPrefix(parts[0], "origin/")
				if strings.Contains(name, "HEAD") {
					continue
				}
				allRefs = append(allRefs, ref{name: name, tip: parts[1], isRemote: true})
			}
		}
	}

	var candidates []Branch
	total := len(allRefs)
	for i, r := range allRefs {
		fmt.Fprintf(os.Stderr, "\rAnalysing branches... %d/%d", i+1, total)
		mergedInto := findContainer(repoPath, r.name, r.tip, false)
		if mergedInto == "" && includeRemote {
			mergedInto = findContainer(repoPath, r.name, r.tip, true)
		}
		if mergedInto != "" {
			candidates = append(candidates, Branch{
				Name:       r.name,
				IsRemote:   r.isRemote,
				MergedInto: mergedInto,
			})
		}
	}
	fmt.Fprintln(os.Stderr) // newline after progress

	for i := range candidates {
		enrichAge(repoPath, &candidates[i])
	}
	sortBranches(candidates, sortBy)
	return candidates, nil
}

// isTrivialAncestor returns true if tip is on the first-parent path of container.
// This means container simply descended FROM tip (e.g. container was branched from
// the candidate, or pulled it in as a sync), rather than tip being merged INTO container.
// In that case "container contains tip" is trivially true and should not count as a merge.
func isTrivialAncestor(repoPath, tip, container string) bool {
out, err := runGit(repoPath, "rev-list", "--first-parent", container)
if err != nil {
return false
}
for _, line := range splitLines(out) {
if strings.HasPrefix(line, tip) {
return true
}
}
return false
}

// preferredBases are checked first when picking which branch to show in "merged into".
// Longer-lived / more significant branches are preferred over feature branches.
var preferredBases = []string{"main", "master", "develop", "development", "trunk"}

// findContainer returns the most meaningful branch (other than branchName itself)
// that contains the given tip commit via a real merge (not trivial ancestry).
// Prefers well-known base branches over incidental containers.
// Returns "" if no valid container is found.
func findContainer(repoPath, branchName, tip string, remote bool) string {
args := []string{"branch", "--contains", tip, "--format=%(refname:short)"}
if remote {
args = append(args[:1], append([]string{"-r"}, args[1:]...)...)
}
out, err := runGit(repoPath, args...)
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
if isTrivialAncestor(repoPath, tip, shortC) {
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

// enrichAge fills in LastCommit, AgeDays, and RelativeAge for a branch.
func enrichAge(repoPath string, b *Branch) {
	ref := b.Name
	if b.IsRemote {
		ref = "origin/" + b.Name
	}
	out, err := runGit(repoPath, "log", "-1", "--format=%ct", ref)
	if err != nil || out == "" {
		return
	}
	ts, err := strconv.ParseInt(out, 10, 64)
	if err != nil {
		return
	}
	commitTime := time.Unix(ts, 0)
	b.LastCommit = commitTime
	b.AgeDays = int(time.Since(commitTime).Hours() / 24)
	b.RelativeAge = relativeTime(commitTime)
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
		re, err := regexp.Compile(p)
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
func FilterProtected(branches []Branch, protected []string) []Branch {
	protectedSet := make(map[string]bool, len(protected))
	for _, p := range protected {
		protectedSet[strings.TrimSpace(p)] = true
	}
	var filtered []Branch
	for _, b := range branches {
		if !protectedSet[b.Name] {
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
// Supports * (any sequence within a segment) and ** (any path).
// Falls back to exact match if path.Match returns an error.
func globMatch(p, name string) bool {
	// path.Match treats / specially; use simple prefix match for "prefix/*" patterns
	if strings.HasSuffix(p, "/*") {
		prefix := strings.TrimSuffix(p, "/*")
		return name == prefix || strings.HasPrefix(name, prefix+"/")
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
//   - Branches not matching any tier are unaffected (prunable as normal).
func FilterByTierHierarchy(branches []Branch, tiers [][]string) []Branch {
	if len(tiers) == 0 {
		return branches
	}
	var filtered []Branch
	for _, b := range branches {
		tier := matchTier(b.Name, tiers)
		switch {
		case tier < 0:
			// Not in any tier — normal branch, keep it.
			filtered = append(filtered, b)
		case tier == 0:
			// Tier 0: never prunable.
		default:
			// Tier N: prunable only if merged into a branch at a lower tier.
			containerTier := matchTier(b.MergedInto, tiers)
			if containerTier >= 0 && containerTier < tier {
				filtered = append(filtered, b)
			}
		}
	}
	return filtered
}

// DeleteBranch deletes a branch, verifying it is still merged into b.MergedInto
// (or into any other branch if MergedInto is empty) before proceeding.
// Uses -D after our own verification so the deletion succeeds regardless of
// which branch is currently checked out.
func DeleteBranch(repoPath string, b Branch) error {
	if b.MergedInto != "" {
		if err := assertMergedInto(repoPath, b, b.MergedInto); err != nil {
			return err
		}
	} else {
		if err := assertMergedAnywhere(repoPath, b); err != nil {
			return err
		}
	}
	if b.IsRemote {
		_, err := runGit(repoPath, "push", "origin", "--delete", b.Name)
		return err
	}
	_, err := runGit(repoPath, "branch", "-D", b.Name)
	return err
}

// assertMergedInto verifies the branch tip is reachable from target.
func assertMergedInto(repoPath string, b Branch, target string) error {
	ref := b.Name
	if b.IsRemote {
		ref = "origin/" + b.Name
	}
	_, err := runGit(repoPath, "merge-base", "--is-ancestor", ref, target)
	if err != nil {
		return fmt.Errorf("branch %q is not fully merged into %s — refusing to delete", b.Name, target)
	}
	return nil
}

// assertMergedAnywhere verifies the branch tip is still contained in some other branch.
func assertMergedAnywhere(repoPath string, b Branch) error {
	ref := b.Name
	if b.IsRemote {
		ref = "origin/" + b.Name
	}
	tip, err := runGit(repoPath, "rev-parse", ref)
	if err != nil {
		return fmt.Errorf("could not resolve %q: %w", b.Name, err)
	}
	if container := findContainer(repoPath, b.Name, tip, false); container != "" {
		return nil
	}
	if b.IsRemote {
		if container := findContainer(repoPath, b.Name, tip, true); container != "" {
			return nil
		}
	}
	return fmt.Errorf("branch %q is no longer merged into any other branch — refusing to delete", b.Name)
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
