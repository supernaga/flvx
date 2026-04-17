import json
import os
import subprocess
import time
import urllib.request
from urllib.error import URLError

def post_json(url, payload, token=None):
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = token
    req = urllib.request.Request(url, data=json.dumps(payload).encode(), headers=headers, method="POST")
    with urllib.request.urlopen(req) as resp:
        return json.loads(resp.read().decode())

def put_json(url, payload, token=None):
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = token
    req = urllib.request.Request(url, data=json.dumps(payload).encode(), headers=headers, method="PUT")
    with urllib.request.urlopen(req) as resp:
        return json.loads(resp.read().decode())

def get_json(url, token=None):
    headers = {}
    if token:
        headers["Authorization"] = token
    req = urllib.request.Request(url, headers=headers, method="GET")
    with urllib.request.urlopen(req) as resp:
        return json.loads(resp.read().decode())

def wait_for_backend(url, timeout=15):
    start = time.time()
    while time.time() - start < timeout:
        try:
            with urllib.request.urlopen(url) as resp:
                if resp.status == 200:
                    return True
        except Exception:
            pass
        time.sleep(0.5)
    return False

def main():
    print("Starting E2E Dash Test...")

    # Start backend
    backend_env = os.environ.copy()
    backend_env["SERVER_ADDR"] = ":8365"
    backend_env["DB_PATH"] = "test_e2e_dash.db"
    backend_env["DASH_RUNTIME_ENABLED"] = "1"
    backend_env["JWT_SECRET"] = "testsecret"
    
    if os.path.exists("go-backend/test_e2e_dash.db"):
        os.remove("go-backend/test_e2e_dash.db")

    backend_out = open("backend_out.log", "w")
    backend_proc = subprocess.Popen(
        ["go", "run", "./cmd/paneld"],
        cwd="go-backend",
        env=backend_env,
        stdout=backend_out,
        stderr=backend_out,
    )
    
    agent_out = open("agent_out.log", "w")
    agent_proc = None

    try:
        if not wait_for_backend("http://127.0.0.1:8365/api/v1/auth/info"):
            print("Backend failed to start")
            backend_proc.terminate()
            backend_proc.wait()
            with open("backend_out.log", "r") as f:
                print("Backend logs:\n", f.read())
            return

        # Login
        login_res = post_json("http://127.0.0.1:8365/api/v1/user/login", {"username": "admin_user", "password": "admin_user"})
        token = login_res["data"]["token"]
        print("Logged in successfully.")

        # Switch engine to dash
        switch_res = put_json("http://127.0.0.1:8365/api/v1/system/runtime", {"engine": "dash"}, token)
        print("Switched runtime engine to dash:", switch_res)

        # Create node
        post_json("http://127.0.0.1:8365/api/v1/node/create", {
            "name": "test-dash-node",
            "serverIp": "127.0.0.1",
            "tcpListenAddr": "127.0.0.1",
            "port": "20000-20100"
        }, token)
        
        nodes_res = post_json("http://127.0.0.1:8365/api/v1/node/list", {"page":1, "pageSize": 10}, token)
        print("nodes_res:", nodes_res)
        node = next((n for n in nodes_res["data"] if n["name"] == "test-dash-node"), None)
        node_id = node["id"]
        
        # Set site IP
        post_json("http://127.0.0.1:8365/api/v1/config/update-single", {"name": "ip", "value": "127.0.0.1:8365"}, token)

        install_res = post_json("http://127.0.0.1:8365/api/v1/node/install", {"id": node_id}, token)
        print("install_res:", install_res)
        cmd = install_res["data"]
        # Extract secret from "install.sh -a ... -s <SECRET>"
        import re
        node_secret = re.search(r"-s '([^']+)'", cmd).group(1) if re.search(r"-s '([^']+)'", cmd) else re.search(r"-s ([^\s]+)", cmd).group(1)
        print("Created node ID:", node_id)

        # Start Gost agent
        with open("go-gost/config.json", "w") as f:
            json.dump({
                "addr": "127.0.0.1:8365",
                "secret": node_secret,
                "http": 20080,
                "tls": 20443,
                "socks": 21080
            }, f)
        
        with open("go-gost/gost.json", "w") as f:
            f.write("{}")

        agent_proc = subprocess.Popen(
            ["go", "run", "."],
            cwd="go-gost",
            stdout=agent_out,
            stderr=agent_out,
        )

        try:
            print("Waiting for node to become online...")
            online = False
            for _ in range(15):
                nodes = post_json("http://127.0.0.1:8365/api/v1/node/list", {}, token)
                node = next((n for n in nodes["data"] if n["id"] == node_id), None)
                if node and node.get("status") == 1:
                    online = True
                    break
                time.sleep(1)
            
            if not online:
                print("Node failed to come online.")
                agent_proc.terminate()
                agent_proc.wait()
                with open("agent_out.log", "r") as f:
                    print("Agent logs:\n", f.read())
                return
            
            print("Node is online.")

            # Create tunnel
            tunnel_res = post_json("http://127.0.0.1:8365/api/v1/tunnel/create", {
                "name": "dash-tunnel",
                "type": 2, # Transit tunnel
                "protocol": "tls",
                "flow": 1,
                "chainGroups": [
                    {"nodes": [node_id]}
                ]
            }, token)
            tunnel_id = tunnel_res["data"]["id"]
            print("Created tunnel ID:", tunnel_id)

            # Create forward
            forward_res = post_json("http://127.0.0.1:8365/api/v1/forward/create", {
                "name": "dash-forward",
                "tunnelId": tunnel_id,
                "nodeId": node_id,
                "inIp": "127.0.0.1",
                "inPort": 20001,
                "remoteAddr": "127.0.0.1:80",
                "protocol": "tcp",
                "strategy": "round"
            }, token)
            forward_id = forward_res["data"]["id"]
            print("Created forward ID:", forward_id)

            print("Waiting for rule apply...")
            time.sleep(3)

            # Check tunnel status
            tunnels = post_json("http://127.0.0.1:8365/api/v1/tunnel/list", {}, token)
            tunnel = next((t for t in tunnels["data"] if t["id"] == tunnel_id), None)
            print("Tunnel status:", tunnel.get("status"))

            forwards = post_json("http://127.0.0.1:8365/api/v1/forward/list", {"nodeId": node_id}, token)
            forward = next((f for f in forwards["data"] if f["id"] == forward_id), None)
            print("Forward status:", forward.get("status"))
            
            print("E2E Test completed successfully.")

        finally:
            if agent_proc:
                agent_proc.terminate()
                agent_proc.wait()
            if os.path.exists("go-gost/config.json"):
                os.remove("go-gost/config.json")
            if os.path.exists("go-gost/gost.json"):
                os.remove("go-gost/gost.json")

    finally:
        backend_proc.terminate()
        backend_proc.wait()
        if os.path.exists("go-backend/test_e2e_dash.db"):
            os.remove("go-backend/test_e2e_dash.db")
        backend_out.close()
        agent_out.close()

if __name__ == "__main__":
    main()
