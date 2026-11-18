import 'dart:async';

import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../core/l10n/l10n.dart';

/// 统一收起软键盘并清理焦点：
/// 1. 释放全局当前 `primaryFocus`（即使焦点位于嵌套或兄弟 `FocusScope`）；
/// 2. 清理当前上下文所属 `FocusScope` 的焦点记忆，防止弹窗/路由返回后键盘自动回弹；
/// 3. 显式调用原生 `TextInput.hide` 兜底收起系统输入法（解决 iOS/Android/HarmonyOS 焦点已失但软键盘滞留的问题）。
void dismissChatKeyboard([BuildContext? context]) {
  FocusManager.instance.primaryFocus?.unfocus();
  if (context != null && context.mounted) {
    FocusScope.of(context).unfocus();
  }
  unawaited(
    SystemChannels.textInput
        .invokeMethod<void>('TextInput.hide')
        .catchError((_) {}),
  );
}

/// 判断当前物理视图是否弹出了系统软键盘。
///
/// 兼容外层 `Scaffold`（如 `MainScaffoldScreen` 底部导航壳）通过
/// `MediaQuery.removeViewInsets(removeBottom: true)` 吞掉子级 `MediaQuery.viewInsets.bottom` 的场景：
/// 同时检查 `MediaQuery.viewInsets` 与底层 `FlutterView.viewInsets`。
bool isChatKeyboardOpen(BuildContext context) {
  final mediaBottom = MediaQuery.maybeViewInsetsOf(context)?.bottom ?? 0.0;
  if (mediaBottom > 0) return true;
  final viewBottom = View.maybeOf(context)?.viewInsets.bottom ?? 0.0;
  return viewBottom > 0;
}

/// 当前全局焦点是否位于可编辑输入框（排除消息气泡内的只读 `SelectableText`）。
bool hasActiveEditableTextFocus() {
  final primary = FocusManager.instance.primaryFocus;
  if (primary == null) return false;
  final ctx = primary.context;
  if (ctx == null) return primary.hasPrimaryFocus;
  final widget = ctx.widget;
  if (widget is EditableText) return !widget.readOnly;
  final ancestorEditable = ctx.findAncestorWidgetOfExactType<EditableText>();
  if (ancestorEditable != null) return !ancestorEditable.readOnly;
  return false;
}

/// 监听底层窗口 `didChangeMetrics` 的软键盘可见性构建器。
///
/// 即使组件树上方存在消费了 `viewInsets` 的嵌套 `Scaffold`，也能在键盘升降时实时触发重绘。
class ChatKeyboardVisibilityBuilder extends StatefulWidget {
  final bool forceVisible;
  final Widget Function(BuildContext context, bool isKeyboardVisible) builder;

  const ChatKeyboardVisibilityBuilder({
    super.key,
    this.forceVisible = false,
    required this.builder,
  });

  @override
  State<ChatKeyboardVisibilityBuilder> createState() =>
      _ChatKeyboardVisibilityBuilderState();
}

class _ChatKeyboardVisibilityBuilderState
    extends State<ChatKeyboardVisibilityBuilder>
    with WidgetsBindingObserver {
  double _lastViewInsetBottom = 0.0;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _lastViewInsetBottom = View.maybeOf(context)?.viewInsets.bottom ?? 0.0;
  }

  @override
  void didChangeMetrics() {
    if (!mounted) return;
    final nextBottom = View.maybeOf(context)?.viewInsets.bottom ?? 0.0;
    if (nextBottom != _lastViewInsetBottom) {
      _lastViewInsetBottom = nextBottom;
      setState(() {});
    }
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final visible = widget.forceVisible || isChatKeyboardOpen(context);
    return widget.builder(context, visible);
  }
}

/// 输入栏上方的「收起键盘」快捷按钮：仅在软键盘弹出时显示，点击保留草稿并收起键盘。
class ChatHideKeyboardButton extends StatelessWidget {
  final Key buttonKey;
  final bool forceVisible;
  final bool compact;

  const ChatHideKeyboardButton({
    super.key,
    this.buttonKey = const ValueKey('composer_hide_keyboard_button'),
    this.forceVisible = false,
    this.compact = false,
  });

