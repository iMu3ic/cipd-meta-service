#!/usr/bin/env python3
"""Print the CIPD packages installed under a checkout, one line per package.

    scripts/cipd_state.py ~/webrtc > state.txt

Run it on a checkout made against real CIPD and on one made through
cipd-meta-service, then `diff` the two outputs. Lines are
"<root>\\t<description.json>\\t<instance ID>".
"""
import json
import os
import sys


def current(slot):
    cur = os.path.join(slot, "_current")
    try:
        return os.path.basename(os.readlink(cur))
    except OSError:  # not a symlink (Windows): the file holds the instance ID
        with open(cur) as f:
            return f.read().strip()


def main():
    if len(sys.argv) != 2:
        sys.exit("usage: cipd_state.py <checkout dir>")
    top = os.path.abspath(sys.argv[1])
    lines = []
    for dirpath, dirnames, _ in os.walk(top):
        if os.path.basename(dirpath) == ".cipd" and os.path.isdir(os.path.join(dirpath, "pkgs")):
            dirnames[:] = []
            pkgs = os.path.join(dirpath, "pkgs")
            for name in sorted(os.listdir(pkgs)):
                slot = os.path.join(pkgs, name)
                if not os.path.isdir(slot):
                    continue
                with open(os.path.join(slot, "description.json")) as f:
                    desc = json.dumps(json.load(f), sort_keys=True)
                root = os.path.relpath(os.path.dirname(dirpath), top)
                lines.append("%s\t%s\t%s" % (root, desc, current(slot)))
            continue
        dirnames[:] = [d for d in dirnames if d not in (".git", "out", "node_modules")]
    for line in sorted(lines):
        print(line)


if __name__ == "__main__":
    main()
