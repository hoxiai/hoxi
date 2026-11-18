/// Tool-step summary formatting shared by the SSE parser and the turn adapter.
///
/// The rules intentionally mirror the desktop timeline (`ToolCard` /
/// `tool-card-state`): file names instead of full paths, read line ranges and
/// `+X -Y` counts computed with the same `countDiffLines` algorithm so mobile
/// and PC show identical numbers.
class CodeToolStepSummary {
  const CodeToolStepSummary._();

  static String formatStepNote(String raw) {
    final trimmed = raw.trim();
    if (trimmed.isEmpty) return '';
    return 'note · $trimmed';
  }

  static int _posInt(dynamic v) =>
      v is num ? v.toInt() : (int.tryParse(v?.toString() ?? '') ?? 0);

  static bool isReadTool(String lower) =>
      lower == 'read' ||
      lower == 'view' ||
      lower == 'read_file' ||
      lower == 'view_file';

  static bool isEditTool(String lower) =>
      lower == 'edit' ||
      lower == 'write' ||
      lower == 'patch' ||
      lower == 'multiedit' ||
      lower == 'edit_file' ||
      lower == 'write_file' ||
      lower == 'apply_patch' ||
      lower == 'replace_file_content' ||
      lower == 'multi_replace_file_content' ||
      lower == 'create_file' ||
      lower == 'write_to_file' ||
      lower == 'str_replace_based_edit_tool' ||
      lower == 'file_edit';

  /// Same counting rules as desktop `countDiffLines`: prefer a unified patch,
  /// otherwise trim the common prefix/suffix of the before/after text.
  static (int, int) _countDiffLines({
    String? patch,
    String? before,
    String? after,
  }) {
    if (patch != null) {
      var additions = 0;
      var deletions = 0;
      for (final line in patch.split('\n')) {
        if (line.startsWith('+') && !line.startsWith('+++')) {
          additions++;
        } else if (line.startsWith('-') && !line.startsWith('---')) {
          deletions++;
        }
      }
      return (additions, deletions);
    }
    if (before != null && after != null) {
      if (before == after) return (0, 0);
      final bLines = before.isEmpty
          ? const <String>[]
          : before.replaceAll('\r\n', '\n').split('\n');
      final aLines = after.isEmpty
          ? const <String>[]
          : after.replaceAll('\r\n', '\n').split('\n');
      var prefix = 0;
      while (prefix < bLines.length &&
          prefix < aLines.length &&
          bLines[prefix] == aLines[prefix]) {
        prefix++;
      }
      var suffix = 0;
      while (suffix < bLines.length - prefix &&
          suffix < aLines.length - prefix &&
          bLines[bLines.length - 1 - suffix] ==
              aLines[aLines.length - 1 - suffix]) {
        suffix++;
      }
      final additions = aLines.length - prefix - suffix;
      final deletions = bLines.length - prefix - suffix;
      return (additions < 0 ? 0 : additions, deletions < 0 ? 0 : deletions);
    }
    if (before == null && after != null && after.isNotEmpty) {
      return (after.replaceAll('\r\n', '\n').split('\n').length, 0);
    }
    return (0, 0);
  }

  static String _chunkText(Object? raw) => raw is String ? raw : '';

