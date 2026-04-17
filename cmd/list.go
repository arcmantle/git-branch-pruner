package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/roen/git-branch-pruner/internal/git"
	"github.com/spf13/cobra"
)

var (
	listOlderThan int
	listRemote    bool
	listTarget    string
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List merged branches that are candidates for deletion",
	RunE: func(cmd *cobra.Command, args []string) error {
		target := listTarget
		if target == "" {
			detected, err := git.DefaultBranch(repoPath)
			if err != nil {
				return fmt.Errorf("could not detect default branch: %w\nUse --target to specify it", err)
			}
			target = detected
		}

		branches, err := git.MergedBranches(repoPath, target, listRemote)
		if err != nil {
			return err
		}
		branches = git.FilterProtected(branches, protectedBranches)
		branches = git.FilterByAge(branches, listOlderThan)

		if len(branches) == 0 {
			fmt.Println("No merged branches found matching the criteria.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintf(w, "BRANCH\tTYPE\tAGE (DAYS)\tLAST COMMIT\n")
		for _, b := range branches {
			bType := "local"
			if b.IsRemote {
				bType = "remote"
			}
			lastCommit := "unknown"
			if !b.MergedAt.IsZero() {
				lastCommit = b.MergedAt.Format("2006-01-02")
			}
			fmt.Fprintf(w, "%s\t%s\t%d\t%s\n", b.Name, bType, b.AgeDays, lastCommit)
		}
		w.Flush()

		fmt.Fprintf(os.Stderr, "\n%d branch(es) found.\n", len(branches))
		return nil
	},
}

func init() {
	listCmd.Flags().IntVar(&listOlderThan, "older-than", 0, "only show branches older than N days")
	listCmd.Flags().BoolVar(&listRemote, "remote", false, "include remote branches")
	listCmd.Flags().StringVar(&listTarget, "target", "", "target branch to check merges against (default: auto-detect)")
	rootCmd.AddCommand(listCmd)
}
