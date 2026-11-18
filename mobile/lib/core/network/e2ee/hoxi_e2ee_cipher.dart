import 'dart:async';
import 'dart:convert';
import 'dart:math';
import 'dart:typed_data';

import 'package:dio/dio.dart' show FormData, ResponseType;

import 'aes_gcm_sha256.dart';

const String hoxiE2eeHeader = 'X-Hoxi-E2EE';
const String hoxiE2eeVersion = 'v1';
const String hoxiE2eeSseVersion = 'v1-sse';

final RegExp _relayPrefixPattern = RegExp(
  r'^/api/tunnel/(?:hoxi|gopanel)-nodes/\d+/proxy(?=/|$)',
);

String normalizeHoxiCleanPath(String rawPathOrUrl) {
  final trimmed = rawPathOrUrl.trim();
  if (trimmed.isEmpty) return '/';
  String path;
  if (trimmed.startsWith('http://') || trimmed.startsWith('https://')) {
    path = Uri.tryParse(trimmed)?.path ?? '/';
  } else {
    final qIdx = trimmed.indexOf('?');
    path = qIdx >= 0 ? trimmed.substring(0, qIdx) : trimmed;
  }
  if (!path.startsWith('/')) {
    path = '/$path';
  }
  try {
    path = Uri.decodeComponent(path);
  } catch (_) {}
  path = path.replaceFirst(_relayPrefixPattern, '');
  if (path.isEmpty) return '/';
  return path;
}

bool isValidHoxiE2eeKeyHex(String? raw) {
  if (raw == null) return false;
  final trimmed = raw.trim();
  if (trimmed.length != 64) return false;
  return RegExp(r'^[0-9a-fA-F]{64}$').hasMatch(trimmed);
}

Uint8List? _decodeHex32(String? raw) {
  if (!isValidHoxiE2eeKeyHex(raw)) return null;
  final clean = raw!.trim();
  final out = Uint8List(32);
  for (var i = 0; i < 32; i++) {
    out[i] = int.parse(clean.substring(i * 2, i * 2 + 2), radix: 16);
  }
  return out;
}

String _encodeBase64UrlUnpadded(Uint8List bytes) =>
    base64Url.encode(bytes).replaceAll('=', '');

Uint8List _decodeBase64UrlFlexible(String raw) {
  var clean = raw.trim();
  final mod = clean.length % 4;
  if (mod == 2) {
    clean = '$clean==';
  } else if (mod == 3) {
    clean = '$clean=';
  } else if (mod == 1) {
    throw const FormatException('invalid base64url length');
  }
  return Uint8List.fromList(base64Url.decode(clean));
}

class HoxiPreparedE2eeRequest {
  final String reqId;
  final String cleanPath;
  final String headerValue;
  final Map<String, dynamic>? encryptedBody;

  const HoxiPreparedE2eeRequest({
    required this.reqId,
    required this.cleanPath,
    required this.headerValue,
    this.encryptedBody,
  });
}

/// Client-side counterpart to `internal/pairing/e2ee.go`, providing directional
/// AES-256-GCM payload encryption, signed request headers, and SSE frame decryption.
class HoxiE2eeCipher {
  static final Map<String, HoxiE2eeCipher> _cache = <String, HoxiE2eeCipher>{};
  static final Random _rng = Random.secure();

  final String keyHex;
  final Aes256Gcm _reqAead;
  final Aes256Gcm _resAead;
  final Uint8List _macKey;

  HoxiE2eeCipher._({
    required this.keyHex,
    required Aes256Gcm reqAead,
    required Aes256Gcm resAead,
    required Uint8List macKey,
  }) : _reqAead = reqAead,
       _resAead = resAead,
       _macKey = macKey;

  static HoxiE2eeCipher? tryFromHex(String? rawHex) {
    final master = _decodeHex32(rawHex);
    if (master == null) return null;
    final normalized = rawHex!.trim().toLowerCase();
    final cached = _cache[normalized];
    if (cached != null) return cached;
    if (_cache.length >= 16) {
      _cache.remove(_cache.keys.first);
    }
    final reqKey = hmacSha256(
      master,
      Uint8List.fromList(utf8.encode('hoxi-e2ee-v1/req')),
    );
    final resKey = hmacSha256(
      master,
      Uint8List.fromList(utf8.encode('hoxi-e2ee-v1/resp')),
    );
    final macKey = hmacSha256(
      master,
      Uint8List.fromList(utf8.encode('hoxi-e2ee-v1/head')),
    );
    final created = HoxiE2eeCipher._(
      keyHex: normalized,
      reqAead: Aes256Gcm(reqKey),
      resAead: Aes256Gcm(resKey),
      macKey: macKey,
    );
    _cache[normalized] = created;
    return created;
  }

