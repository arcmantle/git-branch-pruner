# git-branch-pruner

A CLI tool to find and delete merged git branches that are candidates for cleanup.

## Requirements

Go 1.25 or later.

## Installation

```bash
go install github.com/arcmantle/git-branch-pruner@latest
```

Or build from source:

```bash
git clone https://github.com/arcmantle/git-branch-pruner.git
cd git-branch-pruner
go build -o git-branch-pruner .
```

## Usage

### List merged branches

```bash
# List all merged branches (sorted oldest first)
git-branch-pruner list

# Only branches older than 30 days
git-branch-pruner list --older-than 30

# Include remote branches
git-branch-pruner list --remote

# Sort by name instead of age
git-branch-pruner list --sort name

# Output as JSON (for scripting)
git-branch-pruner list --format json

# Output as CSV
git-branch-pruner list --format csv

# Write output to a file (format auto-detected from extension)
git-branch-pruner list --output branches.json
git-branch-pruner list --output branches.csv

# Include the first-commit author per branch (slower)
git-branch-pruner list --authors

# Check against a specific target branch
git-branch-pruner list --target main
```

#### `list` flags

| Flag | Default | Description |
|------|---------|-------------|
| `--older-than N` | 0 (all) | Only show branches older than N days |
| `--remote` | false | Include remote branches |
| `--target BRANCH` | | Narrow to branches merged into this branch only |
| `--sort age\|name` | age | Sort order: `age` (oldest first) or `name` |
| `--format table\|json\|csv` | table | Output format |
| `--output FILE` | stdout | Write output to file (auto-detects format from `.json`/`.csv` extension) |
| `--authors` | false | Fetch first-commit author per branch (slower) |

### Delete merged branches

```bash
# Preview what would be deleted (dry run)
git-branch-pruner prune --dry-run

# Delete with confirmation prompt
git-branch-pruner prune

# Delete without confirmation
git-branch-pruner prune --no-confirm

# Only delete branches older than 90 days
git-branch-pruner prune --older-than 90

# Include remote branches
git-branch-pruner prune --remote

# Output candidates as JSON without deleting
git-branch-pruner prune --json

# Delete exactly the branches listed in a file (produced by list --output)
git-branch-pruner prune --input branches.csv
```

#### `prune` flags

| Flag | Default | Description |
|------|---------|-------------|
| `--older-than N` | 0 (all) | Only delete branches older than N days |
| `--remote` | false | Include remote branches |
| `--dry-run` | false | Show what would be deleted without deleting |
| `--no-confirm` | false | Skip confirmation prompt (required in non-interactive scripts) |
| `--target BRANCH` | | Narrow to branches merged into this branch only |
| `--sort age\|name` | age | Sort order |
| `--json` | false | Output candidates as JSON without deleting anything |
| `--input FILE` | | CSV or JSON file from `list --output`; delete exactly these branches (only `--protected` is re-applied; age/tier/exclude filters are ignored) |

### Global flags

| Flag | Description |
|------|-------------|
| `-C, --repo PATH` | Path to the git repository (default: current directory) |
| `--protected BRANCHES` | Comma-separated branches to never delete (default: `main,master,develop`). **Replaces** the default list — include all branches you want protected. |
| `--no-color` | Disable color output |
| `--exclude-merged-into PATTERN` | Exclude branches merged into a branch matching this regex (repeatable) |
| `--tier PATTERNS` | Protection tier (repeatable, first = highest priority); e.g. `--tier "main,master"` |
| `--tiers HIERARCHY` | Protection hierarchy in one string, levels separated by `<-`; e.g. `--tiers "main,master <- release/* <- hotfix/*"` |

> **Note:** `--protected` **replaces** the default list (`main`, `master`, `develop`). Always include every branch you want protected.
> Example: `--protected main,master,develop,staging` — not just `--protected staging`.
> The tool will warn you when the override excludes all default branches.

> **Note:** Remote branch operations assume the remote is named `origin`. Repositories
> using a different primary remote name are not currently supported for `--remote` operations.

## Examples

```bash
# Clean up old feature branches in another repo
git-branch-pruner prune -C /path/to/repo --older-than 60

# Custom protected branches
git-branch-pruner prune --protected main,staging,production

# Scripting: get merged branches as JSON
git-branch-pruner list --format json | jq '.branches[].name'

# Full pipeline: list, review, then prune
git-branch-pruner list --older-than 30
git-branch-pruner prune --older-than 30 --dry-run
git-branch-pruner prune --older-than 30

# Review-and-delete workflow: export, edit the file, then prune from it
git-branch-pruner list --output branches.csv
# (edit branches.csv to remove any branches you want to keep)
git-branch-pruner prune --input branches.csv

# Protect a release branch hierarchy
git-branch-pruner list --tiers "main,master <- release/* <- hotfix/*"
```

## License

MIT
