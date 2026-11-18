import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:hoxi/core/network/hoxi_node_probe.dart';
import 'package:hoxi/core/storage/storage_service.dart';
import 'package:hoxi/features/auth/data/hoxi_relay_repository.dart';
import 'package:hoxi/core/storage/node_transport.dart';
import 'package:hoxi/core/storage/server_connection.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  const relay = 'https://panel.example.com/api/tunnel/hoxi-nodes/3/proxy';

  test('builds and recognizes GoPanel and work.fastd.cn relay URLs', () {
    expect(buildHoxiRelayUrl('https://panel.example.com/', 3), relay);
    expect(isHoxiRelayUrl(relay), isTrue);
    expect(hoxiRelayControllerUrl(relay), 'https://panel.example.com');
    expect(extractHoxiRelayNodeId(relay), 3);
    expect(isHoxiRelayUrl('https://work.fastd.cn'), isTrue);
    expect(
      isHoxiRelayUrl('https://work.fastd.cn/api/tunnel/hoxi-nodes/7/proxy'),
      isTrue,
    );
    expect(
      hoxiRelayControllerUrl(
        'https://work.fastd.cn/api/tunnel/hoxi-nodes/7/proxy',
      ),
      'https://work.fastd.cn',
    );
    expect(isHoxiRelayUrl('https://panel.example.com/private-entry'), isFalse);
    expect(isHoxiRelayUrl('https://work.fastd.cn/private-entry'), isFalse);
    expect(
      isHoxiRelayUrl('https://panel.example.com/api/tunnel/hoxi-nodes/x/proxy'),
      isFalse,
    );
    expect(hoxiRelayControllerUrl('http://192.168.1.5:5410'), isNull);
  });

  test('extractHoxiBootstrapRelay synthesizes work.fastd.cn tunnel URL from nodeId', () {
    final extracted = extractHoxiBootstrapRelay({
      'appVersion': '1.0.0',
      'rootDir': '/Users/me/hoxi',
      'tunnel': {
        'nodeId': 12,
        'relayToken': 'tunnel-secret',
      },
    });
    expect(
      extracted.relayUrl,
      'https://work.fastd.cn/api/tunnel/hoxi-nodes/12/proxy',
    );
    expect(extracted.relayToken, 'tunnel-secret');
    expect(extracted.nodeId, 12);
  });

  test('buildHoxiFallbackRelayTransports appends work.fastd.cn fallback for LAN-only node', () {
    final lanOnlyNode = ServerConnection(
      id: 'hoxi-lan',
      name: 'Hoxi · repo',
      url: 'http://192.168.1.20:5417',
      token: 'lan-token',
      nodeType: 'hoxi',
      lastConnectedAt: DateTime.utc(2026),
      transports: const [
        NodeTransport(url: 'http://192.168.1.20:5417', kind: 'lan'),
      ],
    );
    final fallbacks = buildHoxiFallbackRelayTransports(lanOnlyNode);
    expect(fallbacks.map((t) => t.url), contains('https://work.fastd.cn'));
    expect(fallbacks.every((t) => t.isRelay), isTrue);
  });

  test(
    'relay paths survive address normalization instead of becoming an entrance',
    () {
      final result = normalizeServerAddress(relay, 'panel_entry');
      expect(result.baseUrl, relay);
      expect(result.entrance, 'panel_entry');
    },
  );

  test('parses GoPanel hoxi-node listings and skips malformed rows', () {
    final nodes = parseHoxiRelayNodes({
      'items': [
        {
          'nodeId': 3,
          'name': 'Studio',
          'hostname': 'mac.local',
          'status': 'online',
          'version': '0.2.9',
        },
        {
          'nodeId': 4,
          'name': '',
          'hostname': 'mini.local',
          'status': 'offline',
        },
        {'nodeId': 0, 'name': 'broken'},
        'not-a-map',
      ],
    });
    expect(nodes.map((n) => n.nodeId), [3, 4]);
    expect(nodes.first.online, isTrue);
    expect(nodes.last.online, isFalse);
    expect(nodes.last.displayName, 'mini.local');
    expect(parseHoxiRelayNodes(null), isEmpty);
  });

  test(
    're-logging into GoPanel refreshes credentials of its relay connections',
    () async {
      SharedPreferences.setMockInitialValues({});
      FlutterSecureStorage.setMockInitialValues({});
      await StorageService.init();
      final now = DateTime.utc(2026);
      await StorageService.saveServerConnection(
        ServerConnection(
          id: 'gp',
          name: 'GoPanel',
          url: 'https://panel.example.com',
          token: 'old-jwt',
          lastConnectedAt: now,
        ),
      );
      await StorageService.saveServerConnection(
        ServerConnection(
          id: 'mac',
          name: 'Studio',
          url: relay,
          token: 'old-jwt',
          nodeType: 'hoxi',
          lastConnectedAt: now,
        ),
      );
      await StorageService.saveServerConnection(
        ServerConnection(
          id: 'other',
          name: 'Other',
          url: 'https://other.example.com/api/tunnel/hoxi-nodes/9/proxy',
          token: 'other-jwt',
          nodeType: 'hoxi',
          lastConnectedAt: now,
        ),
      );

      await StorageService.saveServerConnection(
        ServerConnection(
          id: 'gp',
          name: 'GoPanel',
          url: 'https://panel.example.com',
          token: 'new-jwt',
          entrance: 'entry',
          lastConnectedAt: now,
        ),
      );

      await StorageService.init();
      final servers = {
        for (final s in StorageService.getServerList()) s.url: s,
      };
      expect(servers[relay]!.token, 'new-jwt');
      expect(servers[relay]!.entrance, 'entry');
      expect(servers[relay]!.url, relay);
      expect(
        servers['https://other.example.com/api/tunnel/hoxi-nodes/9/proxy']!
            .token,
        'other-jwt',
      );
    },
  );

  test(
    're-logging into GoPanel updates relayToken without overwriting LAN pairing token',
    () async {
      SharedPreferences.setMockInitialValues({});
      FlutterSecureStorage.setMockInitialValues({});
      await StorageService.init();
      final now = DateTime.utc(2026);
      const lanUrl = 'http://192.168.1.20:5417';

      await StorageService.saveServerConnection(
        ServerConnection(
          id: 'gp',
          name: 'GoPanel',
          url: 'https://panel.example.com',
          token: 'old-jwt',
          lastConnectedAt: now,
        ),
      );
      await StorageService.saveServerConnection(
        ServerConnection(
          id: 'mac-dual',
          name: 'Studio Dual',
          url: relay,
          token: 'lan-pairing-token',
          relayToken: 'old-jwt',
          nodeType: 'hoxi',
          lastConnectedAt: now,
          transports: const [
            NodeTransport(url: lanUrl, kind: 'lan'),
            NodeTransport(url: relay, kind: 'relay'),
          ],
        ),
      );

      await StorageService.saveServerConnection(
        ServerConnection(
          id: 'gp',
          name: 'GoPanel',
          url: 'https://panel.example.com',
          token: 'refreshed-jwt',
          entrance: 'panel_entry',
          lastConnectedAt: now,
        ),
      );

      final saved = StorageService.getServerList().firstWhere(
        (s) => s.id == 'mac-dual',
      );
      expect(saved.token, 'lan-pairing-token');
      expect(saved.relayToken, 'refreshed-jwt');
      expect(saved.tokenForUrl(lanUrl), 'lan-pairing-token');
      expect(saved.tokenForUrl(relay), 'refreshed-jwt');
    },
  );
}
