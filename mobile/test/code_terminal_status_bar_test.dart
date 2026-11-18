import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:hoxi/features/ai_workspace/presentation/widgets/code_terminal_controls.dart';
import 'package:hoxi/core/l10n/l10n.dart';

void main() {
  Widget subject({
    bool connecting = false,
    bool reconnecting = false,
    bool connectionFailed = false,
    bool serverClosed = false,
    bool relayUnsupported = false,
    VoidCallback? onReconnect,
  }) {
    return MaterialApp(
             localizationsDelegates: AppLocalizations.localizationsDelegates,
             supportedLocales: AppLocalizations.supportedLocales,
      home: Scaffold(
        body: CodeTerminalStatusBar(
          connecting: connecting,
          reconnecting: reconnecting,
          connectionFailed: connectionFailed,
          serverClosed: serverClosed,
          relayUnsupported: relayUnsupported,
          onReconnect: onReconnect ?? () {},
        ),
      ),
    );
  }

  testWidgets('shows automatic reconnect progress', (tester) async {
    await tester.pumpWidget(subject(connecting: true, reconnecting: true));

    expect(find.byType(CircularProgressIndicator), findsOneWidget);
    expect(find.byType(TextButton), findsNothing);
  });

  testWidgets('offers manual retry after network failure', (tester) async {
    var retried = false;
    await tester.pumpWidget(
      subject(connectionFailed: true, onReconnect: () => retried = true),
    );

    expect(find.byIcon(Icons.cloud_off_outlined), findsOneWidget);
    await tester.tap(find.byType(TextButton));
    expect(retried, isTrue);
  });

  testWidgets('explains the relay limit instead of offering a retry', (
    tester,
  ) async {
    await tester.pumpWidget(
      subject(connectionFailed: true, relayUnsupported: true),
    );

    expect(find.byIcon(Icons.vpn_lock_outlined), findsOneWidget);
    expect(find.textContaining('GoPanel tunnel'), findsOneWidget);
    expect(find.byType(TextButton), findsNothing);
  });

  testWidgets('does not retry a server-closed session', (tester) async {
    await tester.pumpWidget(
      subject(connectionFailed: true, serverClosed: true),
    );

    expect(find.byIcon(Icons.stop_circle_outlined), findsOneWidget);
    expect(find.byType(TextButton), findsNothing);
  });
}
