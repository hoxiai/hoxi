import 'dart:async';

// ignore: depend_on_referenced_packages
import 'package:clock/clock.dart';
import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/scheduler.dart';
import 'package:go_router/go_router.dart';

/// 全局页面跳转防连点（前沿节流 Leading Throttle + 路由生命周期守卫 + 根级手势门禁）。
///
/// 解决全站快速点击/多次点击导致连续 2 次进入同一页面的四类根因：
/// 1. `GoRouter` (`context.push`) 在当帧仅更新 `RouteMatchList` 并 `notifyListeners()`，
///    新页面与 `ModalBarrier` 需下一帧才挂载，帧间窗口内连点会重复进栈；
/// 2. 命令式 `Navigator.push` 在同帧或转场动画初期被连续触发，且下层页面的 `context.mounted`
///    仍为 `true`；
/// 3. 跳转前带异步操作（如切节点、拉取会话 `await ...`），等待期间无路由遮罩，连点会并发执行两次 push；
/// 4. 底部弹窗 `Navigator.pop` 后紧跟 `push`，二次点击可能误 pop 新页面或重复 push。
class AppNavigationGuard {
  AppNavigationGuard._();

  /// 全局导航观察器：挂载到 `GoRouter.observers`（或 `MaterialApp.navigatorObservers`）。
  static final AppNavigationObserver observer = AppNavigationObserver._();

  /// 前沿节流（Leading Throttle）时间窗口：首次点击 0 延迟立即响应，窗口期内阻断重复页面进栈与新触控。
  static Duration throttleWindow = const Duration(milliseconds: 500);

  static bool _pendingFramePush = false;
  static bool _frameCallbackScheduled = false;
  static DateTime? _lastPushAt;
  static String? _lastTargetLocation;
  static DateTime? _lastLocationPushAt;
  static PageRoute<dynamic>? _lastPushedPageRoute;
  static DateTime? _lastObservedPagePushAt;

  static GoRouter? _boundRouter;

  /// 当前绑定的根路由上下文（未绑定时返回 null）。
  static BuildContext? get rootNavigatorContext =>
      _boundRouter?.routerDelegate.navigatorKey.currentContext;

  /// 当前绑定的根路由 OverlayState（未绑定时返回 null）。
  static OverlayState? get rootNavigatorOverlay =>
      _boundRouter?.routerDelegate.navigatorKey.currentState?.overlay;
  static VoidCallback? _routerListener;
  static int _lastMatchCount = 0;
  static bool _pruningDuplicateMatch = false;

  static final Set<Route<dynamic>> _routesWithPushInFlight =
      <Route<dynamic>>{};
  static final Set<Route<dynamic>> _poppedRoutesThatPushed =
      <Route<dynamic>>{};
  static final Set<PageRoute<dynamic>> _transitioningPageRoutes =
      <PageRoute<dynamic>>{};
  static final Map<PageRoute<dynamic>, AnimationStatusListener>
  _transitionListeners = <PageRoute<dynamic>, AnimationStatusListener>{};
  static final Set<Object> _activeAsyncTokens = <Object>{};
  static final Set<int> _activePointers = <int>{};

  /// 绑定 `GoRouter` 实例：自动监听声明式路由栈变化，拦截同帧或节流窗口内的重复 `context.push`。
  static GoRouter bindRouter(GoRouter router) {
    if (identical(_boundRouter, router)) {
      return router;
    }
    if (_boundRouter != null && _routerListener != null) {
      try {
        _boundRouter!.routerDelegate.removeListener(_routerListener!);
      } catch (_) {}
    }
    _boundRouter = router;
    try {
      _lastMatchCount =
          router.routerDelegate.currentConfiguration.matches.length;
    } catch (_) {
      _lastMatchCount = 0;
    }
    void onRouterChanged() {
      if (_pruningDuplicateMatch) return;
      final config = router.routerDelegate.currentConfiguration;
      final matches = config.matches;
      final prevCount = _lastMatchCount;
      _lastMatchCount = matches.length;

      if (matches.length > prevCount && matches.length >= 2) {
        final top = matches.last;
        final prev = matches[matches.length - 2];
        final now = clock.now();
        final withinThrottle =
            _pendingFramePush ||
            (_lastPushAt != null &&
                now.difference(_lastPushAt!) < throttleWindow);

        if (top is ImperativeRouteMatch &&
            prev is ImperativeRouteMatch &&
            top.matches.uri.toString() == prev.matches.uri.toString() &&
            withinThrottle) {
          _pruningDuplicateMatch = true;
          final pruned = config.remove(top);
          _lastMatchCount = pruned.matches.length;
          scheduleMicrotask(() {
            try {
              if (identical(_boundRouter, router)) {
                router.routerDelegate.setNewRoutePath(pruned);
              }
            } catch (_) {
            } finally {
              _pruningDuplicateMatch = false;
            }
          });
          return;
        }
        _markFramePushStarted();
      }
    }

    _routerListener = onRouterChanged;
    router.routerDelegate.addListener(onRouterChanged);
    return router;
  }

