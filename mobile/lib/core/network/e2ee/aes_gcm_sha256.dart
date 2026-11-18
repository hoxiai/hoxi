import 'dart:typed_data';

/// Pure-Dart FIPS 180-4 SHA-256, RFC 2104 HMAC-SHA256, and NIST SP 800-38D
/// AES-256-GCM AEAD using unsigned 32-bit arithmetic so Hoxi mobile requires
/// zero extra pub dependencies or platform FFI binaries across iOS/Android/Web.
const List<int> _sha256K = <int>[
  0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5,
  0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
  0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3,
  0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
  0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc,
  0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
  0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7,
  0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
  0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13,
  0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
  0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3,
  0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
  0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5,
  0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
  0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208,
  0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
];

int _rotr32(int x, int n) => ((x >>> n) | ((x << (32 - n)) & 0xffffffff)) & 0xffffffff;

Uint8List sha256Bytes(Uint8List message) {
  var h0 = 0x6a09e667, h1 = 0xbb67ae85, h2 = 0x3c6ef372, h3 = 0xa54ff53a;
  var h4 = 0x510e527f, h5 = 0x9b05688c, h6 = 0x1f83d9ab, h7 = 0x5be0cd19;

  final len = message.length;
  final bitLenHi = (len >>> 29) & 0xffffffff;
  final bitLenLo = (len << 3) & 0xffffffff;
  final padLen = ((55 - (len % 64)) + 64) % 64;
  final totalLen = len + 1 + padLen + 8;
  final padded = Uint8List(totalLen)..setRange(0, len, message);
  padded[len] = 0x80;
  final bd = ByteData.sublistView(padded);
  bd.setUint32(totalLen - 8, bitLenHi, Endian.big);
  bd.setUint32(totalLen - 4, bitLenLo, Endian.big);

  final w = Uint32List(64);
  for (var offset = 0; offset < totalLen; offset += 64) {
    for (var i = 0; i < 16; i++) {
      w[i] = bd.getUint32(offset + i * 4, Endian.big);
    }
    for (var i = 16; i < 64; i++) {
      final s0 = _rotr32(w[i - 15], 7) ^ _rotr32(w[i - 15], 18) ^ (w[i - 15] >>> 3);
      final s1 = _rotr32(w[i - 2], 17) ^ _rotr32(w[i - 2], 19) ^ (w[i - 2] >>> 10);
      w[i] = (w[i - 16] + s0 + w[i - 7] + s1) & 0xffffffff;
    }
    var a = h0, b = h1, c = h2, d = h3, e = h4, f = h5, g = h6, h = h7;
    for (var i = 0; i < 64; i++) {
      final s1 = _rotr32(e, 6) ^ _rotr32(e, 11) ^ _rotr32(e, 25);
      final ch = (e & f) ^ ((~e & 0xffffffff) & g);
      final temp1 = (h + s1 + ch + _sha256K[i] + w[i]) & 0xffffffff;
      final s0 = _rotr32(a, 2) ^ _rotr32(a, 13) ^ _rotr32(a, 22);
      final maj = (a & b) ^ (a & c) ^ (b & c);
      final temp2 = (s0 + maj) & 0xffffffff;
      h = g;
      g = f;
      f = e;
      e = (d + temp1) & 0xffffffff;
      d = c;
      c = b;
      b = a;
      a = (temp1 + temp2) & 0xffffffff;
    }
    h0 = (h0 + a) & 0xffffffff;
    h1 = (h1 + b) & 0xffffffff;
    h2 = (h2 + c) & 0xffffffff;
    h3 = (h3 + d) & 0xffffffff;
    h4 = (h4 + e) & 0xffffffff;
    h5 = (h5 + f) & 0xffffffff;
    h6 = (h6 + g) & 0xffffffff;
    h7 = (h7 + h) & 0xffffffff;
  }

  final out = Uint8List(32);
  final outBd = ByteData.sublistView(out);
  outBd.setUint32(0, h0, Endian.big);
  outBd.setUint32(4, h1, Endian.big);
  outBd.setUint32(8, h2, Endian.big);
  outBd.setUint32(12, h3, Endian.big);
  outBd.setUint32(16, h4, Endian.big);
  outBd.setUint32(20, h5, Endian.big);
  outBd.setUint32(24, h6, Endian.big);
  outBd.setUint32(28, h7, Endian.big);
  return out;
}

