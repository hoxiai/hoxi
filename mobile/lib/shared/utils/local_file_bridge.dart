import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';

enum LocalFileBridgeFailure { cancelled, tooLarge, unsupported, failed }

class LocalFileBridgeException implements Exception {
  final LocalFileBridgeFailure failure;
  final String message;

  const LocalFileBridgeException(this.failure, [this.message = '']);

  @override
  String toString() =>
      message.isNotEmpty ? message : 'LocalFileBridgeException($failure)';
}

/// A local file selected from the device (PDF, Excel, Word, CSV, image, text)
/// ready to upload to a paired Hoxi node via `/api/hoxi/attachments`.
class LocalPickedDocument {
  final String name;
  final String mime;
  final Uint8List bytes;

  const LocalPickedDocument({
    required this.name,
    required this.mime,
    required this.bytes,
  });

  int get sizeBytes => bytes.length;

  String get extension {
    final trimmed = name.trim();
    final idx = trimmed.lastIndexOf('.');
    if (idx <= 0 || idx >= trimmed.length - 1) return '';
    return trimmed.substring(idx + 1).toLowerCase();
  }

  bool get isImage =>
      mime.toLowerCase().startsWith('image/') ||
      const {
        'png',
        'jpg',
        'jpeg',
        'webp',
        'gif',
        'bmp',
        'heic',
        'heif',
      }.contains(extension);

  String get formattedSize {
    final b = sizeBytes;
    if (b < 1024) return '$b B';
    if (b < 1024 * 1024) return '${(b / 1024).toStringAsFixed(1)} KB';
    return '${(b / 1024 / 1024).toStringAsFixed(1)} MB';
  }
}

typedef PickDocumentTestHandler =
    Future<LocalPickedDocument?> Function(int maxBytes);
typedef ShareFileTestHandler =
    Future<bool> Function({
      required String filename,
      required Uint8List bytes,
      required String mime,
    });

/// Native iOS/Android document picker and system share bridge (`hoxi.ai/files`).
/// Allows uploading mobile documents (Excel, PDF, Word, CSV, etc.) to the
/// paired Hoxi node and exporting generated workspace outputs via the OS share
/// sheet (WeChat, Files, WPS, AirDrop, etc.).
class LocalFileBridge {
  static const int defaultMaxUploadBytes = 15 * 1024 * 1024;
  static const MethodChannel _channel = MethodChannel('hoxi.ai/files');

  static PickDocumentTestHandler? _testPickHandler;
  static ShareFileTestHandler? _testShareHandler;

  const LocalFileBridge();

  @visibleForTesting
  static void useForTesting({
    PickDocumentTestHandler? onPickDocument,
    ShareFileTestHandler? onShareFile,
  }) {
    _testPickHandler = onPickDocument;
    _testShareHandler = onShareFile;
  }

  @visibleForTesting
  static void resetForTesting() {
    _testPickHandler = null;
    _testShareHandler = null;
  }

  Future<LocalPickedDocument> pickDocument({
    int maxBytes = defaultMaxUploadBytes,
  }) async {
    final testHandler = _testPickHandler;
    if (testHandler != null) {
      final doc = await testHandler(maxBytes);
      if (doc == null) {
        throw const LocalFileBridgeException(LocalFileBridgeFailure.cancelled);
      }
      if (doc.sizeBytes > maxBytes) {
        throw const LocalFileBridgeException(LocalFileBridgeFailure.tooLarge);
      }
      return doc;
    }
    try {
      final raw = await _channel.invokeMethod<dynamic>('pickDocument', {
        'maxBytes': maxBytes,
      });
      if (raw == null) {
        throw const LocalFileBridgeException(LocalFileBridgeFailure.cancelled);
      }
      if (raw is! Map) {
        throw const LocalFileBridgeException(
          LocalFileBridgeFailure.failed,
          'Unexpected picker response',
        );
      }
      final name = (raw['name'] ?? '').toString().trim();
      final mime = (raw['mime'] ?? 'application/octet-stream')
          .toString()
          .trim();
      final rawBytes = raw['bytes'];
      final Uint8List bytes;
      if (rawBytes is Uint8List) {
        bytes = rawBytes;
      } else if (rawBytes is List<int>) {
        bytes = Uint8List.fromList(rawBytes);
      } else {
        throw const LocalFileBridgeException(
          LocalFileBridgeFailure.failed,
          'Missing document bytes',
        );
      }
      if (bytes.isEmpty) {
        throw const LocalFileBridgeException(
          LocalFileBridgeFailure.failed,
          'Selected file is empty',
        );
      }
      if (bytes.length > maxBytes) {
        throw const LocalFileBridgeException(LocalFileBridgeFailure.tooLarge);
      }
      return LocalPickedDocument(
        name: name.isNotEmpty ? name : 'document',
        mime: mime.isNotEmpty ? mime : 'application/octet-stream',
        bytes: bytes,
      );
    } on LocalFileBridgeException {
      rethrow;
    } on MissingPluginException {
      throw const LocalFileBridgeException(LocalFileBridgeFailure.unsupported);
    } on PlatformException catch (e) {
      if (e.code == 'file_too_large') {
        throw LocalFileBridgeException(
          LocalFileBridgeFailure.tooLarge,
          e.message ?? '',
        );
      }
      if (e.code == 'cancelled') {
        throw const LocalFileBridgeException(LocalFileBridgeFailure.cancelled);
      }
      throw LocalFileBridgeException(
        LocalFileBridgeFailure.failed,
        e.message ?? e.code,
      );
    }
  }

  Future<bool> shareFile({
    required String filename,
    required Uint8List bytes,
    String mime = 'application/octet-stream',
  }) async {
    final safeName = filename.trim().isNotEmpty ? filename.trim() : 'file.bin';
    final safeMime = mime.trim().isNotEmpty
        ? mime.trim()
        : inferMimeFromFilename(safeName);
    final testHandler = _testShareHandler;
    if (testHandler != null) {
      return testHandler(filename: safeName, bytes: bytes, mime: safeMime);
    }
    try {
      final ok = await _channel.invokeMethod<bool>('shareFile', {
        'filename': safeName,
        'mime': safeMime,
        'bytes': bytes,
      });
      return ok ?? false;
    } on MissingPluginException {
      throw const LocalFileBridgeException(LocalFileBridgeFailure.unsupported);
    } on PlatformException catch (e) {
      throw LocalFileBridgeException(
        LocalFileBridgeFailure.failed,
        e.message ?? e.code,
      );
    }
  }

  static String inferMimeFromFilename(String filename) {
    final ext = filename.contains('.')
        ? filename.split('.').last.toLowerCase().trim()
        : '';
    return switch (ext) {
      'xlsx' =>
        'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
      'xls' => 'application/vnd.ms-excel',
      'csv' => 'text/csv',
      'tsv' => 'text/tab-separated-values',
      'pdf' => 'application/pdf',
      'docx' =>
        'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
      'doc' => 'application/msword',
      'pptx' =>
        'application/vnd.openxmlformats-officedocument.presentationml.presentation',
      'ppt' => 'application/vnd.ms-powerpoint',
      'png' => 'image/png',
      'jpg' || 'jpeg' => 'image/jpeg',
      'webp' => 'image/webp',
      'gif' => 'image/gif',
      'svg' => 'image/svg+xml',
      'md' || 'markdown' => 'text/markdown',
      'json' => 'application/json',
      'html' || 'htm' => 'text/html',
      'txt' || 'log' => 'text/plain',
      'zip' => 'application/zip',
      'mp4' => 'video/mp4',
      _ => 'application/octet-stream',
    };
  }
}
