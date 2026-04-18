package cmd

import (
	"strings"
	"testing"

	"github.com/arcmantle/git-branch-pruner/internal/git"
)

// ---------------------------------------------------------------------------
// parseTiers
// ---------------------------------------------------------------------------

func TestParseTiers_Nil(t *testing.T) {
	tiersShorthand = ""
	tierPatterns = nil
	got := parseTiers()
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestParseTiers_Shorthand(t *testing.T) {
	tiersShorthand = "main,master <- release/* <- hotfix/*,support/*"
	tierPatterns = nil
	got := parseTiers()
	tiersShorthand = ""
	if len(got) != 3 {
		t.Fatalf("expected 3 tiers, got %d: %v", len(got), got)
	}
	if len(got[0]) != 2 || got[0][0] != "main" || got[0][1] != "master" {
		t.Errorf("tier 0 = %v, want [main master]", got[0])
	}
	if len(got[1]) != 1 || got[1][0] != "release/*" {
		t.Errorf("tier 1 = %v, want [release/*]", got[1])
	}
	if len(got[2]) != 2 || got[2][0] != "hotfix/*" || got[2][1] != "support/*" {
		t.Errorf("tier 2 = %v, want [hotfix/* support/*]", got[2])
	}
}

func TestParseTiers_TierFlags(t *testing.T) {
	tiersShorthand = ""
	tierPatterns = []string{"main,master", "release/*"}
	got := parseTiers()
	tierPatterns = nil
	if len(got) != 2 {
		t.Fatalf("expected 2 tiers, got %d: %v", len(got), got)
	}
	if len(got[0]) != 2 {
		t.Errorf("tier 0 = %v, want [main master]", got[0])
	}
	if len(got[1]) != 1 || got[1][0] != "release/*" {
		t.Errorf("tier 1 = %v, want [release/*]", got[1])
	}
}

func TestParseTiers_ShorthandPrependedBeforeFlags(t *testing.T) {
	tiersShorthand = "main"
	tierPatterns = []string{"release/*"}
	got := parseTiers()
	tiersShorthand = ""
	tierPatterns = nil
	// shorthand comes first
	if len(got) != 2 || got[0][0] != "main" || got[1][0] != "release/*" {
		t.Errorf("got %v, want [[main] [release/*]]", got)
	}
}

// ---------------------------------------------------------------------------
// visibleLen
// ---------------------------------------------------------------------------

func TestVisibleLen(t *testing.T) {
	cases := []struct {
		s    string
		want int
	}{
		{"hello", 5},
		{"", 0},
		{"\x1b[32mgreen\x1b[0m", 5},
		{"\x1b[1;31mred text\x1b[0m", 8},
		{"no color", 8},
	}
	for _, tc := range cases {
		got := visibleLen(tc.s)
		if got != tc.want {
			t.Errorf("visibleLen(%q) = %d, want %d", tc.s, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// truncate
// ---------------------------------------------------------------------------

func TestTruncate(t *testing.T) {
	cases := []struct {
		s      string
		maxLen int
		want   string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello world", 8, "hello w…"},
		{"abcde", 3, "ab…"},
		{"ab", 2, "ab"},
		{"a", 1, "a"},
	}
	for _, tc := range cases {
		got := truncate(tc.s, tc.maxLen)
		if got != tc.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tc.s, tc.maxLen, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// filterSuffix
// ---------------------------------------------------------------------------

func TestFilterSuffix_Empty(t *testing.T) {
	excludeMergedInto = nil
	tiersShorthand = ""
	tierPatterns = nil
	got := filterSuffix("", 0, true)
	if got != "" {
		t.Errorf("expected empty suffix, got %q", got)
	}
}

func TestFilterSuffix_Target(t *testing.T) {
	excludeMergedInto = nil
	tiersShorthand = ""
	tierPatterns = nil
	got := filterSuffix("main", 0, true)
	if got != " (merged into main)" {
		t.Errorf("got %q", got)
	}
}

func TestFilterSuffix_Multiple(t *testing.T) {
	excludeMergedInto = nil
	tiersShorthand = ""
	tierPatterns = nil
	got := filterSuffix("main", 30, false)
	if got != " (merged into main, older than 30 days, local only)" {
		t.Errorf("got %q", got)
	}
}

func TestFilterSuffix_LocalOnly(t *testing.T) {
	excludeMergedInto = nil
	tiersShorthand = ""
	tierPatterns = nil
	got := filterSuffix("", 0, false)
	if got != " (local only)" {
		t.Errorf("got %q", got)
	}
}

// ---------------------------------------------------------------------------
// isURL
// ---------------------------------------------------------------------------

func TestIsURL(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"https://github.com/org/repo", true},
		{"http://github.com/org/repo", true},
		{"git://github.com/org/repo.git", true},
		{"ssh://git@github.com/org/repo.git", true},
		{"git@github.com:org/repo.git", true},
		{"/path/to/repo", false},
		{"./relative/path", false},
		{"C:\\Users\\repo", false},
		{"", false},
	}
	for _, tc := range cases {
		got := isURL(tc.s)
		if got != tc.want {
			t.Errorf("isURL(%q) = %v, want %v", tc.s, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// loadBranchCSV
// ---------------------------------------------------------------------------

func TestLoadBranchCSV(t *testing.T) {
	input := `# remote_url: https://example.com/repo.git
# generated: 2025-01-01T00:00:00Z
name,type,merged_into,age_days,relative_age,last_commit,sha,author
feature/a,local,main,10,10 days ago,2025-01-01,abc1234567890abcdef1234567890abcdef123456,Alice
feature/b,remote,release/1.0,20,20 days ago,2024-12-22,def1234567890abcdef1234567890abcdef123456,Bob
`
	branches, err := loadBranchCSV(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(branches) != 2 {
		t.Fatalf("expected 2 branches, got %d", len(branches))
	}
	if branches[0].Name != "feature/a" || branches[0].IsRemote != false || branches[0].MergedInto != "main" {
		t.Errorf("branch 0: %+v", branches[0])
	}
	if branches[1].Name != "feature/b" || branches[1].IsRemote != true || branches[1].MergedInto != "release/1.0" {
		t.Errorf("branch 1: %+v", branches[1])
	}
}

func TestLoadBranchCSV_Empty(t *testing.T) {
	input := `name,type,merged_into,age_days,relative_age,last_commit,sha,author
`
	branches, err := loadBranchCSV(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(branches) != 0 {
		t.Errorf("expected 0 branches, got %d", len(branches))
	}
}

// ---------------------------------------------------------------------------
// loadBranchJSON
// ---------------------------------------------------------------------------

func TestLoadBranchJSON_Wrapper(t *testing.T) {
	input := `{
  "meta": {"repo": "/tmp/test"},
  "branches": [
    {"name": "feature/x", "type": "local", "merged_into": "main", "sha": "abc1234"},
    {"name": "fix/y", "type": "remote", "merged_into": "develop", "sha": "def5678"}
  ]
}`
	branches, err := loadBranchJSON(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(branches) != 2 {
		t.Fatalf("expected 2 branches, got %d", len(branches))
	}
	if branches[0].Name != "feature/x" || branches[0].IsRemote != false {
		t.Errorf("branch 0: %+v", branches[0])
	}
	if branches[1].Name != "fix/y" || branches[1].IsRemote != true {
		t.Errorf("branch 1: %+v", branches[1])
	}
}

func TestLoadBranchJSON_Empty(t *testing.T) {
	input := `{"branches": []}`
	branches, err := loadBranchJSON(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(branches) != 0 {
		t.Errorf("expected 0 branches, got %d", len(branches))
	}
}

// ---------------------------------------------------------------------------
// loadBranchCSV / loadBranchJSON — dash-prefixed name validation
// ---------------------------------------------------------------------------

func TestLoadBranchCSV_RejectsDashName(t *testing.T) {
	input := `name,type,merged_into,age_days,relative_age,last_commit,sha,author
--delete,local,main,10,10 days ago,2025-01-01,abc1234567890abcdef1234567890abcdef123456,Alice
`
	_, err := loadBranchCSV(strings.NewReader(input))
	if err == nil {
		t.Fatal("expected error for dash-prefixed branch name, got nil")
	}
	if !strings.Contains(err.Error(), "cannot start with '-'") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestLoadBranchCSV_RejectsDashMergedInto(t *testing.T) {
	input := `name,type,merged_into,age_days,relative_age,last_commit,sha,author
feature/x,local,--all,10,10 days ago,2025-01-01,abc1234567890abcdef1234567890abcdef123456,Alice
`
	_, err := loadBranchCSV(strings.NewReader(input))
	if err == nil {
		t.Fatal("expected error for dash-prefixed merged_into, got nil")
	}
	if !strings.Contains(err.Error(), "must not start with '-'") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestLoadBranchJSON_RejectsDashName(t *testing.T) {
	input := `{"branches": [{"name": "--force", "type": "local", "merged_into": "main"}]}`
	_, err := loadBranchJSON(strings.NewReader(input))
	if err == nil {
		t.Fatal("expected error for dash-prefixed branch name, got nil")
	}
	if !strings.Contains(err.Error(), "cannot start with '-'") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestLoadBranchJSON_RejectsDashMergedInto(t *testing.T) {
	input := `{"branches": [{"name": "feature/x", "type": "local", "merged_into": "--all"}]}`
	_, err := loadBranchJSON(strings.NewReader(input))
	if err == nil {
		t.Fatal("expected error for dash-prefixed merged_into, got nil")
	}
	if !strings.Contains(err.Error(), "must not start with '-'") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// ---------------------------------------------------------------------------
// FilterProtected with globs
// ---------------------------------------------------------------------------

func TestFilterProtected_Glob(t *testing.T) {
	branches := []git.Branch{
		{Name: "main"},
		{Name: "release/1.0"},
		{Name: "release/2.0"},
		{Name: "feature/foo"},
		{Name: "hotfix/urgent"},
	}
	protected := []string{"main", "release/*"}
	got := git.FilterProtected(branches, protected)
	if len(got) != 2 {
		t.Fatalf("expected 2 branches, got %d: %v", len(got), got)
	}
	for _, b := range got {
		if b.Name == "main" || strings.HasPrefix(b.Name, "release/") {
			t.Errorf("protected branch %q survived FilterProtected", b.Name)
		}
	}
}
