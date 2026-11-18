import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../core/network/hoxi_transport_resolver.dart';
import '../../../../core/network/network_providers.dart';
import '../../../../core/network/node_link_status.dart';
import '../../../../core/storage/account_scope_binding.dart';
import '../../../../core/storage/node_transport.dart';
import '../../../../core/storage/server_connection.dart';
import '../../../../core/storage/storage_service.dart';
import '../../data/auth_repository.dart';
import '../../data/aux_service_auth_port.dart';
import '../../data/hoxi_relay_repository.dart';
import '../../models/node_link_kind.dart';
import 'auth_connection_labels.dart';

part 'auth_hoxi_transport_controller.dart';

final authRepositoryProvider = Provider<AuthRepository>(
  (ref) => AuthRepository(ref.watch(nodeApiClientProvider)),
);
final auxServiceAuthPortProvider = Provider<AuxServiceAuthPort?>(
  (ref) => AuxServiceAuthRegistry.create(ref.watch(nodeApiClientProvider)),
);
final hoxiRelayRepositoryProvider = Provider<HoxiRelayRepository>(
  (ref) => HoxiRelayRepository(ref.watch(nodeApiClientProvider)),
);
final hoxiTransportResolverProvider = Provider<HoxiTransportResolver>(
  (ref) => HoxiTransportResolver(),
);

enum AuthFailureKind { connect, qrVerify, switchServer }

class AuthState {
  final bool isLoading;
  final AuthFailureKind? failureKind;
  final String? failureDetail;
  final bool isAuthenticated;
  final bool isCredentialFailure;

  const AuthState({
    this.isLoading = false,
    this.failureKind,
    this.failureDetail,
    this.isAuthenticated = false,
    this.isCredentialFailure = false,
  });

  List<ServerConnection> get savedServers => StorageService.getServerList();
  ServerConnection? get activeServer {
    final activeUrl = StorageService.activeServerUrl;
    if (activeUrl == null || activeUrl.isEmpty) return null;
    for (final s in StorageService.getServerList()) {
      if (StorageService.isServerActive(s)) return s;
    }
    return null;
  }

  AuthState copyWith({
    bool? isLoading,
    AuthFailureKind? failureKind,
    String? failureDetail,
    bool? isAuthenticated,
    bool? isCredentialFailure,
    bool clearFailure = false,
  }) => AuthState(
    isLoading: isLoading ?? this.isLoading,
    failureKind: clearFailure ? null : (failureKind ?? this.failureKind),
    failureDetail: clearFailure ? null : (failureDetail ?? this.failureDetail),
    isAuthenticated: isAuthenticated ?? this.isAuthenticated,
    isCredentialFailure: isCredentialFailure ?? this.isCredentialFailure,
  );
}

final authControllerProvider = NotifierProvider<AuthController, AuthState>(
  AuthController.new,
);

/// 处理节点配对与连接切换控制器（原生支持 Hoxi 桌面端与 GoPanel 面板，扩展服务经 [AuxServiceAuthPort] 驱动）
class AuthController extends Notifier<AuthState> {
  static const _connectionTimeout = Duration(seconds: 20);
  static const _storageTimeout = Duration(seconds: 10);
  late AuthRepository _authRepository;
  AuxServiceAuthPort? _auxAuthPort;
  AuxServiceAuthPort? get auxAuthPort => _auxAuthPort;
  late HoxiRelayRepository _relayRepository;
  late HoxiTransportResolver _resolver;

  @override
  AuthState build() {
    _authRepository = ref.watch(authRepositoryProvider);
    _auxAuthPort = ref.watch(auxServiceAuthPortProvider);
    _relayRepository = ref.watch(hoxiRelayRepositoryProvider);
    _resolver = ref.watch(hoxiTransportResolverProvider);
    return const AuthState();
  }

