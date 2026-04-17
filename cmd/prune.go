package cmd

import (
"bufio"
"fmt"
"os"
"strings"
"text/tabwriter"

"github.com/arcmantle/git-branch-pruner/internal/git"
"github.com/fatih/color"
"github.com/spf13/cobra"
)

var (
pruneOlderThan int
pruneRemote    bool
pruneDryRun    bool
pruneNoConfirm bool
pruneTarget    string
pruneSort      string
pruneJSON      bool
)

var (
successColor = color.New(color.FgGreen)
errorColor   = color.New(color.FgRed)
warnColor    = color.New(color.FgYellow)
boldColor    = color.New(color.Bold)
)

var pruneCmd = &cobra.Command{
Use:   "prune",
Short: "Delete merged branches",
RunE: func(cmd *cobra.Command, args []string) error {
target, err := resolveTarget(pruneTarget)
if err != nil {
return err
}

currentBranch := git.CurrentBranch(repoPath)

branches, err := git.MergedBranches(repoPath, target, pruneRemote, git.SortField(pruneSort))
if err != nil {
return err
}
branches = git.FilterProtected(branches, protectedBranches)
branches = git.FilterByAge(branches, pruneOlderThan)

// Separate out currently checked-out branch (can't delete it)
var skipped []git.Branch
var deletable []git.Branch
for _, b := range branches {
if !b.IsRemote && b.Name == currentBranch {
skipped = append(skipped, b)
} else {
deletable = append(deletable, b)
}
}

if len(skipped) > 0 {
warnColor.Fprintf(os.Stderr, "⚠ Skipping currently checked-out branch: %s\n\n", skipped[0].Name)
}

if len(deletable) == 0 {
fmt.Print("No merged branches found")
fmt.Print(filterSuffix(pruneOlderThan, pruneRemote))
fmt.Println(".")
return nil
}

// Show table preview
boldColor.Printf("The following %d branch(es) will be deleted:\n\n", len(deletable))
w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
headerColor.Fprintf(w, "BRANCH\tTYPE\tAGE\tLAST COMMIT\n")
for _, b := range deletable {
bType := "local"
if b.IsRemote {
bType = remoteColor.Sprint("remote")
}
lastCommit := dimColor.Sprint("unknown")
relAge := dimColor.Sprint("unknown")
if !b.LastCommit.IsZero() {
lastCommit = b.LastCommit.Format("2006-01-02")
relAge = b.RelativeAge
}
fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", b.Name, bType, relAge, lastCommit)
}
w.Flush()
fmt.Println()

if pruneDryRun {
if pruneJSON {
return printJSON(deletable)
}
warnColor.Println("Dry run — no branches were deleted.")
return nil
}

if pruneJSON {
return printJSON(deletable)
}

// Confirm unless --no-confirm
if !pruneNoConfirm {
fmt.Print("Proceed with deletion? [y/N] ")
reader := bufio.NewReader(os.Stdin)
input, _ := reader.ReadString('\n')
input = strings.TrimSpace(strings.ToLower(input))
if input != "y" && input != "yes" {
fmt.Println("Aborted.")
return nil
}
}

// Delete branches
var errs []string
deleted := 0
for _, b := range deletable {
bType := "local"
if b.IsRemote {
bType = "remote"
}
if err := git.DeleteBranch(repoPath, b); err != nil {
errs = append(errs, fmt.Sprintf("%s (%s): %v", b.Name, bType, err))
errorColor.Fprintf(os.Stderr, "  ✗ Failed to delete %s (%s): %v\n", b.Name, bType, err)
} else {
successColor.Printf("  ✓ Deleted %s (%s)\n", b.Name, bType)
deleted++
}
}

fmt.Printf("\n%d/%d branch(es) deleted.\n", deleted, len(deletable))
if len(errs) > 0 {
return fmt.Errorf("%d branch(es) failed to delete", len(errs))
}
return nil
},
}

func init() {
pruneCmd.Flags().IntVar(&pruneOlderThan, "older-than", 0, "only delete branches older than N days")
pruneCmd.Flags().BoolVar(&pruneRemote, "remote", false, "include remote branches")
pruneCmd.Flags().BoolVar(&pruneDryRun, "dry-run", false, "show what would be deleted without deleting")
pruneCmd.Flags().BoolVar(&pruneNoConfirm, "no-confirm", false, "skip confirmation prompt")
pruneCmd.Flags().StringVar(&pruneTarget, "target", "", "target branch to check merges against (default: auto-detect)")
pruneCmd.Flags().StringVar(&pruneSort, "sort", "age", "sort order: age (oldest first) or name")
pruneCmd.Flags().BoolVar(&pruneJSON, "json", false, "output candidate list as JSON (implies --dry-run)")
rootCmd.AddCommand(pruneCmd)
}
