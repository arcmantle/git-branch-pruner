package cmd

import (
	"fmt"
	"os"

	"github.com/fatih/color"
	"github.com/arcmantle/git-branch-pruner/internal/git"
	"github.com/spf13/cobra"
)

var (
	repoPath          string
	protectedDefault  = []string{"main", "master", "develop"}
	protectedBranches []string
	noColor           bool
)

var rootCmd = &cobra.Command{
	Use:   "git-branch-pruner",
	Short: "Find and delete merged git branches",
	Long:  "A CLI tool to identify branches that have been merged and are candidates for deletion, with configurable age filtering.",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if noColor {
			color.NoColor = true
		}
		return git.ValidateRepo(repoPath)
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// resolveTarget returns the explicit target if set, otherwise auto-detects the default branch.
func resolveTarget(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	detected, err := git.DefaultBranch(repoPath)
	if err != nil {
		return "", fmt.Errorf("could not detect default branch: %w\nUse --target to specify it", err)
	}
	return detected, nil
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&repoPath, "repo", "C", "", "path to the git repository (default: current directory)")
	rootCmd.PersistentFlags().StringSliceVar(&protectedBranches, "protected", protectedDefault, "branches to never delete")
	rootCmd.PersistentFlags().BoolVar(&noColor, "no-color", false, "disable color output")
}
