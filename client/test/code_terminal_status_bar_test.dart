import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:hoxi/features/ai_workspace/presentation/widgets/code_terminal_controls.dart';

void main() {
  Widget subject({
    bool connecting = false,
    bool reconnecting = false,
    bool connectionFailed = false,
    bool serverClosed = false,
    VoidCallback? onReconnect,
  }) {
    return MaterialApp(
      home: Scaffold(
        body: CodeTerminalStatusBar(
          connecting: connecting,
          reconnecting: reconnecting,
          connectionFailed: connectionFailed,
          serverClosed: serverClosed,
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

  testWidgets('does not retry a server-closed session', (tester) async {
    await tester.pumpWidget(
      subject(connectionFailed: true, serverClosed: true),
    );

    expect(find.byIcon(Icons.stop_circle_outlined), findsOneWidget);
    expect(find.byType(TextButton), findsNothing);
  });
}
