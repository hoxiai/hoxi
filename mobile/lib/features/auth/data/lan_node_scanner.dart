import 'dart:async';
import 'dart:convert';
import 'dart:io';

import '../models/node_link_kind.dart';

/// 局域网自动发现的节点信息（零公网依赖，纯本地 Wi-Fi 子网扫描）
class DiscoveredLanNode {
  final String url;
  final String title;
  final String subtitle;
  final NodeLinkKind kind;
  final bool requiresToken;

  const DiscoveredLanNode({
    required this.url,
    required this.title,
    required this.subtitle,
    required this.kind,
    this.requiresToken = false,
  });
}

typedef ExtraLanPortProvider = List<int> Function(NodeLinkKind? filterKind);
typedef ExtraLanHostVerifier =
    Future<DiscoveredLanNode?> Function(
      String baseUrl,
      String host,
      int port,
      NodeLinkKind? filterKind,
    );

/// 局域网子网并发探测器：
/// 读取手机当前 Wi-Fi 的私有 IPv4 网段（如 `192.168.1.x/24`），
/// 并发探测标准受控端口（5417/5410/5420: Hoxi 桌面端、5470/15470: GoPanel 面板），
/// 全程不经过任何公网服务器。
class LanNodeScanner {
  static const _connectTimeout = Duration(milliseconds: 260);
  static const _httpTimeout = Duration(milliseconds: 550);

  static ExtraLanPortProvider? extraPortProvider;
  static ExtraLanHostVerifier? extraHostVerifier;

  /// 获取本机局域网 IPv4 地址列表（如 `192.168.1.25`），按物理 Wi-Fi/以太网优先于虚拟网卡排序。
  static Future<List<String>> localPrivateIPv4s() async {
    try {
      final interfaces = await NetworkInterface.list(
        includeLoopback: false,
        type: InternetAddressType.IPv4,
      );
      return prioritizeInterfaceIPv4s([
        for (final iface in interfaces)
          (
            name: iface.name,
            addresses: [for (final addr in iface.addresses) addr.address],
          ),
      ]);
    } catch (_) {
      return const [];
    }
  }

  /// 对网卡及其 IPv4 地址按「物理网卡优先、家庭/办公常见 192.168.x.x 网段优先、VPN/TUN 置后」排序去重。
  static List<String> prioritizeInterfaceIPv4s(
    Iterable<({String name, List<String> addresses})> interfaces,
  ) {
    final scored = <({String ip, int score, int order})>[];
    var order = 0;
    for (final iface in interfaces) {
      final bonus = _interfacePriorityBonus(iface.name);
      for (final raw in iface.addresses) {
        final ip = raw.trim();
        final rank = _privateIPv4Rank(ip);
        if (rank <= 0) continue;
        scored.add((ip: ip, score: rank + bonus, order: order++));
      }
    }
    scored.sort((a, b) {
      final cmp = b.score.compareTo(a.score);
      return cmp != 0 ? cmp : a.order.compareTo(b.order);
    });
    final seen = <String>{};
    return [
      for (final item in scored)
        if (seen.add(item.ip)) item.ip,
    ];
  }

  static bool _isVirtualInterfaceName(String name) {
    final lower = name.trim().toLowerCase();
    const prefixes = [
      'utun',
      'tun',
      'tap',
      'ppp',
      'ipsec',
      'gif',
      'stf',
      'awdl',
      'llw',
      'anpi',
      'bridge',
      'veth',
      'docker',
      'br-',
      'vmnet',
      'vboxnet',
      'virbr',
      'wg',
      'tailscale',
      'zt',
      'cni',
      'flannel',
      'cali',
      'lo',
      'pdp_ip',
      'rmnet',
      'ccmni',
    ];
    for (final prefix in prefixes) {
      if (lower.startsWith(prefix)) return true;
    }
    return false;
  }

  static int _interfacePriorityBonus(String name) {
    if (_isVirtualInterfaceName(name)) return -25;
    final lower = name.trim().toLowerCase();
    if (lower == 'en0' || lower == 'wlan0' || lower == 'eth0') return 15;
    for (final prefix in const [
      'en',
      'eth',
      'wlan',
      'wl',
      'wi-fi',
      'ethernet',
    ]) {
      if (lower.startsWith(prefix)) return 10;
    }
    return 0;
  }

  static int _privateIPv4Rank(String ip) {
    final parts = ip.split('.');
    if (parts.length != 4) return 0;
    final a = int.tryParse(parts[0]) ?? -1;
    final b = int.tryParse(parts[1]) ?? -1;
    final c = int.tryParse(parts[2]) ?? -1;
    final d = int.tryParse(parts[3]) ?? -1;
    if (a < 0 || b < 0 || c < 0 || d < 0) return 0;
    if (a > 255 || b > 255 || c > 255 || d > 255) return 0;
    if (a == 192 && b == 168) {
      return (c == 64 || c == 65) ? 35 : 50;
    }
    if (a == 10) return 40;
    if (a == 172 && b >= 16 && b <= 31) return 30;
    return 0;
  }

