import 'package:dio/dio.dart';

/// 统一异常错误模型
/// 遵循 PROMPT.md 规范：不对外抛出零散的 DioError，所有底层网络或业务异常在此收口
class ApiException implements Exception {
  final int code;
  final String msg;

  ApiException({this.code = -1, required this.msg});

  /// 将 Dio 抛出的原生网络异常转换为项目中统一的 ApiException
  factory ApiException.fromDioException(DioException error) {
    String msg = '';
    int code = -1;

    switch (error.type) {
      case DioExceptionType.connectionTimeout:
        msg = '连接服务器超时';
        break;
      case DioExceptionType.sendTimeout:
        msg = '发送请求超时';
        break;
      case DioExceptionType.receiveTimeout:
        msg = '接收响应超时';
        break;
      case DioExceptionType.badResponse:
        code = error.response?.statusCode ?? -1;
        msg = _handleResponseError(code, error.response?.data);
        break;
      case DioExceptionType.cancel:
        msg = '请求已取消';
        break;
      case DioExceptionType.connectionError:
        msg = '网络连接异常，请检查本地网络或服务器状态';
        break;
      case DioExceptionType.unknown:
        msg = '未知网络错误: ${error.message}';
        break;
      default:
        msg = '发生预期外的网络错误';
    }

    return ApiException(code: code, msg: msg);
  }

  /// 处理服务端返回的具体错误状态码和信息
  static String _handleResponseError(int statusCode, dynamic data) {
    // 安全入口中间件使用 message，业务接口使用 msg。
    if (data is Map &&
        (data.containsKey('msg') || data.containsKey('message'))) {
      final serverMsg = data['msg'] ?? data['message'];
      if (serverMsg != null && serverMsg.toString().isNotEmpty) {
        return serverMsg.toString();
      }
    }

    switch (statusCode) {
      case 41:
        return '请求参数错误';
      case 400:
        return '请求参数错误';
      case 401:
        return '身份验证已过期或无效';
      case 403:
        return '拒绝访问该资源';
      case 404:
        return '请求的资源不存在';
      case 500:
        return '服务器内部错误';
      case 502:
        return '网关错误';
      case 503:
        return '服务不可用';
      default:
        return '请求失败 (状态码: $statusCode)';
    }
  }

  /// 界面各处直接把异常插进提示（'加载失败：$error'），这里只给用户读得懂的那句话；
  /// 此前返回 'ApiException(code: 500, msg: …)' 的调试格式，原样出现在手机界面上。
  @override
  String toString() => msg.isNotEmpty ? msg : '请求失败 (code: $code)';
}
