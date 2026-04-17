package git

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Branch represents a git branch with metadata.
type Branch struct {
	Name     string
	IsRemote bool
	MergedAt time.Time // approximated from last commit date
	AgeDays  int
}

// runGit executes a git command in the given directory and returns trimmed stdout.
func runGit(repoPath string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if repoPath != "" {
		cmd.Dir = repoPath
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %s (%w)", strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
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

// MergedBranches returns branches that have been merged into the target branch.
func MergedBranches(repoPath, targetBranch string, includeRemote bool) ([]Branch, error) {
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
			if name == "" {
				continue
			}
			// Skip HEAD pointer and the target branch on remote
			if strings.Contains(name, "HEAD") {
				continue
			}
			// Strip origin/ prefix for display, but track as remote
			shortName := name
			if strings.HasPrefix(name, "origin/") {
				shortName = strings.TrimPrefix(name, "origin/")
			}
			if shortName == targetBranch {
				continue
			}
			branches = append(branches, Branch{Name: shortName, IsRemote: true})
		}
	}

	// Enrich with age info
	for i := range branches {
		age, lastCommit, err := branchAge(repoPath, &branches[i])
		if err == nil {
			branches[i].AgeDays = age
			branches[i].MergedAt = lastCommit
		}
	}

	return branches, nil
}

// branchAge returns the age in days and the last commit date of a branch.
func branchAge(repoPath string, b *Branch) (int, time.Time, error) {
	ref := b.Name
	if b.IsRemote {
		ref = "origin/" + b.Name
	}
	out, err := runGit(repoPath, "log", "-1", "--format=%ct", ref)
	if err != nil {
		return 0, time.Time{}, err
	}
	ts, err := strconv.ParseInt(out, 10, 64)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("parsing timestamp %q: %w", out, err)
	}
	commitTime := time.Unix(ts, 0)
	days := int(time.Since(commitTime).Hours() / 24)
	return days, commitTime, nil
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
		protectedSet[p] = true
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
