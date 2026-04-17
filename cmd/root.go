package cmd

import (
"fmt"
"os"
"regexp"
"strings"

"github.com/arcmantle/git-branch-pruner/internal/git"
"github.com/fatih/color"
"github.com/spf13/cobra"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// visibleLen returns the printable length of s, ignoring ANSI escape codes.
func visibleLen(s string) int {
return len(ansiRe.ReplaceAllString(s, ""))
}

// printTable prints headers (plain text, rendered bold) and pre-colored rows.
// Column widths are based on visible length so ANSI codes do not affect alignment.
func printTable(headers []string, rows [][]string) {
const colPad = 2
n := len(headers)
widths := make([]int, n)
for i, h := range headers {
widths[i] = len(h)
}
for _, row := range rows {
for j := 0; j < n && j < len(row); j++ {
if w := visibleLen(row[j]); w > widths[j] {
widths[j] = w
}
}
}
for i, h := range headers {
cell := headerColor.Sprint(h)
if i < n-1 {
fmt.Print(cell + strings.Repeat(" ", widths[i]+colPad-len(h)))
} else {
fmt.Println(cell)
}
}
for _, row := range rows {
for j, cell := range row {
if j < n-1 {
fmt.Print(cell + strings.Repeat(" ", widths[j]+colPad-visibleLen(cell)))
} else {
fmt.Println(cell)
}
}
}
}

var urlPrefixes = []string{"https://", "http://", "git://", "ssh://", "git@"}

// isURL returns true if s looks like a remote git URL.
func isURL(s string) bool {
for _, p := range urlPrefixes {
if strings.HasPrefix(s, p) {
return true
}
}
return false
}

var (
repoPath          string
protectedDefault  = []string{"main", "master", "develop"}
protectedBranches []string
noColor           bool
excludeMergedInto []string
tierPatterns      []string // raw --tier values; parsed into tiers [][]string at use
isBareClone       bool
cleanupFns        []func()
)

var rootCmd = &cobra.Command{
Use:   "git-branch-pruner",
Short: "Find and delete merged git branches",
Long:  "A CLI tool to identify branches that have been merged and are candidates for deletion, with configurable age filtering.",
PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
if noColor {
color.NoColor = true
}
// If the first positional arg looks like a URL, clone it to a temp bare repo.
if len(args) > 0 && isURL(args[0]) {
url := args[0]
tmpDir, err := os.MkdirTemp("", "git-branch-pruner-*")
if err != nil {
return fmt.Errorf("creating temp dir: %w", err)
}
cleanupFns = append(cleanupFns, func() { os.RemoveAll(tmpDir) })
fmt.Fprintf(os.Stderr, "Cloning %s ...\n", url)
if err := git.CloneForAnalysis(url, tmpDir); err != nil {
return fmt.Errorf("cloning repository: %w", err)
}
repoPath = tmpDir
isBareClone = true
return nil
}
return git.ValidateRepo(repoPath)
},
}

func Execute() {
defer func() {
for _, fn := range cleanupFns {
fn()
}
}()
if err := rootCmd.Execute(); err != nil {
fmt.Fprintln(os.Stderr, err)
os.Exit(1)
}
}

// resolveBranches returns merged branch candidates.
// Default: branches merged into ANY other branch.
// When --target is set: only branches merged into that specific branch.
func resolveBranches(target string, includeRemote bool, sortBy git.SortField) ([]git.Branch, error) {
if target != "" {
return git.MergedBranches(repoPath, target, includeRemote, sortBy)
}
return git.MergedBranchesAnywhere(repoPath, includeRemote, sortBy)
}

// parseTiers converts the raw --tier string values into [][]string.
// Each --tier value is a comma-separated list of glob patterns at that level.
func parseTiers() [][]string {
if len(tierPatterns) == 0 {
return nil
}
tiers := make([][]string, len(tierPatterns))
for i, raw := range tierPatterns {
for _, p := range strings.Split(raw, ",") {
p = strings.TrimSpace(p)
if p != "" {
tiers[i] = append(tiers[i], p)
}
}
}
return tiers
}

func init() {
rootCmd.PersistentFlags().StringVarP(&repoPath, "repo", "C", "", "path to the git repository (default: current directory)")
rootCmd.PersistentFlags().StringSliceVar(&protectedBranches, "protected", protectedDefault, "branches to never delete")
rootCmd.PersistentFlags().BoolVar(&noColor, "no-color", false, "disable color output")
rootCmd.PersistentFlags().StringArrayVar(&excludeMergedInto, "exclude-merged-into", nil, "exclude branches merged into a branch matching this regex (can be repeated)")
rootCmd.PersistentFlags().StringArrayVar(&tierPatterns, "tier", nil, "protection tier (repeatable, first = highest priority); e.g. --tier \"main,master\" --tier \"release/*\" --tier \"hotfix/*\"")
}