  /// 当前是否处于页面进栈节流/转场保护期（供 `NavigationPointerGate` 同步拦截新触控）。
  static bool get isPointerGateLocked {
    if (_pendingFramePush || _activeAsyncTokens.isNotEmpty) {
      return true;
    }
    final lastPush = _lastPushAt;
    if (lastPush != null &&
        clock.now().difference(lastPush) < throttleWindow) {
      return true;
    }
    for (final route in _transitioningPageRoutes) {
      if (!route.isActive) continue;
      final anim = route.animation;
      if (anim != null &&
          anim.status == AnimationStatus.forward &&
          anim.value < 0.90) {
        return true;
      }
    }
    return false;
  }

  /// 重置内部状态（供单元测试使用）。
  @visibleForTesting
  static void resetForTesting() {
    _pendingFramePush = false;
    _frameCallbackScheduled = false;
    _lastPushAt = null;
    _lastTargetLocation = null;
    _lastLocationPushAt = null;
    _lastPushedPageRoute = null;
    _lastObservedPagePushAt = null;
    _routesWithPushInFlight.clear();
    _poppedRoutesThatPushed.clear();
    for (final entry in _transitionListeners.entries) {
      entry.key.animation?.removeStatusListener(entry.value);
    }
    _transitionListeners.clear();
    _transitioningPageRoutes.clear();
    _activeAsyncTokens.clear();
    _activePointers.clear();
    if (_boundRouter != null && _routerListener != null) {
      try {
        _boundRouter!.routerDelegate.removeListener(_routerListener!);
      } catch (_) {}
    }
    _boundRouter = null;
    _routerListener = null;
    _lastMatchCount = 0;
    _pruningDuplicateMatch = false;
  }

  static void _markFramePushStarted() {
    // 路由进栈已触发，立即释放跳转前的异步准备期锁，避免 await push(...) 等待目标页 pop 期间锁死全屏触控
    _activeAsyncTokens.clear();
    _pendingFramePush = true;
    _lastPushAt = clock.now();
    _cancelActivePointers();
    if (!_frameCallbackScheduled) {
      _frameCallbackScheduled = true;
      try {
        SchedulerBinding.instance.addPostFrameCallback((_) {
          _frameCallbackScheduled = false;
          _pendingFramePush = false;
        });
        SchedulerBinding.instance.ensureVisualUpdate();
      } catch (_) {
        _frameCallbackScheduled = false;
        _pendingFramePush = false;
      }
    }
  }

  static void _cancelActivePointers() {
    if (_activePointers.isEmpty) return;
    final pointers = _activePointers.toList(growable: false);
    _activePointers.clear();
    for (final pointer in pointers) {
      try {
        GestureBinding.instance.cancelPointer(pointer);
      } catch (_) {}
    }
  }

