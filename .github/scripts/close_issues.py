#!/usr/bin/env python3
"""
Scan commits and merged PRs targeting dev/main for closing keywords (closes/fixes/resolves)
and automatically close the referenced GitHub issues when code reaches the main branch.
"""

import json
import os
import re
import subprocess
import sys

# Matches keywords followed by one or more issue numbers:
# e.g., "Closes #102", "fixes #103, #104", "Resolves: #105 and #106", "(closes #100)"
KEYWORD_LIST_PATTERN = re.compile(
    r'(?i)(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\s*[:=]?\s*((?:#\d+[\s,and&]*)+)'
)
ISSUE_NUM_PATTERN = re.compile(r'#(\d+)')


def extract_issues(text: str) -> set[int]:
    if not text:
        return set()
    issues = set()
    for match in KEYWORD_LIST_PATTERN.finditer(text):
        num_str = match.group(1)
        for num in ISSUE_NUM_PATTERN.findall(num_str):
            try:
                issues.add(int(num))
            except ValueError:
                pass
    return issues


def run_cmd(cmd: list[str]) -> tuple[int, str]:
    res = subprocess.run(cmd, capture_output=True, text=True)
    return res.returncode, res.stdout.strip()


def main():
    event_name = os.environ.get("EVENT_NAME", "").strip().lower()
    issues_to_close: dict[int, str] = {}

    print(f"--> Processing event: {event_name}")

    # 1. Pull Request merged directly into main
    if event_name == "pull_request":
        pr_num = os.environ.get("PR_NUMBER", "").strip()
        pr_title = os.environ.get("PR_TITLE", "").strip()
        pr_body = os.environ.get("PR_BODY", "").strip()

        combined = f"{pr_title}\n{pr_body}"
        found = extract_issues(combined)
        print(f"--> Found {len(found)} issues in PR #{pr_num}: {sorted(found)}")
        for num in found:
            issues_to_close[num] = f"Resolved in PR #{pr_num} ({pr_title})."

    # 2. Push directly to main (includes fast-forward git merge dev)
    elif event_name == "push":
        before_sha = os.environ.get("BEFORE_SHA", "").strip()
        after_sha = os.environ.get("AFTER_SHA", "HEAD").strip()

        commit_args = ["-n", "50"]
        if before_sha and before_sha != "0" * 40:
            rc, _ = run_cmd(["git", "rev-parse", "--verify", before_sha])
            if rc == 0:
                commit_args = [f"{before_sha}..{after_sha}"]

        print(f"--> Inspecting commit log range: {' '.join(commit_args)}")
        rc, log_output = run_cmd(["git", "log"] + commit_args + ["--pretty=format:%B"])
        if rc == 0 and log_output:
            commit_issues = extract_issues(log_output)
            print(f"--> Found {len(commit_issues)} issues in commit log: {sorted(commit_issues)}")
            for num in commit_issues:
                issues_to_close[num] = f"Resolved in commit {after_sha[:7]} on main."

        # Scan recently merged PRs into dev whose commits are now reachable in main
        print("--> Scanning recently merged PRs into dev...")
        rc, pr_json = run_cmd([
            "gh", "pr", "list",
            "--state", "merged",
            "--base", "dev",
            "--limit", "30",
            "--json", "number,title,body,mergeCommit"
        ])
        if rc == 0 and pr_json:
            try:
                prs = json.loads(pr_json)
                for pr in prs:
                    pr_num = pr.get("number")
                    pr_title = pr.get("title", "")
                    pr_body = pr.get("body", "")
                    merge_oid = (pr.get("mergeCommit") or {}).get("oid", "")

                    pr_issues = extract_issues(f"{pr_title}\n{pr_body}")
                    if not pr_issues:
                        continue

                    # Check if this PR's merge commit is an ancestor of HEAD
                    is_in_main = False
                    if merge_oid:
                        anc_rc, _ = run_cmd(["git", "merge-base", "--is-ancestor", merge_oid, "HEAD"])
                        if anc_rc == 0:
                            is_in_main = True

                    if is_in_main:
                        print(f"--> dev PR #{pr_num} is merged into main; linked issues: {sorted(pr_issues)}")
                        for num in pr_issues:
                            if num not in issues_to_close:
                                issues_to_close[num] = f"Resolved in dev PR #{pr_num} and merged to main."
            except Exception as e:
                print(f"Warning: Failed to parse merged PR list: {e}", file=sys.stderr)

    else:
        print(f"No action required for event: {event_name}")
        return

    if not issues_to_close:
        print("✓ No linked issues found to close.")
        return

    print(f"\n--> Checking status of {len(issues_to_close)} linked issue(s)...")
    for num, reason in sorted(issues_to_close.items()):
        rc, issue_json = run_cmd(["gh", "issue", "view", str(num), "--json", "state,url"])
        if rc != 0:
            print(f"Warning: Could not fetch info for #{num} (may be a pull request or not exist).")
            continue

        try:
            info = json.loads(issue_json)
            url = info.get("url", "")
            state = info.get("state", "").upper()

            # Ensure we only close issues, not pull requests
            if "/issues/" not in url:
                print(f"Skipping #{num} as it is a pull request ({url}).")
                continue

            if state == "OPEN":
                print(f"Closing open issue #{num} ({reason})...")
                close_rc, close_out = run_cmd([
                    "gh", "issue", "close", str(num),
                    "--reason", "completed",
                    "--comment", reason
                ])
                if close_rc == 0:
                    print(f"✓ Successfully closed #{num}")
                else:
                    print(f"Error: Failed to close #{num}: {close_out}", file=sys.stderr)
            else:
                print(f"Issue #{num} is already {state}.")
        except Exception as e:
            print(f"Error processing issue #{num}: {e}", file=sys.stderr)


if __name__ == "__main__":
    main()
