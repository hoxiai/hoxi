import 'dart:convert';
import 'dart:typed_data';

class CodeStructureEntry {
  final String name;
  final String path;
  final bool isDirectory;
  final String extension;

  const CodeStructureEntry({
    required this.name,
    required this.path,
    required this.isDirectory,
    required this.extension,
  });

  factory CodeStructureEntry.fromJson(Map<String, dynamic> json) {
    return CodeStructureEntry(
      name: (json['name'] ?? '').toString(),
      path: (json['path'] ?? '').toString(),
      isDirectory: json['isDir'] == true,
      extension: (json['extension'] ?? '').toString(),
    );
  }
}

class CodeStructureResult {
  final String path;
  final List<CodeStructureEntry> entries;
  final bool truncated;

  const CodeStructureResult({
    required this.path,
    required this.entries,
    required this.truncated,
  });

  factory CodeStructureResult.fromJson(Map<String, dynamic> json) {
    return CodeStructureResult(
      path: (json['path'] ?? '').toString(),
      entries: (json['entries'] as List<dynamic>? ?? const [])
          .whereType<Map>()
          .map(
            (item) => CodeStructureEntry.fromJson(item.cast<String, dynamic>()),
          )
          .toList(),
      truncated: json['truncated'] == true,
    );
  }
}

class CodeSpreadsheetSheet {
  final String name;
  final List<String> headers;
  final List<List<String>> rows;

  const CodeSpreadsheetSheet({
    required this.name,
    this.headers = const [],
    this.rows = const [],
  });

  factory CodeSpreadsheetSheet.fromJson(Map<String, dynamic> json) {
    final rawHeaders = json['headers'];
    final headers = rawHeaders is List
        ? rawHeaders.map((e) => (e ?? '').toString()).toList(growable: false)
        : const <String>[];
    final rawRows = json['rows'];
    final rows = <List<String>>[];
    if (rawRows is List) {
      for (final row in rawRows) {
        if (row is List) {
          rows.add(row.map((e) => (e ?? '').toString()).toList(growable: false));
        }
      }
    }
    return CodeSpreadsheetSheet(
      name: (json['name'] ?? 'Sheet').toString(),
      headers: headers,
      rows: List<List<String>>.unmodifiable(rows),
    );
  }
}

class CodeSessionFile {
  final String path;
  final String content;
  final String extension;
  final int size;
  final String version;
  final bool isBinary;
  final String mimeType;
  final String extractedText;
  final String base64Content;
  final List<CodeSpreadsheetSheet> sheets;

  const CodeSessionFile({
    required this.path,
    required this.content,
    required this.extension,
    required this.size,
    required this.version,
    this.isBinary = false,
    this.mimeType = '',
    this.extractedText = '',
    this.base64Content = '',
    this.sheets = const [],
  });

  bool get isImage =>
      mimeType.toLowerCase().startsWith('image/') ||
      const {
        'png',
        'jpg',
        'jpeg',
        'webp',
        'gif',
        'bmp',
      }.contains(extension.toLowerCase());

  Uint8List? decodeBase64Bytes() {
    if (base64Content.isEmpty) return null;
    try {
      return base64Decode(base64Content);
    } catch (_) {
      return null;
    }
  }

  factory CodeSessionFile.fromJson(Map<String, dynamic> json) {
    return CodeSessionFile(
      path: (json['path'] ?? '').toString(),
      content: (json['content'] ?? '').toString(),
      extension: (json['extension'] ?? '').toString(),
      size: (json['size'] as num?)?.toInt() ?? 0,
      version: (json['version'] ?? '').toString(),
      isBinary: json['type'] == 'binary' || json['isBinary'] == true,
      mimeType: (json['mimeType'] ?? '').toString(),
      extractedText: (json['extractedText'] ?? '').toString(),
      base64Content: json['encoding'] == 'base64'
          ? (json['content'] ?? '').toString()
          : '',
      sheets: _parseSheets(json['sheets']),
    );
  }

  factory CodeSessionFile.fromHoxiContentJson(
    String targetPath,
    Map<String, dynamic>? data,
  ) {
    final map = data ?? const <String, dynamic>{};
    final isBinary = map['type'] == 'binary';
    final rawContent = (map['content'] ?? '').toString();
    final isBase64 = map['encoding'] == 'base64';
    final ext = targetPath.contains('.')
        ? targetPath.split('.').last.toLowerCase()
        : '';
    final parsedSize = (map['size'] as num?)?.toInt();
    return CodeSessionFile(
      path: targetPath,
      content: isBinary ? '' : rawContent,
      extension: ext,
      size: parsedSize ?? (isBinary ? 0 : utf8.encode(rawContent).length),
      version: (map['version'] ?? 'hoxi').toString(),
      isBinary: isBinary,
      mimeType: (map['mimeType'] ?? '').toString().trim(),
      extractedText: (map['extractedText'] ?? '').toString(),
      base64Content: isBinary && isBase64 ? rawContent : '',
      sheets: _parseSheets(map['sheets']),
    );
  }

  static List<CodeSpreadsheetSheet> _parseSheets(Object? raw) {
    if (raw is! List) return const [];
    return List<CodeSpreadsheetSheet>.unmodifiable(
      raw
          .whereType<Map>()
          .map((m) => CodeSpreadsheetSheet.fromJson(m.cast<String, dynamic>())),
    );
  }
}