  static bool _canInitiatePush(
    BuildContext context, {
    String? targetLocation,
  }) {
    if (!context.mounted) return false;
    if (_pendingFramePush) return false;

    final now = clock.now();
    if (_lastPushAt != null && now.difference(_lastPushAt!) < throttleWindow) {
      return false;
    }

    final modalRoute = ModalRoute.of(context);
    if (modalRoute != null) {
      if (_routesWithPushInFlight.contains(modalRoute)) {
        return false;
      }
      if (!modalRoute.isCurrent) {
        // 允许刚刚通过 popIfCurrent 关闭的弹窗/BottomSheet 执行唯一一次后续 push
        if (modalRoute is PopupRoute &&
            !_poppedRoutesThatPushed.contains(modalRoute)) {
          _poppedRoutesThatPushed.add(modalRoute);
        } else {
          return false;
        }
      }
      final anim = modalRoute.animation;
      if (modalRoute is PageRoute &&
          anim != null &&
          anim.status == AnimationStatus.forward &&
          anim.value < 0.90) {
        return false;
      }
    } else {
      for (final route in _transitioningPageRoutes) {
        final anim = route.animation;
        if (anim != null &&
            anim.status == AnimationStatus.forward &&
            anim.value < 0.90) {
          return false;
        }
      }
    }

    if (targetLocation != null && targetLocation.isNotEmpty) {
      if (_lastTargetLocation == targetLocation &&
          _lastLocationPushAt != null &&
          now.difference(_lastLocationPushAt!) < throttleWindow) {
        return false;
      }
      try {
        final currentUri = GoRouterState.of(context).uri.toString();
        if (currentUri == targetLocation) {
          return false;
        }
      } catch (_) {
        try {
          final currentUri = GoRouter.maybeOf(
            context,
          )?.routerDelegate.currentConfiguration.uri.toString();
          if (currentUri == targetLocation) {
            return false;
          }
        } catch (_) {}
      }
    }

    return true;
  }

  /// 安全执行命令式 `Navigator.push`：自动防连点、防非当前页重复 push，并在目标页退出后自动解锁。
  static Future<T?> push<T extends Object?>(
    BuildContext context,
    Route<T> route, {
    bool rootNavigator = false,
  }) {
    if (!_canInitiatePush(context)) {
      return Future<T?>.value(null);
    }
    final sourceRoute = ModalRoute.of(context);
    if (sourceRoute != null) {
      _routesWithPushInFlight.add(sourceRoute);
    }
    _markFramePushStarted();

    final Future<T?> future;
    try {
      future = Navigator.of(context, rootNavigator: rootNavigator).push(route);
    } catch (_) {
      if (sourceRoute != null) {
        _routesWithPushInFlight.remove(sourceRoute);
      }
      _pendingFramePush = false;
      rethrow;
    }

    future.whenComplete(() {
      if (sourceRoute != null) {
        _routesWithPushInFlight.remove(sourceRoute);
      }
    });
    return future;
  }

  /// 便捷推入 `MaterialPageRoute`。
  static Future<T?> pushMaterialPage<T extends Object?>(
    BuildContext context,
    WidgetBuilder builder, {
    RouteSettings? settings,
    bool maintainState = true,
    bool fullscreenDialog = false,
    bool allowSnapshotting = true,
    bool rootNavigator = false,
  }) {
    return push<T>(
      context,
      MaterialPageRoute<T>(
        builder: builder,
        settings: settings,
        maintainState: maintainState,
        fullscreenDialog: fullscreenDialog,
        allowSnapshotting: allowSnapshotting,
      ),
      rootNavigator: rootNavigator,
    );
  }

  /// 安全执行声明式 `GoRouter` `context.push(location)`。
  static Future<T?> pushNamedLocation<T extends Object?>(
    BuildContext context,
    String location, {
    Object? extra,
  }) {
    if (!_canInitiatePush(context, targetLocation: location)) {
      return Future<T?>.value(null);
    }
    final sourceRoute = ModalRoute.of(context);
    if (sourceRoute != null) {
      _routesWithPushInFlight.add(sourceRoute);
    }
    _lastTargetLocation = location;
    _lastLocationPushAt = clock.now();
    _markFramePushStarted();

    final Future<T?> future;
    try {
      future = GoRouter.of(context).push<T>(location, extra: extra);
    } catch (_) {
      if (sourceRoute != null) {
        _routesWithPushInFlight.remove(sourceRoute);
      }
      _pendingFramePush = false;
      rethrow;
    }

    future.whenComplete(() {
      if (sourceRoute != null) {
        _routesWithPushInFlight.remove(sourceRoute);
      }
      if (_lastTargetLocation == location) {
        _lastTargetLocation = null;
      }
    });
    return future;
  }

