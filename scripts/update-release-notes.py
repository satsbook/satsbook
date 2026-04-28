#!/usr/bin/env python3
"""Replace the releaseNotes field in an umbrel-app.yml file."""
import re
import sys

if len(sys.argv) != 3:
    print(f"Usage: {sys.argv[0]} <notes> <app-file>", file=sys.stderr)
    sys.exit(1)

notes = sys.argv[1]
app_file = sys.argv[2]

content = open(app_file).read()
content = re.sub(
    r"releaseNotes:.*?(?=\n\S)",
    "releaseNotes: >-\n  " + notes.rstrip() + "\n",
    content,
    count=1,
    flags=re.DOTALL,
)
open(app_file, "w").write(content)