Uint8List hmacSha256(Uint8List key, Uint8List message) {
  final actualKey = key.length > 64 ? sha256Bytes(key) : key;
  final blockKey = Uint8List(64)..setRange(0, actualKey.length, actualKey);
  final inner = Uint8List(64 + message.length);
  final outer = Uint8List(64 + 32);
  for (var i = 0; i < 64; i++) {
    inner[i] = blockKey[i] ^ 0x36;
    outer[i] = blockKey[i] ^ 0x5c;
  }
  inner.setRange(64, inner.length, message);
  final innerHash = sha256Bytes(inner);
  outer.setRange(64, 96, innerHash);
  return sha256Bytes(outer);
}

const List<int> _aesSBox = <int>[
  0x63, 0x7c, 0x77, 0x7b, 0xf2, 0x6b, 0x6f, 0xc5, 0x30, 0x01, 0x67, 0x2b, 0xfe, 0xd7, 0xab, 0x76,
  0xca, 0x82, 0xc9, 0x7d, 0xfa, 0x59, 0x47, 0xf0, 0xad, 0xd4, 0xa2, 0xaf, 0x9c, 0xa4, 0x72, 0xc0,
  0xb7, 0xfd, 0x93, 0x26, 0x36, 0x3f, 0xf7, 0xcc, 0x34, 0xa5, 0xe5, 0xf1, 0x71, 0xd8, 0x31, 0x15,
  0x04, 0xc7, 0x23, 0xc3, 0x18, 0x96, 0x05, 0x9a, 0x07, 0x12, 0x80, 0xe2, 0xeb, 0x27, 0xb2, 0x75,
  0x09, 0x83, 0x2c, 0x1a, 0x1b, 0x6e, 0x5a, 0xa0, 0x52, 0x3b, 0xd6, 0xb3, 0x29, 0xe3, 0x2f, 0x84,
  0x53, 0xd1, 0x00, 0xed, 0x20, 0xfc, 0xb1, 0x5b, 0x6a, 0xcb, 0xbe, 0x39, 0x4a, 0x4c, 0x58, 0xcf,
  0xd0, 0xef, 0xaa, 0xfb, 0x43, 0x4d, 0x33, 0x85, 0x45, 0xf9, 0x02, 0x7f, 0x50, 0x3c, 0x9f, 0xa8,
  0x51, 0xa3, 0x40, 0x8f, 0x92, 0x9d, 0x38, 0xf5, 0xbc, 0xb6, 0xda, 0x21, 0x10, 0xff, 0xf3, 0xd2,
  0xcd, 0x0c, 0x13, 0xec, 0x5f, 0x97, 0x44, 0x17, 0xc4, 0xa7, 0x7e, 0x3d, 0x64, 0x5d, 0x19, 0x73,
  0x60, 0x81, 0x4f, 0xdc, 0x22, 0x2a, 0x90, 0x88, 0x46, 0xee, 0xb8, 0x14, 0xde, 0x5e, 0x0b, 0xdb,
  0xe0, 0x32, 0x3a, 0x0a, 0x49, 0x06, 0x24, 0x5c, 0xc2, 0xd3, 0xac, 0x62, 0x91, 0x95, 0xe4, 0x79,
  0xe7, 0xc8, 0x37, 0x6d, 0x8d, 0xd5, 0x4e, 0xa9, 0x6c, 0x56, 0xf4, 0xea, 0x65, 0x7a, 0xae, 0x08,
  0xba, 0x78, 0x25, 0x2e, 0x1c, 0xa6, 0xb4, 0xc6, 0xe8, 0xdd, 0x74, 0x1f, 0x4b, 0xbd, 0x8b, 0x8a,
  0x70, 0x3e, 0xb5, 0x66, 0x48, 0x03, 0xf6, 0x0e, 0x61, 0x35, 0x57, 0xb9, 0x86, 0xc1, 0x1d, 0x9e,
  0xe1, 0xf8, 0x98, 0x11, 0x69, 0xd9, 0x8e, 0x94, 0x9b, 0x1e, 0x87, 0xe9, 0xce, 0x55, 0x28, 0xdf,
  0x8c, 0xa1, 0x89, 0x0d, 0xbf, 0xe6, 0x42, 0x68, 0x41, 0x99, 0x2d, 0x0f, 0xb0, 0x54, 0xbb, 0x16,
];

const List<int> _rcon = <int>[0x01, 0x02, 0x04, 0x08, 0x10, 0x20, 0x40];

int _subWord(int w) =>
    ((_aesSBox[(w >>> 24) & 0xff] << 24) |
        (_aesSBox[(w >>> 16) & 0xff] << 16) |
        (_aesSBox[(w >>> 8) & 0xff] << 8) |
        _aesSBox[w & 0xff]) &
    0xffffffff;

