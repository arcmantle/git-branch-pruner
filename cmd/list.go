package cmd

import (
"encoding/json"
"fmt"
"os"
"text/tabwriter"

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
Use:   "list",
Short: "List merged branches that are candidates for deletion",
RunE: func(cmd *cobra.Command, args []string) error {
target, err := resolveTarget(listTarget)
if err != nil {
return err
}

branches, err := git.MergedBranches(repoPath, target, listRemote, git.SortField(listSort))
if err != nil {
return err
}
branches = git.FilterProtected(branches, protectedBranches)
branches = git.FilterByAge(branches, listOlderThan)

if len(branches) == 0 {
fmt.Print("No merged branches found")
fmt.Print(filterSuffix(listOlderThan, listRemote))
fmt.Println(".")
return nil
}

if listJSON {
return printJSON(branches)
}

w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
headerColor.Fprintf(w, "BRANCH\tTYPE\tAGE\tLAST COMMIT\n")
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
fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", b.Name, bType, relAge, lastCommit)
}
w.Flush()

fmt.Printf("\n%d branch(es) found", len(branches))
fmt.Print(filterSuffix(listOlderThan, listRemote))
fmt.Println(".")
return nil
},
}

type jsonBranch struct {
Name        string `json:"name"`
Type        string `json:"type"`
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
func filterSuffix(olderThan int, remote bool) string {
var parts []string
if olderThan > 0 {
parts = append(parts, fmt.Sprintf("older than %d days", olderThan))
}
if !remote {
parts = append(parts, "local only")
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
listCmd.Flags().StringVar(&listTarget, "target", "", "target branch to check merges against (default: auto-detect)")
listCmd.Flags().StringVar(&listSort, "sort", "age", "sort order: age (oldest first) or name")
listCmd.Flags().BoolVar(&listJSON, "json", false, "output as JSON")
rootCmd.AddCommand(listCmd)
}
