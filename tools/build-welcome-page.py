#!/usr/bin/env python3
"""Render the public static Welcome page for GitHub Pages.

The application renders the same template at runtime with deployment-specific
URLs. GitHub Pages has no runtime configuration, so this script requires the
public values from the publishing workflow and refuses to publish unresolved
placeholders.
"""

import html
import os
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
TEMPLATE = ROOT / "internal" / "dashboard" / "welcome.html"
OUTPUT = ROOT / "site" / "index.html"


def quota_label(value: str) -> str:
    number = float(value)
    for divisor, suffix in ((1e9, "B"), (1e6, "M"), (1e3, "K")):
        if number >= divisor:
            rendered = f"{number / divisor:g}"
            return rendered + suffix
    return f"{number:g}"


def required(name: str) -> str:
    value = os.environ.get(name, "").strip()
    if not value:
        raise SystemExit(f"missing required environment variable: {name}")
    return value.rstrip("/")


page = TEMPLATE.read_text()
gateway = required("WELCOME_GATEWAY_URL")
dashboard = required("WELCOME_DASHBOARD_URL")
quota = os.environ.get("MONTHLY_TOKEN_QUOTA", "10000000000").strip()

page = page.replace("{{GATEWAY_URL}}", html.escape(gateway, quote=True))
page = page.replace("{{DASHBOARD_URL}}", html.escape(dashboard, quote=True))
page = page.replace("{{QUOTA_LABEL}}", html.escape(quota_label(quota), quote=True))

placeholders = ("{{GATEWAY_URL}}", "{{DASHBOARD_URL}}", "{{QUOTA_LABEL}}")
if any(placeholder in page for placeholder in placeholders):
    raise SystemExit("unresolved Welcome page placeholder remains in generated page")

OUTPUT.parent.mkdir(parents=True, exist_ok=True)
OUTPUT.write_text(page)
print(f"wrote {OUTPUT}")