  /// 保护“跳转前含异步请求（如切换节点、拉取会话）”的操作：
  /// 异步执行期间全局锁定导航门禁，防止用户在 await 期间连点触发两次并发跳转。
  static Future<T?> runAsync<T>(
    BuildContext context,
    Future<T> Function() action,
  ) async {
    if (!context.mounted) return null;
    if (_pendingFramePush || _activeAsyncTokens.isNotEmpty) {
      return null;
    }
    if (_lastPushAt != null &&
        clock.now().difference(_lastPushAt!) < throttleWindow) {
      return null;
    }
    final modalRoute = ModalRoute.of(context);
    if (modalRoute != null && !modalRoute.isCurrent) {
      if (modalRoute is! PopupRoute ||
          _poppedRoutesThatPushed.contains(modalRoute)) {
        return null;
      }
    }

    final token = Object();
    _activeAsyncTokens.add(token);
    _cancelActivePointers();
    try {
      return await action();
    } finally {
      _activeAsyncTokens.remove(token);
    }
  }

  /// 用于 BottomSheet / Dialog / Drawer 内部“先关闭弹窗或抽屉再 push 新页面”的场景：
  /// 仅当该弹窗仍是栈顶当前 `PopupRoute`（或 `Scaffold` 抽屉处于打开状态）时才执行关闭，
  /// 避免连点时把刚推入的目标页或底层 `PageRoute` 误 pop 掉。
  static bool popIfCurrent<T extends Object?>(
    BuildContext context, [
    T? result,
  ]) {
    if (!context.mounted) return false;
    final route = ModalRoute.of(context);
    if (route == null) {
      return false;
    }
    if (route is! PopupRoute) {
      final scaffold = Scaffold.maybeOf(context);
      if (scaffold != null && scaffold.isDrawerOpen) {
        scaffold.closeDrawer();
        return true;
      }
      if (scaffold != null && scaffold.isEndDrawerOpen) {
        scaffold.closeEndDrawer();
        return true;
      }
      return false;
    }
    if (!route.isCurrent || _poppedRoutesThatPushed.contains(route)) {
      return false;
    }
    Navigator.of(context).pop<T>(result);
    return true;
  }

  static void _onRoutePushed(
    Route<dynamic> route,
    Route<dynamic>? previousRoute,
  ) {
    if (route is PageRoute<dynamic>) {
      // 忽略 Navigator 首次挂载根路由（previousRoute == null），避免首屏渲染误锁导航门禁
      if (previousRoute == null) {
        return;
      }
      _routesWithPushInFlight.add(previousRoute);
      final now = clock.now();
      final isRapidDuplicatePagePush =
          previousRoute is PageRoute<dynamic> &&
          identical(previousRoute, _lastPushedPageRoute) &&
          _lastObservedPagePushAt != null &&
          now.difference(_lastObservedPagePushAt!) < throttleWindow &&
          route.settings.name == previousRoute.settings.name &&
          route.runtimeType == previousRoute.runtimeType;

      if (isRapidDuplicatePagePush) {
        // 兜底拦截未走 AppNavigationGuard.push 的原生 Navigator.of(context).push 连点重复进栈：
        // 在帧末回调移除重复路由，避免同帧 OverlayEntry 尚未挂载完成时提前 dispose 触发断言
        SchedulerBinding.instance.addPostFrameCallback((_) {
          try {
            final nav = route.navigator;
            if (nav != null && route.isCurrent && nav.canPop()) {
              nav.removeRoute(route);
            }
          } catch (_) {}
        });
        SchedulerBinding.instance.ensureVisualUpdate();
        return;
      }

      _lastPushedPageRoute = route;
      // Use the previous observed push time: guarded pushes update _lastPushAt
      // before didPush, which would misclassify every unnamed child page.
      _lastObservedPagePushAt = now;
      _markFramePushStarted();
      _trackPageTransition(route);
    }
  }

