#!/usr/bin/env python3
"""Enforce the mobile source-file and script size limit without inspecting build outputs."""
from pathlib import Path
import sys

root = Path(__file__).resolve().parents[1]
# gen-l10n 生成的代码随 ARB 键数量线性增长，不是手写源码，不计入行数上限。
generated = {root / 'lib' / 'l10n' / 'gen'}
violations = []
for folder in [
    'lib',
    'test',
    'tool',
    'scripts',
    'ios/Runner',
    'macos/Runner',
    'android/app/src/main/kotlin',
    'ohos/entry/src/main/ets',
]:
    folder_path = root / folder
    if not folder_path.exists():
        continue
    for path in folder_path.rglob('*'):
        if path.suffix not in {'.dart', '.swift', '.kt', '.ets', '.py'}:
            continue
        if any(parent in generated for parent in path.parents):
            continue
        count = len(path.read_text(encoding='utf-8').splitlines())
        if count > 800:
            violations.append(f'{path.relative_to(root)}: {count} lines (max 800)')
if violations:
    print('\n'.join(violations), file=sys.stderr)
    sys.exit(1)
print('Mobile source files meet the 800-line limit.')
