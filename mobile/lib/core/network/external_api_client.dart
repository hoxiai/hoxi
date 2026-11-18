import 'package:dio/dio.dart';

import 'api_exception.dart';

/// 第三方外部服务的唯一网络入口（模型网关、Hoxi 网关 公开目录等）。
///
/// 与 [SiteApiClient] 的区别：站点是 hoxi.ai 自家只读接口，固定 baseUrl 与
/// `Accept-Language`；外部服务是用户可配置的地址（`hoxi.ai`、
/// 用户自己的网关），必须由调用方显式传完整 URL，绝不能落到受控节点的
/// 凭据上。
class ExternalApiClient {
  ExternalApiClient({Dio? dio})
    : _dio =
          dio ??
          Dio(
            BaseOptions(
              connectTimeout: const Duration(seconds: 10),
              receiveTimeout: const Duration(seconds: 90),
              responseType: ResponseType.json,
            ),
          );

  final Dio _dio;

  Future<dynamic> getJson(
    String url, {
    Map<String, dynamic>? query,
    Map<String, String>? headers,
    CancelToken? cancelToken,
  }) async {
    try {
      final response = await _dio.get<dynamic>(
        url,
        queryParameters: query,
        cancelToken: cancelToken,
        options: Options(headers: headers),
      );
      return response.data;
    } on DioException catch (error) {
      throw ApiException.fromDioException(error);
    } catch (error) {
      throw ApiException(msg: error.toString());
    }
  }

  /// 拉取纯文本响应（如 GitHub raw `README.md` 或 `docker-compose.yml` 文档）。
  Future<String> getText(
    String url, {
    Map<String, dynamic>? query,
    Map<String, String>? headers,
    CancelToken? cancelToken,
    Duration? receiveTimeout,
  }) async {
    try {
      final response = await _dio.get<String>(
        url,
        queryParameters: query,
        cancelToken: cancelToken,
        options: Options(
          headers: headers,
          responseType: ResponseType.plain,
          receiveTimeout: receiveTimeout,
        ),
      );
      return response.data ?? '';
    } on DioException catch (error) {
      throw ApiException.fromDioException(error);
    } catch (error) {
      throw ApiException(msg: error.toString());
    }
  }

  /// Public binary assets, without node or account credentials.
  Future<List<int>> getBytes(
    String url, {
    CancelToken? cancelToken,
    Duration? receiveTimeout,
  }) async {
    try {
      final response = await _dio.get<List<int>>(
        url,
        cancelToken: cancelToken,
        options: Options(
          responseType: ResponseType.bytes,
          receiveTimeout: receiveTimeout,
        ),
      );
      return response.data ?? const [];
    } on DioException catch (error) {
      throw ApiException.fromDioException(error);
    } catch (error) {
      throw ApiException(msg: error.toString());
    }
  }

  /// 取二进制响应（语音合成 PCM / MP3 等）。JSON 错误体仍然抛 [ApiException]。
  Future<List<int>> postBytes(
    String url, {
    Object? body,
    Map<String, String>? headers,
    CancelToken? cancelToken,
  }) async {
    try {
      final response = await _dio.post<List<int>>(
        url,
        data: body,
        cancelToken: cancelToken,
        options: Options(
          responseType: ResponseType.bytes,
          headers: {
            'Content-Type': 'application/json',
            ...?headers,
          },
        ),
      );
      final data = response.data;
      if (data == null || data.isEmpty) {
        throw ApiException(msg: '语音合成返回空音频');
      }
      return data;
    } on DioException catch (error) {
      throw ApiException.fromDioException(error);
    } catch (error) {
      if (error is ApiException) rethrow;
      throw ApiException(msg: error.toString());
    }
  }

  /// 打开 SSE 流。解析交给调用方，这里只负责鉴权头、超时与错误收口。
  Future<ResponseBody> openSseStream(
    String url, {
    Object? body,
    Map<String, String>? headers,
    CancelToken? cancelToken,
  }) async {
    try {
      final response = await _dio.post<ResponseBody>(
        url,
        data: body,
        cancelToken: cancelToken,
        options: Options(
          responseType: ResponseType.stream,
          headers: {
            'Accept': 'text/event-stream',
            'Content-Type': 'application/json',
            ...?headers,
          },
        ),
      );
      final stream = response.data;
      if (stream == null) throw ApiException(msg: '未收到 SSE 响应流');
      return stream;
    } on DioException catch (error) {
      throw ApiException.fromDioException(error);
    }
  }
}
