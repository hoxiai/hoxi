import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:hoxi/core/network/e2ee/aes_gcm_sha256.dart';
import 'package:hoxi/core/network/e2ee/hoxi_e2ee_cipher.dart';

void main() {
  test('matches Go stdlib AES-256-GCM and HMAC-SHA256 deterministic vectors', () {
    final seqHex = List<String>.generate(
      32,
      (i) => i.toRadixString(16).padLeft(2, '0'),
    ).join();
    final cipher = HoxiE2eeCipher.tryFromHex(seqHex)!;
    final fixedNonce = Uint8List.fromList(
      List<int>.generate(12, (i) => 0xa0 + i),
    );

    final prepared = cipher.prepareRequest(
      method: 'POST',
      rawPathOrUrl:
          '/api/tunnel/hoxi-nodes/12/proxy/api/hoxi/sessions/s1/prompt?stream=1',
      data: '{"hello":"hoxi"}',
      contentType: 'application/json',
      nowUnixSeconds: 1763300000,
      fixedReqId: 'vec_req_id_12345',
      fixedNonce: fixedNonce,
    );

    expect(prepared.cleanPath, '/api/hoxi/sessions/s1/prompt');
    expect(
      prepared.headerValue.startsWith(
        'v1;id=vec_req_id_12345;ts=1763300000;body=1;sig=',
      ),
      isTrue,
    );

    String padB64(String s) => s.padRight((s.length + 3) & ~3, '=');
    final sigBytes = base64Url.decode(
      padB64(prepared.headerValue.split('sig=').last),
    );
    expect(sigBytes.sublist(0, 4), <int>[154, 234, 23, 160]);

    final reqCipherBytes = base64Url.decode(
      padB64('${prepared.encryptedBody!['c']}'),
    );
    expect(reqCipherBytes.sublist(0, 4), <int>[30, 8, 108, 56]);

    final master = Uint8List.fromList(List<int>.generate(32, (i) => i));
    final resSub = hmacSha256(
      master,
      Uint8List.fromList(utf8.encode('hoxi-e2ee-v1/resp')),
    );
    final resGcm = Aes256Gcm(resSub);
    final respPlain = Uint8List.fromList(utf8.encode('{"status":"ok"}'));
    final respSealed = resGcm.seal(
      nonce: fixedNonce,
      plaintext: respPlain,
      aad: Uint8List.fromList(
        utf8.encode(
          'hoxi-e2ee-v1:resp:vec_req_id_12345:POST:/api/hoxi/sessions/s1/prompt:200',
        ),
      ),
    );
    expect(respSealed.sublist(0, 4), <int>[11, 148, 28, 129]);

    final decodedResp = cipher.decodeResponseData(
      reqId: 'vec_req_id_12345',
      method: 'POST',
      cleanPath: '/api/hoxi/sessions/s1/prompt',
      statusCode: 200,
      rawData: <String, dynamic>{
        '_hoxi_e2ee': 1,
        'ct': 'application/json',
        'n': base64Url.encode(fixedNonce).replaceAll('=', ''),
        'c': base64Url.encode(respSealed).replaceAll('=', ''),
      },
    );
    expect(decodedResp, <String, dynamic>{'status': 'ok'});

    expect(
      () => cipher.decodeResponseData(
        reqId: 'vec_req_id_12345',
        method: 'POST',
        cleanPath: '/api/hoxi/sessions/s1/prompt',
        statusCode: 500,
        rawData: <String, dynamic>{
          '_hoxi_e2ee': 1,
          'ct': 'application/json',
          'n': base64Url.encode(fixedNonce).replaceAll('=', ''),
          'c': base64Url.encode(respSealed).replaceAll('=', ''),
        },
      ),
      throwsFormatException,
    );

    final sseSealed = resGcm.seal(
      nonce: fixedNonce,
      plaintext: Uint8List.fromList(utf8.encode('{"delta":"hi"}')),
      aad: Uint8List.fromList(
        utf8.encode('hoxi-e2ee-v1:sse:vec_req_id_12345:/api/hoxi/event:1'),
      ),
    );
    final sseFrame = jsonEncode(<String, dynamic>{
      '_hoxi_e2ee': 1,
      'seq': 1,
      'n': base64Url.encode(fixedNonce).replaceAll('=', ''),
      'c': base64Url.encode(sseSealed).replaceAll('=', ''),
    });
    expect(
      cipher.openSseDataLine(
        reqId: 'vec_req_id_12345',
        cleanPath: '/api/hoxi/event',
        expectedSeq: 1,
        rawJsonEnvelope: sseFrame,
      ),
      '{"delta":"hi"}',
    );
    expect(
      () => cipher.openSseDataLine(
        reqId: 'vec_req_id_12345',
        cleanPath: '/api/hoxi/event',
        expectedSeq: 2,
        rawJsonEnvelope: sseFrame,
      ),
      throwsFormatException,
    );
  });

  test('normalizes percent-encoded and relay-prefixed paths identically to Go', () {
    expect(
      normalizeHoxiCleanPath(
        '/api/tunnel/hoxi-nodes/8/proxy/api/hoxi/files/my%20doc.md?raw=1',
      ),
      '/api/hoxi/files/my doc.md',
    );
  });
}
