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
        return f"Error: {e}"

print("Checking Authors count:")
print(run_sql("SELECT count() FROM author;"))
print("Checking Commits count:")
print(run_sql("SELECT count() FROM commit;"))
print("Checking Files count:")
print(run_sql("SELECT count() FROM file;"))
print("First Author name:")
print(run_sql("SELECT name FROM author LIMIT 1;"))
