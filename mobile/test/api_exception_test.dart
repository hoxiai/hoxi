import 'dart:convert';
import 'dart:io';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:hoxi/core/network/api_client.dart';
import 'package:hoxi/core/network/api_exception.dart';
import 'package:hoxi/core/storage/storage_service.dart';
import 'package:hoxi/features/auth/data/auth_repository.dart';
import 'package:hoxi/features/auth/models/node_link_kind.dart';
import 'package:hoxi/features/auth/presentation/controllers/auth_controller.dart';
import 'package:hoxi/features/auth/presentation/qr_payload_connector.dart';
import 'package:hoxi/l10n/gen/app_localizations_zh.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  test('界面拼接异常时只显示提示语，不露出调试格式', () {
    final business = ApiException(code: 40, msg: '会话正在交付中');
    expect('指令发送失败：$business', '指令发送失败：会话正在交付中');

    final server = ApiException.fromDioException(
      DioException(
        requestOptions: RequestOptions(path: '/api/code/sessions'),
        type: DioExceptionType.badResponse,
        response: Response(
          requestOptions: RequestOptions(path: '/api/code/sessions'),
          statusCode: 500,
          data: {'msg': '数据库不可用'},
        ),
      ),
    );
    expect(server.toString(), '数据库不可用');
    expect(server.code, 500);
    expect(ApiException(code: 7, msg: '').toString(), '请求失败 (code: 7)');
  });

  test(
    'ApiClient.get 保留 _handleResponse 抛出的 ApiException 状态码，确保 Hoxi 局域网 401 预探测通过',
    () async {
      SharedPreferences.setMockInitialValues({});
      FlutterSecureStorage.setMockInitialValues({});
      await StorageService.init();

      final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      addTearDown(() => server.close(force: true));

      final seenAuthHeaders = <String?>[];
      server.listen((HttpRequest req) async {
        final auth = req.headers.value('authorization');
        seenAuthHeaders.add(auth);
        req.response.headers.contentType = ContentType.json;
        if (auth != 'Bearer valid-lan-token') {
          req.response.statusCode = HttpStatus.unauthorized;
          req.response.write(jsonEncode({'error': 'invalid pairing token'}));
        } else {
          req.response.statusCode = HttpStatus.ok;
          req.response.write(
            jsonEncode({
              'appVersion': '0.6.12',
              'rootDir': '/Users/dev/code/hoxi',
            }),
          );
        }
        await req.response.close();
      });

      // 将局域网 IP (192.168.1.71) 转发到本地测试 HttpServer，完整验证真实 ApiClient.get + _handleResponse 链路
      final bootstrap = await HttpOverrides.runWithHttpOverrides(
        () => AuthRepository(ApiClient()).probeHoxiNode(
          serverUrl: 'http://192.168.1.71:${server.port}',
          token: 'valid-lan-token',
        ),
        _LoopbackProxyHttpOverrides(server.port),
      );

      expect(bootstrap, isNotNull);
      expect(bootstrap!['appVersion'], '0.6.12');
      expect(seenAuthHeaders, [null, 'Bearer valid-lan-token']);

      // 进一步验证完整扫码流程 connectWithQrPayload -> connectWithQrToken -> _pairHoxiWithQr
      seenAuthHeaders.clear();
      final container = ProviderContainer();
      addTearDown(container.dispose);
      final qrPayload = jsonEncode({
        'url': 'http://192.168.1.71:${server.port}',
        'token': 'valid-lan-token',
        'type': 'hoxi',
        'entrance': '',
      });
      final error = await HttpOverrides.runWithHttpOverrides(
        () => connectWithQrPayload(
          controller: container.read(authControllerProvider.notifier),
          readState: () => container.read(authControllerProvider),
          l10n: AppLocalizationsZh(),
          rawPayload: qrPayload,
          expectedKind: NodeLinkKind.hoxi,
          registerPush: false,
        ),
        _LoopbackProxyHttpOverrides(server.port),
      );

      expect(error, isNull);
      expect(StorageService.isActiveHoxi, isTrue);
      expect(
        StorageService.activeServerUrl,
        'http://192.168.1.71:${server.port}',
      );
      expect(StorageService.activeServerToken, 'valid-lan-token');
    },
  );
}

class _LoopbackProxyHttpOverrides extends HttpOverrides {
  _LoopbackProxyHttpOverrides(this.port);

  final int port;

  @override
  HttpClient createHttpClient(SecurityContext? context) {
    final client = super.createHttpClient(context);
    client.findProxy = (_) => 'PROXY 127.0.0.1:$port';
    return client;
  }
}
