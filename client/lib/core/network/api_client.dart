import 'package:dio/dio.dart';
import 'dart:async';
import 'dart:convert';

import '../storage/storage_service.dart';
import 'api_exception.dart';
import 'api_response.dart';
import 'interceptors/auth_interceptor.dart';

/// 全局统一的网络请求客户端
/// 遵循 PROMPT.md 规范：不让网络请求直接散落在页面或子 Widget
class ApiClient {
  // 单例模式，保证全局网络配置一致
  static final ApiClient _instance = ApiClient._internal();
  factory ApiClient() => _instance;

  late final Dio _dio;

  ApiClient._internal() {
    _dio = Dio(
      BaseOptions(
        // 默认的超时时间配置，依据 GoPanel 可能的连接慢情况
        connectTimeout: const Duration(seconds: 15),
        receiveTimeout: const Duration(seconds: 15),
        sendTimeout: const Duration(seconds: 15),
        responseType: ResponseType.json,
        contentType: 'application/json',
      ),
    );

    // 添加处理 URL 和 Token 的认证拦截器
    _dio.interceptors.add(AuthInterceptor());
  }

  /// 封装标准的 GET 请求
  Future<ApiResponse<T>> get<T>(
    String path, {
    Map<String, dynamic>? queryParameters,
    Options? options,
  }) async {
    try {
      final response = await _dio.get(
        path,
        queryParameters: queryParameters,
        options: options,
      );
      return _handleResponse<T>(response);
    } on DioException catch (e) {
      throw ApiException.fromDioException(e);
    } catch (e) {
      throw ApiException(msg: e.toString());
    }
  }

  /// 封装标准的 POST 请求
  Future<ApiResponse<T>> post<T>(
    String path, {
    dynamic data,
    Map<String, dynamic>? queryParameters,
    Options? options,
  }) async {
    try {
      final response = await _dio.post(
        path,
        data: data,
        queryParameters: queryParameters,
        options: options,
      );
      return _handleResponse<T>(response);
    } on DioException catch (e) {
      throw ApiException.fromDioException(e);
    } catch (e) {
      throw ApiException(msg: e.toString());
    }
  }

  /// 封装标准的 PUT 请求
  Future<ApiResponse<T>> put<T>(
    String path, {
    dynamic data,
    Map<String, dynamic>? queryParameters,
    Options? options,
  }) async {
    try {
      final response = await _dio.put(
        path,
        data: data,
        queryParameters: queryParameters,
        options: options,
      );
      return _handleResponse<T>(response);
    } on DioException catch (e) {
      throw ApiException.fromDioException(e);
    } catch (e) {
      throw ApiException(msg: e.toString());
    }
  }

  /// 封装标准的 PATCH 请求（Hoxi 会话更新等接口使用）
  Future<ApiResponse<T>> patch<T>(
    String path, {
    dynamic data,
    Map<String, dynamic>? queryParameters,
    Options? options,
  }) async {
    try {
      final response = await _dio.patch(
        path,
        data: data,
        queryParameters: queryParameters,
        options: options,
      );
      return _handleResponse<T>(response);
    } on DioException catch (e) {
      throw ApiException.fromDioException(e);
    } catch (e) {
      throw ApiException(msg: e.toString());
    }
  }

  /// 封装标准的 DELETE 请求
  Future<ApiResponse<T>> delete<T>(
    String path, {
    dynamic data,
    Map<String, dynamic>? queryParameters,
    Options? options,
  }) async {
    try {
      final response = await _dio.delete(
        path,
        data: data,
        queryParameters: queryParameters,
        options: options,
      );
      return _handleResponse<T>(response);
    } on DioException catch (e) {
      throw ApiException.fromDioException(e);
    } catch (e) {
      throw ApiException(msg: e.toString());
    }
  }

