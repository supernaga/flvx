import json
import os
import subprocess
import time
import urllib.request
import re

PANEL_HOST = "3.38.98.31"
PANEL_URL = f"http://{PANEL_HOST}:6365"
NODE_HOST = "20.118.172.127"

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

def main():
    print("Testing remote E2E Dash flow...")

    # Login
    login_res = post_json(f"{PANEL_URL}/api/v1/user/login", {"username": "flvx", "password": "flvxflvx"})
    print("login res:", login_res)
    token = login_res["data"]["token"]
    print("Logged in successfully.")

    # Set site IP
    post_json(f"{PANEL_URL}/api/v1/config/update-single", {"name": "ip", "value": f"{PANEL_HOST}:6365"}, token)
    
    # Clean existing nodes
    nodes_res = post_json(f"{PANEL_URL}/api/v1/node/list", {"page":1, "pageSize": 100}, token)
    for n in nodes_res["data"]:
        post_json(f"{PANEL_URL}/api/v1/node/delete", {"id": n["id"]}, token)
        
    # Clean existing tunnels
    tunnels_res = post_json(f"{PANEL_URL}/api/v1/tunnel/list", {"page":1, "pageSize": 100}, token)
    for t in tunnels_res["data"]:
        post_json(f"{PANEL_URL}/api/v1/tunnel/delete-with-forwards", {"id": t["id"], "action": "delete_forwards"}, token)

    # Switch engine to dash
    switch_res = put_json(f"{PANEL_URL}/api/v1/system/runtime", {"engine": "dash"}, token)
    print("Switched runtime engine to dash:", switch_res)

    # Wait for switch to complete
    for _ in range(10):
        settings = get_json(f"{PANEL_URL}/api/v1/system/runtime", token)
        if settings["data"]["currentEngine"] == "dash":
            print("Successfully verified engine is now dash.")
            break
        time.sleep(1)
    else:
        print("Failed to switch engine to dash:", settings)
        exit(1)

    # Create node
    post_json(f"{PANEL_URL}/api/v1/node/create", {
        "name": "remote-dash-node",
        "serverIp": NODE_HOST,
        "tcpListenAddr": "0.0.0.0",
        "port": "20000-20100"
    }, token)
    
    nodes_res = post_json(f"{PANEL_URL}/api/v1/node/list", {"page":1, "pageSize": 10}, token)
    node = next((n for n in nodes_res["data"] if n["name"] == "remote-dash-node"), None)
    node_id = node["id"]
    
    install_res = post_json(f"{PANEL_URL}/api/v1/node/install", {"id": node_id}, token)
    cmd = install_res["data"]
    cmd = re.sub(r"curl -L [^\s]+ -o \./install\.sh", "curl -L https://raw.githubusercontent.com/Sagit-chu/flvx/main/install.sh -o ./install.sh", cmd)
    cmd = cmd.replace("VERSION=2.1.9", "VERSION=3.0.0-alpha5")
    print("Install command:", cmd)

    # Deploy node on remote host 2
    print(f"Deploying agent on {NODE_HOST}...")
    # cleanup first
    subprocess.run(["ssh", "-o", "StrictHostKeyChecking=no", "-i", os.path.expanduser("~/.ssh/id_ed25519"), f"root@{NODE_HOST}", 
                    "systemctl stop flux_agent dash; rm -rf /etc/flux_agent /usr/local/bin/flux_agent /usr/local/bin/dash; rm -f install.sh"])
    
    deploy_cmd = ["ssh", "-o", "StrictHostKeyChecking=no", "-i", os.path.expanduser("~/.ssh/id_ed25519"), f"root@{NODE_HOST}", cmd]
    res = subprocess.run(deploy_cmd)
    if res.returncode != 0:
        print("Failed to deploy agent")
        return
        
    print("Waiting for node to become online...")
    online = False
    for _ in range(30):
        nodes = post_json(f"{PANEL_URL}/api/v1/node/list", {"page":1, "pageSize":10}, token)
        node = next((n for n in nodes["data"] if n["id"] == node_id), None)
        if node and node.get("status") == 1:
            online = True
            break
        time.sleep(1)
    
    if not online:
        print("Node failed to come online.")
        subprocess.run(["ssh", "-o", "StrictHostKeyChecking=no", "-i", os.path.expanduser("~/.ssh/id_ed25519"), f"root@{NODE_HOST}", "journalctl -u flux_agent -n 50"])
        return
    
    print("Node is online.")

    # Create tunnel
    tunnel_res = post_json(f"{PANEL_URL}/api/v1/tunnel/create", {
        "name": "dash-tunnel",
        "type": 1,
        "flow": 1,
        "inNodeId": [{"nodeId": node_id, "protocol": "tcp"}],
        "outNodeId": [{"nodeId": node_id, "protocol": "tcp"}],
        "status": 1
    }, token)
    print("tunnel_res:", tunnel_res)
    tunnels_res = post_json(f"{PANEL_URL}/api/v1/tunnel/list", {"page":1, "pageSize":10}, token)
    tunnel = next((t for t in tunnels_res["data"] if t["name"] == "dash-tunnel"), None)
    tunnel_id = tunnel["id"]
    print("Created tunnel ID:", tunnel_id)

    # Create forward
    forward_res = post_json(f"{PANEL_URL}/api/v1/forward/create", {
        "name": "dash-forward",
        "tunnelId": tunnel_id,
        "nodeId": node_id,
        "inIp": "0.0.0.0",
        "inPort": 20001,
        "remoteAddr": "127.0.0.1:20080",
        "protocol": "tcp",
        "strategy": "round"
    }, token)
    print("forward_res:", forward_res)
    forwards_res = post_json(f"{PANEL_URL}/api/v1/forward/list", {"page":1, "pageSize":10, "nodeId": node_id}, token)
    forward = next((f for f in forwards_res["data"] if f["name"] == "dash-forward"), None)
    forward_id = forward["id"]
    print("Created forward ID:", forward_id)

    print("Waiting for rule apply...")
    time.sleep(5)

    # Check tunnel status
    tunnels = post_json(f"{PANEL_URL}/api/v1/tunnel/list", {"page":1, "pageSize":10}, token)
    tunnel = next((t for t in tunnels["data"] if t["id"] == tunnel_id), None)
    print("Tunnel status:", tunnel.get("status"))

    forwards = post_json(f"{PANEL_URL}/api/v1/forward/list", {"page":1, "pageSize":10, "nodeId": node_id}, token)
    forward = next((f for f in forwards["data"] if f["id"] == forward_id), None)
    print("Forward status:", forward.get("status"))
    
    print("Verifying traffic with actual HTTP request to local test server...")
    # Start a dummy HTTP server on the remote host to receive the forwarded traffic
    subprocess.run(["ssh", "-o", "StrictHostKeyChecking=no", "-i", os.path.expanduser("~/.ssh/id_ed25519"), f"root@{NODE_HOST}", "python3 -m http.server 20080 >/dev/null 2>&1 &"])
    time.sleep(2)

    curl_res = subprocess.run(["ssh", "-o", "StrictHostKeyChecking=no", "-i", os.path.expanduser("~/.ssh/id_ed25519"), f"root@{NODE_HOST}", "curl -s -m 10 http://127.0.0.1:20001"], capture_output=True, text=True)
    
    # Cleanup the dummy HTTP server
    subprocess.run(["ssh", "-o", "StrictHostKeyChecking=no", "-i", os.path.expanduser("~/.ssh/id_ed25519"), f"root@{NODE_HOST}", "pkill -f 'python3 -m http.server 20080' || true"])

    if curl_res.returncode == 0 and "Directory listing for" in curl_res.stdout:
        print("Traffic verified: Forward port is passing traffic to local test server correctly!")
    else:
        print("Traffic verify failed.")
        print("curl return code:", curl_res.returncode)
        print("curl stdout:", curl_res.stdout)
        print("curl stderr:", curl_res.stderr)
        
        # Output agent logs for debugging
        print("\nAgent Logs:")
        subprocess.run(["ssh", "-o", "StrictHostKeyChecking=no", "-i", os.path.expanduser("~/.ssh/id_ed25519"), f"root@{NODE_HOST}", "journalctl -u flux_agent -n 100 --no-pager"])
        exit(1)

    print("E2E Test completed successfully.")

if __name__ == "__main__":
    main()
