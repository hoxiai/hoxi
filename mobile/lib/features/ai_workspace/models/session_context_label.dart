import '../../../core/storage/server_connection.dart';
import '../../../core/storage/storage_service.dart';
import 'ai_dev_session.dart';

/// Strips worktree segment suffixes (e.g. `/.hoxi/worktrees/<branch>`) and
/// trailing path separators so project matching and basename fallback always
/// reflect the owning repository/project rather than an ephemeral worktree dir.
String normalizeProjectSourcePath(String? rawPath) {
  var cleaned = (rawPath ?? '').trim();
  if (cleaned.isEmpty) return '';
  final slashPath = cleaned.replaceAll(r'\', '/');
  for (final marker in const <String>[
    '/.hoxi/worktrees/',
    '/.gopanel/worktrees/',
    '/.git/worktrees/',
  ]) {
    final idx = slashPath.indexOf(marker);
    if (idx > 0) {
      cleaned = cleaned.substring(0, idx);
      break;
    }
  }
  cleaned = cleaned.replaceAll(RegExp(r'[/\\]+$'), '');
  return cleaned;
}

String extractDirectoryBasename(String? rawPath) {
  final normalized = normalizeProjectSourcePath(rawPath);
  if (normalized.isEmpty) return '';
  final parts = normalized
      .split(RegExp(r'[/\\]'))
      .map((item) => item.trim())
      .where((item) => item.isNotEmpty && !RegExp(r'^[A-Za-z]:$').hasMatch(item))
      .toList();
  return parts.isEmpty ? '' : parts.last;
}

/// Resolves the human-readable project name for an AI session or task:
/// 1. Explicit [explicitProjectName] if non-empty
/// 2. Matching [CodeProject.name] by [projectId]
/// 3. Matching [CodeProject.name] by normalized [sourceWorkDir] / [workDir]
/// 4. Directory basename of [sourceWorkDir] (preferred over worktree path) or [workDir]
String resolveWorkspaceProjectName({
  String? explicitProjectName,
  int? projectId,
  Iterable<CodeProject> projects = const [],
  String? sourceWorkDir,
  String? workDir,
}) {
  final explicit = (explicitProjectName ?? '').trim();
  if (explicit.isNotEmpty) return explicit;

  if (projectId != null && projectId > 0) {
    for (final project in projects) {
      if (project.id == projectId && project.name.trim().isNotEmpty) {
        return project.name.trim();
      }
    }
  }

  final candidatePaths = <String>[
    normalizeProjectSourcePath(sourceWorkDir),
    normalizeProjectSourcePath(workDir),
  ].where((item) => item.isNotEmpty).toList();

  if (candidatePaths.isNotEmpty) {
    for (final candidate in candidatePaths) {
      final candidateForward = candidate.replaceAll(r'\', '/');
      for (final project in projects) {
        if (project.name.trim().isEmpty) continue;
        final projPrimary =
            normalizeProjectSourcePath(project.workDir).replaceAll(r'\', '/');
        if (projPrimary.isNotEmpty && projPrimary == candidateForward) {
          return project.name.trim();
        }
        final matchesSourceDir = project.sourceDirs.any((dir) {
          final norm = normalizeProjectSourcePath(dir).replaceAll(r'\', '/');
          return norm.isNotEmpty && norm == candidateForward;
        });
        if (matchesSourceDir) {
          return project.name.trim();
        }
      }
    }
  }

  final fromSource = extractDirectoryBasename(sourceWorkDir);
  if (fromSource.isNotEmpty) return fromSource;
  return extractDirectoryBasename(workDir);
}

/// Resolves the active node/machine display name (e.g. `westMacBook-Pro.local`)
/// from saved [ServerConnection] entries, falling back to URL host.
String resolveActiveNodeDisplayName({
  String? activeUrl,
  Iterable<ServerConnection>? servers,
}) {
  final url = (activeUrl ?? StorageService.activeServerUrl ?? '').trim();
  final savedList = servers ?? StorageService.getServerList();
  final matched = url.isEmpty
      ? null
      : savedList
            .where((server) => StorageService.isServerActive(server, url))
            .firstOrNull;
  if (matched != null) {
    final rawName = matched.name.trim();
    if (rawName.isNotEmpty) {
      if (rawName.startsWith('http://') || rawName.startsWith('https://')) {
        final parsedHost = Uri.tryParse(rawName)?.host.trim() ?? '';
        if (parsedHost.isNotEmpty) return parsedHost;
      }
      return rawName;
    }
  }
  if (url.isNotEmpty) {
    final parsed = Uri.tryParse(url.contains('://') ? url : 'https://$url');
    final host = parsed?.host.trim() ?? '';
    if (host.isNotEmpty) return host;
  }
  return '';
}

/// Joins project name and node/machine name into `项目名 · 设备名`, gracefully
/// omitting empty parts so no stray separator is shown.
String composeTaskContextSubtitle({
  String? projectName,
  String? nodeName,
}) {
  final p = (projectName ?? '').trim();
  final n = (nodeName ?? '').trim();
  if (p.isNotEmpty && n.isNotEmpty) {
    return p == n ? p : '$p · $n';
  }
  if (p.isNotEmpty) return p;
  return n;
}
