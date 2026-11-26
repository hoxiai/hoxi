import 'package:dio/dio.dart';

import '../../storage/active_node_context.dart';
import '../e2ee/hoxi_e2ee_cipher.dart';

const String _extraCipherKey = '_hoxiE2eeCipher';
const String _extraReqIdKey = '_hoxiE2eeReqId';
const String _extraCleanPathKey = '_hoxiE2eeCleanPath';

/// Transparent end-to-end encryption interceptor for paired Hoxi desktop nodes.
///
/// Installed after [AuthInterceptor] so `baseUrl` and `nodeContext` are already
/// resolved. Requests to non-E2EE endpoints or unauthenticated LAN preflight
/// probes pass through untouched.
class E2eeInterceptor extends Interceptor {
  const E2eeInterceptor();

  String? _resolveKeyHex(RequestOptions options) {
    final explicit = options.extra['explicitE2eeKey'];
    if (explicit is String && isValidHoxiE2eeKeyHex(explicit)) {
      return explicit.trim();
    }
    if (options.extra['skipActiveCredentials'] == true) {
      return null;
    }
    final node = options.extra['nodeContext'] as ActiveNodeContext?;
    if (node == null || !node.isHoxi) return null;
    final key = node.e2eeKey;
    return isValidHoxiE2eeKeyHex(key) ? key!.trim() : null;
  }

  @override
  void onRequest(RequestOptions options, RequestInterceptorHandler handler) {
    final cipher = HoxiE2eeCipher.tryFromHex(_resolveKeyHex(options));
    if (cipher == null) {
      super.onRequest(options, handler);
      return;
    }
    try {
      final prepared = cipher.prepareRequest(
        method: options.method,
        rawPathOrUrl: options.uri.toString(),
        data: options.data,
        contentType: options.contentType,
      );
      options.headers[hoxiE2eeHeader] = prepared.headerValue;
      options.extra[_extraCipherKey] = cipher;
      options.extra[_extraReqIdKey] = prepared.reqId;
      options.extra[_extraCleanPathKey] = prepared.cleanPath;
      if (prepared.encryptedBody != null) {
        options.data = prepared.encryptedBody;
        options.contentType = 'application/json';
      }
      super.onRequest(options, handler);
    } catch (e) {
      handler.reject(
        DioException(
          requestOptions: options,
          error: e,
          message: 'E2EE 请求封装失败: $e',
        ),
      );
    }
  }

  @override
  void onResponse(Response response, ResponseInterceptorHandler handler) {
    final cipher =
        response.requestOptions.extra[_extraCipherKey] as HoxiE2eeCipher?;
    final reqId = response.requestOptions.extra[_extraReqIdKey] as String?;
    final cleanPath =
        response.requestOptions.extra[_extraCleanPathKey] as String?;
    if (cipher == null || reqId == null || cleanPath == null) {
      super.onResponse(response, handler);
      return;
    }

    final mode = (response.headers.value(hoxiE2eeHeader) ?? '').trim();
    try {
      if (response.requestOptions.responseType == ResponseType.stream &&
          response.data is ResponseBody &&
          mode == hoxiE2eeSseVersion) {
        final rawBody = response.data as ResponseBody;
        final decryptedStream = cipher.transformSseStream(
          source: rawBody.stream,
          reqId: reqId,
          cleanPath: cleanPath,
        );
        response.data = ResponseBody(
          decryptedStream,
          rawBody.statusCode,
          statusMessage: rawBody.statusMessage,
          isRedirect: rawBody.isRedirect,
          headers: rawBody.headers,
        );
        super.onResponse(response, handler);
        return;
      }

      final isEncryptedEnvelope =
          mode == hoxiE2eeVersion ||
          (response.data is Map &&
              (response.data as Map)['_hoxi_e2ee'] == 1);
      if (isEncryptedEnvelope) {
        response.data = cipher.decodeResponseData(
          reqId: reqId,
          method: response.requestOptions.method,
          cleanPath: cleanPath,
          statusCode: response.statusCode ?? 200,
          rawData: response.data,
          responseType: response.requestOptions.responseType,
        );
      }
      super.onResponse(response, handler);
    } catch (e) {
      handler.reject(
        DioException(
          requestOptions: response.requestOptions,
          response: response,
          error: e,
          message: 'E2EE 响应校验失败: $e',
        ),
      );
    }
  }

  @override
  void onError(DioException err, ErrorInterceptorHandler handler) {
    final resp = err.response;
    final cipher =
        err.requestOptions.extra[_extraCipherKey] as HoxiE2eeCipher?;
    final reqId = err.requestOptions.extra[_extraReqIdKey] as String?;
    final cleanPath =
        err.requestOptions.extra[_extraCleanPathKey] as String?;
    if (resp != null && cipher != null && reqId != null && cleanPath != null) {
      final mode = (resp.headers.value(hoxiE2eeHeader) ?? '').trim();
      final isEncryptedEnvelope =
          mode == hoxiE2eeVersion ||
          (resp.data is Map && (resp.data as Map)['_hoxi_e2ee'] == 1);
      if (isEncryptedEnvelope) {
        try {
          resp.data = cipher.decodeResponseData(
            reqId: reqId,
            method: err.requestOptions.method,
            cleanPath: cleanPath,
            statusCode: resp.statusCode ?? 500,
            rawData: resp.data,
            responseType: err.requestOptions.responseType,
          );
        } catch (_) {
          // Leave original error response intact if unauthenticated 401/502 came from proxy.
        }
      } else if (_looksLikeStrippedE2eeHeaderError(err, resp)) {
        resp.data = const <String, dynamic>{
          'error': '中继服务器未透传端到端加密请求头 (X-Hoxi-E2EE)，请升级中继服务后重试',
        };
      }
    }
    super.onError(err, handler);
  }

  static bool _looksLikeStrippedE2eeHeaderError(
    DioException err,
    Response<dynamic> resp,
  ) {
    if ((resp.statusCode ?? 0) != 400) return false;
    final sentBody = err.requestOptions.data;
    if (sentBody is! Map || sentBody['_hoxi_e2ee'] != 1) return false;
    final raw = resp.data;
    final msg = (raw is Map ? (raw['error'] ?? raw['msg'] ?? raw['message']) : raw)
        ?.toString()
        .toLowerCase() ??
        '';
    return msg.contains('x-hoxi-e2ee') ||
        msg.contains('prompt text is required') ||
        msg.contains('attachment data') ||
        msg.contains('empty file') ||
        msg.contains('dataurl');
  }
}
