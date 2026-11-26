/// Security and schema helpers for probing a Hoxi desktop gateway.
///
/// Probing a saved LAN candidate (`http://<lan-ip>:5417`) on an untrusted Wi-Fi
/// network must not hand the node's pairing token to a captive portal or
/// unrelated HTTP service in the first request, nor accept a generic
/// `{"status": "ok"}` payload as proof of a Hoxi node.
library;

import 'dart:convert';

import 'package:dio/dio.dart';

import '../storage/hoxi_relay_defaults.dart';

/// Canonical 401 error message returned by Hoxi's pairing guard
/// (`internal/gateway/ingress.go`) when a request carries no pairing token.
const hoxiInvalidPairingTokenError = 'invalid pairing token';

/// Validates that a `/api/hoxi/bootstrap` JSON payload comes from a real Hoxi
/// gateway. Both `appVersion` and `rootDir` must be non-empty strings; a bare
/// `status` field is never sufficient.
bool isValidHoxiBootstrap(Object? raw) {
  if (raw is! Map) return false;
  final appVersion = raw['appVersion'];
  final rootDir = raw['rootDir'];
  return appVersion is String &&
      appVersion.trim().isNotEmpty &&
      rootDir is String &&
      rootDir.trim().isNotEmpty;
}

/// Reports whether [rawUrl] is a non-loopback cleartext `http://` address where
/// sending bearer credentials on the first packet would expose them to an
/// untrusted local network peer or captive portal.
bool isCleartextLanUrl(String rawUrl) {
  final uri = Uri.tryParse(rawUrl.trim());
  if (uri == null || !uri.hasAuthority) return false;
  if (uri.scheme.toLowerCase() != 'http') return false;
  return !_isLoopbackHost(uri.host);
}

bool _isLoopbackHost(String rawHost) {
  final host = rawHost.trim().toLowerCase().replaceAll(RegExp(r'^\[|\]$'), '');
  return host == '127.0.0.1' || host == 'localhost' || host == '::1';
}

/// Reports whether an unauthenticated preflight response matches Hoxi's pairing
/// token challenge (`HTTP 401` with `{"error": "invalid pairing token"}`).
bool isHoxiPairingChallenge(int? statusCode, Object? data) {
  if (statusCode != 401 || data is! Map) return false;
  final error = data['error'];
  return error is String && error.trim() == hoxiInvalidPairingTokenError;
}

Options buildHoxiPreflightOptions({
  Duration? connectTimeout,
  Duration? receiveTimeout,
}) {
  return Options(
    extra: {
      'skipActiveCredentials': true,
      'allowRawJson': true,
      'connectTimeout': ?connectTimeout,
    },
    sendTimeout: connectTimeout,
    receiveTimeout: receiveTimeout,
    validateStatus: (status) => status == 200 || status == 401,
  );
}

Options buildConnectionOptions({
  required String entrance,
  String token = '',
  Duration? connectTimeout,
  Duration? receiveTimeout,
}) {
  final normalizedEntrance = entrance.trim();
  return Options(
    extra: {
      'skipActiveCredentials': true,
      'connectTimeout': ?connectTimeout,
    },
    sendTimeout: connectTimeout,
    receiveTimeout: receiveTimeout,
    headers: {
      if (token.isNotEmpty) ...{
        'X-Auth': token,
        'X-Mobile-Token': token,
        'X-Mobile-Request': '1',
      },
      if (normalizedEntrance.isNotEmpty)
        'EntranceCode': base64Encode(utf8.encode(normalizedEntrance)),
    },
  );
}

Options buildHoxiProbeOptions({
  required String entrance,
  String token = '',
  String relayDeviceId = '',
  String e2eeKey = '',
  String kexPublicKey = '',
  Duration? connectTimeout,
  Duration? receiveTimeout,
}) {
  final normalizedEntrance = entrance.trim();
  final trimmedToken = token.trim();
  final trimmedDeviceId = relayDeviceId.trim();
  final trimmedE2ee = e2eeKey.trim();
  final trimmedKexPub = kexPublicKey.trim();
  return Options(
    extra: {
      'skipActiveCredentials': true,
      'allowRawJson': true,
      if (trimmedE2ee.isNotEmpty) 'explicitE2eeKey': trimmedE2ee,
      'connectTimeout': ?connectTimeout,
    },
    sendTimeout: connectTimeout,
    receiveTimeout: receiveTimeout,
    headers: {
      if (trimmedToken.isNotEmpty) ...{
        if (trimmedToken.startsWith('Basic ') ||
            trimmedToken.startsWith('Bearer '))
          'Authorization': trimmedToken
        else ...{
          'X-Auth': trimmedToken,
          'Authorization': 'Bearer $trimmedToken',
        },
      },
      if (trimmedDeviceId.isNotEmpty) 'X-Hoxi-Device-Id': trimmedDeviceId,
      if (trimmedKexPub.isNotEmpty) 'X-Hoxi-KEX-Pub': trimmedKexPub,
      if (normalizedEntrance.isNotEmpty)
        'EntranceCode': base64Encode(utf8.encode(normalizedEntrance)),
    },
  );
}

