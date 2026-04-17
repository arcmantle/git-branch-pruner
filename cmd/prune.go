package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/roen/git-branch-pruner/internal/git"
	"github.com/spf13/cobra"
)

var (
	pruneOlderThan int
	pruneRemote    bool
	pruneDryRun    bool
	pruneNoConfirm bool
	pruneTarget    string
)

var pruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Delete merged branches",
	RunE: func(cmd *cobra.Command, args []string) error {
		target := pruneTarget
		if target == "" {
			detected, err := git.DefaultBranch(repoPath)
			if err != nil {
				return fmt.Errorf("could not detect default branch: %w\nUse --target to specify it", err)
			}
			target = detected
		}

		branches, err := git.MergedBranches(repoPath, target, pruneRemote)
		if err != nil {
			return err
		}
		branches = git.FilterProtected(branches, protectedBranches)
		branches = git.FilterByAge(branches, pruneOlderThan)

		if len(branches) == 0 {
			fmt.Println("No merged branches found matching the criteria.")
			return nil
		}

		// Show what will be deleted
		fmt.Printf("The following %d branch(es) will be deleted:\n\n", len(branches))
		for _, b := range branches {
			bType := "local"
			if b.IsRemote {
				bType = "remote"
			}
			fmt.Printf("  • %s (%s, %d days old)\n", b.Name, bType, b.AgeDays)
		}
		fmt.Println()

		if pruneDryRun {
			fmt.Println("Dry run — no branches were deleted.")
			return nil
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
		var errors []string
		deleted := 0
		for _, b := range branches {
			bType := "local"
			if b.IsRemote {
				bType = "remote"
			}
			if err := git.DeleteBranch(repoPath, b); err != nil {
				errors = append(errors, fmt.Sprintf("  ✗ %s (%s): %v", b.Name, bType, err))
			} else {
				fmt.Printf("  ✓ Deleted %s (%s)\n", b.Name, bType)
				deleted++
			}
		}

		fmt.Printf("\n%d/%d branch(es) deleted.\n", deleted, len(branches))
		if len(errors) > 0 {
			fmt.Fprintln(os.Stderr, "\nErrors:")
			for _, e := range errors {
				fmt.Fprintln(os.Stderr, e)
			}
			return fmt.Errorf("%d branch(es) failed to delete", len(errors))
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
	rootCmd.AddCommand(pruneCmd)
}
