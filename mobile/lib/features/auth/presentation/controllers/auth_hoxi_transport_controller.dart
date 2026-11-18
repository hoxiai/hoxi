part of 'auth_controller.dart';

extension _AuthHoxiTransportExt on AuthController {
  Future<bool> _pairHoxiWithQr({
    required ({String baseUrl, String entrance}) server,
    required String token,
    required String relayToken,
    String e2eeKey = '',
    required String relayUrl,
    List<String> peerUrls = const [],
    String customName = '',
  }) async {
    final existing = _findExistingHoxi(server.baseUrl, relayUrl);
    var mergedTransports = NodeTransport.ordered([
      ...buildInitialHoxiTransports(server.baseUrl, relayUrl, peerUrls),
      ...?existing?.transports,
    ]);
    final effectiveRelayToken = relayToken.isNotEmpty
        ? relayToken
        : (existing?.relayToken ?? '');
    final effectiveE2eeKey = e2eeKey.trim().isNotEmpty
        ? e2eeKey.trim()
        : (existing?.e2eeKey ?? '');
    final probedUrls = <String>{};
    Map<String, dynamic>? bootstrap;
    var bootstrapUrl = server.baseUrl;
    Object? credentialError;

    final lanCandidates = <String>[
      if (!isHoxiRelayUrl(server.baseUrl)) server.baseUrl,
      for (final c in mergedTransports)
        if (!c.isRelay && c.url != server.baseUrl) c.url,
    ];
    for (final candidateUrl in lanCandidates) {
      if (!probedUrls.add(candidateUrl)) continue;
      try {
        bootstrap = await _probeHoxiCandidate(
          serverUrl: candidateUrl,
          entrance: server.entrance,
          token: token,
          e2eeKey: effectiveE2eeKey,
          hasFallback: true,
        );
      } catch (error) {
        if (NodeLinkStatus.isCredentialFailure(error)) credentialError = error;
        bootstrap = null;
      }
      if (bootstrap != null) {
        bootstrapUrl = candidateUrl;
        break;
      }
    }

    if (bootstrap == null && !isHoxiRelayUrl(server.baseUrl)) {
      for (final discoveredUrl in await _discoverHoxiLanCandidates([
        server.baseUrl,
        ...peerUrls,
      ])) {
        if (!probedUrls.add(discoveredUrl)) continue;
        try {
          bootstrap = await _probeHoxiCandidate(
            serverUrl: discoveredUrl,
            entrance: server.entrance,
            token: token,
            e2eeKey: effectiveE2eeKey,
            hasFallback: true,
          );
        } catch (error) {
          if (NodeLinkStatus.isCredentialFailure(error)) {
            credentialError = error;
          }
          bootstrap = null;
        }
        if (bootstrap != null) {
          bootstrapUrl = discoveredUrl;
          mergedTransports = NodeTransport.ordered([
            NodeTransport(url: discoveredUrl, kind: 'lan'),
            ...mergedTransports,
          ]);
          break;
        }
      }
    }

    if (bootstrap == null) {
      final relayCandidates = <String>[
        if (isHoxiRelayUrl(server.baseUrl)) server.baseUrl,
        for (final c in mergedTransports)
          if (c.isRelay && c.url != server.baseUrl) c.url,
        ...buildPairingFallbackRelayUrls(
          primaryUrl: server.baseUrl,
          relayUrl: relayUrl,
          transports: mergedTransports,
        ),
      ];
      final savedServers = StorageService.getServerList();
      for (final candidateUrl in relayCandidates) {
        if (!probedUrls.add(candidateUrl)) continue;
        final candidateToken = effectiveRelayToken.trim().isNotEmpty
            ? effectiveRelayToken
            : resolveSavedGoPanelRelayToken(
                candidateUrl,
                savedServers,
                fallbackToken: token,
              );
        try {
          bootstrap = await _probeHoxiRelayWithFallbackTokens(
            serverUrl: candidateUrl,
            entrance: server.entrance,
            primaryToken: candidateToken,
            secondaryToken: token,
            e2eeKey: effectiveE2eeKey,
          );
        } catch (error) {
          if (NodeLinkStatus.isCredentialFailure(error)) {
            credentialError = error;
          }
          bootstrap = null;
        }
        if (bootstrap != null) {
          bootstrapUrl = candidateUrl;
          mergedTransports = NodeTransport.ordered([
            ...mergedTransports,
            NodeTransport(url: candidateUrl, kind: 'relay'),
          ]);
          break;
        }
      }
    }

    if (bootstrap == null) {
      if (credentialError != null) {
        Error.throwWithStackTrace(credentialError, StackTrace.current);
      }
      throw Exception('无法连接到 Hoxi 桌面端');
    }

    final merged = mergeHoxiBootstrapRelay(
      bootstrap: bootstrap,
      baseTransports: mergedTransports,
      currentRelayToken: effectiveRelayToken,
      currentE2eeKey: effectiveE2eeKey,
      savedServers: StorageService.getServerList(),
    );
    final trimmedCustom = customName.trim();
    final connection = ServerConnection(
      id: existing?.id ?? DateTime.now().millisecondsSinceEpoch.toString(),
      name: trimmedCustom.isNotEmpty
          ? trimmedCustom
          : buildHoxiNodeName(bootstrap, bootstrapUrl),
      url: bootstrapUrl,
      entrance: server.entrance,
      token: token,
      relayToken: merged.relayToken,
      e2eeKey: merged.e2eeKey,
      nodeType: 'hoxi',
      lastConnectedAt: DateTime.now(),
      transports: merged.transports,
    );
    final selected = await _resolveHoxiTransport(connection) ?? connection;
    return _saveAndFinish(selected, preserveExistingName: trimmedCustom.isEmpty);
  }

