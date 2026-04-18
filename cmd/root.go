package cmd

import (
"fmt"
"os"
"regexp"
"strings"
"unicode/utf8"

"github.com/arcmantle/git-branch-pruner/internal/git"
"github.com/fatih/color"
"github.com/spf13/cobra"
"golang.org/x/term"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// visibleLen returns the printable rune count of s, ignoring ANSI escape codes.
func visibleLen(s string) int {
	return utf8.RuneCountInString(ansiRe.ReplaceAllString(s, ""))
}

// terminalWidth returns the current terminal width, falling back to 120.
func terminalWidth() int {
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		return w
	}
	return 120
}

// truncate shortens s to at most maxLen visible runes, appending "…" if cut.
func truncate(s string, maxLen int) string {
	if utf8.RuneCountInString(s) <= maxLen {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxLen-1]) + "…"
}

// printTable prints headers and rows with automatic column alignment.
// The first column (BRANCH) is capped to maxBranchCol; longer names are truncated
// with an ellipsis. The cap also respects the terminal width as a lower bound.
func printTable(headers []string, rows [][]string) {
	const colPad = 2
	const minBranchCol = 20
	const maxBranchCol = 70
	n := len(headers)

	// Compute natural widths from data (ignoring ANSI).
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

	// Cap column 0: never exceed maxBranchCol, and also never overflow the terminal.
	otherWidth := 0
	for i := 1; i < n; i++ {
		otherWidth += widths[i] + colPad
	}
	termCap := terminalWidth() - otherWidth - colPad
	if termCap < minBranchCol {
		termCap = minBranchCol
	}
	cap0 := maxBranchCol
	if termCap < cap0 {
		cap0 = termCap
	}
	if widths[0] > cap0 {
		widths[0] = cap0
	}

	// Print header row.
	for i, h := range headers {
		cell := headerColor.Sprint(h)
		if i < n-1 {
			fmt.Print(cell + strings.Repeat(" ", widths[i]+colPad-utf8.RuneCountInString(h)))
		} else {
			fmt.Println(cell)
		}
	}

	// Print data rows, truncating column 0 if needed.
	for _, row := range rows {
		for j, cell := range row {
			if j == 0 {
				cell = truncate(cell, widths[0])
			}
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
tiersShorthand    string   // raw --tiers value; e.g. "main,master <- release/* <- hotfix/*"
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

// parseTiers converts --tier and --tiers into [][]string.
// --tiers "main,master <- release/* <- hotfix/*" is a shorthand for
// multiple --tier flags. Both can be combined; --tiers is prepended.
func parseTiers() [][]string {
	var raw []string

	// Parse --tiers shorthand: split on "<-", trim whitespace around each segment.
	if tiersShorthand != "" {
		for _, segment := range strings.Split(tiersShorthand, "<-") {
			segment = strings.TrimSpace(segment)
			if segment != "" {
				raw = append(raw, segment)
			}
		}
	}

	raw = append(raw, tierPatterns...)

	if len(raw) == 0 {
		return nil
	}
	tiers := make([][]string, len(raw))
	for i, r := range raw {
		for _, p := range strings.Split(r, ",") {
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
rootCmd.PersistentFlags().StringArrayVar(&tierPatterns, "tier", nil, "protection tier (repeatable, first = highest priority); e.g. --tier \"main,master\" --tier \"release/*\"")
rootCmd.PersistentFlags().StringVar(&tiersShorthand, "tiers", "", "protection hierarchy in one string, levels separated by <-; e.g. --tiers \"main,master <- release/* <- hotfix/*,support/*\"")
}
