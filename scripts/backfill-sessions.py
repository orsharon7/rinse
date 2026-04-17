#!/usr/bin/env python3
"""Backfill today's RINSE cycles from log files into ~/.rinse/rinse.db"""

import re, sqlite3, uuid
from datetime import datetime
from pathlib import Path

DB_PATH = Path.home() / ".rinse" / "rinse.db"
LOG_DIR = Path.home() / ".rinse" / "sessions" / "raw-logs"

# Create DB and schema
DB_PATH.parent.mkdir(parents=True, exist_ok=True)
conn = sqlite3.connect(str(DB_PATH))
conn.executescript("""
CREATE TABLE IF NOT EXISTS sessions (
  id TEXT PRIMARY KEY,
  repo TEXT NOT NULL,
  pr_number INTEGER NOT NULL,
  pr_title TEXT,
  branch TEXT,
  started_at DATETIME NOT NULL,
  completed_at DATETIME,
  duration_seconds INTEGER,
  estimated_time_saved_seconds INTEGER,
  iterations INTEGER DEFAULT 0,
  total_comments_fixed INTEGER DEFAULT 0,
  outcome TEXT,
  model TEXT,
  log_file TEXT,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS comment_events (
  id TEXT PRIMARY KEY,
  session_id TEXT REFERENCES sessions(id),
  iteration INTEGER,
  comment_count INTEGER,
  recorded_at DATETIME
);
""")
conn.commit()

sessions_inserted = 0

for log_path in sorted(LOG_DIR.glob("rinse-pr*.log")):
    pr_match = re.search(r"rinse-pr(\d+)", log_path.name)
    if not pr_match:
        continue
    pr_num = int(pr_match.group(1))

    try:
        content = log_path.read_text(errors="ignore")

        # Timestamps
        timestamps = re.findall(r"\[(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})\]", content)
        if not timestamps:
            continue

        start_dt = datetime.strptime(timestamps[0], "%Y-%m-%d %H:%M:%S")
        end_dt = datetime.strptime(timestamps[-1], "%Y-%m-%d %H:%M:%S")
        duration = int((end_dt - start_dt).total_seconds())
        if duration < 10:
            continue

        # Iterations
        iterations = len(re.findall(r"━━━ Iteration \d+", content))
        if iterations == 0:
            iterations = len(re.findall(r"Iteration \d+\s+\d{2}:\d{2}", content))
        iterations = max(1, iterations)

        # Comments fixed (Copilot reply calls)
        comments_fixed = len(re.findall(r"comments/\d+/replies.*?POST", content))
        if comments_fixed == 0:
            comments_fixed = len(
                re.findall(r"reply_to.*?comment", content, re.IGNORECASE)
            )
        if comments_fixed == 0:
            # Fallback: count "Now reply to" lines
            comments_fixed = len(re.findall(r"Now reply to", content))

        # Outcome
        if "Merged PR" in content or (
            "squash" in content.lower() and "merged" in content.lower()
        ):
            outcome = "merged"
        elif "PR closed" in content or "closed pull request" in content.lower():
            outcome = "closed"
        elif "Copilot reviewing" in content:
            outcome = "open"
        else:
            outcome = "open"

        # Repo / branch from log
        repo_match = re.search(
            r"repo[=\s]+([a-z0-9_\-]+/[a-z0-9_\-]+)", content, re.IGNORECASE
        )
        repo = repo_match.group(1) if repo_match else "orsharon7/rinse"

        branch_match = re.search(r"branch '([^']+)'", content)
        branch = branch_match.group(1) if branch_match else None

        # PR title from log
        title_match = re.search(r"PR #\d+[:\s]+(.+?)[\n\r]", content)
        pr_title = title_match.group(1).strip()[:200] if title_match else None

        session_id = str(uuid.uuid4())
        estimated_saved = comments_fixed * 240  # 4 min per comment

        # Insert session
        try:
            conn.execute(
                """
                INSERT OR IGNORE INTO sessions
                (id, repo, pr_number, pr_title, branch, started_at, completed_at,
                 duration_seconds, estimated_time_saved_seconds, iterations,
                 total_comments_fixed, outcome, log_file)
                VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
            """,
                (
                    session_id,
                    repo,
                    pr_num,
                    pr_title,
                    branch,
                    start_dt.isoformat(),
                    end_dt.isoformat(),
                    duration,
                    estimated_saved,
                    iterations,
                    comments_fixed,
                    outcome,
                    log_path.name,
                ),
            )
            conn.commit()
            sessions_inserted += 1
            print(
                f"  ✅ PR #{pr_num:3d} | {duration // 60:3d}m | {iterations} iters | {comments_fixed} comments | {outcome}"
            )
        except Exception as e:
            print(f"  ⚠️  PR #{pr_num}: DB error: {e}")

    except Exception as e:
        print(f"  ❌ PR #{pr_num}: {e}")

# Summary stats
print(f"\n{'─' * 45}")
cur = conn.execute(
    "SELECT COUNT(*), SUM(duration_seconds), SUM(total_comments_fixed), SUM(estimated_time_saved_seconds), SUM(iterations) FROM sessions"
)
row = cur.fetchone()
total, dur, comments, saved, iters = row
print(f"  Cycles in DB:      {total}")
print(f"  Total run time:    {dur // 60 if dur else 0} min")
print(f"  Comments fixed:    {comments or 0}")
print(
    f"  Avg iterations:    {(iters / total):.1f}" if total else "  Avg iterations:   —"
)
print(f"  Est. time saved:   ~{(saved or 0) // 3600:.1f} hours")
print(f"\n  DB: {DB_PATH}")
conn.close()