int _rotWord(int w) => (((w << 8) & 0xffffffff) | ((w >>> 24) & 0xff)) & 0xffffffff;

int _xtime(int x) => ((x << 1) ^ (((x >>> 7) & 1) * 0x1b)) & 0xff;

class Aes256Gcm {
  final Uint8List _roundKeys = Uint8List(240);
  late final Uint8List _h = Uint8List(16);

  Aes256Gcm(Uint8List key) {
    if (key.length != 32) {
      throw ArgumentError('AES-256 key must be 32 bytes');
    }
    final w = Uint32List(60);
    final keyBd = ByteData.sublistView(key);
    for (var i = 0; i < 8; i++) {
      w[i] = keyBd.getUint32(i * 4, Endian.big);
    }
    for (var i = 8; i < 60; i++) {
      var temp = w[i - 1];
      if (i % 8 == 0) {
        temp = _subWord(_rotWord(temp)) ^ ((_rcon[(i ~/ 8) - 1] << 24) & 0xffffffff);
      } else if (i % 8 == 4) {
        temp = _subWord(temp);
      }
      w[i] = (w[i - 8] ^ temp) & 0xffffffff;
    }
    final rkBd = ByteData.sublistView(_roundKeys);
    for (var i = 0; i < 60; i++) {
      rkBd.setUint32(i * 4, w[i], Endian.big);
    }
    _encryptBlock(Uint8List(16), _h);
  }

  void _encryptBlock(Uint8List input, Uint8List out) {
    final s = Uint8List(16);
    for (var i = 0; i < 16; i++) {
      s[i] = input[i] ^ _roundKeys[i];
    }
    final tmp = Uint8List(16);
    for (var round = 1; round <= 14; round++) {
      // SubBytes + ShiftRows combined
      tmp[0] = _aesSBox[s[0]];
      tmp[1] = _aesSBox[s[5]];
      tmp[2] = _aesSBox[s[10]];
      tmp[3] = _aesSBox[s[15]];
      tmp[4] = _aesSBox[s[4]];
      tmp[5] = _aesSBox[s[9]];
      tmp[6] = _aesSBox[s[14]];
      tmp[7] = _aesSBox[s[3]];
      tmp[8] = _aesSBox[s[8]];
      tmp[9] = _aesSBox[s[13]];
      tmp[10] = _aesSBox[s[2]];
      tmp[11] = _aesSBox[s[7]];
      tmp[12] = _aesSBox[s[12]];
      tmp[13] = _aesSBox[s[1]];
      tmp[14] = _aesSBox[s[6]];
      tmp[15] = _aesSBox[s[11]];

      final rkOffset = round * 16;
      if (round < 14) {
        for (var c = 0; c < 4; c++) {
          final idx = c * 4;
          final a0 = tmp[idx], a1 = tmp[idx + 1], a2 = tmp[idx + 2], a3 = tmp[idx + 3];
          final t = a0 ^ a1 ^ a2 ^ a3;
          s[idx] = a0 ^ t ^ _xtime(a0 ^ a1) ^ _roundKeys[rkOffset + idx];
          s[idx + 1] = a1 ^ t ^ _xtime(a1 ^ a2) ^ _roundKeys[rkOffset + idx + 1];
          s[idx + 2] = a2 ^ t ^ _xtime(a2 ^ a3) ^ _roundKeys[rkOffset + idx + 2];
          s[idx + 3] = a3 ^ t ^ _xtime(a3 ^ a0) ^ _roundKeys[rkOffset + idx + 3];
        }
      } else {
        for (var i = 0; i < 16; i++) {
          out[i] = tmp[i] ^ _roundKeys[rkOffset + i];
        }
      }
    }
  }

