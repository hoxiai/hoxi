import 'dart:convert';

import 'package:flutter/services.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

import 'node_transport.dart';
import 'server_connection.dart';

String normalizeServerEntrance(String value) {
  return value.trim().replaceAll(RegExp(r'^/+|/+$'), '');
}

bool isValidServerEntrance(String value) {
  final normalized = normalizeServerEntrance(value);
  return normalized.isEmpty || RegExp(r'^[A-Za-z0-9_-]+$').hasMatch(normalized);
}

({String baseUrl, String entrance}) normalizeServerAddress(
  String serverUrl, [
  String entrance = '',
]) {
  final uri = Uri.parse(serverUrl.trim());
  if (uri.scheme != 'http' && uri.scheme != 'https') {
    throw const FormatException('serverAddressHttpRequired');
  }
  if (uri.host.isEmpty || uri.userInfo.isNotEmpty) {
    throw const FormatException('serverAddressInvalid');
  }
  final explicitEntrance = normalizeServerEntrance(entrance);
  // Hoxi 中继地址的路径是隧道路由而不是安全入口，必须原样保留。
  if (isHoxiRelayUrl(serverUrl)) {
    return (
      baseUrl: uri
          .replace(query: null, fragment: null)
          .toString()
          .replaceFirst(RegExp(r'/+$'), ''),
      entrance: explicitEntrance,
    );
  }
  final pathEntrance = normalizeServerEntrance(uri.path);
  if (explicitEntrance.isNotEmpty &&
      pathEntrance.isNotEmpty &&
      explicitEntrance != pathEntrance) {
    throw const FormatException('serverEntranceMismatch');
  }
  final baseUrl = uri.replace(path: '', query: null, fragment: null).toString();
  return (
    baseUrl: baseUrl.replaceFirst(RegExp(r'/$'), ''),
    entrance: explicitEntrance.isNotEmpty ? explicitEntrance : pathEntrance,
  );
}

String buildCodeInstructionDraftKey(String? serverUrl, int sessionId) {
  final server = (serverUrl ?? '').trim().toLowerCase();
  final encoded = base64Url.encode(utf8.encode(server)).replaceAll('=', '');
  return 'code_instruction_draft_${encoded.isEmpty ? 'local' : encoded}_$sessionId';
}

MacOsOptions buildMacOsSecureStorageOptions({required bool releaseMode}) {
  return const MacOsOptions(usesDataProtectionKeychain: false);
}

/// Safely deletes a key from [FlutterSecureStorage].
///
/// On macOS / iOS platforms without iCloud Keychain entitlements,
/// deleting a non-existent item attempts to query and delete synchronizable
/// keychain items, which throws -34018 (errSecMissingEntitlement).
/// If the key does not exist or throws -34018, the desired state (key absent)
/// is already satisfied, so the error is gracefully suppressed.
Future<void> safeSecureStorageDelete(
  FlutterSecureStorage storage,
  String key,
) async {
  try {
    final exists = await storage.containsKey(key: key);
    if (!exists) return;
    await storage.delete(key: key);
  } on PlatformException catch (e) {
    if (e.code == 'Unexpected security result code' &&
        (e.message?.contains('-34018') ?? false || e.details == -34018)) {
      return;
    }
    rethrow;
  }
}

final RegExp _foldedSetCookieSplit = RegExp(
  r',(?=\s*[A-Za-z0-9!#$%&'
  "'"
  r'*+.^_`|~-]+=)',
);

/// Extracts `name=value` pairs from one or more `Set-Cookie` headers, including
/// comma-folded headers while ignoring commas inside `Expires=...` attributes.
String parseSetCookieHeaders(List<String> headers) {
  final pairs = <String, String>{};
  for (final header in headers) {
    for (final entry in header.split(_foldedSetCookieSplit)) {
      final firstPart = entry.split(';').first.trim();
      final eqIndex = firstPart.indexOf('=');
      if (eqIndex <= 0) continue;
      final name = firstPart.substring(0, eqIndex).trim();
      final value = firstPart.substring(eqIndex + 1).trim();
      if (name.isEmpty) continue;
      pairs[name] = '$name=$value';
    }
  }
  return pairs.values.join('; ');
}

/// Merges [incoming] `name=value; ...` pairs onto [existing] cookies by name.
String mergeCookieStrings(String? existing, String incoming) {
  final pairs = <String, String>{};
  void ingest(String? raw) {
    if (raw == null || raw.trim().isEmpty) return;
    for (final part in raw.split(';')) {
      final trimmed = part.trim();
      if (trimmed.isEmpty) continue;
      final eqIndex = trimmed.indexOf('=');
      if (eqIndex <= 0) {
        pairs[trimmed] = trimmed;
      } else {
        final name = trimmed.substring(0, eqIndex).trim();
        final value = trimmed.substring(eqIndex + 1).trim();
        if (name.isEmpty) continue;
        pairs[name] = '$name=$value';
      }
    }
  }

  ingest(existing);
  ingest(incoming);
  return pairs.values.join('; ');
}

