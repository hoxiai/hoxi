#!/usr/bin/env python3
"""Enforce the single network entry points declared in AGENTS.md 2.9.

Rules:
  1. `Dio(` may only appear under lib/core/network/.
  2. Raw `HttpClient(` may only appear under lib/core/network/ or
     lib/features/auth/data/ (zero-dependency LAN subnet discovery probes).
  3. The hoxi.ai base URL may only be declared in lib/core/network/hoxi_site_base_url.dart
     (Brand.domain is an identifier, not a request target).
  4. WebSocket connections may only be opened under lib/core/network/
     (use connectNodeWebSocket); HTTP/SSE go through the unified ApiClients.
"""
from pathlib import Path
import re
import sys

root = Path(__file__).resolve().parents[1]
lib = root / 'lib'

DIO_ALLOWED = {lib / 'core' / 'network'}
HTTP_CLIENT_ALLOWED = {
    lib / 'core' / 'network',
    lib / 'features' / 'auth' / 'data',
}
BASE_URL_ALLOWED = {lib / 'core' / 'network' / 'hoxi_site_base_url.dart'}
WS_ALLOWED = {lib / 'core' / 'network'}

failures = []

for path in sorted(lib.rglob('*.dart')):
    if 'l10n/gen' in path.parts:
        continue
    text = path.read_text(encoding='utf-8')
    rel = path.relative_to(root)
    # Strip line comments (`//` and `///`) so explanatory comments do not false-positive.
    code = '\n'.join(
        line for line in text.splitlines() if not line.lstrip().startswith('//')
    )
    if re.search(r'\bDio\s*\(', code) and not any(
        parent == path.parent or parent in path.parents for parent in DIO_ALLOWED
    ):
        failures.append(
            f'{rel}: create Dio only in lib/core/network (use ApiClient / '
            'SiteApiClient / ExternalApiClient)'
        )
    if re.search(r'\bHttpClient\s*\(', code) and not any(
        parent == path.parent or parent in path.parents
        for parent in HTTP_CLIENT_ALLOWED
    ):
        failures.append(
            f'{rel}: create raw HttpClient only in lib/core/network or '
            'lib/features/auth/data LAN discovery'
        )
    if 'https://hoxi.ai' in code and path not in BASE_URL_ALLOWED:
        failures.append(
            f'{rel}: do not hardcode https://hoxi.ai; use HoxiSiteBaseUrl.value'
        )
    if re.search(
        r'\b(?:IOWebSocketChannel|WebSocketChannel|WebSocket)\s*\.\s*connect\s*\(',
        code,
    ) and not any(
        parent == path.parent or parent in path.parents for parent in WS_ALLOWED
    ):
        failures.append(
            f'{rel}: open WebSocket only in lib/core/network '
            '(use connectNodeWebSocket)'
        )

if failures:
    print('\n'.join(failures), file=sys.stderr)
    sys.exit(1)
print('Network entry points are respected.')
