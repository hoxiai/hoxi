#!/usr/bin/env python3
"""Enforce layered architecture, sublayer purity, and cross-feature boundaries.

Rules:
  1. Every local or `package:hoxi/...` import/export/part/part-of directive in
     `lib/` and `test/` must resolve to an existing file, and `part` / `part of`
     pairs must match symmetrically.
  2. Single-line `export` compatibility shims are forbidden in `lib/`.
  3. Vertical layers flow strictly top-down:
       main.dart -> app -> features -> shared -> core -> l10n
     (`core` never imports `shared`/`features`/`app`; `shared` never imports
      `features`/`app`; `features` never import `app`).
  4. Top-level orchestrator `chat_hub` must not be imported by any other feature.
  5. `ai_workspace` must not import `task_center`; `auth` must stay isolated.
     other; `ai_workspace` must not import `task_center`; `auth` must not import
     any other feature.
  6. Clean Architecture sublayer purity:
     - `models/` must never depend upward on `data/`, `services/`, or `presentation/`
       (`controllers/`, `screens/`, `widgets/`).
     - `data/` and `services/` must never depend upward on `presentation/`
       (`controllers/`, `screens/`, `widgets/`).
  7. The module graph across `lib/` (`layer:*` and `feature:*`) must remain a
     strict Directed Acyclic Graph (zero dependency cycles).
"""
from __future__ import annotations

from collections import defaultdict
from pathlib import Path
import re
import sys

ROOT = Path(__file__).resolve().parents[1]
LIB = ROOT / 'lib'
TEST = ROOT / 'test'
GENERATED = {LIB / 'l10n' / 'gen'}

BUSINESS_SIBLINGS: set[str] = set()
PRESENTATION_SUBLAYERS = {'presentation', 'controllers', 'screens', 'widgets'}

DIRECTIVE_RE = re.compile(
    r'''^\s*(import|export|part(?:\s+of)?)\s+['"]([^'"]+)['"]''',
    re.MULTILINE,
)


def is_generated(path: Path) -> bool:
    return any(parent in GENERATED for parent in path.parents)


def resolve_uri(src_file: Path, uri: str) -> Path | None:
    if uri.startswith('dart:'):
        return None
    if uri.startswith('package:'):
        if not uri.startswith('package:hoxi/'):
            return None
        return (LIB / uri[len('package:hoxi/'):]).resolve()
    return (src_file.parent / uri).resolve()


def module_and_sublayer(path: Path) -> tuple[str, str]:
    rel = path.relative_to(ROOT)
    parts = rel.parts
    if parts[0] == 'test':
        return 'test', 'test'
    if parts[0] == 'lib':
        if len(parts) == 2:
            return 'layer:root', 'root'
        if parts[1] == 'features' and len(parts) >= 3:
            sub = parts[3] if len(parts) >= 4 else 'other'
            return f'feature:{parts[2]}', sub
        sub = parts[2] if len(parts) >= 3 else 'root'
        return f'layer:{parts[1]}', sub
    return 'other', 'other'


def find_sccs(adj: dict[str, set[str]]) -> list[list[str]]:
    index = 0
    indices: dict[str, int] = {}
    lowlink: dict[str, int] = {}
    stack: list[str] = []
    on_stack: set[str] = set()
    sccs: list[list[str]] = []

    def strongconnect(node: str) -> None:
        nonlocal index
        indices[node] = index
        lowlink[node] = index
        index += 1
        stack.append(node)
        on_stack.add(node)
        for nxt in sorted(adj.get(node, ())):
            if nxt not in indices:
                strongconnect(nxt)
                lowlink[node] = min(lowlink[node], lowlink[nxt])
            elif nxt in on_stack:
                lowlink[node] = min(lowlink[node], indices[nxt])
        if lowlink[node] == indices[node]:
            comp: list[str] = []
            while True:
                w = stack.pop()
                on_stack.remove(w)
                comp.append(w)
                if w == node:
                    break
            if len(comp) > 1:
                sccs.append(sorted(comp))

    for node in sorted(adj):
        if node not in indices:
            strongconnect(node)
    return sccs