  Future<bool> _connectHoxi({
    required ({String baseUrl, String entrance}) server,
    required String username,
    required String password,
    String token = '',
    String customName = '',
  }) async {
    final resolvedToken = resolveBasicOrRawToken(username, password, token);
    final existing = _findExistingHoxi(server.baseUrl, '');
    final existingE2eeKey = existing?.e2eeKey ?? '';
    var resolvedUrl = server.baseUrl;
    var bootstrap = await _probeHoxiCandidate(
      serverUrl: resolvedUrl,
      entrance: server.entrance,
      token: resolvedToken,
      e2eeKey: existingE2eeKey,
      hasFallback: !isHoxiRelayUrl(resolvedUrl),
    );
    if (bootstrap == null && !isHoxiRelayUrl(resolvedUrl)) {
      for (final discoveredUrl in await _discoverHoxiLanCandidates([
        resolvedUrl,
      ])) {
        if (discoveredUrl == resolvedUrl) continue;
        try {
          bootstrap = await _probeHoxiCandidate(
            serverUrl: discoveredUrl,
            entrance: server.entrance,
            token: resolvedToken,
            e2eeKey: existingE2eeKey,
            hasFallback: true,
          );
        } catch (_) {
          bootstrap = null;
        }
        if (bootstrap != null) {
          resolvedUrl = discoveredUrl;
          break;
        }
      }
    }
    if (bootstrap == null && !isHoxiRelayUrl(resolvedUrl)) {
      final fallbackRelays = <String>[
        if (existing != null)
          for (final t in existing.transports)
            if (t.isRelay) t.url,
        ...buildPairingFallbackRelayUrls(
          primaryUrl: resolvedUrl,
          relayUrl: '',
          transports: existing?.transports ?? const [],
        ),
      ];
      final seenRelay = <String>{};
      final savedServers = StorageService.getServerList();
      for (final relayCandidate in fallbackRelays) {
        if (!seenRelay.add(relayCandidate)) continue;
        final candidateRelayToken = (existing?.relayToken ?? '').trim().isNotEmpty
            ? existing!.relayToken
            : resolveSavedGoPanelRelayToken(
                relayCandidate,
                savedServers,
                fallbackToken: resolvedToken,
              );
        try {
          bootstrap = await _probeHoxiRelayWithFallbackTokens(
            serverUrl: relayCandidate,
            entrance: server.entrance,
            primaryToken: candidateRelayToken,
            secondaryToken: resolvedToken,
            e2eeKey: existingE2eeKey,
          );
        } catch (_) {
          bootstrap = null;
        }
        if (bootstrap != null) {
          resolvedUrl = relayCandidate;
          break;
        }
      }
    }
    if (bootstrap == null) {
      throw Exception('未检测到 Hoxi 桌面端，请确认地址与桌面端已启动');
    }
    final merged = mergeHoxiBootstrapRelay(
      bootstrap: bootstrap,
      baseTransports: [
        ...buildInitialHoxiTransports(
          resolvedUrl,
          '',
          resolvedUrl == server.baseUrl ? const [] : [server.baseUrl],
        ),
        ...?existing?.transports,
      ],
      currentRelayToken: existing?.relayToken ?? '',
      currentE2eeKey: existingE2eeKey,
      savedServers: StorageService.getServerList(),
    );
    final trimmedCustom = customName.trim();
    return _saveAndFinish(
      ServerConnection(
        id: existing?.id ?? DateTime.now().millisecondsSinceEpoch.toString(),
        name: trimmedCustom.isNotEmpty
            ? trimmedCustom
            : buildHoxiNodeName(bootstrap, resolvedUrl),
        url: resolvedUrl,
        entrance: server.entrance,
        token: resolvedToken,
        relayToken: merged.relayToken,
        e2eeKey: merged.e2eeKey,
        nodeType: 'hoxi',
        lastConnectedAt: DateTime.now(),
        transports: merged.transports,
      ),
      preserveExistingName: trimmedCustom.isEmpty,
    );
  }

