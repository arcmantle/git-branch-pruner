package git

import (
	"bytes"
	"fmt"
	"os/exec"
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

// MergedBranches returns branches that have been merged into the target branch,
// sorted by sortBy field descending (oldest first for age).
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
		branches = append(branches, Branch{Name: name, IsRemote: false})
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
			branches = append(branches, Branch{Name: shortName, IsRemote: true})
		}
	}

	// Enrich with age info
	for i := range branches {
		enrichAge(repoPath, &branches[i])
	}

	// Sort
	sortBranches(branches, sortBy)

	return branches, nil
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
		// Default: oldest first (highest AgeDays first)
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

// DeleteBranch deletes a branch locally or on the remote.
func DeleteBranch(repoPath string, b Branch) error {
	if b.IsRemote {
		_, err := runGit(repoPath, "push", "origin", "--delete", b.Name)
		return err
	}
	_, err := runGit(repoPath, "branch", "-d", b.Name)
	return err
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
