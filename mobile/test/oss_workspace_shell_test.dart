import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:hoxi/app/oss/oss_workspace_shell.dart';
import 'package:hoxi/core/l10n/l10n.dart';
import 'package:hoxi/core/storage/storage_service.dart';
import 'package:hoxi/features/auth/models/node_link_kind.dart';
import 'package:hoxi/features/auth/presentation/screens/node_link_kind_screen.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  setUp(() async {
    SharedPreferences.setMockInitialValues({});
    FlutterSecureStorage.setMockInitialValues({});
    await StorageService.init();
    OssAppRouter.ensureRoutesRegistered();
  });

  testWidgets('OSS onboarding screen exposes QR scan and Hoxi desktop pairing actions', (
    tester,
  ) async {
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          supportedNodeLinkKindsProvider.overrideWithValue(const [
            NodeLinkKind.hoxi,
            NodeLinkKind.gopanel,
          ]),
        ],
        child: MaterialApp(
          locale: const Locale('zh'),
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          home: const OssWorkspaceShellScreen(),
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.byKey(const ValueKey('oss_scan_qr_button')), findsOneWidget);
    expect(find.byKey(const ValueKey('oss_connect_hoxi_button')), findsOneWidget);
    expect(find.text('Hoxi 桌面端'), findsOneWidget);
  });

  testWidgets('OSS node picker shows only Hoxi Desktop and GoPanel', (
    tester,
  ) async {
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          supportedNodeLinkKindsProvider.overrideWithValue(const [
            NodeLinkKind.hoxi,
            NodeLinkKind.gopanel,
          ]),
        ],
        child: MaterialApp(
          locale: const Locale('zh'),
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          home: const NodeLinkKindScreen(),
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('Hoxi 桌面端'), findsOneWidget);
    expect(find.text('GoPanel'), findsOneWidget);
    expect(find.byKey(const ValueKey('node_link_kind_hoxi')), findsOneWidget);
    expect(
      find.byKey(const ValueKey('node_link_kind_gopanel')),
      findsOneWidget,
    );
    expect(find.byIcon(Icons.chevron_right_rounded), findsNWidgets(2));
  });
}
