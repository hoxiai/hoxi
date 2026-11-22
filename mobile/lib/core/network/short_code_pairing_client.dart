import 'dart:convert';
import 'dart:io';
import 'dart:math';
import 'dart:typed_data';

import 'package:dio/dio.dart';

const String reservedDemoShortCode = '888888';
const String pairingRelayUrlPrefsKey = 'hoxi.pairing.relayUrl';

/// Build-time default pairing relay origin. Empty in OSS builds; official
/// commercial builds inject `--dart-define=HOXI_PAIRING_RELAY_URL=https://link.hoxi.ai`.
const String defaultPairingRelayUrl = String.fromEnvironment(
  'HOXI_PAIRING_RELAY_URL',
  defaultValue: '',
);

enum ShortCodePairingErrorKind {
  invalidInput,
  unavailable,
  conflict,
  rateLimited,
  network,
}

class ShortCodePairingException implements Exception {
  const ShortCodePairingException(this.kind, this.message);

  final ShortCodePairingErrorKind kind;
  final String message;

  @override
  String toString() => 'ShortCodePairingException($kind): $message';
}

/// Preserves an idempotent 64-char hex request_id across transient redemption
/// retries while redacting itself in logs.
class ShortCodeClaim {
  String _origin = '';
  String _code = '';
  String _requestId = '';

  String ensureRequestId(String origin, String code, DateTime now) {
    if (_requestId.isEmpty || _origin != origin || _code != code) {
      _origin = origin;
      _code = code;
      _requestId = generateShortCodeRequestId(now);
    }
    return _requestId;
  }

  @override
  String toString() => 'ShortCodeClaim([REDACTED])';
}

bool isValidShortCode(String raw) {
  final code = raw.trim();
  if (code.length != 6) return false;
  for (var i = 0; i < 6; i++) {
    final unit = code.codeUnitAt(i);
    if (unit < 0x30 || unit > 0x39) return false;
  }
  return true;
}

bool isValidShortCodeRequestId(String raw) {
  if (raw.length != 64) return false;
  for (var i = 0; i < 64; i++) {
    final c = raw.codeUnitAt(i);
    final isDigit = c >= 0x30 && c <= 0x39;
    final isLowerHex = c >= 0x61 && c <= 0x66;
    if (!isDigit && !isLowerHex) return false;
  }
  return true;
}

/// Generates a 64-char lowercase hex request ID:
/// 12-char Unix millisecond timestamp + 52 random hex chars (208 random bits).
String generateShortCodeRequestId([DateTime? now]) {
  final ts = (now ?? DateTime.now().toUtc()).millisecondsSinceEpoch;
  if (ts < 0) {
    throw const ShortCodePairingException(
      ShortCodePairingErrorKind.invalidInput,
      'invalid system clock',
    );
  }
  final rng = Random.secure();
  final randBytes = Uint8List(26);
  for (var i = 0; i < randBytes.length; i++) {
    randBytes[i] = rng.nextInt(256);
  }
  final sb = StringBuffer()..write(ts.toRadixString(16).padLeft(12, '0'));
  for (final b in randBytes) {
    sb.write(b.toRadixString(16).padLeft(2, '0'));
  }
  return sb.toString();
}

/// Validates a pairing relay origin: requires `https://host` (or literal
/// loopback HTTP for local testing), with no credentials, sub-path, query,
/// or fragment.
Uri parseShortCodeRelayOrigin(String rawOrigin) {
  final trimmed = rawOrigin.trim();
  if (trimmed.isEmpty || trimmed.contains('#')) {
    throw const ShortCodePairingException(
      ShortCodePairingErrorKind.invalidInput,
      'invalid pairing relay origin',
    );
  }
  final uri = Uri.tryParse(trimmed);
  if (uri == null || !uri.hasAuthority || uri.host.isEmpty) {
    throw const ShortCodePairingException(
      ShortCodePairingErrorKind.invalidInput,
      'invalid pairing relay origin',
    );
  }
  final host = uri.host.toLowerCase().replaceAll(RegExp(r'^\[|\]$'), '');
  final ip = InternetAddress.tryParse(host);
  final isLoopback = ip != null && ip.isLoopback;
  final schemeOk =
      uri.scheme == 'https' || (uri.scheme == 'http' && isLoopback);
  final pathOk = uri.path.isEmpty || uri.path == '/';
  if (!schemeOk ||
      uri.userInfo.isNotEmpty ||
      !pathOk ||
      uri.hasQuery ||
      uri.hasFragment) {
    throw const ShortCodePairingException(
      ShortCodePairingErrorKind.invalidInput,
      'pairing relay origin must be https://host',
    );
  }
  return Uri(
    scheme: uri.scheme,
    host: uri.host,
    port: uri.hasPort ? uri.port : null,
  );
}

