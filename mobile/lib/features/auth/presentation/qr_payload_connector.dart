import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/l10n/l10n.dart';
import '../../../core/storage/server_connection.dart';
import '../../../core/storage/storage_service.dart';
import '../../../shared/navigation/feature_route_registry.dart';
import '../../../core/network/hoxi_node_probe.dart';
import '../data/aux_service_auth_port.dart';
import '../data/hoxi_relay_repository.dart';
import '../models/node_link_kind.dart';
import 'auth_failure_text.dart';
import 'controllers/auth_controller.dart';
import 'widgets/hoxi_relay_nodes_sheet.dart';

List<String> _parsePeerUrls(Object? raw) {
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

/// 解析二维码或剪贴板配对内容并执行连接。
///
/// 返回 `null` 表示连接成功；返回非空字符串表示可直接展示给用户的本地化错误文案。
Future<String?> connectWithQrPayload({
  WidgetRef? ref,
  AuthController? controller,
  AuthState Function()? readState,
  required AppLocalizations l10n,
  required String rawPayload,
  NodeLinkKind? expectedKind,
  String customName = '',
  String? replacingConnectionId,
  bool registerPush = true,
}) async {
  assert(
    ref != null || (controller != null && readState != null),
    'Either ref or controller+readState must be provided.',
  );
  final AuthController authController =
      controller ?? ref!.read(authControllerProvider.notifier);
  AuthState currentAuthState() =>
      readState != null ? readState() : ref!.read(authControllerProvider);
  final AuxServiceAuthPort? auxPort = ref != null
      ? ref.read(auxServiceAuthPortProvider)
      : authController.auxAuthPort;

  try {
    String? url;
    String token = '';
    String relayToken = '';
    String relayDeviceId = '';
    String e2eeSecret = '';
    String entrance = '';
    String relay = '';
    List<String> peerUrls = const [];
    String? nodeType;
    String payloadCustomName = customName.trim();

    final trimmed = rawPayload.trim();
    if (trimmed.isEmpty) {
      return l10n.qrErrorUnrecognized;
    }

    if (trimmed.startsWith('{')) {
      final dynamic decoded = jsonDecode(trimmed);
      if (decoded is! Map) {
        return l10n.qrErrorUnrecognized;
      }
      final data = Map<String, dynamic>.from(decoded);
      final extracted = extractHoxiBootstrapRelay(data);
      url = data['url']?.toString().trim();
      token = (data['token']?.toString() ?? '').trim();
      relayToken = (data['relayToken']?.toString() ?? '').trim();
      if (relayToken.isEmpty) relayToken = extracted.relayToken;
      relayDeviceId = (data['relayDeviceId']?.toString() ?? '').trim();
      if (relayDeviceId.isEmpty) relayDeviceId = extracted.relayDeviceId;
      e2eeSecret = extracted.e2eeKey;
      entrance = (data['entrance']?.toString() ?? '').trim();
      relay = (data['relay'] is String ? data['relay'].toString() : '').trim();
      if (relay.isEmpty || relay.startsWith('/api/tunnel/')) {
        relay = extracted.relayUrl.isNotEmpty ? extracted.relayUrl : relay;
      }
      peerUrls = _parsePeerUrls(data['peers'] ?? data['lanUrls']);
      if (peerUrls.isEmpty) peerUrls = extracted.peerUrls;
      nodeType = (data['type'] ?? data['nodeType'])?.toString().trim();
      if (payloadCustomName.isEmpty) {
        payloadCustomName =
            (data['name'] ?? data['alias'] ?? '').toString().trim();
      }
      final jsonPairCode =
          (data['mobilePairing'] ?? data['code'] ?? '').toString().trim();
      if (token.isEmpty && jsonPairCode.isNotEmpty) {
        token = 'gopanel_pair:$jsonPairCode';
        nodeType ??= 'gopanel';
      }

      if ((nodeType ?? '').toLowerCase() == 'gopanel-relay') {
        if (expectedKind == NodeLinkKind.gopanel) {
          return l10n.qrErrorWrongKindHoxi;
        }
        return await connectGopanelRelayPayload(
          replacingConnectionId: replacingConnectionId,
          ref: ref,
          controller: authController,
          readState: currentAuthState,
          l10n: l10n,
          data: data,
          customName: payloadCustomName,
          registerPush: registerPush,
        );
      }
    } else if (trimmed.startsWith('http://') || trimmed.startsWith('https://')) {
      final uri = Uri.tryParse(trimmed);
      final rawPath = uri?.path.replaceAll(RegExp(r'^/+|/+$'), '') ?? '';
      final relayParam =
          (uri?.queryParameters['relay'] ??
                  uri?.queryParameters['relayUrl'] ??
                  uri?.queryParameters['tunnelUrl'] ??
                  '')
              .trim();
      if (payloadCustomName.isEmpty && uri != null) {
        payloadCustomName =
            (uri.queryParameters['name'] ?? uri.queryParameters['alias'] ?? '')
                .trim();
      }
      final isGopanelMobileAuth =
          uri != null &&
          (uri.queryParameters.containsKey('mobilePairing') ||
              ((rawPath.endsWith('mobile/auth') ||
                      rawPath == 'mobile' ||
                      relayParam.contains('/api/tunnel/gopanel-nodes/')) &&
                  uri.queryParameters.containsKey('code')));
      if (isGopanelMobileAuth) {
        final pairCode =
            (uri.queryParameters['mobilePairing'] ??
                    uri.queryParameters['code'] ??
                    '')
                .trim();
        token = pairCode.isEmpty ? '' : 'gopanel_pair:$pairCode';
        relay = relayParam;
        peerUrls = _parsePeerUrls(uri.queryParameters['peers']);
        nodeType = 'gopanel';
        if (uri.queryParameters.containsKey('entrance')) {
          entrance = (uri.queryParameters['entrance'] ?? '').trim();
        } else if (uri.queryParameters.containsKey('mobilePairing') &&
            rawPath.isNotEmpty &&
            rawPath != 'mobile' &&
            rawPath != 'mobile/auth') {
          entrance = rawPath;
        }
        url = isHoxiRelayUrl(trimmed)
            ? Uri(
                scheme: uri.scheme,
                userInfo: uri.userInfo,
                host: uri.host,
                port: uri.hasPort ? uri.port : null,
                path: uri.path,
              ).toString()
            : Uri(
                scheme: uri.scheme,
                userInfo: uri.userInfo,
                host: uri.host,
                port: uri.hasPort ? uri.port : null,
              ).toString();
      } else if (uri != null &&
          (uri.queryParameters.containsKey('token') ||
              uri.queryParameters.containsKey('relay') ||
              uri.queryParameters.containsKey('relayUrl') ||
              uri.queryParameters.containsKey('tunnelUrl') ||
              uri.queryParameters.containsKey('nodeId') ||
              uri.queryParameters.containsKey('tunnelNodeId') ||
              uri.queryParameters.containsKey('type'))) {
        token = (uri.queryParameters['token'] ?? '').trim();
        relayToken =
            (uri.queryParameters['relayToken'] ??
                    uri.queryParameters['tunnelToken'] ??
                    '')
                .trim();
        relayDeviceId = (uri.queryParameters['relayDeviceId'] ?? '').trim();
        e2eeSecret = extractHoxiBootstrapRelay(uri.queryParameters).e2eeKey;
        entrance = (uri.queryParameters['entrance'] ?? '').trim();
        final panelParam =
            (uri.queryParameters['panel'] ??
                    uri.queryParameters['panelUrl'] ??
                    uri.queryParameters['controllerUrl'] ??
                    defaultHoxiRelayControllerUrl)
                .trim()
                .replaceAll(RegExp(r'/+$'), '');
        final nodeIdParam = int.tryParse(
          (uri.queryParameters['nodeId'] ??
                  uri.queryParameters['tunnelNodeId'] ??
                  '')
              .trim(),
        );
        if (relayParam.startsWith('/api/tunnel/') && panelParam.isNotEmpty) {
          relay = '$panelParam$relayParam';
        } else if (relayParam.isNotEmpty) {
          relay = relayParam;
        } else if (nodeIdParam != null &&
            nodeIdParam > 0 &&
            panelParam.isNotEmpty) {
          relay = buildHoxiRelayUrl(panelParam, nodeIdParam);
        }
        peerUrls = _parsePeerUrls(uri.queryParameters['peers']);
        final inferredType =
            auxPort?.inferQrUrlNodeType(uri: uri, token: token) ?? 'hoxi';
        nodeType = (uri.queryParameters['type'] ?? inferredType).trim();
        final cleanParams = Map<String, String>.from(uri.queryParameters)
          ..remove('token')
          ..remove('relayToken')
          ..remove('relayDeviceId')
          ..remove('tunnelToken')
          ..removeWhere((k, _) => k.toLowerCase() == 'e2eekey')
          ..remove('entrance')
          ..remove('relay')
          ..remove('relayUrl')
          ..remove('tunnelUrl')
          ..remove('nodeId')
          ..remove('tunnelNodeId')
          ..remove('panel')
          ..remove('panelUrl')
          ..remove('controllerUrl')
          ..remove('peers')
          ..remove('type')
          ..remove('name')
          ..remove('alias');
        final isAdminLoginPath = uri.path.endsWith('/admin/login');
        final cleanPath = isAdminLoginPath
            ? uri.path.substring(0, uri.path.length - '/admin/login'.length)
            : uri.path;
        url = Uri(
          scheme: uri.scheme,
          userInfo: uri.userInfo,
          host: uri.host,
          port: uri.hasPort ? uri.port : null,
          path: cleanPath,
          queryParameters: cleanParams.isEmpty ? null : cleanParams,
        ).toString();
      } else {
        url = trimmed;
        nodeType = 'hoxi';
      }
    }

    if (url == null || !url.startsWith('http')) {
      return l10n.qrErrorMissingUrl;
    }
    final effectiveType = (expectedKind?.id ?? nodeType ?? '').toLowerCase();
    if (token.isEmpty && effectiveType != 'hoxi') {
      return l10n.qrErrorMissingToken;
    }

    if (expectedKind != null && expectedKind.isConnectable) {
      final scanned = (nodeType ?? '').toLowerCase();
      final bool matches;
      if (expectedKind == NodeLinkKind.hoxi) {
        matches =
            scanned != 'gopanel' &&
            !(auxPort?.isKnownNonHoxiScannedType(scanned) ?? false);
      } else if (expectedKind == NodeLinkKind.gopanel) {
        matches = scanned == 'gopanel';
      } else {
        matches =
            auxPort?.matchExpectedQrKind(
              expectedKind: expectedKind,
              scannedType: scanned,
              token: token,
            ) ??
            false;
      }
      if (!matches) {
        final customErr = auxPort?.customKindMismatchError(
          scannedType: scanned,
          l10n: l10n,
        );
        if (customErr != null) return customErr;
        return expectedKind == NodeLinkKind.hoxi
            ? l10n.qrErrorWrongKindPanel
            : l10n.qrErrorWrongKindHoxi;
      }
    }

    final resolvedNodeType = auxPort != null
        ? auxPort.normalizeQrTokenNodeType(
            token: token,
            fallbackNodeType: expectedKind?.id ?? nodeType,
          )
        : (expectedKind?.id ?? nodeType);

    final success = await authController.connectWithQrToken(
      replacingConnectionId: replacingConnectionId,
      serverUrl: url,
      entrance: entrance,
      token: token,
      relayToken: relayToken,
      relayDeviceId: relayDeviceId,
      e2eeKey: e2eeSecret,
      nodeType: resolvedNodeType,
      relayUrl: relay,
      peerUrls: peerUrls,
      customName: payloadCustomName,
    );

    if (success) {
      if (registerPush &&
          ref != null &&
          (isHoxiRelayUrl(url) || isHoxiRelayUrl(relay))) {
        FeatureRouteRegistry.onRelayConnectedPushHook?.call(ref);
      }
      return null;
    }

    final failure = authFailureMessage(
      l10n,
      currentAuthState(),
    );
    return failure.isEmpty ? l10n.qrErrorVerifyFailed : failure;
  } catch (_) {
    return l10n.qrErrorUnrecognized;
  }
}

/// 扫描或粘贴 GoPanel 面板出的中继节点码：复用本机已保存的同面板登录态添加 Hoxi 中继连接。
Future<String?> connectGopanelRelayPayload({
  WidgetRef? ref,
  AuthController? controller,
  AuthState Function()? readState,
  required AppLocalizations l10n,
  required Map<String, dynamic> data,
  String customName = '',
  String? replacingConnectionId,
  bool registerPush = true,
}) async {
  final AuthController authController =
      controller ?? ref!.read(authControllerProvider.notifier);
  AuthState currentAuthState() =>
      readState != null ? readState() : ref!.read(authControllerProvider);
  final rawPanel = (data['panel']?.toString() ?? '').trim();
  final nodeId = int.tryParse('${data['nodeId']}') ?? 0;
  if (nodeId <= 0) {
    return l10n.qrErrorRelayMissing;
  }
  final panel = rawPanel.isNotEmpty
      ? rawPanel
      : defaultHoxiRelayControllerUrl.trim();
  final gopanel = panel.isEmpty ? null : findSavedGoPanelConnection(panel);
  final directToken =
      (data['relayToken'] ?? data['token'] ?? '').toString().trim();
  if ((gopanel == null && directToken.isEmpty) ||
      (gopanel == null && panel.isEmpty)) {
    return rawPanel.isEmpty
        ? l10n.qrErrorRelayMissing
        : l10n.qrErrorRelaySignInFirst;
  }
  final targetUrl = buildHoxiRelayUrl(gopanel?.url ?? panel, nodeId);
  final success = await authController.connectWithQrToken(
    replacingConnectionId: replacingConnectionId,
    serverUrl: targetUrl,
    entrance: gopanel?.entrance ?? (data['entrance']?.toString() ?? '').trim(),
    token: gopanel?.token ?? directToken,
    nodeType: 'hoxi',
    customName: customName,
  );
  if (success) {
    if (registerPush && ref != null) {
      FeatureRouteRegistry.onRelayConnectedPushHook?.call(ref);
    }
    return null;
  }
  final failure = authFailureMessage(
    l10n,
    currentAuthState(),
  );
  return failure.isEmpty ? l10n.qrErrorRelayConnectFailed : failure;
}

/// 已保存的 GoPanel 连接中，与面板根地址一致的一条。
ServerConnection? findSavedGoPanelConnection(String panelUrl) {
  final target = _panelOrigin(panelUrl);
  for (final server in StorageService.getServerList()) {
    if (server.isGoPanel && _panelOrigin(server.url) == target) {
      return server;
    }
  }
  return null;
}

String _panelOrigin(String url) {
  final uri = Uri.tryParse(url.trim());
  if (uri == null || !uri.hasAuthority) {
    return url.trim().replaceAll(RegExp(r'/+$'), '').toLowerCase();
  }
  return '${uri.scheme}://${uri.authority}'.toLowerCase();
}

/// 弹出某台已保存 GoPanel 下的 Hoxi 桌面中继列表，选中后连接并激活该电脑节点。
Future<bool> showAndConnectHoxiRelayFromGoPanel({
  required BuildContext context,
  required WidgetRef ref,
  required ServerConnection gopanel,
}) async {
  final node = await showModalBottomSheet<HoxiRelayNode>(
    context: context,
    isScrollControlled: true,
    builder: (sheetContext) => HoxiRelayNodesSheet(
      gopanel: gopanel,
      onSelect: (selected) => Navigator.of(sheetContext).pop(selected),
    ),
  );
  if (node == null || !context.mounted) return false;
  final success = await ref
      .read(authControllerProvider.notifier)
      .connectWithQrToken(
        serverUrl: buildHoxiRelayUrl(gopanel.url, node.nodeId),
        entrance: gopanel.entrance,
        token: gopanel.token,
        nodeType: 'hoxi',
      );
  if (!context.mounted) return false;
  if (success) {
    FeatureRouteRegistry.onRelayConnectedPushHook?.call(ref);
    return true;
  }
  ScaffoldMessenger.of(context).showSnackBar(
    SnackBar(
      content: Text(
        context.l10n.serverListRelayConnectFailed(node.displayName),
      ),
      backgroundColor: Colors.red,
    ),
  );
  return false;
}