/// Extracts relay transport metadata, direct peer candidates, and optional
/// LAN-upgraded E2EE keys from a `/api/hoxi/bootstrap` payload or QR JSON.
({
  String relayUrl,
  String relayToken,
  String relayDeviceId,
  String e2eeKey,
  int? nodeId,
  List<String> peerUrls,
})
extractHoxiBootstrapRelay(
  Map<String, dynamic>? payload, {
  String? defaultControllerUrl,
}) {
  if (payload == null || payload.isEmpty) {
    return (
      relayUrl: '',
      relayToken: '',
      relayDeviceId: '',
      e2eeKey: '',
      nodeId: null,
      peerUrls: const [],
    );
  }
  Map<String, dynamic>? nestedMap(Object? raw) =>
      raw is Map ? raw.cast<String, dynamic>() : null;

  final tunnel =
      nestedMap(payload['tunnel']) ??
      nestedMap(payload['relay']) ??
      nestedMap(payload['remoteTunnel']);

  String firstNonEmpty(Iterable<Object?> values) {
    for (final v in values) {
      if (v is String && v.trim().isNotEmpty) return v.trim();
    }
    return '';
  }

  int? firstPositiveInt(Iterable<Object?> values) {
    for (final v in values) {
      if (v == null) continue;
      final parsed = v is int
          ? v
          : (v is num ? v.toInt() : int.tryParse('$v'.trim()));
      if (parsed != null && parsed > 0) return parsed;
    }
    return null;
  }

  List<String> parsePeers(Object? raw) {
    if (raw is List) {
      return raw
          .map((e) => e.toString().trim().replaceAll(RegExp(r'/+$'), ''))
          .where((s) => s.startsWith('http://') || s.startsWith('https://'))
          .toList();
    }
    if (raw is String && raw.trim().isNotEmpty) {
      return raw
          .split(',')
          .map((e) => e.trim().replaceAll(RegExp(r'/+$'), ''))
          .where((s) => s.startsWith('http://') || s.startsWith('https://'))
          .toList();
    }
    return const [];
  }

  final nodeId = firstPositiveInt([
    payload['nodeId'],
    payload['tunnelNodeId'],
    tunnel?['nodeId'],
    tunnel?['id'],
  ]);
  final controllerUrl = firstNonEmpty([
    payload['controllerUrl'],
    payload['panelUrl'],
    payload['panel'],
    tunnel?['controllerUrl'],
    tunnel?['panelUrl'],
    tunnel?['panel'],
    defaultControllerUrl ?? defaultHoxiRelayControllerUrl,
  ]).replaceAll(RegExp(r'/+$'), '');

  final rawRelay = firstNonEmpty([
    payload['relayUrl'],
    if (payload['relay'] is String) payload['relay'],
    payload['tunnelUrl'],
    payload['publicUrl'],
    tunnel?['relayUrl'],
    tunnel?['proxyUrl'],
    tunnel?['publicUrl'],
    tunnel?['url'],
  ]).replaceAll(RegExp(r'/+$'), '');

  final relayToken = firstNonEmpty([
    payload['relayToken'],
    payload['tunnelToken'],
    tunnel?['relayToken'],
    tunnel?['token'],
    tunnel?['accessToken'],
  ]);

  final relayDeviceId = firstNonEmpty([
    payload['relayDeviceId'],
    tunnel?['relayDeviceId'],
    tunnel?['deviceId'],
  ]);

  final e2eeKey = firstNonEmpty([
    payload['e2eeKey'],
    tunnel?['e2eeKey'],
  ]);

  final peerUrls = <String>{
    ...parsePeers(
      payload['peers'] ?? payload['lanUrls'] ?? payload['addresses'],
    ),
    ...parsePeers(tunnel?['peers'] ?? tunnel?['lanUrls']),
  }.toList();

  var resolvedRelay = '';
  if (rawRelay.startsWith('/api/tunnel/') && controllerUrl.isNotEmpty) {
    resolvedRelay = '$controllerUrl$rawRelay';
  } else if (rawRelay.startsWith('http://') ||
      rawRelay.startsWith('https://')) {
    final uri = Uri.tryParse(rawRelay);
    if (nodeId != null &&
        uri != null &&
        (uri.path.isEmpty || uri.path == '/')) {
      resolvedRelay = '$rawRelay/api/tunnel/hoxi-nodes/$nodeId/proxy';
    } else {
      resolvedRelay = rawRelay;
    }
  } else if (nodeId != null && controllerUrl.isNotEmpty) {
    resolvedRelay = '$controllerUrl/api/tunnel/hoxi-nodes/$nodeId/proxy';
  }

  return (
    relayUrl: resolvedRelay,
    relayToken: relayToken,
    relayDeviceId: relayDeviceId,
    e2eeKey: e2eeKey,
    nodeId: nodeId,
    peerUrls: peerUrls,
  );
}
