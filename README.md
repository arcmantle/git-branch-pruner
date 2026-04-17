# git-branch-pruner

A CLI tool to find and delete merged git branches that are candidates for cleanup.

## Installation

```bash
go install github.com/roen/git-branch-pruner@latest
```

Or build from source:

```bash
git clone https://github.com/roen/git-branch-pruner.git
cd git-branch-pruner
go build -o git-branch-pruner .
```

## Usage

### List merged branches

```bash
# List all merged branches
git-branch-pruner list

# Only branches older than 30 days
git-branch-pruner list --older-than 30

# Include remote branches
git-branch-pruner list --remote

# Check against a specific target branch
git-branch-pruner list --target main
```

### Delete merged branches

```bash
# Preview what would be deleted
git-branch-pruner prune --dry-run

# Delete with confirmation prompt
git-branch-pruner prune

# Delete without confirmation
git-branch-pruner prune --no-confirm

# Only delete branches older than 90 days
git-branch-pruner prune --older-than 90

# Include remote branches
git-branch-pruner prune --remote
```

### Global flags

| Flag | Description |
|------|-------------|
| `-C, --repo` | Path to the git repository (default: current directory) |
| `--protected` | Branches to never delete (default: `main,master,develop`) |

## Examples

```bash
# Clean up old feature branches in another repo
git-branch-pruner prune -C /path/to/repo --older-than 60

# Custom protected branches
git-branch-pruner prune --protected main,staging,production

# Full pipeline: list, review, then prune
git-branch-pruner list --older-than 30
git-branch-pruner prune --older-than 30 --dry-run
git-branch-pruner prune --older-than 30
```

## License

MIT
