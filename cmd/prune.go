package cmd

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

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
	pruneInput     string // path to CSV or JSON file produced by list --output
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

Use --input to supply a CSV or JSON file produced by 'list --output': only the
branches listed in the file will be deleted. Edit the file first to remove any
branches you want to keep.

A remote URL can be passed as an argument — the repository will be cloned
automatically (blobless bare clone) and cleaned up after the command finishes.
Branches are deleted on the remote via git push.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 && !isBareClone {
			return fmt.Errorf("prune does not accept positional arguments (got %q); use --target to specify a branch, or pass a URL to delete branches on a remote repository", args[0])
		}
		if err := git.ValidateSortField(pruneSort); err != nil {
			return err
		}

		// --json implies read-only: treat as dry-run
		if pruneJSON {
			pruneDryRun = true
		}

		currentBranch := git.CurrentBranch(cmdCtx, repoPath)

		var deletable []git.Branch

		if pruneInput != "" {
			// Warn about flags that are silently ignored when --input is used.
			var ignored []string
			if cmd.Flags().Changed("older-than") {
				ignored = append(ignored, "--older-than")
			}
			if cmd.Flags().Changed("target") {
				ignored = append(ignored, "--target")
			}
			if cmd.Flags().Changed("remote") {
				ignored = append(ignored, "--remote")
			}
			if cmd.Flags().Changed("sort") {
				ignored = append(ignored, "--sort")
			}
			if cmd.Flags().Changed("exclude-merged-into") {
				ignored = append(ignored, "--exclude-merged-into")
			}
			if cmd.Flags().Changed("tier") || cmd.Flags().Changed("tiers") {
				ignored = append(ignored, "--tier/--tiers")
			}
			if len(ignored) > 0 {
				warnColor.Fprintf(color.Error, "⚠ %s ignored when --input is used\n\n", strings.Join(ignored, ", "))
			}

			// Load branches from file instead of running git analysis.
			loaded, meta, err := loadBranchFile(pruneInput)
			if err != nil {
				return err
			}

			// Warn if the file's remote URL doesn't match the current repo.
			if fileURL := meta["remote_url"]; fileURL != "" {
				if currentURL := git.RemoteURL(cmdCtx, repoPath); currentURL != "" && currentURL != fileURL {
					warnColor.Fprintf(color.Error, "⚠ Input file was generated for %s but current repo remote is %s\n\n", fileURL, currentURL)
				}
			}
			// Apply the same safety filters as the live path.
			beforeProtect := len(loaded)
			loaded = git.FilterProtected(loaded, protectedBranches)
			if skipped := beforeProtect - len(loaded); skipped > 0 {
				warnColor.Fprintf(color.Error, "⚠ Skipped %d protected branch(es) from input file\n\n", skipped)
			}
			// Still skip the currently checked-out branch.
			for _, b := range loaded {
				if !b.IsRemote && b.Name == currentBranch {
					warnColor.Fprintf(color.Error, "⚠ Skipping currently checked-out branch: %s\n\n", b.Name)
				} else {
					deletable = append(deletable, b)
				}
			}
		} else {
			includeRemote := pruneRemote || isBareClone
			branches, err := resolveBranches(pruneTarget, includeRemote, git.SortField(pruneSort))
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

			for _, b := range branches {
				if !b.IsRemote && b.Name == currentBranch {
					warnColor.Fprintf(color.Error, "⚠ Skipping currently checked-out branch: %s\n\n", b.Name)
				} else {
					deletable = append(deletable, b)
				}
			}
		}

		// In a bare clone every branch must be pushed as a remote deletion;
		// the temp clone has no meaningful local branches to remove.
		if isBareClone {
			for i := range deletable {
				deletable[i].IsRemote = true
			}
		}

		if len(deletable) == 0 {
			fmt.Print("No merged branches found")
			if pruneInput == "" {
				fmt.Print(filterSuffix(pruneTarget, pruneOlderThan, pruneRemote))
			}
			fmt.Println(".")
			return nil
		}

		// --json: output candidates as JSON without any destructive action
		if pruneJSON {
			return writeJSON(os.Stdout, deletable, nil)
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

		// Delete branches — each branch carries its MergedInto target for verification.
		// A shared FirstParentCache avoids redundant rev-list --first-parent calls
		// when multiple branches have an empty MergedInto (e.g. loaded from --input).
		// Remote branches are batched into a single "git push origin --delete" call;
		// if the batch fails we fall back to individual pushes for precise error reporting.
		var errs []string
		deleted := 0
		fpc := git.NewFirstParentCache()

		// Separate local and remote; verify all before touching anything.
		var localBranches []git.Branch
		var remoteBranches []git.Branch
		for _, b := range deletable {
			if err := git.VerifyMerged(cmdCtx, repoPath, b, fpc); err != nil {
				bType := "local"
				if b.IsRemote {
					bType = "remote"
				}
				errs = append(errs, fmt.Sprintf("%s (%s): %v", b.Name, bType, err))
				errorColor.Fprintf(color.Error, "  ✗ Failed to verify %s (%s): %v\n", b.Name, bType, err)
			} else if b.IsRemote {
				remoteBranches = append(remoteBranches, b)
			} else {
				localBranches = append(localBranches, b)
			}
		}

		// Delete local branches one by one.
		for _, b := range localBranches {
			if err := git.DeleteLocalBranch(cmdCtx, repoPath, b); err != nil {
				errs = append(errs, fmt.Sprintf("%s (local): %v", b.Name, err))
				errorColor.Fprintf(color.Error, "  ✗ Failed to delete %s (local): %v\n", b.Name, err)
			} else {
				successColor.Printf("  ✓ Deleted %s (local)\n", b.Name)
				deleted++
			}
		}

		// Delete remote branches in one batch push; fall back to individual on failure.
		if len(remoteBranches) > 0 {
			names := make([]string, len(remoteBranches))
			for i, b := range remoteBranches {
				names[i] = b.Name
			}
			if err := git.BatchDeleteRemoteBranches(cmdCtx, repoPath, names); err != nil {
				// Batch failed — retry individually for precise per-branch errors.
				// If a branch was already deleted in the (partially successful) batch,
				// git reports "remote ref does not exist" — treat that as success.
				for _, b := range remoteBranches {
					if err2 := git.BatchDeleteRemoteBranches(cmdCtx, repoPath, []string{b.Name}); err2 != nil {
						errMsg := err2.Error()
						if strings.Contains(errMsg, "remote ref does not exist") ||
							strings.Contains(errMsg, "could not find remote ref") {
							successColor.Printf("  ✓ Deleted %s (remote)\n", b.Name)
							deleted++
						} else {
							errs = append(errs, fmt.Sprintf("%s (remote): %v", b.Name, err2))
							errorColor.Fprintf(color.Error, "  ✗ Failed to delete %s (remote): %v\n", b.Name, err2)
						}
					} else {
						successColor.Printf("  ✓ Deleted %s (remote)\n", b.Name)
						deleted++
					}
				}
			} else {
				for _, b := range remoteBranches {
					successColor.Printf("  ✓ Deleted %s (remote)\n", b.Name)
					deleted++
				}
			}
		}

		fmt.Printf("\n%d/%d branch(es) deleted.\n", deleted, len(deletable))
		if len(errs) > 0 {
			return fmt.Errorf("%d branch(es) failed to delete", len(errs))
		}
		return nil
	},
}