  static String extract(
    String tool,
    Map<String, dynamic> state,
  ) {
    final lowerTool = tool.toLowerCase();
    final input = state['input'] is Map
        ? (state['input'] as Map).cast<String, dynamic>()
        : const <String, dynamic>{};
    final meta = state['metadata'] is Map
        ? (state['metadata'] as Map).cast<String, dynamic>()
        : const <String, dynamic>{};
    if (lowerTool == 'todowrite') {
      final rawTodos = input['todos'] is List
          ? input['todos'] as List
          : (meta['todos'] is List ? meta['todos'] as List : const []);
      if (rawTodos.isNotEmpty) {
        final done = rawTodos
            .whereType<Map>()
            .where((t) => (t['status'] ?? '').toString() == 'completed')
            .length;
        return '$tool · $done/${rawTodos.length} 项任务';
      }
    }
    if (lowerTool == 'question' && input['questions'] is List) {
      final firstQ = (input['questions'] as List).whereType<Map>().firstOrNull;
      final qText = (firstQ?['question'] ?? firstQ?['header'] ?? '')
          .toString()
          .trim()
          .replaceAll(RegExp(r'\s+'), ' ');
      if (qText.isNotEmpty) {
        final clipped = qText.length > 80
            ? '${qText.substring(0, 80)}...'
            : qText;
        return '$tool · $clipped';
      }
    }
    final isRead = isReadTool(lowerTool);
    final isEdit = isEditTool(lowerTool);
    var detail = '';
    if (isRead || isEdit) {
      for (final key in const <String>[
        'filePath',
        'file_path',
        'Path',
        'path',
        'AbsolutePath',
        'TargetFile',
        'target_file',
        'targetFile',
        'file',
        'filename',
      ]) {
        final raw = (input[key] ?? '').toString().trim();
        if (raw.isNotEmpty) {
          detail = raw;
          break;
        }
      }
      if (detail.isEmpty) {
        final fdiff = meta['filediff'] is Map
            ? meta['filediff'] as Map
            : const {};
        detail = (fdiff['file'] ??
                fdiff['path'] ??
                meta['filePath'] ??
                meta['filepath'] ??
                '')
            .toString()
            .trim();
      }
    }
    if (detail.isEmpty) detail = (state['title'] ?? '').toString().trim();
    if (detail.isEmpty && input.isNotEmpty) {
      for (final key in const <String>[
        'command',
        'CommandLine',
        'description',
        'filePath',
        'file_path',
        'Path',
        'path',
        'AbsolutePath',
        'TargetFile',
        'pattern',
        'query',
        'url',
      ]) {
        final raw = (input[key] ?? '').toString().trim();
        if (raw.isNotEmpty) {
          detail = raw.replaceAll(RegExp(r'\s+'), ' ');
          if (!isRead && !isEdit && detail.length > 80) {
            detail = '${detail.substring(0, 80)}...';
          }
          break;
        }
      }
    }
    if (detail.isEmpty || detail == tool) return tool;
    if (isRead) {
      final range = _readLineRange(input, meta, state);
      return range == null ? '$tool · $detail' : '$tool · $detail · $range';
    } else if (isEdit) {
      final (add, del) = _editDiffCounts(input, meta);
      if (add > 0 || del > 0) return '$tool · $detail · +$add -$del';
    }
    return '$tool · $detail';
  }

  /// Mirrors desktop `resolveReadLineRange`: explicit start/end → offset+limit →
  /// tool output line numbers, falling back to nothing rather than guessing.
  static String? _readLineRange(
    Map<String, dynamic> input,
    Map<String, dynamic> meta,
    Map<String, dynamic> state,
  ) {
    final start = _posInt(
      input['StartLine'] ??
          input['startLine'] ??
          input['start_line'] ??
          meta['startLine'],
    );
    final end = _posInt(
      input['EndLine'] ??
          input['endLine'] ??
          input['end_line'] ??
          meta['endLine'],
    );
    if (start > 0 && end > 0) {
      return end > start ? '$start-$end' : '$start';
    }
    if (start > 0) return '$start+';
    if (end > 0) return '1-$end';

    final offset = _posInt(input['offset'] ?? meta['offset']);
    final limit = _posInt(input['limit'] ?? input['lineCount'] ?? meta['lines']);
    if (offset > 0 && limit > 0) {
      final endLine = offset + limit - 1;
      return endLine > offset ? '$offset-$endLine' : '$offset';
    }
    if (limit > 0) return limit > 1 ? '1-$limit' : '1';
    if (offset > 1) return '$offset+';

    final out = state['output'] is String
        ? (state['output'] as String).trim()
        : '';
    if (out.isEmpty) return null;
    final nums = RegExp(r'^\s*0*(\d+)\s*[|→\t]', multiLine: true).allMatches(out);
    if (nums.isNotEmpty) {
      final first = int.tryParse(nums.first.group(1)!);
      final last = int.tryParse(nums.last.group(1)!);
      if (first != null && last != null && last >= first) {
        return '$first-$last';
      }
    }
    final match = RegExp(
      r'total\s+(\d+)\s+lines',
      caseSensitive: false,
    ).firstMatch(out);
    final count = match != null
        ? (int.tryParse(match.group(1)!) ?? 0)
        : out
              .split('\n')
              .where((l) => l != '<file>' && l != '</file>')
              .length;
    if (count <= 0) return null;
    return count > 1 ? '1-$count' : '1';
  }

