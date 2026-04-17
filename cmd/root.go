package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	repoPath         string
	protectedDefault = []string{"main", "master", "develop"}
	protectedBranches []string
)

var rootCmd = &cobra.Command{
	Use:   "git-branch-pruner",
	Short: "Find and delete merged git branches",
	Long:  "A CLI tool to identify branches that have been merged and are candidates for deletion, with configurable age filtering.",
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&repoPath, "repo", "C", "", "path to the git repository (default: current directory)")
	rootCmd.PersistentFlags().StringSliceVar(&protectedBranches, "protected", protectedDefault, "branches to never delete")
}