  Future<List<String>> _discoverHoxiLanCandidates(
    Iterable<String> hintUrls,
  ) async {
    final extraPorts = <int>{
      for (final raw in hintUrls)
        if (Uri.tryParse(raw.trim()) case final uri?
            when !isHoxiRelayUrl(raw) &&
                uri.hasPort &&
                uri.port > 0 &&
                uri.port <= 65535)
          uri.port,
    };
    try {
      return await _authRepository.discoverHoxiLanUrls(
        extraPorts: extraPorts.toList(),
      );
    } catch (_) {
      return const [];
    }
  }

  ServerConnection? _findExistingHoxi(String primaryUrl, String relayUrl) {
    if (_replacingConnectionId != null) {
      return StorageService.getServerList()
          .where((s) => s.id == _replacingConnectionId && s.isHoxi)
          .firstOrNull;
    }
    final urls = <String>{
      if (primaryUrl.trim().isNotEmpty) primaryUrl.trim(),
      if (relayUrl.trim().isNotEmpty) relayUrl.trim(),
    };
    if (urls.isEmpty) return null;
    for (final item in StorageService.getServerList()) {
      if (item.isHoxi &&
          (urls.contains(item.url) ||
              item.transports.any((t) => urls.contains(t.url)))) {
        return item;
      }
    }
    return null;
  }

  Future<Map<String, dynamic>?> _probeHoxiRelayWithFallbackTokens({
    required String serverUrl,
    required String entrance,
    required String primaryToken,
    required String secondaryToken,
    String e2eeKey = '',
  }) async {
    Object? credentialError;
    final tokens = <String>[
      primaryToken.trim(),
      if (secondaryToken.trim().isNotEmpty &&
          secondaryToken.trim() != primaryToken.trim())
        secondaryToken.trim(),
    ];
    for (final candidateToken in tokens) {
      try {
        final bootstrap = await _probeHoxiCandidate(
          serverUrl: serverUrl,
          entrance: entrance,
          token: candidateToken,
          e2eeKey: e2eeKey,
          hasFallback: true,
          isRelay: true,
        );
        if (bootstrap != null) return bootstrap;
      } catch (error) {
        if (NodeLinkStatus.isCredentialFailure(error)) credentialError = error;
      }
    }
    if (credentialError != null) {
      Error.throwWithStackTrace(credentialError, StackTrace.current);
    }
    return null;
  }

  Future<Map<String, dynamic>?> _probeHoxiCandidate({
    required String serverUrl,
    required String entrance,
    required String token,
    String e2eeKey = '',
    required bool hasFallback,
    bool isRelay = false,
  }) async {
    final timeout = !hasFallback
        ? AuthController._connectionTimeout
        : ((isRelay || isHoxiRelayUrl(serverUrl))
              ? _resolver.relayTimeout
              : _resolver.lanTimeout);
    try {
      return await _authRepository
          .probeHoxiNode(
            serverUrl: serverUrl,
            entrance: entrance,
            token: token,
            e2eeKey: e2eeKey,
          )
          .timeout(timeout);
    } on TimeoutException {
      if (!hasFallback) rethrow;
      return null;
    }
  }