  /// Mirrors desktop `resolveToolFileDiffs` + `countDiffLines`.
  static (int, int) _editDiffCounts(
    Map<String, dynamic> input,
    Map<String, dynamic> meta,
  ) {
    final metaFiles = meta['files'];
    if (metaFiles is List && metaFiles.isNotEmpty) {
      var add = 0;
      var del = 0;
      var hasPayload = false;
      for (final raw in metaFiles.whereType<Map>()) {
        final item = raw.cast<String, dynamic>();
        final patch = item['diff'] is String ? item['diff'] as String : null;
        final before = item['before'] is String
            ? item['before'] as String
            : null;
        final after = item['after'] is String ? item['after'] as String : null;
        final itemAdd = item['additions'];
        final itemDel = item['deletions'];
        if (patch == null &&
            before == null &&
            after == null &&
            itemAdd is! num &&
            itemDel is! num) {
          continue;
        }
        hasPayload = true;
        final (countAdd, countDel) = _countDiffLines(
          patch: patch,
          before: before,
          after: after,
        );
        add += itemAdd is num ? itemAdd.toInt() : countAdd;
        del += itemDel is num ? itemDel.toInt() : countDel;
      }
      if (hasPayload) return (add, del);
    }

    if (input['patchText'] is String &&
        (input['patchText'] as String).isNotEmpty) {
      return _countDiffLines(patch: input['patchText'] as String);
    }

    final filediff = meta['filediff'] is Map
        ? (meta['filediff'] as Map).cast<String, dynamic>()
        : const <String, dynamic>{};
    final patch = meta['diff'] ?? meta['patch'] ?? input['patch'] ?? input['diff'];

    String? chunksBefore;
    String? chunksAfter;
    final chunks = input['ReplacementChunks'];
    if (chunks is List && chunks.isNotEmpty) {
      final beforeParts = <String>[];
      final afterParts = <String>[];
      for (final raw in chunks.whereType<Map>()) {
        final chunk = raw.cast<String, dynamic>();
        final target = _chunkText(
          chunk['TargetContent'] ?? chunk['targetContent'],
        );
        final replacement = _chunkText(
          chunk['ReplacementContent'] ?? chunk['replacementContent'],
        );
        if (target.isNotEmpty) beforeParts.add(target);
        if (replacement.isNotEmpty) afterParts.add(replacement);
      }
      if (beforeParts.isNotEmpty || afterParts.isNotEmpty) {
        chunksBefore = beforeParts.join('\n');
        chunksAfter = afterParts.join('\n');
      }
    }

    final before = _firstNonEmptyString([
      filediff['before'],
      input['oldString'],
      input['old_string'],
      input['TargetContent'],
      input['targetContent'],
      input['Target'],
      chunksBefore,
    ]);
    final after = _firstNonEmptyString([
      filediff['after'],
      input['newString'],
      input['new_string'],
      input['ReplacementContent'],
      input['replacementContent'],
      input['Replacement'],
      input['Content'],
      input['content'],
      input['file_text'],
      input['CodeContent'],
      input['codeContent'],
      chunksAfter,
    ]);

    final filediffAdd = filediff['additions'];
    final filediffDel = filediff['deletions'];
    final hasOwnPayload =
        patch is String ||
        before != null ||
        after != null ||
        filediffAdd is num ||
        filediffDel is num;
    if (!hasOwnPayload) return (0, 0);

    final (countAdd, countDel) = _countDiffLines(
      patch: patch is String ? patch : null,
      before: before,
      after: after,
    );
    return (
      filediffAdd is num ? filediffAdd.toInt() : countAdd,
      filediffDel is num ? filediffDel.toInt() : countDel,
    );
  }

  static String? _firstNonEmptyString(List<Object?> candidates) {
    for (final candidate in candidates) {
      if (candidate is String && candidate.isNotEmpty) return candidate;
    }
    return null;
  }
}
