import 'package:dio/dio.dart';

import '../storage/active_node_context.dart';
import '../storage/node_transport.dart';
import '../storage/server_connection.dart';
import '../storage/storage_service.dart';
import 'hoxi_node_probe.dart';
import 'hoxi_transport_resolver.dart';

/// Reports whether a url answers for a node, using that node's credentials.
typedef TransportProbe = Future<bool> Function(String url, ServerConnection node);

/// Re-resolves a Hoxi or GoPanel node's transport after a connection failure.
///
/// A request that fails to connect (Wi-Fi drop, left the LAN, dead relay) cannot
/// be retried here, but the next request should not fail for the same reason.
/// This prunes the cached transport, re-probes the node's transports in
/// preference order and, when a different one answers, updates the active node
/// address so subsequent requests use it.
class HoxiTransportFailover {
  HoxiTransportFailover({
    required TransportProbe probe,
    HoxiTransportResolver? resolver,
    this.minInterval = const Duration(seconds: 15),
  }) : _probe = probe,
       _resolver = resolver ?? HoxiTransportResolver();

  final TransportProbe _probe;
  final HoxiTransportResolver _resolver;

  /// Minimum gap between failover attempts, so a burst of failures does not
  /// repeatedly probe the network.
  final Duration minInterval;

  DateTime? _lastAttempt;
  bool _running = false;

  /// Whether [err] is a transport-level failure (not an HTTP status or a
  /// cancelled/superseded request) worth re-resolving.
  static bool isConnectionFailure(DioException err) {
    switch (err.type) {
      case DioExceptionType.connectionError:
      case DioExceptionType.connectionTimeout:
      case DioExceptionType.receiveTimeout:
      case DioExceptionType.sendTimeout:
        return true;
      default:
        return false;
    }
  }

  /// Probes the node's transports and switches the active address when a
  /// different transport answers. When a Hoxi node's saved transports do not
  /// answer (for example, when leaving the LAN with only a LAN URL saved),
  /// falls back to probing configured relay controller candidates.
  Future<void> handle(DioException err) async {
    final node = err.requestOptions.extra['nodeContext'] as ActiveNodeContext?;
    if (node == null ||
        (!node.isHoxi && !node.isGoPanel) ||
        !node.isConnected) {
      return;
    }
    if (_running) return;
    final now = DateTime.now();
    if (_lastAttempt != null && now.difference(_lastAttempt!) < minInterval) {
      return;
    }

    _running = true;
    _lastAttempt = now;
    try {
      final saved = _savedFor(node.url!);
      if (saved == null) return;
      _resolver.invalidate();
      final transports = saved.transports.isNotEmpty
          ? saved.transports
          : NodeTransport.single(saved.url, relay: isHoxiRelayUrl(saved.url));
      if (transports.length <= 1 && !saved.isHoxi) return;
      var chosen = await _resolver.resolve(
        transports,
        probe: (url) => _probe(url, saved),
        force: true,
      );
      if ((chosen == null || chosen == node.url) && saved.isHoxi) {
        final probedUrls = <String>{for (final t in transports) t.url};
        for (final fallback in buildHoxiFallbackRelayTransports(saved)) {
          if (!probedUrls.add(fallback.url)) continue;
          try {
            if (await _probe(fallback.url, saved)) {
              chosen = fallback.url;
              break;
            }
          } catch (_) {
            // Try the next fallback relay candidate.
          }
        }
      }
      if (chosen == null || chosen == node.url) return;
      if (!StorageService.isCurrentNode(node)) return;
      await StorageService.updateActiveServerAddress(
        chosen,
        entrance: saved.entrance,
      );
    } catch (_) {
      // A failover attempt must never surface as an unhandled error.
    } finally {
      _running = false;
    }
  }

  ServerConnection? _savedFor(String url) {
    for (final server in StorageService.getServerList()) {
      if ((server.isHoxi || server.isGoPanel) &&
          (server.url == url || server.transports.any((t) => t.url == url))) {
        return server;
      }
    }
    return null;
  }

  /// Builds a probe that calls the node's `/api/hoxi/bootstrap` (for Hoxi nodes)
  /// or `/api/mobile/health` (for GoPanel nodes), verifying non-loopback
  /// cleartext LAN endpoints with a credential-free preflight before sending
  /// the node's pairing token.
  static TransportProbe dioProbe(Dio dio) {
    return (url, node) async {
      try {
        if (node.isGoPanel) {
          final response = await dio.get<Object?>(
            '$url/api/mobile/health',
            options: buildConnectionOptions(
              entrance: node.entrance,
              token: node.tokenForUrl(url).trim(),
            ),
          );
          return response.statusCode == 200;
        }
        final isRelay =
            node.transports.any((t) => t.url == url && t.isRelay) ||
            isHoxiRelayUrl(url);
        final token = node.tokenForUrl(url).trim();
        if (token.isNotEmpty && isCleartextLanUrl(url) && !isRelay) {
          final preflight = await dio.get<Object?>(
            '$url/api/hoxi/bootstrap',
            options: Options(
              extra: const {
                'skipActiveCredentials': true,
                'allowRawJson': true,
              },
              validateStatus: (status) => status != null,
            ),
          );
          if (preflight.statusCode == 200 &&
              isValidHoxiBootstrap(preflight.data)) {
            return true;
          }
          if (!isHoxiPairingChallenge(preflight.statusCode, preflight.data)) {
            return false;
          }
        }

        final tokensToTry = <String>[
          token,
          if (isRelay &&
              node.token.trim().isNotEmpty &&
              node.token.trim() != token)
            node.token.trim(),
        ];
        if (isRelay) {
          final controllerOrigin = hoxiRelayControllerUrl(url);
          if (controllerOrigin != null) {
            for (final server in StorageService.getServerList()) {
              if (server.isGoPanel &&
                  server.url.trim().replaceAll(RegExp(r'/+$'), '') ==
                      controllerOrigin &&
                  server.token.trim().isNotEmpty &&
                  !tokensToTry.contains(server.token.trim())) {
                tokensToTry.add(server.token.trim());
              }
            }
          }
        }

        for (final candidateToken in tokensToTry) {
          try {
            final response = await dio.get<Map<String, dynamic>>(
              '$url/api/hoxi/bootstrap',
              options: buildHoxiProbeOptions(
                entrance: node.entrance,
                token: candidateToken,
                e2eeKey: node.e2eeKey,
              ),
            );
            if (response.statusCode == 200 &&
                isValidHoxiBootstrap(response.data)) {
              return true;
            }
          } catch (_) {
            // Try the next candidate credential if available.
          }
        }
        return false;
      } catch (_) {
        return false;
      }
    };
  }
}