bool doesServerMatchUrl(ServerConnection server, String? activeUrl) {
  final current = activeUrl?.trim() ?? '';
  if (current.isEmpty) return false;
  if (server.url == current) return true;
  return server.transports.any((t) => t.url == current);
}

bool matchesSavedServerConnection(
  ServerConnection item,
  ServerConnection target,
) {
  if (item.id == target.id || item.url == target.url) return true;
  if (item.nodeType != target.nodeType) return false;
  return item.transports.any((t) => t.url == target.url) ||
      target.transports.any((t) => t.url == item.url);
}

ServerConnection buildSavedServerConnection(
  ServerConnection? previous,
  ServerConnection incoming, {
  required bool preserveExistingName,
  required bool replace,
}) {
  final name =
      preserveExistingName &&
          previous != null &&
          previous.name.trim().isNotEmpty
      ? previous.name
      : incoming.name;
  if (previous == null || replace) return incoming.copyWith(name: name);
  return previous.copyWith(
    name: name,
    url: incoming.url,
    entrance: incoming.entrance,
    token: incoming.token,
    relayToken: incoming.relayToken.isNotEmpty
        ? incoming.relayToken
        : previous.relayToken,
    e2eeKey: incoming.e2eeKey.isNotEmpty
        ? incoming.e2eeKey
        : previous.e2eeKey,
    nodeType: incoming.nodeType,
    lastConnectedAt: incoming.lastConnectedAt,
    transports: NodeTransport.ordered([
      ...incoming.transports,
      ...previous.transports,
    ]),
  );
}

/// Preserves both the previous and newly selected transport addresses when
/// failover or transport resolution switches a Hoxi node's active address.
ServerConnection buildAddressSwitchedServerConnection(
  ServerConnection base, {
  required String nextUrl,
  String? entrance,
}) {
  final trimmedNext = nextUrl.trim();
  List<NodeTransport> updatedTransports = base.transports;
  if (base.isHoxi && trimmedNext.isNotEmpty) {
    if (updatedTransports.isNotEmpty || base.url != trimmedNext) {
      updatedTransports = NodeTransport.ordered([
        ...updatedTransports,
        if (base.url.trim().isNotEmpty)
          NodeTransport(
            url: base.url.trim(),
            kind: isHoxiRelayUrl(base.url) ? 'relay' : 'lan',
          ),
        NodeTransport(
          url: trimmedNext,
          kind: isHoxiRelayUrl(trimmedNext) ? 'relay' : 'lan',
        ),
      ]);
    }
  }
  return base.copyWith(
    url: trimmedNext,
    entrance: entrance ?? base.entrance,
    lastConnectedAt: DateTime.now(),
    transports: updatedTransports,
  );
}

ServerConnection buildUpdatedServerDetails({
  required ServerConnection previous,
  required String trimmedName,
  required String trimmedUrl,
  String? entrance,
  String? token,
}) {
  final newEntrance = (entrance ?? previous.entrance).trim();
  final hasExplicitToken = token != null && token.trim().isNotEmpty;
  final trimmedToken = hasExplicitToken ? token.trim() : '';
  final editingRelayEndpoint = previous.isHoxi && isHoxiRelayUrl(trimmedUrl);
  final usesSeparateRelayToken =
      editingRelayEndpoint &&
      (previous.relayToken.isNotEmpty ||
          previous.transports.any((t) => t.isLan));

  final newToken = hasExplicitToken
      ? (usesSeparateRelayToken ? previous.token : trimmedToken)
      : previous.token;
  final newRelayToken = hasExplicitToken && usesSeparateRelayToken
      ? trimmedToken
      : previous.relayToken;

  List<NodeTransport> updatedTransports = previous.transports;
  if (previous.isHoxi && previous.url != trimmedUrl) {
    final idx = updatedTransports.indexWhere((t) => t.url == previous.url);
    if (idx >= 0) {
      final oldT = updatedTransports[idx];
      final nextList = List<NodeTransport>.from(updatedTransports);
      nextList[idx] = NodeTransport(url: trimmedUrl, kind: oldT.kind);
      updatedTransports = NodeTransport.ordered(nextList);
    } else {
      final kind = isHoxiRelayUrl(trimmedUrl) ? 'relay' : 'lan';
      updatedTransports = NodeTransport.ordered([
        NodeTransport(url: trimmedUrl, kind: kind),
        ...updatedTransports,
      ]);
    }
  }

  return previous.copyWith(
    name: trimmedName,
    url: trimmedUrl,
    entrance: newEntrance,
    token: newToken,
    relayToken: newRelayToken,
    transports: updatedTransports,
  );
}
