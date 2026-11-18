import 'dart:async';
import 'dart:convert';

import '../../../../core/brand/brand.dart';
import '../../../../core/network/api_exception.dart';
import '../../../../core/storage/node_transport.dart';
import '../../../../core/storage/server_connection.dart';
import '../../data/hoxi_relay_repository.dart';

String buildHoxiNodeName(Map<String, dynamic> bootstrap, String baseUrl) {
  final rootDir = (bootstrap['rootDir'] ?? '').toString().trim();
  if (rootDir.isNotEmpty) {
    final parts = rootDir.split(RegExp(r'[/\\]')).where((s) => s.isNotEmpty);
    if (parts.isNotEmpty) return '${Brand.nameEn} · ${parts.last}';
  }
  return '${Brand.nameEn} · ${Uri.tryParse(baseUrl)?.host ?? baseUrl}';
}

String authConnectionFailureDetail(Object error) {
  if (error is ApiException) return error.msg;
  if (error is TimeoutException) return 'timeout';
  if (error is FormatException) return error.message;
  return error.toString().replaceFirst('Exception: ', '');
}

String resolveBasicOrRawToken(String username, String password, String token) {
  if (token.isNotEmpty) return token;
  if (username.isNotEmpty && password.isNotEmpty) {
    return 'Basic ${base64Encode(utf8.encode('$username:$password'))}';
  }
  return password;
}

List<NodeTransport> buildInitialHoxiTransports(
  String primaryUrl,
  String relayUrl, [
  List<String> peerUrls = const [],
]) => NodeTransport.ordered([
  for (final raw in [primaryUrl, ...peerUrls])
    if (raw.trim().isNotEmpty)
      NodeTransport(
        url: raw.trim(),
        kind: isHoxiRelayUrl(raw.trim()) ? 'relay' : 'lan',
      ),
  if (relayUrl.trim().isNotEmpty)
    NodeTransport(url: relayUrl.trim(), kind: 'relay'),
]);

ServerConnection applyBootstrapRelayToConnection(
  ServerConnection conn,
  Map<String, dynamic>? bootstrap,
  List<ServerConnection> savedServers,
) {
  if (bootstrap == null) return conn;
  final merged = mergeHoxiBootstrapRelay(
    bootstrap: bootstrap,
    baseTransports: conn.transports,
    currentRelayToken: conn.relayToken,
    currentE2eeKey: conn.e2eeKey,
    savedServers: savedServers,
  );
  return conn.copyWith(
    relayToken: merged.relayToken,
    e2eeKey: merged.e2eeKey,
    transports: merged.transports,
  );
}
