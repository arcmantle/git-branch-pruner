package git

import (
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// globMatch / matchGlob
// ---------------------------------------------------------------------------

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pattern string
		name    string
		want    bool
	}{
		// Exact match
		{"main", "main", true},
		{"main", "master", false},

		// /* prefix — matches direct children and nested sub-paths
		{"release/*", "release/1.0", true},
		{"release/*", "release/1.0/patch", true}, // /* is intentionally broad
		{"release/*", "release", false},
		{"release/*", "releases/1.0", false},
		// bare prefix without slash must NOT match the /* pattern
		{"hotfix/*", "hotfix", false},
		{"hotfix/*", "hotfix/critical", true},
		{"hotfix/*", "feature/x", false},

		// * within a segment (matchGlob path)
		{"feature/*", "feature/foo", true},
		{"feat*", "feature", true},
		{"feat*", "feat", true},
		{"feat*", "fe", false},

		// ? wildcard
		{"v1.?", "v1.0", true},
		{"v1.?", "v1.12", false},
		{"v1.?", "v1./", false},

		// No match when pattern has extra chars
		{"main", "main2", false},
		{"main2", "main", false},
	}
	for _, tc := range cases {
		got := globMatch(tc.pattern, tc.name)
		if got != tc.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		pattern string
		name    string
		want    bool
	}{
		{"main", "main", true},
		{"main", "master", false},
		{"feat*", "feature", true},
		{"feat*", "feat", true},
		{"feat*", "fe", false},
		// * should NOT cross /
		{"*", "feature/foo", false},
		{"feature/*", "feature/foo", true},
		{"feature/*", "feature/foo/bar", false}, // * doesn't cross /
	}
	for _, tc := range cases {
		got, err := matchGlob(tc.pattern, tc.name)
		if err != nil {
			t.Errorf("matchGlob(%q, %q) unexpected error: %v", tc.pattern, tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// matchTier
// ---------------------------------------------------------------------------

func TestMatchTier(t *testing.T) {
	tiers := [][]string{
		{"main", "master"},
		{"release/*"},
		{"hotfix/*", "support/*"},
	}
	cases := []struct {
		name string
		want int
	}{
		{"main", 0},
		{"master", 0},
		{"release/1.0", 1},
		{"hotfix/urgent", 2},
		{"support/legacy", 2},
		{"feature/foo", -1},
		{"develop", -1},
	}
	for _, tc := range cases {
		got := matchTier(tc.name, tiers)
		if got != tc.want {
			t.Errorf("matchTier(%q) = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// FilterByTierHierarchy
// ---------------------------------------------------------------------------

func makeBranch(name, mergedInto string) Branch {
	return Branch{Name: name, MergedInto: mergedInto}
}

func TestFilterByTierHierarchy_Empty(t *testing.T) {
	branches := []Branch{makeBranch("feature/x", "main")}
	got := FilterByTierHierarchy(branches, nil)
	if len(got) != 1 {
		t.Fatalf("expected 1 branch with nil tiers, got %d", len(got))
	}
}

func TestFilterByTierHierarchy(t *testing.T) {
	tiers := [][]string{
		{"main", "master"},  // tier 0 — never prunable
		{"release/*"},       // tier 1 — prunable only if merged into tier 0
		{"hotfix/*"},        // tier 2 — prunable only if merged into tier < 2
	}

	cases := []struct {
		branch     Branch
		wantKept   bool
		desc       string
	}{
		// Tier 0 — never prunable
		{makeBranch("main", "master"), false, "tier-0 main → never prunable"},
		{makeBranch("master", "main"), false, "tier-0 master → never prunable"},

		// Tier 1 — prunable if merged into tier 0
		{makeBranch("release/1.0", "main"), true, "tier-1 release → merged into main (tier 0) → prunable"},
		{makeBranch("release/1.0", "release/2.0"), false, "tier-1 release → merged into release (tier 1) → not prunable"},
		{makeBranch("release/1.0", "feature/x"), false, "tier-1 release → merged into non-tier → not prunable"},

		// Tier 2 — prunable if merged into tier 0 or 1
		{makeBranch("hotfix/urgent", "main"), true, "tier-2 hotfix → merged into main (tier 0) → prunable"},
		{makeBranch("hotfix/urgent", "release/1.0"), true, "tier-2 hotfix → merged into release (tier 1) → prunable"},
		{makeBranch("hotfix/urgent", "hotfix/other"), false, "tier-2 hotfix → merged into hotfix (tier 2) → not prunable"},

		// Not in any tier (implicit lowest) — prunable only if merged into an explicit tier branch
		{makeBranch("feature/foo", "main"), true, "no-tier feature → merged into main (tier 0) → prunable"},
		{makeBranch("feature/foo", "release/1.0"), true, "no-tier feature → merged into release (tier 1) → prunable"},
		{makeBranch("feature/foo", "feature/bar"), false, "no-tier feature → merged into no-tier → not prunable"},
		{makeBranch("feature/foo", "(any)"), false, "no-tier feature → (any) not in tiers → not prunable"},
	}

	for _, tc := range cases {
		result := FilterByTierHierarchy([]Branch{tc.branch}, tiers)
		kept := len(result) == 1
		if kept != tc.wantKept {
			t.Errorf("[%s] kept=%v, want %v", tc.desc, kept, tc.wantKept)
		}
	}
}

// ---------------------------------------------------------------------------
// FilterProtected
// ---------------------------------------------------------------------------

func TestFilterProtected(t *testing.T) {
	branches := []Branch{
		makeBranch("main", ""),
		makeBranch("master", ""),
		makeBranch("develop", ""),
		makeBranch("feature/foo", "main"),
		makeBranch("hotfix/x", "main"),
	}
	protected := []string{"main", "master", "develop"}
	got := FilterProtected(branches, protected)
	if len(got) != 2 {
		t.Fatalf("expected 2 branches, got %d: %v", len(got), got)
	}
	for _, b := range got {
		for _, p := range protected {
			if b.Name == p {
				t.Errorf("protected branch %q survived FilterProtected", p)
			}
		}
	}
}

func TestFilterProtected_TrimSpace(t *testing.T) {
	branches := []Branch{makeBranch("main", ""), makeBranch("feature/x", "")}
	got := FilterProtected(branches, []string{" main "}) // space-padded
	if len(got) != 1 || got[0].Name != "feature/x" {
		t.Errorf("expected only feature/x, got %v", got)
	}
}

func TestFilterProtected_Empty(t *testing.T) {
	branches := []Branch{makeBranch("main", ""), makeBranch("feature/x", "")}
	got := FilterProtected(branches, nil)
	if len(got) != 2 {
		t.Errorf("nil protected list should keep all branches, got %d", len(got))
	}
}

// ---------------------------------------------------------------------------
// FilterByAge
// ---------------------------------------------------------------------------

func TestFilterByAge(t *testing.T) {
	now := time.Now()
	branches := []Branch{
		{Name: "old", AgeDays: 100, LastCommit: now.AddDate(0, 0, -100)},
		{Name: "recent", AgeDays: 5, LastCommit: now.AddDate(0, 0, -5)},
		{Name: "borderline", AgeDays: 30, LastCommit: now.AddDate(0, 0, -30)},
	}

	got := FilterByAge(branches, 30)
	if len(got) != 2 {
		t.Fatalf("expected 2 branches (old + borderline), got %d: %v", len(got), got)
	}

	got0 := FilterByAge(branches, 0)
	if len(got0) != 3 {
		t.Errorf("olderThan=0 should return all branches, got %d", len(got0))
	}

	gotNeg := FilterByAge(branches, -1)
	if len(gotNeg) != 3 {
		t.Errorf("negative olderThan should return all branches, got %d", len(gotNeg))
	}
}

// ---------------------------------------------------------------------------
// FilterByExcludeMergedInto
// ---------------------------------------------------------------------------

func TestFilterByExcludeMergedInto(t *testing.T) {
	branches := []Branch{
		makeBranch("feature/a", "main"),
		makeBranch("feature/b", "release/1.0"),
		makeBranch("hotfix/x", "release/2.0"),
		makeBranch("task/y", "feature/z"),
	}

	got, err := FilterByExcludeMergedInto(branches, []string{`release/.*`})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 branches (feature/a, task/y), got %d: %v", len(got), got)
	}

	_, err = FilterByExcludeMergedInto(branches, []string{`[invalid`})
	if err == nil {
		t.Error("expected error for invalid regex, got nil")
	}
}

func TestFilterByExcludeMergedInto_Empty(t *testing.T) {
	branches := []Branch{makeBranch("feature/a", "main")}
	got, err := FilterByExcludeMergedInto(branches, nil)
	if err != nil || len(got) != 1 {
		t.Errorf("nil patterns should return all branches unchanged, got %v err=%v", got, err)
	}
}

// ---------------------------------------------------------------------------
// relativeTime
// ---------------------------------------------------------------------------

func TestRelativeTime(t *testing.T) {
	now := time.Now()
	cases := []struct {
		t    time.Time
		want string
	}{
		{now.Add(-30 * time.Second), "just now"},
		{now.Add(-2 * time.Minute), "2 minutes ago"},
		{now.Add(-1 * time.Minute), "1 minute ago"},
		{now.Add(-3 * time.Hour), "3 hours ago"},
		{now.Add(-1 * time.Hour), "1 hour ago"},
		{now.Add(-10 * 24 * time.Hour), "10 days ago"},
		{now.Add(-1 * 24 * time.Hour), "1 day ago"},
		{now.Add(-45 * 24 * time.Hour), "1 month ago"},
		{now.Add(-400 * 24 * time.Hour), "1 year ago"},
	}
	for _, tc := range cases {
		got := relativeTime(tc.t)
		if got != tc.want {
			t.Errorf("relativeTime(%v) = %q, want %q", tc.t, got, tc.want)
		}
	}
}