// loadBranchFile reads a CSV or JSON file produced by 'list --output' and
// returns a slice of Branch values. Format is detected from the file extension.
func loadBranchFile(path string) ([]git.Branch, map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("opening input file: %w", err)
	}
	defer f.Close()

	ext := strings.ToLower(path)
	switch {
	case strings.HasSuffix(ext, ".json"):
		branches, meta, err := loadBranchJSON(f)
		return branches, meta, err
	case strings.HasSuffix(ext, ".csv"):
		branches, meta, err := loadBranchCSV(f)
		return branches, meta, err
	default:
		return nil, nil, fmt.Errorf("unsupported input file format for %q: must be .csv or .json", path)
	}
}

// stripBOM removes a UTF-8 BOM (0xEF 0xBB 0xBF) from the start of s.
// CSV files created on Windows (e.g. by Excel) often include a BOM which
// would otherwise corrupt the first header field name.
func stripBOM(s string) string {
	return strings.TrimPrefix(s, "\xef\xbb\xbf")
}

func loadBranchCSV(r io.Reader) ([]git.Branch, map[string]string, error) {
	meta := make(map[string]string)
	// Strip # comment lines before passing to csv.Reader, and extract meta values.
	var filtered strings.Builder
	scanner := bufio.NewScanner(r)
	first := true
	for scanner.Scan() {
		line := scanner.Text()
		if first {
			line = stripBOM(line)
			first = false
		}
		if strings.HasPrefix(line, "#") {
			// Extract meta from comment lines like "# remote_url: https://..."
			if kv := strings.SplitN(strings.TrimPrefix(line, "#"), ":", 2); len(kv) == 2 {
				meta[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
			}
			continue
		}
		filtered.WriteString(line + "\n")
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, fmt.Errorf("reading CSV: %w", err)
	}

	cr := csv.NewReader(strings.NewReader(filtered.String()))
	records, err := cr.ReadAll()
	if err != nil {
		return nil, nil, fmt.Errorf("parsing CSV: %w", err)
	}
	if len(records) < 2 {
		return nil, meta, nil
	}
	// Build column index from header row so the file is order-independent.
	header := records[0]
	idx := make(map[string]int, len(header))
	for i, h := range header {
		idx[h] = i
	}
	if _, ok := idx["name"]; !ok {
		return nil, nil, fmt.Errorf("CSV header missing required \"name\" column (got: %s)", strings.Join(header, ", "))
	}
	col := func(row []string, name string) string {
		if i, ok := idx[name]; ok && i < len(row) {
			return row[i]
		}
		return ""
	}
	var branches []git.Branch
	for _, row := range records[1:] {
		name := col(row, "name")
		if name == "" {
			continue
		}
		if strings.HasPrefix(name, "-") {
			return nil, nil, fmt.Errorf("invalid branch name %q in CSV: branch names cannot start with '-'", name)
		}
		mergedInto := col(row, "merged_into")
		if mergedInto != "" && strings.HasPrefix(mergedInto, "-") {
			return nil, nil, fmt.Errorf("invalid merged_into value %q in CSV: must not start with '-'", mergedInto)
		}
		b := git.Branch{
			Name:        name,
			IsRemote:    col(row, "type") == "remote",
			MergedInto:  mergedInto,
			SHA:         col(row, "sha"),
			RelativeAge: col(row, "relative_age"),
			Author:      col(row, "author"),
		}
		if v := col(row, "age_days"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				b.AgeDays = n
			}
		}
		if v := col(row, "last_commit"); v != "" {
			if t, err := time.Parse("2006-01-02", v); err == nil {
				b.LastCommit = t
			}
		}
		branches = append(branches, b)
	}
	return branches, meta, nil
}

