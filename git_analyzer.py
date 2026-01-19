import subprocess
import collections
from datetime import datetime
import os

def run_git_command(args):
    """Runs a git command and returns the output as a list of strings."""
    try:
        result = subprocess.run(
            ['git'] + args,
            capture_output=True,
            text=True,
            encoding='utf-8',
            check=True
        )
        return result.stdout.strip().split('\n')
    except subprocess.CalledProcessError as e:
        print(f"Error running git command: {e}")
        return []

def analyze_history():
    print("# Git History Analysis Report\n")

    # 1. General Stats
    commits = run_git_command(['log', '--format=%at'])
    if not commits:
        print("No commits found.")
        return

    total_commits = len(commits)
    timestamps = [int(ts) for ts in commits if ts.strip()]
    timestamps.sort()
    
    start_date = datetime.fromtimestamp(timestamps[0])
    end_date = datetime.fromtimestamp(timestamps[-1])
    duration = end_date - start_date
    
    print("## General Statistics")
    print(f"- **Total Commits**: {total_commits}")
    print(f"- **First Commit**: {start_date.strftime('%Y-%m-%d')}")
    print(f"- **Last Commit**: {end_date.strftime('%Y-%m-%d')}")
    print(f"- **Project Age**: {duration.days} days")
    print("\n")

    # 2. Activity Trends (Commits per Year)
    print("## Activity by Year")
    years = [datetime.fromtimestamp(ts).year for ts in timestamps]
    year_counts = collections.Counter(years)
    for year in sorted(year_counts.keys()):
        print(f"- **{year}**: {year_counts[year]} commits")
    print("\n")

    # 3. Top Contributors
    print("## Top Contributors")
    authors = run_git_command(['log', '--format=%aN'])
    author_counts = collections.Counter(authors)
    for author, count in author_counts.most_common(10):
        print(f"- **{author}**: {count} commits")
    print("\n")

    # 4. File Hotspots (Most frequently changed files)
    # Limit to top 10, exclude merges
    print("## File Hotspots (Top 10)")
    # --name-only lists changed files, --format='' suppresses commit info
    files = run_git_command(['log', '--name-only', '--format=', '--no-merges'])
    # Filter out empty lines
    files = [f for f in files if f.strip()]
    file_counts = collections.Counter(files)
    
    for file_path, count in file_counts.most_common(10):
        print(f"- `{file_path}`: {count} changes")
    print("\n")

if __name__ == "__main__":
    analyze_history()
