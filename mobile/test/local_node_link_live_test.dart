import 'dart:io';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:hoxi/core/storage/storage_service.dart';
import 'package:hoxi/features/auth/models/node_link_kind.dart';
import 'package:hoxi/features/auth/presentation/controllers/auth_controller.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// 真实本地节点联调：用 App 自己的连接链路（AuthController + AuthRepository）
/// 关联本机 Hoxi 桌面端与 GoPanel 面板。
///
/// 需要真实服务在跑，默认跳过。启用方式：
///   HOXI_LIVE_GOPANEL_URL=http://127.0.0.1:5471 \
///   HOXI_LIVE_GOPANEL_EMAIL=e2e@hoxi.local \
///   HOXI_LIVE_GOPANEL_PASSWORD=E2ePass123456 \
///   HOXI_LIVE_HOXI_URL=http://127.0.0.1:5410 \
///   flutter test test/local_node_link_live_test.dart
///
/// 只读连接与列表查询，不做任何修改类操作。
void main() {
  final hoxiUrl = Platform.environment['HOXI_LIVE_HOXI_URL'] ?? '';
  final panelUrl = Platform.environment['HOXI_LIVE_GOPANEL_URL'] ?? '';
  final panelEmail = Platform.environment['HOXI_LIVE_GOPANEL_EMAIL'] ?? '';
  final panelPassword = Platform.environment['HOXI_LIVE_GOPANEL_PASSWORD'] ?? '';
  final panelEntrance =
      Platform.environment['HOXI_LIVE_GOPANEL_ENTRANCE'] ?? '';

  final enabled = hoxiUrl.isNotEmpty && panelUrl.isNotEmpty;

  setUp(() async {
    // flutter_test 默认拦截真实网络，这里显式放行本地联调。
    HttpOverrides.global = null;
    SharedPreferences.setMockInitialValues({});
    FlutterSecureStorage.setMockInitialValues({});
    await StorageService.init();
  });

  Future<ProviderContainer> connect(
    NodeLinkKind kind, {
    required String url,
    String entrance = '',
    String username = '',
    String password = '',
  }) async {
    final container = ProviderContainer();
    addTearDown(container.dispose);
    final ok = await container
        .read(authControllerProvider.notifier)
        .connectAndLogin(
          serverUrl: url,
          entrance: entrance,
          username: username,
          password: password,
          kind: kind,
        );
    final state = container.read(authControllerProvider);
    expect(ok, isTrue, reason: state.failureKind?.name ?? '连接失败');
    return container;
  }

  test(
    'links the local Hoxi desktop through the app connection path',
    () async {
      await connect(NodeLinkKind.hoxi, url: hoxiUrl);

      expect(StorageService.isActiveHoxi, isTrue);
      expect(StorageService.activeServerUrl, hoxiUrl);
      expect(StorageService.activeServerNodeType, 'hoxi');
      final saved = StorageService.getServerList();
      expect(saved, hasLength(1));
      expect(saved.single.nodeType, 'hoxi');
      expect(saved.single.name, startsWith('Hoxi · '));
    },
    skip: !enabled || hoxiUrl.isEmpty,
  );

  test(
    'links the local GoPanel panel through the app connection path',
    () async {
      final container = await connect(
        NodeLinkKind.gopanel,
        url: panelUrl,
        entrance: panelEntrance,
        username: panelEmail,
        password: panelPassword,
      );

      expect(StorageService.isActiveHoxi, isFalse);
      expect(StorageService.activeServerNodeType, 'gopanel');
      expect(StorageService.activeServerToken, isNotEmpty);
      final saved = StorageService.getServerList();
      expect(saved, hasLength(1));
      expect(saved.single.nodeType, 'gopanel');
      expect(container.read(authControllerProvider).failureKind, isNull);
    },
    skip: !enabled || panelEmail.isEmpty || panelPassword.isEmpty,
  );

  test(
    'switching back to the saved Hoxi desktop keeps its node type',
    () async {
      await connect(NodeLinkKind.hoxi, url: hoxiUrl);
      final hoxi = StorageService.getServerList().single;
      await connect(
        NodeLinkKind.gopanel,
        url: panelUrl,
        entrance: panelEntrance,
        username: panelEmail,
        password: panelPassword,
      );
      expect(StorageService.isActiveHoxi, isFalse);

      final container = ProviderContainer();
      addTearDown(container.dispose);
      final ok = await container
          .read(authControllerProvider.notifier)
          .switchToSavedServer(hoxi);
      expect(ok, isTrue);
      expect(StorageService.isActiveHoxi, isTrue);
      expect(StorageService.getServerList(), hasLength(2));
    },
    skip: !enabled || panelEmail.isEmpty || panelPassword.isEmpty,
  );

  test(
    'GoPanel kind refuses empty credentials instead of probing Hoxi',
    () async {
      final container = ProviderContainer();
      addTearDown(container.dispose);
      final ok = await container
          .read(authControllerProvider.notifier)
          .connectAndLogin(
            serverUrl: panelUrl,
            username: '',
            password: '',
            kind: NodeLinkKind.gopanel,
          );
      expect(ok, isFalse);
      expect(StorageService.activeServerUrl, isNull);
    },
    skip: !enabled,
  );
}