  @override
  Widget build(BuildContext context) {
    return ChatKeyboardVisibilityBuilder(
      forceVisible: forceVisible,
      builder: (context, isKeyboardVisible) {
        if (!isKeyboardVisible) return const SizedBox.shrink();
        return Align(
          alignment: Alignment.centerRight,
          child: TextFieldTapRegion(
            child: TextButton.icon(
              key: buttonKey,
              style: compact
                  ? TextButton.styleFrom(
                      visualDensity: VisualDensity.compact,
                      tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                      padding: const EdgeInsets.symmetric(
                        horizontal: 10,
                        vertical: 4,
                      ),
                    )
                  : null,
              onPressed: () => dismissChatKeyboard(context),
              icon: const Icon(Icons.keyboard_hide_rounded, size: 18),
              label: Text(context.l10n.chatHideKeyboard),
            ),
          ),
        );
      },
    );
  }
}

/// 聊天消息流 / 空白区手势收起键盘容器（包裹在输入栏上方的消息区与顶栏外）：
///
/// 解决 Flutter 聊天页中 `SelectableText` / `MarkdownBody(selectable: true)` 与 `TextField`
/// 共用 `TextFieldTapRegion(groupId: EditableText)` 导致点击消息气泡不触发 `onTapOutside`，
/// 以及内部 `TapGestureRecognizer` 抢赢手势竞技场导致外层 `GestureDetector.onTap` 失效的问题：
/// - 软键盘弹出或输入框聚焦时，轻点消息气泡或空白处立即收起键盘（保留长按选词复制与消息内按钮点击）；
/// - 在消息区任意位置上下滑动（即使消息不足一屏无法滚动）立即收起键盘；
/// - 软键盘未弹出且输入框未聚焦时零拦截，不影响常规文本选择与滚动。
class ChatKeyboardDismissArea extends StatefulWidget {
  final Widget child;

  const ChatKeyboardDismissArea({
    super.key,
    required this.child,
  });

  @override
  State<ChatKeyboardDismissArea> createState() =>
      _ChatKeyboardDismissAreaState();
}

class _ChatKeyboardDismissAreaState extends State<ChatKeyboardDismissArea> {
  static const double _dragDismissThreshold = 8.0;

  int? _activePointer;
  Offset? _downPosition;
  Duration? _downTimeStamp;
  bool _armedOnDown = false;
  bool _dismissedOnMove = false;

  void _resetPointer() {
    _activePointer = null;
    _downPosition = null;
    _downTimeStamp = null;
    _armedOnDown = false;
    _dismissedOnMove = false;
  }

  void _handlePointerDown(PointerDownEvent event) {
    _activePointer = event.pointer;
    _downPosition = event.position;
    _downTimeStamp = event.timeStamp;
    _dismissedOnMove = false;
    _armedOnDown = isChatKeyboardOpen(context) || hasActiveEditableTextFocus();
  }

  void _handlePointerMove(PointerMoveEvent event) {
    if (!_armedOnDown ||
        _dismissedOnMove ||
        event.pointer != _activePointer ||
        _downPosition == null) {
      return;
    }
    if ((event.position - _downPosition!).distance > _dragDismissThreshold) {
      _dismissedOnMove = true;
      dismissChatKeyboard(context);
    }
  }

  void _handlePointerUp(PointerUpEvent event) {
    if (!_armedOnDown ||
        event.pointer != _activePointer ||
        _downPosition == null ||
        _downTimeStamp == null) {
      if (event.pointer == _activePointer) _resetPointer();
      return;
    }
    final distance = (event.position - _downPosition!).distance;
    final elapsed = event.timeStamp - _downTimeStamp!;
    final shouldDismiss =
        !_dismissedOnMove &&
        distance <= kTouchSlop &&
        elapsed < kLongPressTimeout;
    _resetPointer();
    if (shouldDismiss) {
      dismissChatKeyboard(context);
    }
  }

  void _handlePointerCancel(PointerCancelEvent event) {
    if (event.pointer == _activePointer) {
      _resetPointer();
    }
  }

  @override
  Widget build(BuildContext context) {
    return Listener(
      behavior: HitTestBehavior.translucent,
      onPointerDown: _handlePointerDown,
      onPointerMove: _handlePointerMove,
      onPointerUp: _handlePointerUp,
      onPointerCancel: _handlePointerCancel,
      child: widget.child,
    );
  }
}
