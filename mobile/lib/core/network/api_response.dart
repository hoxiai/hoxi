/// 统一网络请求响应模型
/// 遵循 PROMPT.md 规范：请求模型、响应模型、错误模型要统一，不对每个接口各自造结构
class ApiResponse<T> {
  final int code;
  final String msg;
  final T? data;
  final List<String> setCookies;

  ApiResponse({
    required this.code,
    required this.msg,
    this.data,
    this.setCookies = const [],
  });

  /// 根据远程面板的标准 JSON 构建响应模型
  factory ApiResponse.fromJson(
    Map<String, dynamic> json, {
    List<String> setCookies = const [],
  }) {
    return ApiResponse<T>(
      code: json['code'] as int? ?? -1,
      msg: (json['msg'] ?? json['msg']) as String? ?? '',
      data: json['data'] as T?,
      setCookies: setCookies,
    );
  }

  /// 判断当前响应是否代表成功业务结果（兼容 0 与 20000 成功状态码）
  bool get isSuccess => code == 0 || code == 20000;
}
