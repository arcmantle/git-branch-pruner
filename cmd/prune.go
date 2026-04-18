package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/arcmantle/git-branch-pruner/internal/git"
	"github.com/fatih/color"
	"github.com/mattn/go-isatty"
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
	Use:   "prune [url]",
	Short: "Delete merged branches",
	Long: `Delete branches that have been merged and are safe to remove.

By default, finds branches merged into ANY other existing branch — this correctly
handles repos with multiple long-lived branches (hotfix/*, release/*, support/*).

Use --target to narrow deletion to branches merged into a specific branch only.

Note: prune does not support remote URLs. Use 'list <url>' to analyse a remote
repository without cloning it yourself.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if isBareClone {
			return fmt.Errorf("prune is not supported for remote URLs — use 'list <url>' to analyse a remote repository")
		}

		// --json implies read-only: treat as dry-run
		if pruneJSON {
			pruneDryRun = true
		}

		currentBranch := git.CurrentBranch(repoPath)

		branches, err := resolveBranches(pruneTarget, pruneRemote, git.SortField(pruneSort))
		if err != nil {
			return err
		}
		branches = git.FilterProtected(branches, protectedBranches)
		branches = git.FilterByAge(branches, pruneOlderThan)
		branches = git.FilterByTierHierarchy(branches, parseTiers())

		var filterErr error
		branches, filterErr = git.FilterByExcludeMergedInto(branches, excludeMergedInto)
		if filterErr != nil {
			return filterErr
		}

		// Separate out currently checked-out branch (git refuses to delete it)
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
			fmt.Print(filterSuffix(pruneTarget, pruneOlderThan, pruneRemote))
			fmt.Println(".")
			return nil
		}

		// --json: output candidates as JSON without any destructive action
		if pruneJSON {
			return writeJSON(os.Stdout, deletable)
		}

		// Show table preview
		if pruneDryRun {
			warnColor.Printf("Dry run — the following %d branch(es) would be deleted:\n\n", len(deletable))
		} else {
			boldColor.Printf("The following %d branch(es) will be deleted:\n\n", len(deletable))
		}
		printBranchTable(deletable)
		fmt.Println()

		if pruneDryRun {
			warnColor.Println("Dry run — no branches were deleted.")
			return nil
		}

		// Safety: refuse to proceed if stdin is not a terminal and --no-confirm wasn't explicit
		if !pruneNoConfirm && !isatty.IsTerminal(os.Stdin.Fd()) && !isatty.IsCygwinTerminal(os.Stdin.Fd()) {
			return fmt.Errorf("stdin is not a terminal; use --dry-run to preview, or --no-confirm to bypass this check in scripts")
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

		// Delete branches — each branch carries its MergedInto target for verification
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

func printBranchTable(branches []git.Branch) {
	var rows [][]string
	for _, b := range branches {
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
		mergedInto := dimColor.Sprint("(any)")
		if b.MergedInto != "" {
			mergedInto = b.MergedInto
		}
		sha := dimColor.Sprint("unknown")
		if b.ShortSHA != "" {
			sha = b.ShortSHA
		}
		rows = append(rows, []string{b.Name, bType, mergedInto, relAge, lastCommit, sha})
	}
	printTable([]string{"BRANCH", "TYPE", "MERGED INTO", "AGE", "LAST COMMIT", "SHA"}, rows)
}

func init() {
	pruneCmd.Flags().IntVar(&pruneOlderThan, "older-than", 0, "only delete branches older than N days")
	pruneCmd.Flags().BoolVar(&pruneRemote, "remote", false, "include remote branches")
	pruneCmd.Flags().BoolVar(&pruneDryRun, "dry-run", false, "show what would be deleted without deleting")
	pruneCmd.Flags().BoolVar(&pruneNoConfirm, "no-confirm", false, "skip confirmation prompt (required when stdin is not a terminal)")
	pruneCmd.Flags().StringVar(&pruneTarget, "target", "", "narrow to branches merged into this specific branch only")
	pruneCmd.Flags().StringVar(&pruneSort, "sort", "age", "sort order: age (oldest first) or name")
	pruneCmd.Flags().BoolVar(&pruneJSON, "json", false, "output candidate list as JSON without deleting anything")
	rootCmd.AddCommand(pruneCmd)
}
