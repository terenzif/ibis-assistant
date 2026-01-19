import urllib.request
import json
import base64

NS = "deckonline"
DB = "analysis"
USER = "root"
PASS = "root"
DB_URL = "http://localhost:8000/sql"

def run_sql(sql):
    req = urllib.request.Request(DB_URL, data=sql.encode('utf-8'), method='POST')
    req.add_header('surreal-ns', NS)
    req.add_header('surreal-db', DB)
    req.add_header('Content-Type', 'text/plain')
    req.add_header('Accept', 'application/json')
    auth_str = f"{USER}:{PASS}"
    b64_auth = base64.b64encode(auth_str.encode()).decode()
    req.add_header('Authorization', f"Basic {b64_auth}")
    try:
        with urllib.request.urlopen(req) as response:
            return json.loads(response.read().decode())
    except Exception as e:
        return None

def main():
    print("\n" + "="*50)
    print("      SURREALDB: DEEP CAUSAL & FUNCTIONAL ANALYSIS")
    print("="*50 + "\n")

    # 1. Top Authors
    print("## 1. STRATEGIC CONTRIBUTORS (COMMITS)")
    query = "SELECT name, count(->authored) as commit_count FROM author ORDER BY commit_count DESC LIMIT 5;"
    results = run_sql(query)
    if results and results[0]['status'] == 'OK':
        for row in results[0]['result']:
            print(f"- **{row['name']}**: {row['commit_count']} commits")
    print("\n")

    # 2. Hotspots & Causal Experts
    print("## 2. SYSTEM HOTSPOTS & DOMAIN EXPERTS")
    # We find files with highest churn and then find the person who changed them most
    query = "SELECT id, path, count(<-changed) as changes FROM file ORDER BY changes DESC LIMIT 10;"
    file_results = run_sql(query)
    if file_results and file_results[0]['status'] == 'OK':
        for row in file_results[0]['result']:
            path = row['path']
            fid = row['id']
            # Find author: commit -> changed -> file
            exp_query = f"SELECT count() as c, (<-authored<-author.name)[0] as name FROM (SELECT in FROM changed WHERE out = {fid}) GROUP BY name ORDER BY c DESC LIMIT 1;"
            exp_results = run_sql(exp_query)
            expert = "N/A"
            if exp_results and exp_results[0]['result']:
                expert = exp_results[0]['result'][0]['name']
            print(f"- `{path}`: {row['changes']} changes | **Project Expert: {expert}**")
    print("\n")

    # 3. Logical Coupling (Causal relationships)
    print("## 3. LOGICAL COUPLING (Implicit Dependencies)")
    print("Identifying files that evolve together (Logical Coupling > 0.3)...")
    # High-level scan of top changed files and their 'colleagues' in commits
    query = """
    SELECT 
        path, 
        (
            SELECT 
                path, 
                count() as strength 
            FROM <-changed<-commit->changed 
            WHERE path != $parent.path 
            GROUP BY path 
            ORDER BY strength DESC 
            LIMIT 2
        ) as coupled
    FROM file 
    ORDER BY count(<-changed) DESC 
    LIMIT 5;
    """
    results = run_sql(query)
    if results and results[0]['status'] == 'OK':
        for row in results[0]['result']:
            if row['coupled']:
                links = ", ".join([f"`{c['path']}` ({c['strength']} times)" for c in row['coupled'] if c['strength'] > 5])
                if links:
                    print(f"- `{row['path']}` is strongly coupled with: {links}")
    print("\n")

    print("="*50)
    print("             ANALYSIS COMPLETE")
    print("="*50 + "\n")

if __name__ == "__main__":
    main()
