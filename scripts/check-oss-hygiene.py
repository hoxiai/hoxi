#!/usr/bin/env python3
"""Verify open-source repository hygiene across tracked files.

Ensures no local credentials, signing material, private workspace wiring,
internal commercial workflows, or hardcoded personal home paths are tracked
in the public repository.

Usage:
  python3 scripts/check-oss-hygiene.py [--dir <path>]
"""
from __future__ import annotations

import os
from pathlib import Path
import re
import subprocess
import sys

FORBIDDEN_DIR_PREFIXES = (
    '.ai/',
    'work/',
    'client/',
    'site/',
    '.agents/',
    '.claude/',
    '.opencode/',
)

FORBIDDEN_EXACT_PATHS = {
    'verify.sh',
    'go.work',
    'go.work.sum',
    'internal/app/work_bridge.go',
    '.github/workflows/release.yml',
    '.github/workflows/ci-work-integration.yml',
}

FORBIDDEN_PATH_RES = (
    re.compile(r'^scripts/publish-[^/]+\.mjs$'),
)

SECRET_SUFFIXES = (
    '.jks',
    '.keystore',
    '.p12',
    '.pfx',
    '.pem',
    '.key',
    '.p8',
    '.mobileprovision',
    '.cer',
)

SECRET_FILENAMES = {
    'key.properties',
    'local.properties',
    'GoogleService-Info.plist',
    'google-services.json',
}

TEXT_EXTS = {
    '.go', '.mod', '.ts', '.tsx', '.js', '.mjs', '.cjs', '.json', '.md',
    '.yml', '.yaml', '.sh', '.ps1', '.nsi', '.nsh', '.desktop', '.plist',
    '.entitlements', '.dart', '.arb', '.swift', '.kt', '.java', '.m', '.mm',
    '.h', '.cc', '.cpp', '.xml', '.gradle', '.kts', '.pbxproj', '.html', '.css',
    '.py',
}

PRIVATE_KEY_HEADER_RE = re.compile(
    r'-----BEGIN (?:RSA |EC |DSA |OPENSSH |ENCRYPTED )?PRIVATE KEY-----'
)

PERSONAL_HOME_RE = re.compile(
    r'(?:/Users/|C:(?:\\|/)Users(?:\\|/))([A-Za-z0-9._-]+)(?:/|\\)'
)

ALLOWED_PLACEHOLDER_USERS = {
    'alice', 'bob', 'charlie', 'dev', 'developer', 'example', 'me',
    'name', 'runner', 'shared', 'test', 'u', 'user', 'username',
    'you', 'yourname',
}


def is_test_or_fixture(rel: str) -> bool:
    lower = rel.lower()
    base = Path(rel).name.lower()
    return (
        '_test.' in base
        or '.test.' in base
        or '/__tests__/' in lower
        or '/testdata/' in lower
        or lower.startswith('examples/')
        or lower.endswith('.cases.json')
    )


def list_candidate_files(root: Path, use_git: bool) -> list[str]:
    if use_git and (root / '.git').exists():
        res = subprocess.run(
            ['git', '-C', str(root), 'ls-files', '-z'],
            capture_output=True,
            check=True,
        )
        raw = res.stdout.decode('utf-8', errors='surrogateescape')
        return sorted(p for p in raw.split('\0') if p)

    files: list[str] = []
    for cur_root, dirs, filenames in os.walk(root, followlinks=False):
        dirs[:] = sorted(d for d in dirs if d != '.git')
        rel_dir = Path(cur_root).relative_to(root)
        for fn in sorted(filenames):
            rel = (rel_dir / fn).as_posix()
            if rel.startswith('./'):
                rel = rel[2:]
            files.append(rel)
    return files


def scan_repo(root: Path, use_git: bool = True) -> tuple[int, list[str]]:
    violations: list[str] = []
    scanned_text = 0

    for rel in list_candidate_files(root, use_git):
        if rel in FORBIDDEN_EXACT_PATHS or any(rel.startswith(p) for p in FORBIDDEN_DIR_PREFIXES):
            violations.append(f'{rel}: forbidden private or workspace-local path is tracked')
            continue
        if any(rx.match(rel) for rx in FORBIDDEN_PATH_RES):
            violations.append(f'{rel}: forbidden private script path is tracked')
            continue

        p = root / rel
        if not p.is_file() or p.is_symlink():
            continue

        fn = p.name
        lower_fn = fn.lower()
        if (
            fn in SECRET_FILENAMES
            or lower_fn == '.env'
            or (lower_fn.startswith('.env.') and lower_fn != '.env.example')
            or lower_fn.endswith(SECRET_SUFFIXES)
        ):
            violations.append(f'{rel}: secret or local credential file must not be tracked')
            continue

        if p.suffix.lower() not in TEXT_EXTS and fn not in ('LICENSE', 'NOTICE'):
            continue
        try:
            content = p.read_text(encoding='utf-8')
        except (UnicodeDecodeError, OSError):
            continue

        scanned_text += 1
        test_file = is_test_or_fixture(rel)
        for idx, line in enumerate(content.splitlines(), 1):
            if not test_file and PRIVATE_KEY_HEADER_RE.search(line):
                violations.append(f'{rel}:{idx}: private key PEM block detected')
            if not test_file:
                for m in PERSONAL_HOME_RE.finditer(line):
                    user = m.group(1)
                    if user.lower() not in ALLOWED_PLACEHOLDER_USERS:
                        violations.append(
                            f'{rel}:{idx}: hardcoded personal home path for "{user}" -> {line.strip()[:120]}'
                        )

    return scanned_text, violations


def main() -> int:
    use_git = True
    target = Path(__file__).resolve().parents[1]
    args = sys.argv[1:]
    if len(args) == 2 and args[0] == '--dir':
        target = Path(args[1]).resolve()
        use_git = (target / '.git').exists()
    elif len(args) == 1 and args[0] in ('-h', '--help'):
        print('Usage: scripts/check-oss-hygiene.py [--dir <path>]')
        return 0
    elif args:
        print('Usage: scripts/check-oss-hygiene.py [--dir <path>]', file=sys.stderr)
        return 1

    if not target.is_dir():
        print(f'Target directory does not exist: {target}', file=sys.stderr)
        return 1

    scanned, violations = scan_repo(target, use_git=use_git)
    if violations:
        print(f'OSS_HYGIENE_FAIL: found {len(violations)} violation(s) in {target}:', file=sys.stderr)
        for item in violations[:30]:
            print(f'  - {item}', file=sys.stderr)
        return 1

    print(f'OSS_HYGIENE_OK: scanned {scanned} tracked text files in {target} (0 violations)')
    return 0


if __name__ == '__main__':
    sys.exit(main())
