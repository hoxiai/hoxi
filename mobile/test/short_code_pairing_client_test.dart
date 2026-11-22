import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:hoxi/core/network/short_code_pairing_client.dart';

class _StubAdapter implements HttpClientAdapter {
  _StubAdapter(this.onFetch);

  final Future<ResponseBody> Function(
    RequestOptions options,
    Map<String, dynamic> body,
  )
  onFetch;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final chunks = <int>[];
    if (requestStream != null) {
      await for (final chunk in requestStream) {
        chunks.addAll(chunk);
      }
    }
    final raw = utf8.decode(chunks);
    final decoded = raw.isEmpty
        ? <String, dynamic>{}
        : Map<String, dynamic>.from(jsonDecode(raw) as Map);
    return onFetch(options, decoded);
  }

  @override
  void close({bool force = false}) {}
}

void main() {
  test('generates valid 64-char lowercase hex request IDs', () {
    final fixed = DateTime.utc(2026, 11, 19, 14, 0, 0);
    final id = generateShortCodeRequestId(fixed);
    expect(isValidShortCodeRequestId(id), isTrue);
    expect(
      id.startsWith(
        fixed.millisecondsSinceEpoch.toRadixString(16).padLeft(12, '0'),
      ),
      isTrue,
    );
  });

  test('validates relay origins and rejects insecure or malformed URLs', () {
    for (final bad in const [
      '',
      'http://link.hoxi.example',
      'https://link.hoxi.example/sub',
      'https://link.hoxi.example?a=1',
      'https://link.hoxi.example#frag',
    ]) {
      expect(
        () => parseShortCodeRelayOrigin(bad),
        throwsA(isA<ShortCodePairingException>()),
        reason: 'expected $bad rejected',
      );
    }
    expect(
      parseShortCodeRelayOrigin('https://link.hoxi.example/').toString(),
      'https://link.hoxi.example',
    );
  });

  test('redeems 6-digit code with stable claim request_id and rejects 888888', () async {
    final seenIds = <String>[];
    final dio = Dio(BaseOptions(validateStatus: (_) => true));
    dio.httpClientAdapter = _StubAdapter((options, body) async {
      expect(
        options.uri.toString(),
        'https://link.hoxi.example/v1/codes/redeem',
      );
      seenIds.add(body['request_id'] as String);
      expect(body['code'], '482910');
      final payload = jsonEncode(<String, dynamic>{
        'ticket': jsonEncode(<String, dynamic>{
          'type': 'hoxi',
          'url': 'https://desktop.hoxi.example',
          'token': 't'.padRight(64, '0'),
          'kex': 'X25519-AES256GCM-v1',
        }),
        'expires_at_ms': 1763560860000,
      });
      return ResponseBody.fromString(
        payload,
        200,
        headers: {
          Headers.contentTypeHeader: [Headers.jsonContentType],
        },
      );
    });

    final client = ShortCodePairingClient(dio: dio);
    final claim = ShortCodeClaim();
    expect(claim.toString(), contains('REDACTED'));

    final r1 = await client.redeem(
      relayOrigin: 'https://link.hoxi.example',
      code: '482 910',
      claim: claim,
    );
    final r2 = await client.redeem(
      relayOrigin: 'https://link.hoxi.example',
      code: '482910',
      claim: claim,
    );
    expect(seenIds.length, 2);
    expect(seenIds[0], seenIds[1]);
    expect(r1.ticket, contains('X25519-AES256GCM-v1'));
    expect(r2.expiresAtMs, 1763560860000);

    await expectLater(
      client.redeem(
        relayOrigin: 'https://link.hoxi.example',
        code: reservedDemoShortCode,
      ),
      throwsA(
        isA<ShortCodePairingException>().having(
          (e) => e.kind,
          'kind',
          ShortCodePairingErrorKind.invalidInput,
        ),
      ),
    );
  });

  test('maps 404 and 429 relay errors cleanly', () async {
    var status = 404;
    final dio = Dio(BaseOptions(validateStatus: (_) => true));
    dio.httpClientAdapter = _StubAdapter(
      (_, _) async => ResponseBody.fromString('{"error":"gone"}', status),
    );
    final client = ShortCodePairingClient(dio: dio);

    await expectLater(
      client.redeem(relayOrigin: 'https://link.hoxi.example', code: '123456'),
      throwsA(
        isA<ShortCodePairingException>().having(
          (e) => e.kind,
          'kind',
          ShortCodePairingErrorKind.unavailable,
        ),
      ),
    );

    status = 429;
    await expectLater(
      client.redeem(relayOrigin: 'https://link.hoxi.example', code: '123456'),
      throwsA(
        isA<ShortCodePairingException>().having(
          (e) => e.kind,
          'kind',
          ShortCodePairingErrorKind.rateLimited,
        ),
      ),
    );
  });
}