  Future<bool> connectAndLogin({
    required String serverUrl,
    String entrance = '',
    String username = '',
    String password = '',
    String token = '',
    String customName = '',
    NodeLinkKind? kind,
    String? replacingConnectionId,
  }) async {
    _replacingConnectionId = replacingConnectionId;
    state = state.copyWith(isLoading: true, clearFailure: true);
    try {
      final server = normalizeServerAddress(serverUrl, entrance);
      if (!isValidServerEntrance(server.entrance)) {
        throw const FormatException('entranceInvalid');
      }
      final u = username.trim(),
          p = password.trim(),
          t = token.trim(),
          cn = customName.trim();
      if (kind == NodeLinkKind.gopanel) {
        return await _loginGoPanel(
          server: server,
          username: u,
          password: p,
          customName: cn,
        );
      }
      if (kind == NodeLinkKind.hoxi) {
        return await _connectHoxi(
          server: server,
          username: u,
          password: p,
          token: t,
          customName: cn,
        );
      }
      if (kind == null) {
        return await _connectAuto(
          server: server,
          username: u,
          password: p,
          token: t,
          customName: cn,
        );
      }
      final aux = _auxAuthPort;
      if (aux == null || !aux.supportsNodeType(kind.id)) {
        throw UnsupportedError('Unsupported node kind: ${kind.id}');
      }
      final result = await aux
          .loginWithCredentials(
            kind: kind,
            serverUrl: server.baseUrl,
            entrance: server.entrance,
            username: u,
            password: p,
            token: t,
          )
          .timeout(_connectionTimeout);
      return await _saveNode(
        name: result.displayName,
        customName: cn,
        url: server.baseUrl,
        entrance: server.entrance,
        token: result.token,
        nodeType: kind.id,
      );
    } catch (e) {
      state = state.copyWith(
        isLoading: false,
        failureKind: AuthFailureKind.connect,
        failureDetail: authConnectionFailureDetail(e),
      );
      return false;
    } finally {
      _replacingConnectionId = null;
    }
  }

  Future<bool> _connectAuto({
    required ({String baseUrl, String entrance}) server,
    required String username,
    required String password,
    required String token,
    required String customName,
  }) async {
    if (username.isEmpty && password.isEmpty) {
      return _connectHoxi(
        server: server,
        username: username,
        password: password,
        token: token,
        customName: customName,
      );
    }
    try {
      return await _loginGoPanel(
        server: server,
        username: username,
        password: password,
        customName: customName,
      );
    } catch (loginErr) {
      try {
        return await _connectHoxi(
          server: server,
          username: username,
          password: password,
          token: token,
          customName: customName,
        );
      } catch (_) {
        throw loginErr;
      }
    }
  }

  Future<bool?> pollBrowserAuthorize({
    required String nodeType,
    required String serverUrl,
    required String stateToken,
    String customName = '',
    String? replacingConnectionId,
  }) async {
    final aux = _auxAuthPort;
    if (aux == null || !aux.supportsNodeType(nodeType)) return null;
    _replacingConnectionId = replacingConnectionId;
    try {
      final server = normalizeServerAddress(serverUrl, '');
      final payload = await aux
          .pollBrowserAuth(
            nodeType: nodeType,
            serverUrl: server.baseUrl,
            state: stateToken,
          )
          .timeout(_connectionTimeout);
      final status = (payload['status'] ?? '').toString().trim().toLowerCase();
      if (status == 'pending') return false;
      if (status != 'authorized') return null;
      final rawToken = (payload['rawToken'] ?? payload['token'] ?? '')
          .toString()
          .trim();
      if (rawToken.isEmpty) return null;
      final displayName = aux.formatAuthorizedDisplayName(
        nodeType: nodeType,
        payload: payload,
      );
      return await _saveNode(
        name: displayName,
        customName: customName,
        url: server.baseUrl,
        token: rawToken,
        nodeType: nodeType,
      );
    } catch (_) {
      return false;
    } finally {
      _replacingConnectionId = null;
    }
  }