def main() -> int:
    failures: list[str] = []
    part_children: dict[Path, set[Path]] = defaultdict(set)
    part_parents: dict[Path, Path] = {}
    adj: dict[str, set[str]] = defaultdict(set)
    sample_edge: dict[tuple[str, str], str] = {}

    scan_dirs = [d for d in (LIB, TEST) if d.exists()]
    for base in scan_dirs:
        for path in sorted(base.rglob('*.dart')):
            path = path.resolve()
            if is_generated(path):
                continue
            rel = path.relative_to(ROOT)
            text = path.read_text(encoding='utf-8')

            if base == LIB:
                meaningful = [
                    ln.strip()
                    for ln in text.splitlines()
                    if ln.strip() and not ln.strip().startswith('//')
                ]
                if len(meaningful) == 1 and meaningful[0].startswith('export '):
                    failures.append(
                        f'{rel}: single-line export shim is forbidden; '
                        'import canonical module path directly'
                    )

            src_mod, src_sub = module_and_sublayer(path)
            for raw_kind, uri in DIRECTIVE_RE.findall(text):
                kind = ' '.join(raw_kind.split())
                target = resolve_uri(path, uri)
                if target is None:
                    continue
                if not target.exists():
                    failures.append(f'{rel}: broken {kind} -> {uri}')
                    continue

                if kind == 'part':
                    part_children[path].add(target)
                elif kind == 'part of':
                    part_parents[path] = target

                if src_mod == 'test':
                    continue
                dst_mod, dst_sub = module_and_sublayer(target)
                dst_rel = target.relative_to(ROOT)

                # Rule 6: Clean Architecture sublayer purity (both intra- and cross-module)
                if src_sub == 'models' and dst_sub in (
                    {'data', 'services'} | PRESENTATION_SUBLAYERS
                ):
                    failures.append(
                        f'{rel}: models sublayer must not depend upward on '
                        f'{dst_sub}/ ({dst_rel})'
                    )
                elif src_sub in {'data', 'services'} and (
                    dst_sub in PRESENTATION_SUBLAYERS
                ):
                    failures.append(
                        f'{rel}: {src_sub} sublayer must not depend upward on '
                        f'{dst_sub}/ ({dst_rel})'
                    )

                if src_mod == dst_mod:
                    continue

                adj[src_mod].add(dst_mod)
                sample_edge.setdefault((src_mod, dst_mod), f'{rel} -> {dst_rel}')

                # Rule 3: Vertical layer boundaries
                if src_mod == 'layer:core' and dst_mod not in {'layer:l10n'}:
                    failures.append(
                        f'{rel}: lib/core must not depend on {dst_mod} ({dst_rel})'
                    )
                elif src_mod == 'layer:shared' and dst_mod not in {
                    'layer:core',
                    'layer:l10n',
                }:
                    failures.append(
                        f'{rel}: lib/shared must not depend on {dst_mod} ({dst_rel})'
                    )
                elif src_mod.startswith('feature:') and dst_mod in {
                    'layer:app',
                    'layer:root',
                }:
                    failures.append(
                        f'{rel}: feature module must not depend upward on '
                        f'{dst_mod} ({dst_rel})'
                    )

                # Rule 4 & 5: Cross-feature boundaries
                if src_mod.startswith('feature:') and dst_mod.startswith('feature:'):
                    src_feat = src_mod.split(':', 1)[1]
                    dst_feat = dst_mod.split(':', 1)[1]
                    if dst_feat == 'chat_hub':
                        failures.append(
                            f'{rel}: {src_feat} must not reverse-import chat_hub '
                            f'({dst_rel})'
                        )
                    if (
                        src_feat in BUSINESS_SIBLINGS
                        and dst_feat in BUSINESS_SIBLINGS
                    ):
                        failures.append(
                            f'{rel}: sibling business domains must stay isolated '
                            f'({src_feat} -> {dst_feat} via {dst_rel})'
                        )
                    if src_feat == 'ai_workspace' and dst_feat == 'task_center':
                        failures.append(
                            f'{rel}: ai_workspace must not import task_center '
                            f'({dst_rel})'
                        )
                    if src_feat == 'auth':
                        failures.append(
                            f'{rel}: foundational feature auth must not import '
                            f'{dst_feat} ({dst_rel})'
                        )

    # Symmetric part / part-of validation
    for parent, children in sorted(part_children.items()):
        for child in sorted(children):
            declared_parent = part_parents.get(child)
            if declared_parent != parent:
                failures.append(
                    f'{parent.relative_to(ROOT)}: declares `part` '
                    f'{child.relative_to(ROOT)}, but child `part of` does not match'
                )
    for child, parent in sorted(part_parents.items()):
        if child not in part_children.get(parent, set()):
            failures.append(
                f'{child.relative_to(ROOT)}: declares `part of` '
                f'{parent.relative_to(ROOT)}, but parent is missing matching `part`'
            )

    # Rule 7: Strict DAG check across lib/ modules
    for scc in find_sccs(adj):
        edges_desc = [
            f'{a} -> {b} ({sample_edge[(a, b)]})'
            for a in scc
            for b in scc
            if b in adj.get(a, ())
        ]
        failures.append(
            f'Dependency cycle detected among {scc}: ' + '; '.join(edges_desc)
        )

    if failures:
        print('\n'.join(failures), file=sys.stderr)
        return 1

    print(
        'Feature, sublayer, and layer boundaries are respected '
        '(strict DAG, 0 shims, 0 sublayer leaks).'
    )
    return 0


if __name__ == '__main__':
    sys.exit(main())