  /// 扫描当前 Wi-Fi `/24` 子网（以及本机回环/本机局域网 IP）内的可用节点。
  static Future<List<DiscoveredLanNode>> scan({
    NodeLinkKind? filterKind,
    List<int> extraPorts = const [],
  }) async {
    final localIps = await localPrivateIPv4s();
    final registeredPorts =
        extraPortProvider?.call(filterKind) ?? const <int>[];

    final portSet = <int>{
      for (final p in extraPorts)
        if (p > 0 && p <= 65535) p,
      if (filterKind == null || filterKind == NodeLinkKind.hoxi) ...[
        5417,
        5410,
        5420,
      ],
      if (filterKind == null || filterKind == NodeLinkKind.gopanel) ...[
        5470,
        15470,
      ],
      for (final p in registeredPorts)
        if (p > 0 && p <= 65535) p,
    };
    final ports = portSet.toList();
    if (ports.isEmpty) return const [];

    final seenHosts = <String>{};
    final hosts = <String>[];
    for (final selfIp in localIps) {
      if (seenHosts.add(selfIp)) {
        hosts.add(selfIp);
      }
    }
    if (seenHosts.add('127.0.0.1')) {
      hosts.add('127.0.0.1');
    }

    final scannedPrefixes = <String>{};
    for (final ip in localIps) {
      final lastDot = ip.lastIndexOf('.');
      if (lastDot <= 0) continue;
      final prefix = ip.substring(0, lastDot);
      if (!scannedPrefixes.add(prefix)) continue;
      for (var i = 1; i <= 254; i++) {
        final candidate = '$prefix.$i';
        if (seenHosts.add(candidate)) {
          hosts.add(candidate);
        }
      }
      if (scannedPrefixes.length >= 2) break;
    }
    if (hosts.isEmpty) return const [];

    final found = <DiscoveredLanNode>[];
    final seenUrls = <String>{};

    const batchSize = 32;
    for (var offset = 0; offset < hosts.length; offset += batchSize) {
      final slice = hosts.sublist(
        offset,
        (offset + batchSize).clamp(0, hosts.length),
      );
      final probes = <Future<DiscoveredLanNode?>>[];
      for (final host in slice) {
        for (final port in ports) {
          if ((port == 5410 || port == 5420) &&
              host != '127.0.0.1' &&
              !localIps.contains(host) &&
              !extraPorts.contains(port)) {
            continue;
          }
          probes.add(_probeHostPort(host, port, filterKind: filterKind));
        }
      }
      final results = await Future.wait(probes);
      for (final node in results) {
        if (node != null && seenUrls.add(node.url)) {
          found.add(node);
        }
      }
    }
    return found;
  }

  static Future<DiscoveredLanNode?> _probeHostPort(
    String host,
    int port, {
    NodeLinkKind? filterKind,
  }) async {
    Socket? socket;
    try {
      socket = await Socket.connect(host, port, timeout: _connectTimeout);
    } catch (_) {
      return null;
    } finally {
      socket?.destroy();
    }

    final baseUrl = 'http://$host:$port';
    if (filterKind == NodeLinkKind.gopanel || port == 5470 || port == 15470) {
      return _verifyGoPanel(baseUrl, host);
    }
    if (filterKind == NodeLinkKind.hoxi ||
        port == 5417 ||
        port == 5410 ||
        port == 5420) {
      return _verifyHoxi(baseUrl, host);
    }
    return extraHostVerifier?.call(baseUrl, host, port, filterKind);
  }

  static Future<DiscoveredLanNode?> _verifyHoxi(
    String baseUrl,
    String host,
  ) async {
    final client = HttpClient()..connectionTimeout = _httpTimeout;
    try {
      final req = await client
          .getUrl(Uri.parse('$baseUrl/api/hoxi/bootstrap'))
          .timeout(_httpTimeout);
      final res = await req.close().timeout(_httpTimeout);
      final body = await res
          .transform(utf8.decoder)
          .join()
          .timeout(_httpTimeout);
      if (res.statusCode == 200 || res.statusCode == 401) {
        final decoded = jsonDecode(body);
        if (decoded is Map) {
          final isPairingChallenge =
              res.statusCode == 401 &&
              decoded['error']?.toString().trim() == 'invalid pairing token';
          final rootDir = decoded['rootDir']?.toString().trim() ?? '';
          final appVer = decoded['appVersion']?.toString().trim() ?? '';
          if (isPairingChallenge || (rootDir.isNotEmpty && appVer.isNotEmpty)) {
            final label = rootDir.isNotEmpty
                ? rootDir
                      .split(RegExp(r'[/\\]'))
                      .where((s) => s.isNotEmpty)
                      .last
                : host;
            return DiscoveredLanNode(
              url: baseUrl,
              title: 'Hoxi · $label',
              subtitle: isPairingChallenge
                  ? '$baseUrl · 局域网直连（需扫码或配对令牌）'
                  : '$baseUrl · 本机直连（免令牌）',
              kind: NodeLinkKind.hoxi,
              requiresToken: isPairingChallenge,
            );
          }
        }
      }
    } catch (_) {
      // Ignore non-Hoxi service
    } finally {
      client.close(force: true);
    }
    return null;
  }

  static Future<DiscoveredLanNode?> _verifyGoPanel(
    String baseUrl,
    String host,
  ) async {
    final client = HttpClient()..connectionTimeout = _httpTimeout;
    try {
      final req = await client
          .getUrl(Uri.parse('$baseUrl/api/mobile/health'))
          .timeout(_httpTimeout);
      final res = await req.close().timeout(_httpTimeout);
      if (res.statusCode != 200) return null;
      final body = await res
          .transform(utf8.decoder)
          .join()
          .timeout(_httpTimeout);
      final decoded = jsonDecode(body);
      if (decoded is Map && decoded['code'] == 0 && decoded['data'] is Map) {
        final data = decoded['data'] as Map;
        if (data['available'] == true) {
          final ver = (data['version'] ?? '').toString().trim();
          return DiscoveredLanNode(
            url: baseUrl,
            title: ver.isNotEmpty ? 'GoPanel $ver ($host)' : 'GoPanel ($host)',
            subtitle: '$baseUrl · 局域网直连',
            kind: NodeLinkKind.gopanel,
          );
        }
      }
    } catch (_) {
      // Ignore non-GoPanel service
    } finally {
      client.close(force: true);
    }
    return null;
  }
}