  static Uint8List _randomBytes(int length) {
    final out = Uint8List(length);
    for (var i = 0; i < length; i++) {
      out[i] = _rng.nextInt(256);
    }
    return out;
  }

  String buildRequestHeader({
    required String method,
    required String cleanPath,
    required String reqId,
    required int unixSeconds,
    required bool hasBody,
  }) {
    final upper = method.trim().toUpperCase();
    final ts = '$unixSeconds';
    final bodyFlag = hasBody ? '1' : '0';
    final msg = Uint8List.fromList(
      utf8.encode('hoxi-e2ee-v1:head:$upper:$cleanPath:$reqId:$ts:$bodyFlag'),
    );
    final sig = _encodeBase64UrlUnpadded(hmacSha256(_macKey, msg));
    if (hasBody) {
      return 'v1;id=$reqId;ts=$ts;body=1;sig=$sig';
    }
    return 'v1;id=$reqId;ts=$ts;sig=$sig';
  }

  Map<String, dynamic> sealRequestEnvelope({
    required String reqId,
    required String method,
    required String cleanPath,
    required int unixSeconds,
    required String contentType,
    required Uint8List plaintext,
    Uint8List? fixedNonce,
  }) {
    final nonce = fixedNonce ?? _randomBytes(12);
    final upper = method.trim().toUpperCase();
    final aad = Uint8List.fromList(
      utf8.encode('hoxi-e2ee-v1:req:$reqId:$upper:$cleanPath:$unixSeconds'),
    );
    final sealed = _reqAead.seal(nonce: nonce, plaintext: plaintext, aad: aad);
    return <String, dynamic>{
      '_hoxi_e2ee': 1,
      if (contentType.isNotEmpty) 'ct': contentType,
      'n': _encodeBase64UrlUnpadded(nonce),
      'c': _encodeBase64UrlUnpadded(sealed),
    };
  }

  HoxiPreparedE2eeRequest prepareRequest({
    required String method,
    required String rawPathOrUrl,
    Object? data,
    String? contentType,
    int? nowUnixSeconds,
    String? fixedReqId,
    Uint8List? fixedNonce,
  }) {
    final cleanPath = normalizeHoxiCleanPath(rawPathOrUrl);
    final reqId = fixedReqId ?? _encodeBase64UrlUnpadded(_randomBytes(12));
    final ts =
        nowUnixSeconds ?? (DateTime.now().millisecondsSinceEpoch ~/ 1000);

    final shouldEncryptBody =
        data != null && data is! FormData && data is! Stream;
    Map<String, dynamic>? envelope;
    if (shouldEncryptBody) {
      late final Uint8List bodyBytes;
      var effectiveCt = (contentType ?? '').trim();
      if (data is Uint8List) {
        bodyBytes = data;
        if (effectiveCt.isEmpty) effectiveCt = 'application/octet-stream';
      } else if (data is List<int>) {
        bodyBytes = Uint8List.fromList(data);
        if (effectiveCt.isEmpty) effectiveCt = 'application/octet-stream';
      } else if (data is String) {
        bodyBytes = Uint8List.fromList(utf8.encode(data));
        if (effectiveCt.isEmpty) effectiveCt = 'application/json';
      } else {
        bodyBytes = Uint8List.fromList(utf8.encode(jsonEncode(data)));
        if (effectiveCt.isEmpty) effectiveCt = 'application/json';
      }
      envelope = sealRequestEnvelope(
        reqId: reqId,
        method: method,
        cleanPath: cleanPath,
        unixSeconds: ts,
        contentType: effectiveCt,
        plaintext: bodyBytes,
        fixedNonce: fixedNonce,
      );
    }

    final header = buildRequestHeader(
      method: method,
      cleanPath: cleanPath,
      reqId: reqId,
      unixSeconds: ts,
      hasBody: envelope != null,
    );
    return HoxiPreparedE2eeRequest(
      reqId: reqId,
      cleanPath: cleanPath,
      headerValue: header,
      encryptedBody: envelope,
    );
  }

