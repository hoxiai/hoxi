import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:hoxi/core/navigation/navigation_guard.dart';

void main() {
  setUp(() {
    AppNavigationGuard.resetForTesting();
  });

  testWidgets(
    'AppNavigationGuard.push blocks rapid synchronous and cross-frame double taps and unlocks after pop',
    (tester) async {
      var detailBuilds = 0;

      await tester.pumpWidget(
        NavigationPointerGate(
          child: MaterialApp(
            navigatorObservers: [AppNavigationGuard.observer],
            home: Builder(
              builder: (context) => Scaffold(
                body: Center(
                  child: ElevatedButton(
                    key: const ValueKey('open_detail_btn'),
                    onPressed: () {
                      AppNavigationGuard.push<void>(
                        context,
                        MaterialPageRoute<void>(
                          builder: (pageCtx) {
                            detailBuilds++;
                            return Scaffold(
                              appBar: AppBar(title: const Text('Detail Page')),
                              body: Center(
                                child: ElevatedButton(
                                  key: const ValueKey('back_btn'),
                                  onPressed: () => Navigator.of(pageCtx).pop(),
                                  child: const Text('Back'),
                                ),
                              ),
                            );
                          },
                        ),
                      );
                    },
                    child: const Text('Open Detail'),
                  ),
                ),
              ),
            ),
          ),
        ),
      );

      final buttonFinder = find.byKey(const ValueKey('open_detail_btn'));
      final buttonWidget = tester.widget<ElevatedButton>(buttonFinder);

      // Simulate rapid double invoke in the same frame before transition starts
      buttonWidget.onPressed!();
      buttonWidget.onPressed!();

      // Advance one frame (16ms) and verify pointer gate is STILL locked within throttleWindow
      await tester.pump(const Duration(milliseconds: 20));
      expect(AppNavigationGuard.isPointerGateLocked, isTrue);

      await tester.pumpAndSettle();

      expect(detailBuilds, 1);
      expect(find.text('Detail Page'), findsOneWidget);

      // Pop back and verify source route is unlocked immediately for future navigation
      await tester.tap(find.byKey(const ValueKey('back_btn')));
      await tester.pumpAndSettle();

      expect(find.text('Open Detail'), findsOneWidget);

      await tester.tap(buttonFinder);
      await tester.pumpAndSettle();

      expect(detailBuilds, 2);
      expect(find.text('Detail Page'), findsOneWidget);
    },
  );

  testWidgets(
    'AppNavigationObserver automatically removes duplicate PageRoute when raw Navigator.push is called twice',
    (tester) async {
      await tester.pumpWidget(
        NavigationPointerGate(
          child: MaterialApp(
            navigatorObservers: [AppNavigationGuard.observer],
            home: Builder(
              builder: (context) => Scaffold(
                body: Center(
                  child: ElevatedButton(
                    key: const ValueKey('raw_push_btn'),
                    onPressed: () {
                      Navigator.of(context).push<void>(
                        MaterialPageRoute<void>(
                          builder: (pageCtx) => Scaffold(
                            appBar: AppBar(title: const Text('Raw Page')),
                            body: Center(
                              child: ElevatedButton(
                                key: const ValueKey('raw_back_btn'),
                                onPressed: () => Navigator.of(pageCtx).pop(),
                                child: const Text('Back'),
                              ),
                            ),
                          ),
                        ),
                      );
                    },
                    child: const Text('Raw Open'),
                  ),
                ),
              ),
            ),
          ),
        ),
      );

      final btn = tester.widget<ElevatedButton>(
        find.byKey(const ValueKey('raw_push_btn')),
      );

      // Simulate two unguarded raw Navigator.push calls within throttleWindow
      btn.onPressed!();
      btn.onPressed!();

      await tester.pumpAndSettle();

      expect(find.text('Raw Page'), findsOneWidget);

      // A single pop should return directly to the home page (proving only 1 route remained on stack)
      await tester.tap(find.byKey(const ValueKey('raw_back_btn')));
      await tester.pumpAndSettle();

      expect(find.text('Raw Open'), findsOneWidget);
    },
  );

  testWidgets(
    'AppNavigationGuard.bindRouter deduplicates rapid double context.push calls to same route',
    (tester) async {
      var pushedPageBuilds = 0;
      final router = AppNavigationGuard.bindRouter(
        GoRouter(
          observers: [AppNavigationGuard.observer],
          routes: [
            GoRoute(
              path: '/',
              builder: (context, state) => Scaffold(
                body: Center(
                  child: ElevatedButton(
                    key: const ValueKey('go_push_btn'),
                    onPressed: () {
                      GoRouter.of(context).push('/target');
                    },
                    child: const Text('Go Target'),
                  ),
                ),
              ),
            ),
            GoRoute(
              path: '/target',
              builder: (context, state) {
                pushedPageBuilds++;
                return Scaffold(
                  appBar: AppBar(title: const Text('Target Screen')),
                );
              },
            ),
          ],
        ),
      );

      await tester.pumpWidget(
        NavigationPointerGate(
          child: MaterialApp.router(routerConfig: router),
        ),
      );

      final btn = tester.widget<ElevatedButton>(
        find.byKey(const ValueKey('go_push_btn')),
      );

      // Rapid double call before GoRouter rebuilds the Navigator pages
      btn.onPressed!();
      btn.onPressed!();

      await tester.pumpAndSettle();

      expect(pushedPageBuilds, 1);
      expect(find.text('Target Screen'), findsOneWidget);
    },
  );

  testWidgets(
    'AppNavigationGuard.runAsync prevents parallel async pre-navigation tasks from pushing twice',
    (tester) async {
      var asyncExecutions = 0;
      var pushedCount = 0;

      await tester.pumpWidget(
        NavigationPointerGate(
          child: MaterialApp(
            navigatorObservers: [AppNavigationGuard.observer],
            home: Builder(
              builder: (context) => Scaffold(
                body: Center(
                  child: ElevatedButton(
                    key: const ValueKey('async_open_btn'),
                    onPressed: () {
                      AppNavigationGuard.runAsync<void>(context, () async {
                        asyncExecutions++;
                        await Future<void>.delayed(
                          const Duration(milliseconds: 80),
                        );
                        if (!context.mounted) return;
                        await AppNavigationGuard.push<void>(
                          context,
                          MaterialPageRoute<void>(
                            builder: (_) {
                              pushedCount++;
                              return const Scaffold(
                                body: Text('Async Target'),
                              );
                            },
                          ),
                        );
                      });
                    },
                    child: const Text('Async Open'),
                  ),
                ),
              ),
            ),
          ),
        ),
      );

      final btn = tester.widget<ElevatedButton>(
        find.byKey(const ValueKey('async_open_btn')),
      );

      // Fire two rapid taps while the async pre-navigation task is still awaiting
      btn.onPressed!();
      btn.onPressed!();

      await tester.pump(const Duration(milliseconds: 100));
      await tester.pumpAndSettle();

      expect(asyncExecutions, 1);
      expect(pushedCount, 1);
      expect(find.text('Async Target'), findsOneWidget);
    },
  );

  testWidgets(
    'BottomSheet popIfCurrent + push only pops the sheet and pushes target page once on double tap',
    (tester) async {
      var targetBuilds = 0;

      await tester.pumpWidget(
        NavigationPointerGate(
          child: MaterialApp(
            navigatorObservers: [AppNavigationGuard.observer],
            home: Builder(
              builder: (context) => Scaffold(
                body: Center(
                  child: ElevatedButton(
                    key: const ValueKey('open_sheet_btn'),
                    onPressed: () {
                      showModalBottomSheet<void>(
                        context: context,
                        builder: (sheetCtx) => Center(
                          child: ElevatedButton(
                            key: const ValueKey('sheet_item_btn'),
                            onPressed: () {
                              if (!AppNavigationGuard.popIfCurrent(sheetCtx)) {
                                return;
                              }
                              AppNavigationGuard.push<void>(
                                sheetCtx,
                                MaterialPageRoute<void>(
                                  builder: (_) {
                                    targetBuilds++;
                                    return const Scaffold(
                                      body: Text('Sheet Target Page'),
                                    );
                                  },
                                ),
                              );
                            },
                            child: const Text('Sheet Item'),
                          ),
                        ),
                      );
                    },
                    child: const Text('Open Sheet'),
                  ),
                ),
              ),
            ),
          ),
        ),
      );

      await tester.tap(find.byKey(const ValueKey('open_sheet_btn')));
      await tester.pumpAndSettle();

      final sheetBtn = tester.widget<ElevatedButton>(
        find.byKey(const ValueKey('sheet_item_btn')),
      );

      // Simulate double tap on the sheet item
      sheetBtn.onPressed!();
      sheetBtn.onPressed!();

      await tester.pumpAndSettle();

      expect(targetBuilds, 1);
      expect(find.text('Sheet Target Page'), findsOneWidget);
    },
  );

  testWidgets(
    'AppNavigationGuard.runAsync unlocks pointer gate after push so target workbench page remains clickable before pop',
    (tester) async {
      var innerTapCount = 0;

      await tester.pumpWidget(
        NavigationPointerGate(
          child: MaterialApp(
            navigatorObservers: [AppNavigationGuard.observer],
            home: Builder(
              builder: (context) => Scaffold(
                body: Center(
                  child: ElevatedButton(
                    key: const ValueKey('open_workbench_btn'),
                    onPressed: () {
                      AppNavigationGuard.runAsync<void>(context, () async {
                        await Future<void>.delayed(
                          const Duration(milliseconds: 20),
                        );
                        if (!context.mounted) return;
                        // Simulates _openNodeWorkbench awaiting push until the workbench page pops
                        await AppNavigationGuard.push<void>(
                          context,
                          MaterialPageRoute<void>(
                            builder: (pageCtx) => Scaffold(
                              body: Column(
                                children: [
                                  ElevatedButton(
                                    key: const ValueKey('inner_action_btn'),
                                    onPressed: () => innerTapCount++,
                                    child: const Text('Inner Action'),
                                  ),
                                  ElevatedButton(
                                    key: const ValueKey('inner_back_btn'),
                                    onPressed: () => Navigator.of(pageCtx).pop(),
                                    child: const Text('Back'),
                                  ),
                                ],
                              ),
                            ),
                          ),
                        );
                      });
                    },
                    child: const Text('Open Workbench'),
                  ),
                ),
              ),
            ),
          ),
        ),
      );

      await tester.tap(find.byKey(const ValueKey('open_workbench_btn')));
      await tester.pump(const Duration(milliseconds: 30));
      await tester.pumpAndSettle();
      // Advance past throttleWindow (500ms)
      await tester.pump(const Duration(milliseconds: 550));

      expect(AppNavigationGuard.isPointerGateLocked, isFalse);

      // Verify clicks inside the pushed page work normally while await push(...) is still pending
      await tester.tap(find.byKey(const ValueKey('inner_action_btn')));
      await tester.pump();
      expect(innerTapCount, 1);

      // Verify back button works via real pointer tap
      await tester.tap(find.byKey(const ValueKey('inner_back_btn')));
      await tester.pumpAndSettle();
      expect(find.text('Open Workbench'), findsOneWidget);
    },
  );

  testWidgets(
    'AppNavigationGuard.popIfCurrent inside a Scaffold Drawer only closes the drawer and never pops underlying PageRoute on double tap',
    (tester) async {
      var threadSelectionCount = 0;

      await tester.pumpWidget(
        NavigationPointerGate(
          child: MaterialApp(
            navigatorObservers: [AppNavigationGuard.observer],
            home: Builder(
              builder: (context) => Scaffold(
                body: Center(
                  child: ElevatedButton(
                    key: const ValueKey('open_conv_btn'),
                    onPressed: () {
                      Navigator.of(context).push<void>(
                        MaterialPageRoute<void>(
                          builder: (_) => Scaffold(
                            appBar: AppBar(title: const Text('Conversation Page')),
                            drawer: Drawer(
                              child: Builder(
                                builder: (drawerCtx) => Center(
                                  child: ElevatedButton(
                                    key: const ValueKey('drawer_item_btn'),
                                    onPressed: () {
                                      if (!AppNavigationGuard.popIfCurrent(
                                        drawerCtx,
                                      )) {
                                        return;
                                      }
                                      threadSelectionCount++;
                                    },
                                    child: const Text('Switch Thread'),
                                  ),
                                ),
                              ),
                            ),
                            body: const Text('Active Conversation'),
                          ),
                        ),
                      );
                    },
                    child: const Text('Open Conversation'),
                  ),
                ),
              ),
            ),
          ),
        ),
      );

      await tester.tap(find.byKey(const ValueKey('open_conv_btn')));
      await tester.pumpAndSettle();
      await tester.pump(const Duration(milliseconds: 550));

      final ScaffoldState state = tester.firstState(
        find.byWidgetPredicate((w) => w is Scaffold && w.drawer != null),
      );
      state.openDrawer();
      await tester.pumpAndSettle();

      final drawerBtn = tester.widget<ElevatedButton>(
        find.byKey(const ValueKey('drawer_item_btn')),
      );

      // Simulate rapid double tap on Drawer item that does not push a new route
      drawerBtn.onPressed!();
      drawerBtn.onPressed!();
      await tester.pumpAndSettle();

      expect(threadSelectionCount, 1);
      // Verify the underlying Conversation Page was NOT popped off the Navigator
      expect(find.text('Active Conversation'), findsOneWidget);
    },
  );
}