func loadBranchJSON(r io.Reader) ([]git.Branch, map[string]string, error) {
	// Support both old format (array) and new format ({meta, branches}).
	var raw json.RawMessage
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return nil, nil, fmt.Errorf("parsing JSON: %w", err)
	}
	var records []jsonBranch
	meta := make(map[string]string)
	// Try wrapper format first.
	var wrapper struct {
		Meta     map[string]string `json:"meta"`
		Branches []jsonBranch      `json:"branches"`
	}
	if err := json.Unmarshal(raw, &wrapper); err == nil && wrapper.Branches != nil {
		records = wrapper.Branches
		for k, v := range wrapper.Meta {
			meta[k] = v
		}
	} else if err := json.Unmarshal(raw, &records); err != nil {
		return nil, nil, fmt.Errorf("parsing JSON: %w", err)
	}
	var branches []git.Branch
	for _, jb := range records {
		if jb.Name == "" {
			continue
		}
		if strings.HasPrefix(jb.Name, "-") {
			return nil, nil, fmt.Errorf("invalid branch name %q in JSON: branch names cannot start with '-'", jb.Name)
		}
		if jb.MergedInto != "" && strings.HasPrefix(jb.MergedInto, "-") {
			return nil, nil, fmt.Errorf("invalid merged_into value %q in JSON: must not start with '-'", jb.MergedInto)
		}
		b := git.Branch{
			Name:        jb.Name,
			IsRemote:    jb.Type == "remote",
			MergedInto:  jb.MergedInto,
			SHA:         jb.SHA,
			AgeDays:     jb.AgeDays,
			RelativeAge: jb.RelativeAge,
			Author:      jb.Author,
		}
		if jb.LastCommit != "" {
			if t, err := time.Parse("2006-01-02", jb.LastCommit); err == nil {
				b.LastCommit = t
			}
		}
		branches = append(branches, b)
	}
	return branches, meta, nil
}

func printBranchTable(branches []git.Branch) {
	hasAuthor := false
	for _, b := range branches {
		if b.Author != "" {
			hasAuthor = true
			break
		}
	}
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
		if len(b.SHA) >= 7 {
			sha = b.SHA[:7]
		} else if b.SHA != "" {
			sha = b.SHA
		}
		row := []string{b.Name, bType, mergedInto, relAge, lastCommit, sha}
		if hasAuthor {
			author := b.Author
			if author == "" {
				author = dimColor.Sprint("unknown")
			}
			row = append(row, author)
		}
		rows = append(rows, row)
	}
	headers := []string{"BRANCH", "TYPE", "MERGED INTO", "AGE", "LAST COMMIT", "SHA"}
	if hasAuthor {
		headers = append(headers, "AUTHOR")
	}
	printTable(headers, rows)
}

func init() {
	pruneCmd.Flags().IntVar(&pruneOlderThan, "older-than", 0, "only delete branches older than N days")
	pruneCmd.Flags().BoolVar(&pruneRemote, "remote", false, "include remote branches")
	pruneCmd.Flags().BoolVar(&pruneDryRun, "dry-run", false, "show what would be deleted without deleting")
	pruneCmd.Flags().BoolVar(&pruneNoConfirm, "no-confirm", false, "skip confirmation prompt (required when stdin is not a terminal)")
	pruneCmd.Flags().StringVar(&pruneTarget, "target", "", "narrow to branches merged into this specific branch only")
	pruneCmd.Flags().StringVar(&pruneSort, "sort", "age", "sort order: age (oldest first) or name")
	pruneCmd.Flags().BoolVar(&pruneJSON, "json", false, "output candidate list as JSON without deleting anything")
	pruneCmd.Flags().StringVar(&pruneInput, "input", "", "CSV or JSON file from 'list --output'; delete exactly these branches")
	rootCmd.AddCommand(pruneCmd)
}
