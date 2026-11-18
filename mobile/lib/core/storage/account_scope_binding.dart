/// 账号作用域与代次解耦绑定口。
///
/// 供底层节点存储与认证模块在不依赖外部账号模块的前提下感知可选的账号隔离作用域；
/// 开源薄壳未绑定外部账号会话时默认返回空作用域。
abstract final class AccountScopeBinding {
  static String? Function() _scopeReader = _defaultScope;
  static int Function() _epochReader = _defaultEpoch;

  static String? _defaultScope() => null;
  static int _defaultEpoch() => 0;

  static String? get accountScope => _scopeReader();
  static int get identityEpoch => _epochReader();

  static void bind({
    required String? Function() scopeReader,
    required int Function() epochReader,
  }) {
    _scopeReader = scopeReader;
    _epochReader = epochReader;
  }
}
