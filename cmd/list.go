package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/arcmantle/git-branch-pruner/internal/git"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var (
	listOlderThan int
	listRemote    bool
	listTarget    string
	listSort      string
	listJSON      bool
)

var (
	headerColor = color.New(color.Bold)
	remoteColor = color.New(color.FgYellow)
	dimColor    = color.New(color.Faint)
)

var listCmd = &cobra.Command{
	Use:   "list [url]",
	Short: "List merged branches that are candidates for deletion",
	Long: `List branches that have been merged and are safe to delete.

By default, finds branches merged into ANY other existing branch — this correctly
handles repos with multiple long-lived branches (hotfix/*, release/*, support/*).

Use --target to narrow the search to branches merged into a specific branch only.

A remote URL can be passed as an argument — the repository will be cloned
automatically (blobless bare clone) and cleaned up after the command finishes.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		includeRemote := listRemote || isBareClone
		branches, err := resolveBranches(listTarget, includeRemote, git.SortField(listSort))
		if err != nil {
			return err
		}
		branches = git.FilterProtected(branches, protectedBranches)
		branches = git.FilterByAge(branches, listOlderThan)
		branches = git.FilterByTierHierarchy(branches, parseTiers())

		var filterErr error
		branches, filterErr = git.FilterByExcludeMergedInto(branches, excludeMergedInto)
		if filterErr != nil {
			return filterErr
		}

		if len(branches) == 0 {
			fmt.Print("No merged branches found")
			fmt.Print(filterSuffix(listTarget, listOlderThan, includeRemote))
			fmt.Println(".")
			return nil
		}

		if listJSON {
			return printJSON(branches)
		}

		var rows [][]string
		for _, b := range branches {
			bType := "local"
			if b.IsRemote || isBareClone {
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
			rows = append(rows, []string{b.Name, bType, mergedInto, relAge, lastCommit})
		}
		printTable([]string{"BRANCH", "TYPE", "MERGED INTO", "AGE", "LAST COMMIT"}, rows)

		fmt.Printf("\n%d branch(es) found", len(branches))
		fmt.Print(filterSuffix(listTarget, listOlderThan, includeRemote))
		fmt.Println(".")
		return nil
	},
}

type jsonBranch struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	MergedInto  string `json:"merged_into,omitempty"`
	AgeDays     int    `json:"age_days"`
	RelativeAge string `json:"relative_age"`
	LastCommit  string `json:"last_commit,omitempty"`
}

func printJSON(branches []git.Branch) error {
	out := make([]jsonBranch, len(branches))
	for i, b := range branches {
		bType := "local"
		if b.IsRemote {
			bType = "remote"
		}
		jb := jsonBranch{
			Name:        b.Name,
			Type:        bType,
			MergedInto:  b.MergedInto,
			AgeDays:     b.AgeDays,
			RelativeAge: b.RelativeAge,
		}
		if !b.LastCommit.IsZero() {
			jb.LastCommit = b.LastCommit.Format("2006-01-02")
		}
		out[i] = jb
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// filterSuffix builds a human-readable description of active filters.
func filterSuffix(target string, olderThan int, remote bool) string {
	var parts []string
	if target != "" {
		parts = append(parts, "merged into "+target)
	}
	if olderThan > 0 {
		parts = append(parts, fmt.Sprintf("older than %d days", olderThan))
	}
	if !remote {
		parts = append(parts, "local only")
	}
	for _, p := range excludeMergedInto {
		parts = append(parts, "excluding merged-into \""+p+"\"")
	}
	if len(parts) == 0 {
		return ""
	}
	s := " ("
	for i, p := range parts {
		if i > 0 {
			s += ", "
		}
		s += p
	}
	return s + ")"
}

func init() {
	listCmd.Flags().IntVar(&listOlderThan, "older-than", 0, "only show branches older than N days")
	listCmd.Flags().BoolVar(&listRemote, "remote", false, "include remote branches")
	listCmd.Flags().StringVar(&listTarget, "target", "", "narrow to branches merged into this specific branch only")
	listCmd.Flags().StringVar(&listSort, "sort", "age", "sort order: age (oldest first) or name")
	listCmd.Flags().BoolVar(&listJSON, "json", false, "output as JSON")
	rootCmd.AddCommand(listCmd)
}
