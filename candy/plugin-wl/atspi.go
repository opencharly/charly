package main

// atspiScript is the Python helper for AT-SPI2 introspection, embedded as a Go constant and
// executed on the venue via `/usr/bin/python3 -c` (ported verbatim from charly/wl.go). The
// system python is used explicitly so the system RPM packages (python3-pyatspi,
// python3-gobject) resolve, not a pixi python first on PATH.
const atspiScript = `
import gi, json, sys
gi.require_version("Atspi", "2.0")
from gi.repository import Atspi

Atspi.init()
action = sys.argv[1] if len(sys.argv) > 1 else "tree"
query = sys.argv[2] if len(sys.argv) > 2 else ""

def node_info(node, depth=0):
    if not node:
        return None
    name = node.get_name() or ""
    role = node.get_role_name() or ""
    info = {"name": name, "role": role, "depth": depth}
    try:
        comp = node.get_component_iface()
        if comp:
            ext = comp.get_extents(Atspi.CoordType.SCREEN)
            info["x"] = ext.x
            info["y"] = ext.y
            info["width"] = ext.width
            info["height"] = ext.height
    except Exception:
        pass
    acts = node.get_action_iface()
    if acts:
        info["actions"] = [acts.get_action_name(i) for i in range(acts.get_n_actions())]
    return info

def walk(node, depth=0, results=None):
    if results is None:
        results = []
    if not node:
        return results
    info = node_info(node, depth)
    if info:
        results.append(info)
    for i in range(node.get_child_count()):
        walk(node.get_child_at_index(i), depth + 1, results)
    return results

def find_matches(node, query, depth=0, results=None):
    if results is None:
        results = []
    if not node:
        return results
    name = (node.get_name() or "").lower()
    role = (node.get_role_name() or "").lower()
    q = query.lower()
    if ":" in q:
        qn, qr = q.split(":", 1)
        match = (qn in name) and (qr in role)
    else:
        match = (q in name) or (q in role)
    if match:
        info = node_info(node, depth)
        if info:
            results.append(info)
    for i in range(node.get_child_count()):
        find_matches(node.get_child_at_index(i), query, depth + 1, results)
    return results

def click_match(node, query, depth=0):
    if not node:
        return False
    name = (node.get_name() or "").lower()
    role = (node.get_role_name() or "").lower()
    q = query.lower()
    if ":" in q:
        qn, qr = q.split(":", 1)
        match = (qn in name) and (qr in role)
    else:
        match = (q in name) or (q in role)
    if match:
        acts = node.get_action_iface()
        if acts:
            for i in range(acts.get_n_actions()):
                aname = acts.get_action_name(i)
                if aname in ("click", "press", "activate"):
                    acts.do_action(i)
                    print(json.dumps({"clicked": True, "name": node.get_name(), "role": node.get_role_name(), "action": aname}))
                    return True
    for i in range(node.get_child_count()):
        if click_match(node.get_child_at_index(i), query, depth + 1):
            return True
    return False

desktop = Atspi.get_desktop(0)
if action == "tree":
    results = []
    for i in range(desktop.get_child_count()):
        app = desktop.get_child_at_index(i)
        results.extend(walk(app))
    print(json.dumps(results, indent=2))
elif action == "find":
    if not query:
        print("Error: query required for find", file=sys.stderr)
        sys.exit(1)
    results = []
    for i in range(desktop.get_child_count()):
        app = desktop.get_child_at_index(i)
        results.extend(find_matches(app, query))
    print(json.dumps(results, indent=2))
elif action == "click":
    if not query:
        print("Error: query required for click", file=sys.stderr)
        sys.exit(1)
    found = False
    for i in range(desktop.get_child_count()):
        app = desktop.get_child_at_index(i)
        if click_match(app, query):
            found = True
            break
    if not found:
        print(json.dumps({"clicked": False, "error": f"no clickable element matching '{query}'"}))
        sys.exit(1)
else:
    print(f"Unknown action: {action}", file=sys.stderr)
    sys.exit(1)
`
