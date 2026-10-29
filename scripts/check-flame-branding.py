#!/usr/bin/env python3
"""Fail when user-visible product branding regresses to DJOneHub."""

from pathlib import Path
import re
import sys

ROOT = Path(__file__).resolve().parents[1]
USER_FACING_FILES = [
    "app/Sources/IncomingCallCard.swift",
    "app/Sources/Views/ContentView.swift",
    "app/Sources/Views/TrafficRoutingView.swift",
    "app/Sources/Views/CallView.swift",
    "app/Sources/Views/AIIntegrationView.swift",
    "app/Sources/Views/HomeView.swift",
    "app/Sources/Views/AboutView.swift",
    "app/Sources/DashboardStore.swift",
    "app/Sources/ESIMQRCode.swift",
    "app/Sources/RoutingStore.swift",
    "app/Sources/CLIIntegrationManager.swift",
    "app/Sources/DJOneHubNativeApp.swift",
    "scripts/dmg-background.svg",
    "scripts/dmgbuild.json",
    "mise.toml",
]

# Internal Swift type names, executable names and explicit upstream attribution are
# intentionally compatible for now; only quoted/user-facing text is checked.
OLD_BRAND_IN_LITERAL = re.compile(r'["“][^"”\n]*DJOneHub[^"”\n]*["”]')
ALLOWED_LITERAL_FRAGMENTS = (
    "DJOneHubNative",
    "ZenGeekLabs/DJOneHub",
    "cr-zhichen/DJOneHubNative",
    "Build/Products/Release/DJOneHub.app",
    "pkill -x DJOneHub",
    "DJOneHub Dev Local Signing",
)
ALLOWED_LINE_FRAGMENTS = (
    'product: "DJOneHub"',
    'marker.product == "DJOneHub"',
)

failures: list[str] = []
for relative in USER_FACING_FILES:
    path = ROOT / relative
    text = path.read_text(encoding="utf-8")
    for line_number, line in enumerate(text.splitlines(), 1):
        if any(fragment in line for fragment in ALLOWED_LINE_FRAGMENTS):
            continue
        for match in OLD_BRAND_IN_LITERAL.finditer(line):
            literal = match.group(0)
            if any(fragment in literal for fragment in ALLOWED_LITERAL_FRAGMENTS):
                continue
            failures.append(f"{relative}:{line_number}: {literal}")

plist = (ROOT / "app/Resources/Info.plist").read_text(encoding="utf-8")
if "DJI One 4G 模块" in plist:
    failures.append("app/Resources/Info.plist: microphone usage text still exposes old DJI One product wording")

project = (ROOT / "project.yml").read_text(encoding="utf-8")
for required in (
    'DISPLAY_NAME: "Flame DJ Hub"',
    "PRODUCT_BUNDLE_IDENTIFIER: cn.flameapi.flamedjhub.dev",
):
    if required not in project:
        failures.append(f"project.yml: missing {required}")

if failures:
    print("Flame branding check failed:")
    for failure in failures:
        print(f"- {failure}")
    sys.exit(1)

print("Flame branding check passed")
