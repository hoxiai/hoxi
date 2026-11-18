import 'dart:convert';
import 'dart:math';
import 'dart:typed_data';

import 'aes_gcm_sha256.dart';

const String hoxiKexClientPubHeader = 'X-Hoxi-KEX-Pub';
const String hoxiKexAlgorithm = 'X25519-AES256GCM-v1';

final BigInt _curve25519P = (BigInt.one << 255) - BigInt.from(19);
final BigInt _curve25519A24 = BigInt.from(121665);
final Uint8List _curve25519BasePoint = Uint8List(32)..[0] = 9;

String _encodeB64UrlNoPad(Uint8List bytes) =>
    base64Url.encode(bytes).replaceAll('=', '');

Uint8List _decodeB64UrlFlex(String raw) {
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

BigInt _decodeLittleEndian255(Uint8List bytes32, {bool clampScalar = false}) {
  if (bytes32.length != 32) {
    throw ArgumentError('X25519 input must be 32 bytes');
  }
  final buf = Uint8List.fromList(bytes32);
  if (clampScalar) {
    buf[0] &= 248;
    buf[31] &= 127;
    buf[31] |= 64;
  } else {
    buf[31] &= 127;
  }
  var acc = BigInt.zero;
  for (var i = 31; i >= 0; i--) {
    acc = (acc << 8) | BigInt.from(buf[i]);
  }
  return acc;
}

Uint8List _encodeLittleEndian256(BigInt value) {
  var v = value % _curve25519P;
  if (v.isNegative) v += _curve25519P;
  final out = Uint8List(32);
  for (var i = 0; i < 32; i++) {
    out[i] = (v & BigInt.from(0xff)).toInt();
    v = v >> 8;
  }
  return out;
}

/// RFC 7748 Curve25519 scalar multiplication in pure Dart (zero FFI/CGO).
Uint8List x25519ScalarMult(Uint8List scalar32, Uint8List uCoord32) {
  final k = _decodeLittleEndian255(scalar32, clampScalar: true);
  final u = _decodeLittleEndian255(uCoord32) % _curve25519P;

  final p = _curve25519P;
  final x1 = u;
  var x2 = BigInt.one;
  var z2 = BigInt.zero;
  var x3 = u;
  var z3 = BigInt.one;
  var swap = 0;

  for (var t = 254; t >= 0; t--) {
    final kt = ((k >> t) & BigInt.one) == BigInt.one ? 1 : 0;
    swap ^= kt;
    if (swap == 1) {
      final tx = x2;
      x2 = x3;
      x3 = tx;
      final tz = z2;
      z2 = z3;
      z3 = tz;
    }
    swap = kt;

    final a = (x2 + z2) % p;
    final aa = (a * a) % p;
    final b = (x2 - z2 + p) % p;
    final bb = (b * b) % p;
    final e = (aa - bb + p) % p;
    final c = (x3 + z3) % p;
    final d = (x3 - z3 + p) % p;
    final da = (d * a) % p;
    final cb = (c * b) % p;

    final sum = (da + cb) % p;
    x3 = (sum * sum) % p;
    final diff = (da - cb + p) % p;
    z3 = (x1 * ((diff * diff) % p)) % p;
    x2 = (aa * bb) % p;
    z2 = (e * ((aa + (_curve25519A24 * e) % p) % p)) % p;
  }

  if (swap == 1) {
    final tx = x2;
    x2 = x3;
    x3 = tx;
    final tz = z2;
    z2 = z3;
    z3 = tz;
  }

  final zInv = z2.modPow(p - BigInt.two, p);
  final shared = _encodeLittleEndian256((x2 * zInv) % p);
  var nonZero = 0;
  for (final byte in shared) {
    nonZero |= byte;
  }
  if (nonZero == 0) {
    throw const FormatException('rejected low-order X25519 public key');
  }
  return shared;
}

Uint8List x25519PublicFromPrivate(Uint8List privateKey32) =>
    x25519ScalarMult(privateKey32, _curve25519BasePoint);

/// Ephemeral client X25519 keypair used during zero-knowledge E2EE bootstrap
/// negotiation with Hoxi desktop (`internal/pairing/kex.go`).
class HoxiX25519KeyPair {
  static final Random _rng = Random.secure();

  final Uint8List _privateKey;
  final Uint8List publicKey;

  HoxiX25519KeyPair._(this._privateKey, this.publicKey);

  factory HoxiX25519KeyPair.fromSeed(Uint8List seed32) {
    if (seed32.length != 32) {
      throw ArgumentError('X25519 seed must be 32 bytes');
    }
    final priv = Uint8List.fromList(seed32);
    return HoxiX25519KeyPair._(priv, x25519PublicFromPrivate(priv));
  }

  factory HoxiX25519KeyPair.generate() {
    final seed = Uint8List(32);
    for (var i = 0; i < 32; i++) {
      seed[i] = _rng.nextInt(256);
    }
    return HoxiX25519KeyPair.fromSeed(seed);
  }

  String get publicKeyBase64Url => _encodeB64UrlNoPad(publicKey);

  /// Decapsulates an `e2eeKex` JSON bundle from `/api/hoxi/bootstrap` into a
  /// 64-character lowercase hex E2EE master key, or returns `null` on failure.
  String? tryOpenMasterKeyHex(Map<dynamic, dynamic>? bundle) {
    if (bundle == null) return null;
    try {
      return openMasterKeyHex(bundle);
    } catch (_) {
      return null;
    }
  }

  String openMasterKeyHex(Map<dynamic, dynamic> bundle) {
    final alg = '${bundle['alg'] ?? ''}'.trim();
    if (alg != hoxiKexAlgorithm) {
      throw const FormatException('unsupported e2eeKex algorithm');
    }
    final rawServerPub = _decodeB64UrlFlex('${bundle['serverPub'] ?? ''}');
    final nonce = _decodeB64UrlFlex('${bundle['nonce'] ?? ''}');
    final sealed = _decodeB64UrlFlex('${bundle['sealedKey'] ?? ''}');
    if (rawServerPub.length != 32 || nonce.length != 12) {
      throw const FormatException('invalid e2eeKex parameters');
    }

    final shared = x25519ScalarMult(_privateKey, rawServerPub);
    final infoPrefix = utf8.encode('hoxi-kex-x25519-v1|');
    final sep = utf8.encode('|');
    final info = Uint8List(
      infoPrefix.length + publicKey.length + sep.length + rawServerPub.length,
    );
    var offset = 0;
    info.setRange(offset, offset + infoPrefix.length, infoPrefix);
    offset += infoPrefix.length;
    info.setRange(offset, offset + publicKey.length, publicKey);
    offset += publicKey.length;
    info.setRange(offset, offset + sep.length, sep);
    offset += sep.length;
    info.setRange(offset, offset + rawServerPub.length, rawServerPub);

    final wrapKey = hmacSha256(shared, info);
    final clientB64 = _encodeB64UrlNoPad(publicKey);
    final serverB64 = _encodeB64UrlNoPad(rawServerPub);
    final aad = Uint8List.fromList(
      utf8.encode('hoxi-kex-v1:$clientB64:$serverB64'),
    );
    final plain = Aes256Gcm(
      wrapKey,
    ).open(nonce: nonce, ciphertextWithTag: sealed, aad: aad);
    if (plain.length != 32) {
      throw const FormatException('invalid unwrapped e2ee master key length');
    }
    final sb = StringBuffer();
    for (final b in plain) {
      sb.write(b.toRadixString(16).padLeft(2, '0'));
    }
    return sb.toString();
  }
}