  /// GET 获取 SSE 的 data 行（text/event-stream）
  /// 仅用于后端返回非标准 JSON 的 SSE 日志流（如 /api/pipeline/logs）
  Future<List<String>> getSseDataLines(
    String path, {
    Map<String, dynamic>? queryParameters,
    Options? options,
    Duration timeout = const Duration(seconds: 20),
    Duration maxWait = const Duration(seconds: 3),
    int maxLines = 2000,
  }) async {
    final res = await _dio.get<ResponseBody>(
      path,
      queryParameters: queryParameters,
      options: (options ?? Options()).copyWith(
        responseType: ResponseType.stream,
      ),
    );

    final body = res.data;
    if (body == null) {
      throw ApiException(msg: 'SSE 响应为空');
    }

    final lines = <String>[];
    final completer = Completer<List<String>>();
    StreamSubscription<String>? sub;
    Timer? timer;

    Future<void> finish() async {
      if (!completer.isCompleted) {
        completer.complete(lines);
      }
      timer?.cancel();
      timer = null;
      await sub?.cancel();
      sub = null;
    }

    timer = Timer(maxWait, () {
      finish();
    });

    sub = body.stream
        .cast<List<int>>()
        .transform(utf8.decoder)
        .transform(const LineSplitter())
        .listen(
          (line) {
            if (!line.startsWith('data:')) return;
            final data = line.substring(5).trimLeft();
            if (data == 'EOF') {
              finish();
              return;
            }
            if (data.isEmpty) return;
            lines.add(data);
            if (lines.length >= maxLines) {
              finish();
            }
          },
          onError: (e) {
            if (!completer.isCompleted) {
              completer.completeError(ApiException(msg: e.toString()));
            }
            timer?.cancel();
          },
          onDone: () {
            finish();
          },
          cancelOnError: true,
        );

    return completer.future.timeout(
      timeout,
      onTimeout: () async {
        await finish();
        return lines;
      },
    );
  }

  Future<ResponseBody> openSseStream(
    String path, {
    Map<String, dynamic>? queryParameters,
    CancelToken? cancelToken,
  }) async {
    try {
      final response = await _dio.get<ResponseBody>(
        path,
        queryParameters: queryParameters,
        cancelToken: cancelToken,
        options: Options(
          responseType: ResponseType.stream,
          receiveTimeout: null,
          headers: const {'Accept': 'text/event-stream'},
        ),
      );
      final body = response.data;
      if (body == null) throw ApiException(msg: 'SSE 响应为空');
      return body;
    } on DioException catch (error) {
      throw ApiException.fromDioException(error);
    }
  }

  /// 处理 Dio 的 Response 对象，转换为项目的统一 ApiResponse
  ApiResponse<T> _handleResponse<T>(Response response) {
    final responseData = response.data;
    final allowRawJson =
        StorageService.isActiveHoxi ||
        response.requestOptions.extra['allowRawJson'] == true;

    // 1. GoPanel 标准 JSON 包装格式: {code: xxx, msg: xxx, data: xxx}
    if (responseData is Map<String, dynamic> &&
        responseData.containsKey('code')) {
      final apiResponse = ApiResponse<T>.fromJson(responseData);
      if (!apiResponse.isSuccess) {
        throw ApiException(code: apiResponse.code, msg: apiResponse.msg);
      }
      return apiResponse;
    }

    // 2. Hoxi 原生 JSON 格式（直接返回对象、数组或空 204）
    if (allowRawJson) {
      if (responseData is Map<String, dynamic>) {
        final errorMsg = responseData['error'];
        if (errorMsg is String &&
            errorMsg.isNotEmpty &&
            (response.statusCode ?? 200) >= 400) {
          throw ApiException(
            code: response.statusCode ?? 500,
            msg: errorMsg,
          );
        }
        return ApiResponse<T>(
          code: response.statusCode ?? 200,
          msg: 'ok',
          data: responseData as T?,
        );
      }
      if (responseData is Map) {
        final casted = responseData.cast<String, dynamic>();
        return ApiResponse<T>(
          code: response.statusCode ?? 200,
          msg: 'ok',
          data: casted as T?,
        );
      }
      if (responseData is List ||
          responseData is bool ||
          responseData == null ||
          (response.statusCode == 204)) {
        return ApiResponse<T>(
          code: response.statusCode ?? 200,
          msg: 'ok',
          data: responseData as T?,
        );
      }
    }

    // 兼容未包含 'code' 字段但在 GoPanel 模式下仍被当作 Map 解析的场景
    if (responseData is Map<String, dynamic>) {
      final apiResponse = ApiResponse<T>.fromJson(responseData);
      if (!apiResponse.isSuccess) {
        throw ApiException(code: apiResponse.code, msg: apiResponse.msg);
      }
      return apiResponse;
    }

    // 如果接口直接返回非包装数据或列表
    throw ApiException(msg: '未识别的服务器响应格式');
  }
}
