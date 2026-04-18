package cmd

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/arcmantle/git-branch-pruner/internal/git"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var (
	listOlderThan int
	listRemote    bool
	listTarget    string
	listSort      string
	listJSON      bool   // kept for backward compatibility
	listFormat    string // table | json | csv
	listOutput    string // file path; empty = stdout
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
		// --json is a legacy alias for --format json
		format := listFormat
		if listJSON && format == "table" {
			format = "json"
		}
		// Auto-detect format from output file extension if --format not explicitly set
		if listOutput != "" && !cmd.Flags().Changed("format") {
			switch {
			case strings.HasSuffix(listOutput, ".csv"):
				format = "csv"
			case strings.HasSuffix(listOutput, ".json"):
				format = "json"
			}
		}

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

		// Resolve output writer.
		out, closeOut, err := resolveOutput(listOutput)
		if err != nil {
			return err
		}
		defer closeOut()

		// When writing to a file, disable color in the output.
		fileMode := listOutput != ""

		if len(branches) == 0 {
			if !fileMode {
				fmt.Print("No merged branches found")
				fmt.Print(filterSuffix(listTarget, listOlderThan, includeRemote))
				fmt.Println(".")
			}
			return nil
		}

		switch format {
		case "json":
			return writeJSON(out, branches, buildMeta())
		case "csv":
			return writeCSV(out, branches, buildMeta())
		default:
			rows := buildRows(branches, fileMode)
			if fileMode {
				printTableTo(out, []string{"BRANCH", "TYPE", "MERGED INTO", "AGE", "LAST COMMIT", "SHA"}, rows)
				fmt.Fprintf(os.Stderr, "Wrote %d branch(es) to %s\n", len(branches), listOutput)
			} else {
				printTable([]string{"BRANCH", "TYPE", "MERGED INTO", "AGE", "LAST COMMIT", "SHA"}, rows)
				fmt.Printf("\n%d branch(es) found", len(branches))
				fmt.Print(filterSuffix(listTarget, listOlderThan, includeRemote))
				fmt.Println(".")
			}
		}
		return nil
	},
}

// resolveOutput returns a writer and a close func for the given path.
// If path is empty, returns os.Stdout with a no-op closer.
func resolveOutput(path string) (io.Writer, func(), error) {
	if path == "" {
		return os.Stdout, func() {}, nil
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, nil, fmt.Errorf("opening output file: %w", err)
	}
	return f, func() { f.Close() }, nil
}

// buildRows converts branches into string rows.
// fileMode strips ANSI color codes so file output is clean plain text.
func buildRows(branches []git.Branch, fileMode bool) [][]string {
	var rows [][]string
	for _, b := range branches {
		bType := "local"
		if b.IsRemote || isBareClone {
			if fileMode {
				bType = "remote"
			} else {
				bType = remoteColor.Sprint("remote")
			}
		}
		lastCommit := "unknown"
		relAge := "unknown"
		if !b.LastCommit.IsZero() {
			lastCommit = b.LastCommit.Format("2006-01-02")
			relAge = b.RelativeAge
		} else if !fileMode {
			lastCommit = dimColor.Sprint("unknown")
			relAge = dimColor.Sprint("unknown")
		}
		mergedInto := "(any)"
		if b.MergedInto != "" {
			mergedInto = b.MergedInto
		} else if !fileMode {
			mergedInto = dimColor.Sprint("(any)")
		}
		sha := "unknown"
		if b.ShortSHA != "" {
			sha = b.ShortSHA
		} else if !fileMode {
			sha = dimColor.Sprint("unknown")
		}
		rows = append(rows, []string{b.Name, bType, mergedInto, relAge, lastCommit, sha})
	}
	return rows
}

// printTableTo writes a plain-text table to w (no color, no ellipsis truncation).
func printTableTo(w io.Writer, headers []string, rows [][]string) {
	const colPad = 2
	n := len(headers)
	widths := make([]int, n)
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for j := 0; j < n && j < len(row); j++ {
			if l := len(row[j]); l > widths[j] {
				widths[j] = l
			}
		}
	}
	// Header
	for i, h := range headers {
		if i < n-1 {
			fmt.Fprintf(w, "%-*s", widths[i]+colPad, h)
		} else {
			fmt.Fprintln(w, h)
		}
	}
	// Rows
	for _, row := range rows {
		for j, cell := range row {
			if j < n-1 {
				fmt.Fprintf(w, "%-*s", widths[j]+colPad, cell)
			} else {
				fmt.Fprintln(w, cell)
			}
		}
	}
}

type jsonBranch struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	MergedInto  string `json:"merged_into,omitempty"`
	AgeDays     int    `json:"age_days"`
	RelativeAge string `json:"relative_age"`
	LastCommit  string `json:"last_commit,omitempty"`
	SHA         string `json:"sha,omitempty"`
}

func writeJSON(w io.Writer, branches []git.Branch, meta map[string]string) error {
	type output struct {
		Meta     map[string]string `json:"meta,omitempty"`
		Branches []jsonBranch      `json:"branches"`
	}
	records := make([]jsonBranch, len(branches))
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
			SHA:         b.ShortSHA,
		}
		if !b.LastCommit.IsZero() {
			jb.LastCommit = b.LastCommit.Format("2006-01-02")
		}
		records[i] = jb
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(output{Meta: meta, Branches: records})
}

func writeCSV(w io.Writer, branches []git.Branch, meta map[string]string) error {
	// Write repo metadata as # comment lines so the file is self-documenting.
	// These lines are skipped by loadBranchCSV.
	fmt.Fprintf(w, "# repo: %s\n", meta["repo"])
	if r := meta["remote_url"]; r != "" {
		fmt.Fprintf(w, "# remote_url: %s\n", r)
	}
	fmt.Fprintf(w, "# generated: %s\n", meta["generated"])

	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"name", "type", "merged_into", "age_days", "relative_age", "last_commit", "sha"}); err != nil {
		return err
	}
	for _, b := range branches {
		bType := "local"
		if b.IsRemote || isBareClone {
			bType = "remote"
		}
		mergedInto := b.MergedInto
		lastCommit := ""
		if !b.LastCommit.IsZero() {
			lastCommit = b.LastCommit.Format("2006-01-02")
		}
		if err := cw.Write([]string{
			b.Name,
			bType,
			mergedInto,
			fmt.Sprintf("%d", b.AgeDays),
			b.RelativeAge,
			lastCommit,
			b.ShortSHA,
		}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// buildMeta returns metadata about the repo being analysed.
func buildMeta() map[string]string {
	repo := repoPath
	if repo == "" {
		if wd, err := os.Getwd(); err == nil {
			repo = wd
		}
	} else if abs, err := filepath.Abs(repo); err == nil {
		repo = abs
	}
	m := map[string]string{
		"repo":      repo,
		"generated": time.Now().UTC().Format(time.RFC3339),
	}
	if remote := git.RemoteURL(repoPath); remote != "" {
		m["remote_url"] = remote
	}
	return m
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
	listCmd.Flags().BoolVar(&listJSON, "json", false, "output as JSON (deprecated: use --format json)")
	listCmd.Flags().StringVar(&listFormat, "format", "table", "output format: table, json, csv")
	listCmd.Flags().StringVar(&listOutput, "output", "", "write output to file instead of stdout (auto-detects format from extension: .csv, .json)")
	rootCmd.AddCommand(listCmd)
}