  Future<bool> _probeHoxiUrl(
    ServerConnection conn,
    String url, {
    void Function(Map<String, dynamic>)? onBootstrap,
  }) async {
    final isRelay =
        conn.transports.any((t) => t.url == url && t.isRelay) ||
        isHoxiRelayUrl(url);
    final timeout = isRelay ? _resolver.relayTimeout : _resolver.lanTimeout;
    final primaryToken = conn.tokenForUrl(url).trim();
    final inherited = isRelay
        ? resolveSavedGoPanelRelayToken(url, StorageService.getServerList())
        : '';
    final tokens = <String>[
      primaryToken,
      if (isRelay &&
          conn.token.trim().isNotEmpty &&
          conn.token.trim() != primaryToken)
        conn.token.trim(),
      if (inherited.isNotEmpty &&
          inherited != primaryToken &&
          inherited != conn.token.trim())
        inherited,
    ];
    Object? credentialError;
    for (final candidateToken in tokens) {
      try {
        final bootstrap = await _authRepository
            .probeHoxiNode(
              serverUrl: url,
              entrance: conn.entrance,
              token: candidateToken,
              e2eeKey: conn.e2eeKey,
            )
            .timeout(timeout);
        if (bootstrap != null) {
          onBootstrap?.call(bootstrap);
          return true;
        }
      } catch (error) {
        if (NodeLinkStatus.isCredentialFailure(error)) credentialError = error;
      }
    }
    if (credentialError != null) {
      Error.throwWithStackTrace(credentialError, StackTrace.current);
    }
    return false;
  }

  Future<ServerConnection?> _resolveHoxiTransport(
    ServerConnection conn, {
    bool force = false,
  }) async {
    final transports = conn.transports.isNotEmpty
        ? conn.transports
        : NodeTransport.single(conn.url, relay: isHoxiRelayUrl(conn.url));
    Object? credentialError;
    Map<String, dynamic>? latestBootstrap;
    final chosen = await _resolver.resolve(
      transports,
      probe: (url) async {
        try {
          return await _probeHoxiUrl(
            conn,
            url,
            onBootstrap: (b) => latestBootstrap = b,
          );
        } catch (error) {
          if (NodeLinkStatus.isCredentialFailure(error)) {
            credentialError = error;
          }
          rethrow;
        }
      },
      force: force,
    );
    if (chosen != null) {
      return applyBootstrapRelayToConnection(
        conn.copyWith(url: chosen),
        latestBootstrap,
        StorageService.getServerList(),
      );
    }
    if (credentialError != null) {
      Error.throwWithStackTrace(credentialError!, StackTrace.current);
    }
    final knownUrls = <String>{conn.url, for (final t in transports) t.url};
    for (final discoveredUrl in await _discoverHoxiLanCandidates(knownUrls)) {
      if (!knownUrls.add(discoveredUrl)) continue;
      try {
        if (await _probeHoxiUrl(
          conn,
          discoveredUrl,
          onBootstrap: (b) => latestBootstrap = b,
        )) {
          return applyBootstrapRelayToConnection(
            conn.copyWith(
              url: discoveredUrl,
              transports: NodeTransport.ordered([
                NodeTransport(url: discoveredUrl, kind: 'lan'),
                ...transports,
              ]),
            ),
            latestBootstrap,
            StorageService.getServerList(),
          );
        }
      } catch (error) {
        if (NodeLinkStatus.isCredentialFailure(error)) credentialError = error;
      }
    }

    final savedServers = StorageService.getServerList();
    final extraRelayUrls = await _relayRepository.discoverRemoteRelayNodeUrls(
      conn,
      savedServers,
    );
    for (final fallback in buildHoxiFallbackRelayTransports(
      conn,
      extraRelayUrls: extraRelayUrls,
    )) {
      if (!knownUrls.add(fallback.url)) continue;
      try {
        if (await _probeHoxiUrl(conn, fallback.url)) {
          final inheritedToken = conn.relayToken.trim().isNotEmpty
              ? conn.relayToken
              : resolveSavedGoPanelRelayToken(fallback.url, savedServers);
          return conn.copyWith(
            url: fallback.url,
            relayToken: inheritedToken.isNotEmpty
                ? inheritedToken
                : conn.relayToken,
            transports: NodeTransport.ordered([
              ...transports,
              NodeTransport(url: fallback.url, kind: 'relay'),
            ]),
          );
        }
      } catch (error) {
        if (NodeLinkStatus.isCredentialFailure(error)) credentialError = error;
      }
    }
    if (credentialError != null) {
      Error.throwWithStackTrace(credentialError!, StackTrace.current);
    }
    return null;
  }
}
