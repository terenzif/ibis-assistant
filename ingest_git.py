import subprocess
import json
import urllib.request
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
    except urllib.error.URLError as e:
        print(f"Error querying DB: {e}")
        if hasattr(e, 'read'):
            print(e.read().decode())
        return None

def run_git_log():
    cmd = [
        'git', 'log', 
        '--pretty=format:COMMIT|%H|%an|%aI|%s', 
        '--name-only',
        '--reverse'
    ]
    result = subprocess.run(cmd, capture_output=True, text=True, encoding='utf-8', errors='replace')
    return result.stdout.splitlines()

def main():
    print("Connecting to SurrealDB (REST)...")
    
    # Init DB
    print("Cleaning old data...")
    run_sql("DELETE author; DELETE commit; DELETE file; DELETE changed; DELETE authored;")

    print("Reading git history...")
    lines = run_git_log()
    
    current_commit = None
    count = 0
    batch_sql = ""
    
    print("Ingesting data...")
    
    for line in lines:
        line = line.strip()
        if not line:
            continue
            
        if line.startswith("COMMIT|"):
            parts = line.split("|", 4)
            if len(parts) < 5: continue
            
            _, commit_hash, author_name, date, message = parts
            safe_message = message.replace("'", "\\'").replace('"', '\\"') # Escape
            safe_author = author_name.replace(" ", "_").replace("'", "").replace('"', '').lower()
            
            author_id = f"author:{safe_author}"
            commit_id = f"commit:{commit_hash}"
            current_commit = commit_id
            
            # Batch SQL
            batch_sql += f"UPSERT {author_id} SET name = '{author_name}';\n"
            batch_sql += f"CREATE {commit_id} SET hash = '{commit_hash}', date = '{date}', message = '{safe_message}';\n"
            batch_sql += f"RELATE {author_id}->authored->{commit_id};\n"
            
            count += 1
            
        else:
            file_path = line
            # Simple escape for file path to use as ID part
            safe_file = file_path.replace("/", "_").replace(".", "_").replace(" ", "_").replace("-", "_")
            file_id = f"file:{safe_file}"
            
            batch_sql += f"UPSERT {file_id} SET path = '{file_path}';\n"
            if current_commit:
                batch_sql += f"RELATE {current_commit}->changed->{file_id};\n"
        
        # Execute batch small to avoid HTTP issues
        if len(batch_sql) > 10000:
            run_sql(batch_sql)
            batch_sql = ""
            if count % 100 == 0:
                print(f"Processed {count} commits...")

    if batch_sql:
        run_sql(batch_sql)

    print(f"Finished! Ingested {count} commits.")

if __name__ == "__main__":
    main()
