import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:gopanel/features/ai_workspace/presentation/controllers/ai_workspace_controller.dart';
import 'package:gopanel/features/ai_workspace/presentation/widgets/ai_session_overview_card.dart';

void main() {
  test('等待人工审批时指示条保留并说明是在等审批', () {
    const waiting = AiWorkspaceState(currentStage: 'awaiting_approval');
    expect(waiting.isAiThinking, isTrue);
    expect(waiting.indicatorActivityKind, 'approval_waiting');

    const streaming = AiWorkspaceState(
      currentStage: 'awaiting_approval',
      streamActivityKind: 'command',
    );
    expect(streaming.indicatorActivityKind, 'command');
    expect(const AiWorkspaceState().isAiThinking, isFalse);
  });

  testWidgets('会话概览不再露出英文阶段枚举', (tester) async {
    tester.binding.platformDispatcher.localeTestValue = const Locale('zh');
    addTearDown(tester.binding.platformDispatcher.clearLocaleTestValue);
    for (final (stage, label) in [
      ('awaiting_approval', '待审批'),
      ('interactive', '终端交互中'),
      ('delivering', '交付中'),
    ]) {
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: AiSessionOverviewCard(
              workspace: '/srv/shop',
              currentSession: null,
              currentTask: null,
              stage: stage,
            ),
          ),
        ),
      );
      expect(find.text(label), findsOneWidget);
      expect(find.text(stage), findsNothing);
    }
  });
}