class ShortCodePairingClient {
  ShortCodePairingClient({Dio? dio, DateTime Function()? now})
    : _dio =
          dio ??
          Dio(
            BaseOptions(
              connectTimeout: const Duration(seconds: 10),
              receiveTimeout: const Duration(seconds: 10),
              sendTimeout: const Duration(seconds: 10),
              followRedirects: false,
              validateStatus: (_) => true,
            ),
          ),
      _now = now ?? (() => DateTime.now().toUtc());

  final Dio _dio;
  final DateTime Function() _now;

  /// Exchanges a 6-digit pairing PIN at `<origin>/v1/codes/redeem` for the
  /// desktop's zero-knowledge connection ticket JSON (without `e2eeKey`).
  Future<({String ticket, int expiresAtMs})> redeem({
    required String relayOrigin,
    required String code,
    ShortCodeClaim? claim,
  }) async {
    final normalizedCode = code.replaceAll(RegExp(r'\s+'), '');
    if (!isValidShortCode(normalizedCode) ||
        normalizedCode == reservedDemoShortCode) {
      throw const ShortCodePairingException(
        ShortCodePairingErrorKind.invalidInput,
        'invalid 6-digit pairing code',
      );
    }
    final origin = parseShortCodeRelayOrigin(relayOrigin);
    final effectiveClaim = claim ?? ShortCodeClaim();
    final requestId = effectiveClaim.ensureRequestId(
      origin.toString(),
      normalizedCode,
      _now(),
    );
    final endpoint = origin.resolve('/v1/codes/redeem').toString();

    Response<dynamic> resp;
    try {
      resp = await _dio.post<dynamic>(
        endpoint,
        data: jsonEncode(<String, dynamic>{
          'request_id': requestId,
          'code': normalizedCode,
        }),
        options: Options(
          headers: const <String, String>{
            'Content-Type': 'application/json',
            'Accept': 'application/json',
          },
          followRedirects: false,
          validateStatus: (_) => true,
        ),
      );
    } on DioException catch (e) {
      throw ShortCodePairingException(
        ShortCodePairingErrorKind.network,
        e.message ?? 'pairing relay request failed',
      );
    }

    final status = resp.statusCode ?? 0;
    if (status != 200) {
      if (status == 404 || status == 410) {
        throw const ShortCodePairingException(
          ShortCodePairingErrorKind.unavailable,
          'pairing code expired or already used',
        );
      }
      if (status == 409) {
        throw const ShortCodePairingException(
          ShortCodePairingErrorKind.conflict,
          'pairing request conflict',
        );
      }
      if (status == 429) {
        throw const ShortCodePairingException(
          ShortCodePairingErrorKind.rateLimited,
          'pairing service rate limit exceeded',
        );
      }
      throw ShortCodePairingException(
        ShortCodePairingErrorKind.network,
        'pairing relay returned HTTP $status',
      );
    }

    final rawData = resp.data;
    Map<String, dynamic>? body;
    if (rawData is Map) {
      body = Map<String, dynamic>.from(rawData);
    } else if (rawData is String && rawData.trim().isNotEmpty) {
      try {
        final decoded = jsonDecode(rawData);
        if (decoded is Map) {
          body = Map<String, dynamic>.from(decoded);
        }
      } catch (_) {}
    }
    final ticket = (body?['ticket'] ?? '').toString().trim();
    final expRaw = body?['expires_at_ms'];
    final expiresAtMs = expRaw is int
        ? expRaw
        : (expRaw is num ? expRaw.toInt() : int.tryParse('$expRaw') ?? 0);
    if (ticket.isEmpty) {
      throw const ShortCodePairingException(
        ShortCodePairingErrorKind.unavailable,
        'empty pairing ticket',
      );
    }
    return (ticket: ticket, expiresAtMs: expiresAtMs);
  }
}