  ({Uint8List bytes, String contentType}) openResponseBytes({
    required String reqId,
    required String method,
    required String cleanPath,
    required int statusCode,
    required Map<dynamic, dynamic> envelope,
  }) {
    if (envelope['_hoxi_e2ee'] != 1) {
      throw const FormatException('unsupported e2ee response version');
    }
    final nonce = _decodeBase64UrlFlexible('${envelope['n'] ?? ''}');
    final ct = _decodeBase64UrlFlexible('${envelope['c'] ?? ''}');
    final upper = method.trim().toUpperCase();
    final aad = Uint8List.fromList(
      utf8.encode('hoxi-e2ee-v1:resp:$reqId:$upper:$cleanPath:$statusCode'),
    );
    final plain = _resAead.open(nonce: nonce, ciphertextWithTag: ct, aad: aad);
    final innerCt = '${envelope['ct'] ?? ''}'.trim();
    return (bytes: plain, contentType: innerCt);
  }

  Object? decodeResponseData({
    required String reqId,
    required String method,
    required String cleanPath,
    required int statusCode,
    required Object? rawData,
    ResponseType? responseType,
  }) {
    Map<dynamic, dynamic>? env;
    if (rawData is Map && rawData['_hoxi_e2ee'] == 1) {
      env = rawData;
    } else if (rawData is String && rawData.trimLeft().startsWith('{')) {
      final parsed = jsonDecode(rawData);
      if (parsed is Map && parsed['_hoxi_e2ee'] == 1) {
        env = parsed;
      }
    } else if (rawData is List<int>) {
      final parsed = jsonDecode(utf8.decode(rawData));
      if (parsed is Map && parsed['_hoxi_e2ee'] == 1) {
        env = parsed;
      }
    }
    if (env == null) return rawData;

    final opened = openResponseBytes(
      reqId: reqId,
      method: method,
      cleanPath: cleanPath,
      statusCode: statusCode,
      envelope: env,
    );
    if (responseType == ResponseType.bytes) {
      return opened.bytes;
    }
    if (opened.bytes.isEmpty) {
      return statusCode == 204 ? null : '';
    }
    final text = utf8.decode(opened.bytes);
    if (responseType == ResponseType.plain) {
      return text;
    }
    final lowerCt = opened.contentType.toLowerCase();
    if (lowerCt.contains('json') || responseType == ResponseType.json) {
      try {
        return jsonDecode(text);
      } catch (_) {
        if (lowerCt.contains('json')) rethrow;
        return text;
      }
    }
    return text;
  }

  String openSseDataLine({
    required String reqId,
    required String cleanPath,
    required int expectedSeq,
    required String rawJsonEnvelope,
  }) {
    final decoded = jsonDecode(rawJsonEnvelope);
    if (decoded is! Map ||
        decoded['_hoxi_e2ee'] != 1 ||
        decoded['seq'] != expectedSeq) {
      throw const FormatException('invalid e2ee sse frame');
    }
    final nonce = _decodeBase64UrlFlexible('${decoded['n'] ?? ''}');
    final ct = _decodeBase64UrlFlexible('${decoded['c'] ?? ''}');
    final aad = Uint8List.fromList(
      utf8.encode('hoxi-e2ee-v1:sse:$reqId:$cleanPath:$expectedSeq'),
    );
    final plain = _resAead.open(nonce: nonce, ciphertextWithTag: ct, aad: aad);
    return utf8.decode(plain);
  }

  Stream<Uint8List> transformSseStream({
    required Stream<Uint8List> source,
    required String reqId,
    required String cleanPath,
  }) async* {
    var expectedSeq = 0;
    await for (final line
        in source
            .cast<List<int>>()
            .transform(utf8.decoder)
            .transform(const LineSplitter())) {
      if (line.startsWith('data:')) {
        final rawPayload = line.substring(5).trimLeft();
        if (rawPayload.startsWith('{') && rawPayload.contains('"_hoxi_e2ee"')) {
          expectedSeq++;
          final plain = openSseDataLine(
            reqId: reqId,
            cleanPath: cleanPath,
            expectedSeq: expectedSeq,
            rawJsonEnvelope: rawPayload,
          );
          yield Uint8List.fromList(utf8.encode('data: $plain\n'));
          continue;
        }
      }
      yield Uint8List.fromList(utf8.encode('$line\n'));
    }
  }
}