  static void _onRouteRemovedOrPopped(
    Route<dynamic> route,
    Route<dynamic>? previousRoute,
  ) {
    _routesWithPushInFlight.remove(route);
    _poppedRoutesThatPushed.remove(route);
    if (previousRoute != null) {
      _routesWithPushInFlight.remove(previousRoute);
    }
    _lastTargetLocation = null;
    if (route is PageRoute<dynamic>) {
      if (identical(_lastPushedPageRoute, route)) {
        _lastPushedPageRoute = null;
        _lastObservedPagePushAt = null;
        _lastPushAt = null;
        _pendingFramePush = false;
      }
      _untrackPageTransition(route);
    }
  }

  static void _trackPageTransition(PageRoute<dynamic> route) {
    final anim = route.animation;
    if (anim == null) return;
    if (anim.status != AnimationStatus.completed && anim.value < 1.0) {
      _transitioningPageRoutes.add(route);
      void listener(AnimationStatus status) {
        if (status == AnimationStatus.completed ||
            status == AnimationStatus.dismissed) {
          _untrackPageTransition(route);
        }
      }

      _transitionListeners[route] = listener;
      anim.addStatusListener(listener);
    }
  }

  static void _untrackPageTransition(PageRoute<dynamic> route) {
    final listener = _transitionListeners.remove(route);
    if (listener != null) {
      route.animation?.removeStatusListener(listener);
    }
    _transitioningPageRoutes.remove(route);
  }
}

/// 路由观察器：自动追踪全站所有 `PageRoute` 的进退栈与转场动画状态。
class AppNavigationObserver extends NavigatorObserver {
  AppNavigationObserver._();

  @override
  void didPush(Route<dynamic> route, Route<dynamic>? previousRoute) {
    super.didPush(route, previousRoute);
    AppNavigationGuard._onRoutePushed(route, previousRoute);
  }

  @override
  void didPop(Route<dynamic> route, Route<dynamic>? previousRoute) {
    super.didPop(route, previousRoute);
    AppNavigationGuard._onRouteRemovedOrPopped(route, previousRoute);
  }

  @override
  void didRemove(Route<dynamic> route, Route<dynamic>? previousRoute) {
    super.didRemove(route, previousRoute);
    AppNavigationGuard._onRouteRemovedOrPopped(route, previousRoute);
  }

  @override
  void didReplace({Route<dynamic>? newRoute, Route<dynamic>? oldRoute}) {
    super.didReplace(newRoute: newRoute, oldRoute: oldRoute);
    if (oldRoute != null) {
      AppNavigationGuard._onRouteRemovedOrPopped(oldRoute, null);
    }
    if (newRoute != null) {
      AppNavigationGuard._onRoutePushed(newRoute, null);
    }
  }
}

/// 根级触控门禁组件：包裹在 `MaterialApp` 外层，在页面进栈当帧、异步跳转等待期及转场动画期间
/// 从 `hitTest` 层直接拦截多余连点与多指并发点击。
class NavigationPointerGate extends SingleChildRenderObjectWidget {
  const NavigationPointerGate({super.key, required super.child});

  @override
  RenderObject createRenderObject(BuildContext context) {
    return _RenderNavigationPointerGate();
  }
}

class _RenderNavigationPointerGate extends RenderProxyBoxWithHitTestBehavior {
  _RenderNavigationPointerGate() : super(behavior: HitTestBehavior.translucent);

  @override
  bool hitTest(BoxHitTestResult result, {required Offset position}) {
    if (AppNavigationGuard.isPointerGateLocked) {
      return false;
    }
    return super.hitTest(result, position: position);
  }

  @override
  void handleEvent(PointerEvent event, BoxHitTestEntry entry) {
    if (event is PointerDownEvent) {
      if (AppNavigationGuard.isPointerGateLocked) {
        GestureBinding.instance.cancelPointer(event.pointer);
        return;
      }
      AppNavigationGuard._activePointers.add(event.pointer);
    } else if (event is PointerUpEvent || event is PointerCancelEvent) {
      AppNavigationGuard._activePointers.remove(event.pointer);
    }
    super.handleEvent(event, entry);
  }
}