  Future<bool> connectWithQrToken({
    required String serverUrl,
    String entrance = '',
    required String token,
    String relayToken = '',
    String e2eeKey = '',
    String? nodeType,
    String relayUrl = '',
    List<String> peerUrls = const [],
    String customName = '',
    String? replacingConnectionId,
  }) async {
    _replacingConnectionId = replacingConnectionId;
    state = state.copyWith(isLoading: true, clearFailure: true);
    try {
      final server = normalizeServerAddress(serverUrl, entrance);
      if (!isValidServerEntrance(server.entrance)) {
        throw const FormatException('entranceInvalid');
      }
      final cn = customName.trim();
      final aux = _auxAuthPort;
      final normalizedType =
          (aux?.normalizeQrTokenNodeType(
                    token: token,
                    fallbackNodeType: nodeType,
                  ) ??
                  nodeType ??
                  '')
              .trim()
              .toLowerCase();
      if (aux != null && aux.supportsNodeType(normalizedType)) {
        final validated = await aux
            .validateQrOrSaved(
              nodeType: normalizedType,
              serverUrl: server.baseUrl,
              entrance: server.entrance,
              token: token,
              peerUrls: peerUrls,
            )
            .timeout(_connectionTimeout);
        return await _saveNode(
          name: validated.displayName,
          customName: cn,
          url: validated.url,
          entrance: server.entrance,
          token: validated.token,
          nodeType: normalizedType,
          transports: buildInitialHoxiTransports(
            validated.url,
            relayUrl,
            [server.baseUrl, ...peerUrls],
          ),
        );
      }
      if (normalizedType == 'hoxi' || token.trim().isEmpty) {
        return await _pairHoxiWithQr(
          server: server,
          token: token,
          relayToken: relayToken,
          e2eeKey: e2eeKey,
          relayUrl: relayUrl,
          peerUrls: peerUrls,
          customName: cn,
        );
      }
      try {
        final resolved = await _authRepository
            .resolveGoPanelQrToken(
              serverUrl: server.baseUrl,
              entrance: server.entrance,
              token: token,
              relayUrl: relayUrl,
              peerUrls: peerUrls,
            )
            .timeout(_connectionTimeout);
        return await _saveNode(
          name: resolved.displayName,
          customName: cn,
          url: resolved.url,
          entrance: resolved.entrance,
          token: resolved.token,
          nodeType: 'gopanel',
          transports: buildInitialHoxiTransports(
            server.baseUrl,
            relayUrl,
            peerUrls,
          ),
        );
      } catch (_) {
        if (token.trim().startsWith('gopanel_pair:')) rethrow;
        return await _pairHoxiWithQr(
          server: server,
          token: token,
          relayToken: relayToken,
          e2eeKey: e2eeKey,
          relayUrl: relayUrl,
          peerUrls: peerUrls,
          customName: cn,
        );
      }
    } catch (e) {
      state = state.copyWith(
        isLoading: false,
        failureKind: AuthFailureKind.qrVerify,
        failureDetail: authConnectionFailureDetail(e),
      );
      return false;
    } finally {
      _replacingConnectionId = null;
    }
  }

  Future<bool> failoverActiveHoxiNode(ServerConnection server) async {
    if (!server.isHoxi) return false;
    try {
      final selected = await _resolveHoxiTransport(server, force: true);
      if (selected == null || !StorageService.isServerActive(server)) {
        return false;
      }
      await _saveConnection(selected, preserveExistingName: true);
      return true;
    } catch (_) {
      return false;
    }
  }

  Future<bool> switchToSavedServer(ServerConnection server) async {
    final accountEpoch = server.accountScope == null
        ? null
        : AccountScopeBinding.identityEpoch;
    state = state.copyWith(
      isLoading: true,
      clearFailure: true,
      isCredentialFailure: false,
    );
    try {
      if (server.accountScope != null &&
          server.accountScope != AccountScopeBinding.accountScope) {
        throw StateError('Connection account changed');
      }
      var target = server;
      if (server.isHoxi) {
        final selected = await _resolveHoxiTransport(server, force: true);
        if (selected == null) throw Exception('Hoxi 桌面端不可达');
        target = selected;
      } else if (server.isAuxService) {
        final aux = _auxAuthPort;
        if (aux == null || !aux.supportsNodeType(server.nodeType)) {
          throw UnsupportedError('Unsupported service node: ${server.nodeType}');
        }
        await aux
            .validateQrOrSaved(
              nodeType: server.nodeType,
              serverUrl: server.url,
              entrance: server.entrance,
              token: server.token,
            )
            .timeout(_connectionTimeout);
      } else {
        final resolved = await _authRepository
            .resolveGoPanelQrToken(
              serverUrl: server.url,
              entrance: server.entrance,
              token: server.token,
              peerUrls: server.transports.map((t) => t.url).toList(),
            )
            .timeout(_connectionTimeout);
        target = server.copyWith(url: resolved.url);
      }
      await _saveConnection(
        target,
        preserveExistingName: true,
        matchByIdOnly: true,
        expectedIdentityEpoch: accountEpoch,
      );
      state = state.copyWith(
        isLoading: false,
        isAuthenticated: true,
        isCredentialFailure: false,
      );
      return true;
    } catch (e) {
      state = state.copyWith(
        isLoading: false,
        isCredentialFailure: NodeLinkStatus.isCredentialFailure(e),
        failureKind: AuthFailureKind.switchServer,
        failureDetail: authConnectionFailureDetail(e),
      );
      return false;
    }
  }