  void _gfMulInPlace(Uint8List y) {
    final hBd = ByteData.sublistView(_h);
    var v0 = hBd.getUint32(0, Endian.big);
    var v1 = hBd.getUint32(4, Endian.big);
    var v2 = hBd.getUint32(8, Endian.big);
    var v3 = hBd.getUint32(12, Endian.big);
    var z0 = 0, z1 = 0, z2 = 0, z3 = 0;
    for (var i = 0; i < 16; i++) {
      final b = y[i];
      for (var bit = 7; bit >= 0; bit--) {
        if (((b >>> bit) & 1) != 0) {
          z0 ^= v0;
          z1 ^= v1;
          z2 ^= v2;
          z3 ^= v3;
        }
        final lsb = v3 & 1;
        v3 = ((v3 >>> 1) | ((v2 & 1) << 31)) & 0xffffffff;
        v2 = ((v2 >>> 1) | ((v1 & 1) << 31)) & 0xffffffff;
        v1 = ((v1 >>> 1) | ((v0 & 1) << 31)) & 0xffffffff;
        v0 = (v0 >>> 1) & 0xffffffff;
        if (lsb != 0) {
          v0 = (v0 ^ 0xe1000000) & 0xffffffff;
        }
      }
    }
    final yBd = ByteData.sublistView(y);
    yBd.setUint32(0, z0, Endian.big);
    yBd.setUint32(4, z1, Endian.big);
    yBd.setUint32(8, z2, Endian.big);
    yBd.setUint32(12, z3, Endian.big);
  }

  Uint8List _ghash(Uint8List aad, Uint8List ciphertext) {
    final y = Uint8List(16);
    void feed(Uint8List data) {
      var offset = 0;
      while (offset < data.length) {
        final end = offset + 16 <= data.length ? offset + 16 : data.length;
        for (var i = 0; i < end - offset; i++) {
          y[i] ^= data[offset + i];
        }
        _gfMulInPlace(y);
        offset += 16;
      }
    }

    feed(aad);
    feed(ciphertext);
    final lenBlock = Uint8List(16);
    final bd = ByteData.sublistView(lenBlock);
    bd.setUint32(0, (aad.length >>> 29) & 0xffffffff, Endian.big);
    bd.setUint32(4, (aad.length << 3) & 0xffffffff, Endian.big);
    bd.setUint32(8, (ciphertext.length >>> 29) & 0xffffffff, Endian.big);
    bd.setUint32(12, (ciphertext.length << 3) & 0xffffffff, Endian.big);
    for (var i = 0; i < 16; i++) {
      y[i] ^= lenBlock[i];
    }
    _gfMulInPlace(y);
    return y;
  }

  void _gctr(Uint8List nonce12, Uint8List input, Uint8List output) {
    final cb = Uint8List(16)..setRange(0, 12, nonce12);
    final cbBd = ByteData.sublistView(cb);
    final keystream = Uint8List(16);
    var counter = 2;
    for (var offset = 0; offset < input.length; offset += 16) {
      cbBd.setUint32(12, counter & 0xffffffff, Endian.big);
      counter = (counter + 1) & 0xffffffff;
      _encryptBlock(cb, keystream);
      final chunk = input.length - offset < 16 ? input.length - offset : 16;
      for (var i = 0; i < chunk; i++) {
        output[offset + i] = input[offset + i] ^ keystream[i];
      }
    }
  }

  Uint8List seal({
    required Uint8List nonce,
    required Uint8List plaintext,
    required Uint8List aad,
  }) {
    if (nonce.length != 12) {
      throw ArgumentError('GCM nonce must be 12 bytes');
    }
    final out = Uint8List(plaintext.length + 16);
    final ctView = Uint8List.sublistView(out, 0, plaintext.length);
    _gctr(nonce, plaintext, ctView);

    final j0 = Uint8List(16)..setRange(0, 12, nonce);
    j0[15] = 1;
    final tagMask = Uint8List(16);
    _encryptBlock(j0, tagMask);
    final ghash = _ghash(aad, ctView);
    for (var i = 0; i < 16; i++) {
      out[plaintext.length + i] = ghash[i] ^ tagMask[i];
    }
    return out;
  }

  Uint8List open({
    required Uint8List nonce,
    required Uint8List ciphertextWithTag,
    required Uint8List aad,
  }) {
    if (nonce.length != 12 || ciphertextWithTag.length < 16) {
      throw const FormatException('invalid e2ee envelope');
    }
    final ctLen = ciphertextWithTag.length - 16;
    final ctView = Uint8List.sublistView(ciphertextWithTag, 0, ctLen);

    final j0 = Uint8List(16)..setRange(0, 12, nonce);
    j0[15] = 1;
    final tagMask = Uint8List(16);
    _encryptBlock(j0, tagMask);
    final ghash = _ghash(aad, ctView);
    var diff = 0;
    for (var i = 0; i < 16; i++) {
      final expected = ghash[i] ^ tagMask[i];
      diff |= expected ^ ciphertextWithTag[ctLen + i];
    }
    if (diff != 0) {
      throw const FormatException('invalid e2ee authentication tag');
    }
    final plain = Uint8List(ctLen);
    _gctr(nonce, ctView, plain);
    return plain;
  }
}
