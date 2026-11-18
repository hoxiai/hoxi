import 'package:flutter/foundation.dart' show listEquals;

import '../storage/node_transport.dart';

/// Chooses which transport to use for a Hoxi node.
///
/// Transports are probed in preference order (LAN first, then relays) with a
/// short timeout, and the first reachable one wins. The winner is cached for
/// [cacheTtl] so a known-good transport is reused without re-probing; pass
/// [force] (or call [invalidate]) to re-probe after a failure or a network
/// change.
class HoxiTransportResolver {
  HoxiTransportResolver({
    this.lanTimeout = const Duration(seconds: 2),
    this.relayTimeout = const Duration(seconds: 6),
    this.cacheTtl = const Duration(minutes: 5),
  });

  final Duration lanTimeout;
  final Duration relayTimeout;
  final Duration cacheTtl;

  String? _cachedUrl;
  DateTime? _cachedAt;
  List<String> _cachedSignature = const [];

  /// Returns the first reachable url among [transports], or null when none is
  /// reachable. [probe] reports whether a url answers.
  Future<String?> resolve(
    List<NodeTransport> transports, {
    required Future<bool> Function(String url) probe,
    bool force = false,
  }) async {
    final ordered = NodeTransport.ordered(transports);
    if (ordered.isEmpty) return null;
    final signature = ordered.map((t) => t.url).toList();
    if (!force && _cacheValid(signature)) {
      return _cachedUrl;
    }
    for (final transport in ordered) {
      final timeout = transport.isRelay ? relayTimeout : lanTimeout;
      try {
        if (await probe(transport.url).timeout(timeout)) {
          _remember(transport.url, signature);
          return transport.url;
        }
      } catch (_) {
        // Probe failed or timed out; try the next transport.
      }
    }
    _clearCache();
    return null;
  }

  /// Drops the cached transport so the next [resolve] re-probes.
  void invalidate() => _clearCache();

  bool _cacheValid(List<String> signature) {
    if (_cachedUrl == null || _cachedAt == null) return false;
    if (DateTime.now().difference(_cachedAt!) > cacheTtl) return false;
    return listEquals(signature, _cachedSignature);
  }

  void _remember(String url, List<String> signature) {
    _cachedUrl = url;
    _cachedAt = DateTime.now();
    _cachedSignature = signature;
  }

  void _clearCache() {
    _cachedUrl = null;
    _cachedAt = null;
    _cachedSignature = const [];
  }
}
