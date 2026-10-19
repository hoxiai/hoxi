import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:hoxi/core/network/api_exception.dart';

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
}
