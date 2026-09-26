#!/usr/bin/env bash
#
# prune-worktrees removes linked git worktrees under .worktrees/ whose
# corresponding GitHub PR is merged or closed, provided the worktree has no
# uncommitted changes.
#
set -euo pipefail

REPO_ROOT=$(cd "$(git rev-parse --git-common-dir)/.." && pwd -P)
CURRENT_DIR=$(pwd -P)

dry_run=0
if [[ "${1:-}" == "--dry-run" || "${1:-}" == "-n" ]]; then
  dry_run=1
fi

current_wt=""
current_branch=""

process_worktree() {
  local wt="$1"
  local branch="$2"

  if [[ -z "$wt" || -z "$branch" ]]; then
    return
  fi

  # Only inspect worktrees under .worktrees/
  if [[ "$wt" != "$REPO_ROOT/.worktrees/"* ]]; then
    return
  fi

  # Skip current worktree
  local abs_wt
  abs_wt=$(cd "$wt" 2>/dev/null && pwd -P || echo "$wt")
  if [[ "$abs_wt" == "$CURRENT_DIR" ]]; then
    echo "Skipping current worktree: $branch ($wt)"
    return
  fi

  # Check if worktree directory exists
  if [[ ! -d "$wt" ]]; then
    echo "Pruning missing worktree entry: $wt"
    if [[ "$dry_run" -eq 0 ]]; then
      git worktree prune
    fi
    return
  fi

  # Check PR status via gh
  local pr_state
  pr_state=$(gh pr view "$branch" --json state -q .state 2>/dev/null || true)

  if [[ "$pr_state" == "MERGED" || "$pr_state" == "CLOSED" ]]; then
    # Check for uncommitted changes
    local status
    status=$(git -C "$wt" status --porcelain 2>/dev/null || true)
    if [[ -n "$status" ]]; then
      echo "Skipping $branch ($wt): PR is $pr_state, but worktree has uncommitted changes"
      return
    fi

    if [[ "$dry_run" -eq 1 ]]; then
      echo "[dry-run] Would remove worktree for $pr_state PR: $branch ($wt)"
    else
      echo "Removing worktree for $pr_state PR: $branch ($wt)"
      git worktree remove "$wt"
      if [[ "$pr_state" == "MERGED" ]]; then
        git branch -d "$branch" 2>/dev/null || true
      fi
    fi
  elif [[ "$pr_state" == "OPEN" ]]; then
    echo "Keeping $branch ($wt): PR is still OPEN"
  else
    echo "Keeping $branch ($wt): no merged or closed PR found"
  fi
}

while IFS= read -r line || [[ -n "$line" ]]; do
  if [[ "$line" =~ ^worktree[[:space:]]+(.*) ]]; then
    current_wt="${BASH_REMATCH[1]}"
  elif [[ "$line" =~ ^branch[[:space:]]+refs/heads/(.*) ]]; then
    current_branch="${BASH_REMATCH[1]}"
  elif [[ -z "$line" ]]; then
    process_worktree "$current_wt" "$current_branch"
    current_wt=""
    current_branch=""
  fi
done < <(git worktree list --porcelain)

if [[ -n "$current_wt" && -n "$current_branch" ]]; then
  process_worktree "$current_wt" "$current_branch"
fi