  Future<bool> _loginGoPanel({
    required ({String baseUrl, String entrance}) server,
    required String username,
    required String password,
    String customName = '',
  }) async {
    if (username.isEmpty || password.isEmpty) {
      throw Exception('连接 GoPanel 面板需要填写账号与密码');
    }
    final token = await _authRepository
        .login(
          serverUrl: server.baseUrl,
          entrance: server.entrance,
          name: username,
          password: password,
        )
        .timeout(_connectionTimeout);
    return _saveNode(
      name: username,
      customName: customName,
      url: server.baseUrl,
      entrance: server.entrance,
      token: token,
      nodeType: 'gopanel',
    );
  }

  Future<bool> _saveNode({
    required String name,
    String customName = '',
    required String url,
    String entrance = '',
    required String token,
    required String nodeType,
    List<NodeTransport> transports = const [],
  }) {
    final trimmedCustom = customName.trim();
    return _saveAndFinish(
      ServerConnection(
        id: DateTime.now().millisecondsSinceEpoch.toString(),
        name: trimmedCustom.isNotEmpty ? trimmedCustom : name,
        url: url,
        entrance: entrance,
        token: token,
        nodeType: nodeType,
        lastConnectedAt: DateTime.now(),
        transports: transports,
      ),
      preserveExistingName: trimmedCustom.isEmpty,
    );
  }

  Future<bool> _saveAndFinish(
    ServerConnection server, {
    bool preserveExistingName = false,
  }) async {
    if (_replacingConnectionId != null) {
      final target = StorageService.getServerList()
          .where((s) => s.id == _replacingConnectionId)
          .firstOrNull;
      if (target == null || target.nodeType != server.nodeType) {
        throw StateError('Connection authorization target changed');
      }
      server = server.copyWith(id: target.id, accountScope: target.accountScope);
    }
    await _saveConnection(
      server,
      preserveExistingName: preserveExistingName,
      matchByIdOnly: _replacingConnectionId != null,
    ).timeout(_storageTimeout);
    state = state.copyWith(isLoading: false, isAuthenticated: true);
    return true;
  }

  String? _replacingConnectionId;
  Future<void> _saveConnection(
    ServerConnection server, {
    bool preserveExistingName = false,
    bool matchByIdOnly = false,
    int? expectedIdentityEpoch,
  }) async {
    expectedIdentityEpoch ??= server.accountScope == null
        ? null
        : AccountScopeBinding.identityEpoch;
    if (server.accountScope != null &&
        server.accountScope != AccountScopeBinding.accountScope) {
      throw StateError('Connection account changed');
    }
    if (!await StorageService.saveServerConnection(
      server,
      preserveExistingName: preserveExistingName,
      matchByIdOnly: matchByIdOnly,
      expectedIdentityEpoch: expectedIdentityEpoch,
    )) {
      throw StateError('Could not save connection');
    }
    if (expectedIdentityEpoch != null &&
        expectedIdentityEpoch != AccountScopeBinding.identityEpoch) {
      throw StateError('Connection account changed');
    }
    final saved =
        StorageService.getServerList()
            .where((s) => s.id == server.id)
            .firstOrNull ??
        StorageService.getServerList()
            .where((s) => StorageService.isServerActive(s, server.url))
            .firstOrNull;
    await StorageService.activateServer(saved ?? server);
  }

  Future<void> logout() async {
    await StorageService.clearActiveServer();
    state = const AuthState(isAuthenticated: false);
  }
}
