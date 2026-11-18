import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:hoxi/core/network/e2ee/aes_gcm_sha256.dart';
import 'package:hoxi/core/network/e2ee/x25519_kem.dart';
import 'package:hoxi/features/ai_workspace/models/code_conversation_stream.dart';

Uint8List _hexToBytes(String hex) {
  final clean = hex.trim();
  final out = Uint8List(clean.length ~/ 2);
  for (var i = 0; i < out.length; i++) {
    out[i] = int.parse(clean.substring(i * 2, i * 2 + 2), radix: 16);
  }
  return out;
}

String _bytesToHex(Uint8List bytes) {
  final sb = StringBuffer();
  for (final b in bytes) {
    sb.write(b.toRadixString(16).padLeft(2, '0'));
  }
  return sb.toString();
}

String _b64UrlNoPad(Uint8List bytes) =>
    base64Url.encode(bytes).replaceAll('=', '');

void main() {
  group('RFC 7748 X25519 & Zero-Knowledge E2EE KEM', () {
    test('matches RFC 7748 Section 6.1 official Curve25519 test vectors', () {
      final alicePriv = _hexToBytes(
        '77076d0a7318a57d3c16c17251b26645df4c2f87ebc0992ab177fba51db92c2a',
      );
      const wantAlicePub =
          '8520f0098930a754748b7ddcb43ef75a0dbf3a0d26381af4eba4a98eaa9b4e6a';
      final bobPriv = _hexToBytes(
        '5dab087e624a8a4b79e17f8b83800ee66f3bb1292618b6fd1c2f8b27ff88e0eb',
      );
      const wantBobPub =
          'de9edb7d7b7dc1b4d35b61c2ece435373f8343c85b78674dadfc7e146f882b4f';
      const wantShared =
          '4a5d9d5ba4ce2de1728e3bf480350f25e07e21c947d19e3376f09b3c1e161742';

      final alicePub = x25519PublicFromPrivate(alicePriv);
      final bobPub = x25519PublicFromPrivate(bobPriv);
      expect(_bytesToHex(alicePub), equals(wantAlicePub));
      expect(_bytesToHex(bobPub), equals(wantBobPub));

      final sharedAB = x25519ScalarMult(alicePriv, bobPub);
      final sharedBA = x25519ScalarMult(bobPriv, alicePub);
      expect(_bytesToHex(sharedAB), equals(wantShared));
      expect(_bytesToHex(sharedBA), equals(wantShared));
    });

    test('decapsulates server-sealed E2EE master key and rejects tampering', () {
      final clientPair = HoxiX25519KeyPair.generate();
      final serverSeed = _hexToBytes(
        '5dab087e624a8a4b79e17f8b83800ee66f3bb1292618b6fd1c2f8b27ff88e0eb',
      );
      final serverPub = x25519PublicFromPrivate(serverSeed);
      const masterKeyHex =
          '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef';
      final masterBytes = _hexToBytes(masterKeyHex);

      // Simulate Go pairing.SealMasterKeyX25519 wire bundle.
      final shared = x25519ScalarMult(serverSeed, clientPair.publicKey);
      final prefix = utf8.encode('hoxi-kex-x25519-v1|');
      final sep = utf8.encode('|');
      final info = Uint8List.fromList([
        ...prefix,
        ...clientPair.publicKey,
        ...sep,
        ...serverPub,
      ]);
      final wrapKey = hmacSha256(shared, info);
      final nonce = Uint8List.fromList(List<int>.generate(12, (i) => i + 1));
      final serverPubB64 = _b64UrlNoPad(serverPub);
      final aad = Uint8List.fromList(
        utf8.encode(
          'hoxi-kex-v1:${clientPair.publicKeyBase64Url}:$serverPubB64',
        ),
      );
      final sealed = Aes256Gcm(
        wrapKey,
      ).seal(nonce: nonce, plaintext: masterBytes, aad: aad);

      final bundle = <String, dynamic>{
        'alg': hoxiKexAlgorithm,
        'serverPub': serverPubB64,
        'nonce': _b64UrlNoPad(nonce),
        'sealedKey': _b64UrlNoPad(sealed),
      };

      expect(clientPair.openMasterKeyHex(bundle), equals(masterKeyHex));

      // Attacker keypair must fail decapsulation.
      final attacker = HoxiX25519KeyPair.generate();
      expect(attacker.tryOpenMasterKeyHex(bundle), isNull);

      // Reject all-zero low-order Curve25519 point.
      expect(
        () => x25519ScalarMult(serverSeed, Uint8List(32)),
        throwsFormatException,
      );
    });
  });

  group('CodeConversationStreamParser seq & resync', () {
    test('tracks monotonic seq across heartbeats and resets on resync', () {
      const sid = 'ses_seq_demo';
      final parser = CodeConversationStreamParser(targetSessionId: sid);

      final initEvts = parser.add(
        'id: 10\n'
        'data: {"seq":10,"type":"server.heartbeat","properties":{}}\n\n'
        'id: 11\n'
        'data: {"seq":11,"type":"message.part.delta","properties":{"sessionID":"$sid","messageID":"msg_1","partID":"prt_1","field":"text","delta":"Hello "}}\n\n',
      );
      expect(parser.lastSeq, equals(11));
      expect(initEvts, hasLength(1));
      expect(initEvts.single.type, equals('delta'));
      expect(initEvts.single.content, equals('Hello '));

      // Simulate reconnect ?after_seq=11 catching up missed frame 12.
      final replayed = parser.add(
        'id: 12\n'
        'data: {"seq":12,"type":"message.part.delta","properties":{"sessionID":"$sid","messageID":"msg_1","partID":"prt_1","field":"text","delta":"world!"}}\n\n',
      );
      expect(parser.lastSeq, equals(12));
      expect(replayed, hasLength(1));
      expect(replayed.single.content, equals('world!'));

      // Simulate ring-buffer eviction triggering hoxi.stream.resync at seq 99.
      final resyncEvts = parser.add(
        'id: 99\n'
        'data: {"seq":99,"type":"hoxi.stream.resync","properties":{"reason":"after_seq_evicted"}}\n\n',
      );
      expect(parser.lastSeq, equals(99));
      expect(resyncEvts, hasLength(1));
      expect(resyncEvts.single.type, equals('resync'));
    });
  });
}
