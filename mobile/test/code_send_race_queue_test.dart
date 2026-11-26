import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:hoxi/core/models/node_skill.dart';
import 'package:hoxi/features/ai_workspace/models/ai_dev_session.dart';
import 'package:hoxi/features/ai_workspace/models/ai_session_state_info.dart';
import 'package:hoxi/features/ai_workspace/presentation/controllers/ai_workspace_controller.dart';

import 'support/fake_ai_workspace_queue_repository.dart';

void main() {
  testWidgets(
    'queued upload failure retains the original item and only retries on request',
    (tester) async {
      final repo = FakeAiWorkspaceQueueRepository();
      final container = ProviderContainer(
        overrides: [aiWorkspaceRepositoryProvider.overrideWithValue(repo)],
      );
      addTearDown(() {
        container.dispose();
        unawaited(repo.stream.close());
      });
      final controller = container.read(aiWorkspaceControllerProvider.notifier);
      await tester.pump();
      repo
        ..currentStage = 'running'
        ..sessionStatus = 'running';
      repo.emitSse('event: delta\ndata: {"runId":1,"status":"running","content":"busy"}\n\n');
      await tester.pump();
      const image = 'data:image/jpeg;base64,AAAA';
      await controller.sendMessage('review the image', imageDataUrl: image);
      final original = container.read(aiWorkspaceControllerProvider).queuedPrompts.single;

      repo
        ..failNextUpload = true
        ..currentStage = 'idle'
        ..sessionStatus = 'active';
      repo.emitSse('data: {"type":"session.idle","properties":{}}\n\n');
      await tester.pump();

      final failed = container.read(aiWorkspaceControllerProvider).queuedPrompts.single;
      expect(failed.id, original.id);
      expect(failed.text, original.text);
      expect(failed.imageDataUrl, image);
      expect(failed.requiresManualRetry, isTrue);

      await controller.refreshCurrentSession(trustServerState: true);
      await tester.pump();
      expect(repo.sentCommands, isEmpty);
      expect(container.read(aiWorkspaceControllerProvider).queuedPrompts, hasLength(1));

      const draftSkill = NodeSkillSelection(key: 'skl_draft', name: 'Draft');
      controller.selectComposerSkill(draftSkill);
      expect(await controller.sendQueuedPromptNow(failed.id), isTrue);
      expect(repo.sentCommands, hasLength(1));
      expect(repo.sentSkills, [null]);
      expect(container.read(aiWorkspaceControllerProvider).selectedSkill, draftSkill);
      expect(container.read(aiWorkspaceControllerProvider).queuedPrompts, isEmpty);
      container.dispose();
    },
  );

  testWidgets('editing a queued prompt restores its exact skill selection', (tester) async {
    final repo = FakeAiWorkspaceQueueRepository();
    final container = ProviderContainer(
      overrides: [aiWorkspaceRepositoryProvider.overrideWithValue(repo)],
    );
    addTearDown(() {
      container.dispose();
      unawaited(repo.stream.close());
    });
    final controller = container.read(aiWorkspaceControllerProvider.notifier);
    await tester.pump();
    final sessionId = container.read(aiWorkspaceControllerProvider).currentSession!.id;
    const queuedSkill = NodeSkillSelection(key: 'skl_queued', name: 'Queued');
    controller.enqueueFollowUpPrompt(sessionId: sessionId, text: 'with skill', skillSelection: queuedSkill);
    controller.enqueueFollowUpPrompt(sessionId: sessionId, text: 'without skill');
    final queue = container.read(aiWorkspaceControllerProvider).queuedPrompts;
    expect(controller.popQueuedPromptForEdit(queue.first.id)?.skillSelection, queuedSkill);
    expect(container.read(aiWorkspaceControllerProvider).selectedSkill, queuedSkill);
    expect(controller.popQueuedPromptForEdit(queue.last.id)?.text, 'without skill');
    expect(container.read(aiWorkspaceControllerProvider).selectedSkill, isNull);
    container.dispose();
  });

  testWidgets(
    'fast SSE done arriving before sendAiCommand returns does not bounce back to running/queued',
    (tester) async {
      final repo = FakeAiWorkspaceQueueRepository();
      final sendCompleter = Completer<AiInstructionSendResult>();
      repo.pendingSendCompleter = sendCompleter;

      final container = ProviderContainer(
        overrides: [aiWorkspaceRepositoryProvider.overrideWithValue(repo)],
      );
      addTearDown(() {
        container.dispose();
        unawaited(repo.stream.close());
      });

      container.read(aiWorkspaceControllerProvider);
      await tester.pump();
      await tester.pump();

      final sendFuture = container
          .read(aiWorkspaceControllerProvider.notifier)
          .sendMessage('快速问题');
      await tester.pump();

      // SSE delivers reply + session.idle before HTTP response of sendAiCommand arrives.
      repo
        ..currentStage = 'idle'
        ..sessionStatus = 'active';
      repo.emitSse(
        'data: {"type":"message.part.delta","properties":{"delta":"你好"}}\n\n'
        'data: {"type":"session.idle","properties":{}}\n\n',
      );
      await tester.pump();

      // Now HTTP response for sendAiCommand completes (and sets busyGraceActive=true).
      repo.pendingSendCompleter = null;
      sendCompleter.complete(repo.buildSendResult('快速问题'));
      expect(await sendFuture, isTrue);
      await tester.pump();
      await tester.pump();

      final ws = container.read(aiWorkspaceControllerProvider);
      expect(ws.isSending, isFalse);
      expect(ws.isStreaming, isFalse);
      expect(ws.currentStage, 'idle');
      expect(ws.isAiThinking, isFalse);

      // Sending a second message immediately dispatches without queuing.
      await container
          .read(aiWorkspaceControllerProvider.notifier)
          .sendMessage('第二条消息');
      expect(repo.sentCommands, ['快速问题', '第二条消息']);
      expect(
        container.read(aiWorkspaceControllerProvider).queuedPrompts,
        isEmpty,
      );
      container.dispose();
    },
  );

  testWidgets(
    'SSE done arriving during post-send refreshCurrentSession stays idle and does not lock isStreaming',
    (tester) async {
      final repo = FakeAiWorkspaceQueueRepository();
      final refreshCompleter = Completer<AiSessionStateInfo>();

      final container = ProviderContainer(
        overrides: [aiWorkspaceRepositoryProvider.overrideWithValue(repo)],
      );
      addTearDown(() {
        container.dispose();
        unawaited(repo.stream.close());
      });

      container.read(aiWorkspaceControllerProvider);
      await tester.pump();
      await tester.pump();

      // Hold the post-send refreshCurrentSession in flight.
      repo.pendingStateCompleter = refreshCompleter;
      final sendFuture = container
          .read(aiWorkspaceControllerProvider.notifier)
          .sendMessage('第一轮');
      await tester.pump();

      // SSE emits done while Refresh #1 is still awaiting HTTP state.
      repo
        ..currentStage = 'idle'
        ..sessionStatus = 'active';
      repo.emitSse('data: {"type":"session.idle","properties":{}}\n\n');
      await tester.pump();

      // Resolve Refresh #1 with stale 'running', then allow coalesced Refresh #2 to see 'idle'.
      repo.pendingStateCompleter = null;
      refreshCompleter.complete(
        AiSessionStateInfo.fromJson(const {
          'session': {
            'id': 7,
            'projectId': 1,
            'title': 'Session 7',
            'agentName': 'codex',
            'status': 'running',
            'currentStage': 'running',
          },
          'currentStage': 'running',
        }),
      );
      expect(await sendFuture, isTrue);
      await tester.pump();
      await tester.pump();

      final ws = container.read(aiWorkspaceControllerProvider);
      expect(ws.isSending, isFalse);
      expect(ws.isStreaming, isFalse);
      expect(ws.currentStage, 'idle');
      expect(ws.isAiThinking, isFalse);

      // Next message sends immediately instead of queuing.
      await container
          .read(aiWorkspaceControllerProvider.notifier)
          .sendMessage('第二轮');
      expect(repo.sentCommands, ['第一轮', '第二轮']);
      expect(
        container.read(aiWorkspaceControllerProvider).queuedPrompts,
        isEmpty,
      );
      container.dispose();
    },
  );

  testWidgets(
    'queued follow-up prompt still drains on SSE done even if post-done refresh fails transiently',
    (tester) async {
      final repo = FakeAiWorkspaceQueueRepository();
      final container = ProviderContainer(
        overrides: [aiWorkspaceRepositoryProvider.overrideWithValue(repo)],
      );
      addTearDown(() {
        container.dispose();
        unawaited(repo.stream.close());
      });

      container.read(aiWorkspaceControllerProvider);
      await tester.pump();
      await tester.pump();

      // Start an active turn and queue a follow-up message while running.
      repo
        ..currentStage = 'running'
        ..sessionStatus = 'running';
      repo.emitSse(
        'event: delta\ndata: {"runId":1,"status":"running","content":"思考中"}\n\n',
      );
      await tester.pump();

      await container
          .read(aiWorkspaceControllerProvider.notifier)
          .sendMessage('排队后续指令');
      expect(
        container.read(aiWorkspaceControllerProvider).queuedPrompts,
        hasLength(1),
      );
      expect(repo.sentCommands, isEmpty);

      // Session finishes, but the immediate refreshCurrentSession throws once.
      repo
        ..currentStage = 'idle'
        ..sessionStatus = 'active'
        ..failNextRefresh = true;
      repo.emitSse('data: {"type":"session.idle","properties":{}}\n\n');
      await tester.pump();
      await tester.pump();
      await tester.pump();

      // Even though refresh failed, queued prompt must still drain and send.
      expect(repo.sentCommands, ['排队后续指令']);
      expect(
        container.read(aiWorkspaceControllerProvider).queuedPrompts,
        isEmpty,
      );
      container.dispose();
    },
  );

  testWidgets(
    'multiple queued prompts continue draining when an earlier drained prompt completes during send',
    (tester) async {
      final repo = FakeAiWorkspaceQueueRepository();
      final container = ProviderContainer(
        overrides: [aiWorkspaceRepositoryProvider.overrideWithValue(repo)],
      );
      addTearDown(() => unawaited(repo.stream.close()));

      container.read(aiWorkspaceControllerProvider);
      await tester.pump();
      await tester.pump();

      // Queue two follow-up prompts while session is running.
      repo
        ..currentStage = 'running'
        ..sessionStatus = 'running';
      repo.emitSse(
        'event: delta\ndata: {"runId":1,"status":"running","content":"处理中"}\n\n',
      );
      await tester.pump();

      await container
          .read(aiWorkspaceControllerProvider.notifier)
          .sendMessage('排队 1');
      await container
          .read(aiWorkspaceControllerProvider.notifier)
          .sendMessage('排队 2');
      expect(
        container.read(aiWorkspaceControllerProvider).queuedPrompts,
        hasLength(2),
      );

      // Prepare a fast send completer for '排队 1' during which SSE immediately emits done.
      final p1Completer = Completer<AiInstructionSendResult>();
      repo
        ..currentStage = 'idle'
        ..sessionStatus = 'active'
        ..pendingSendCompleter = p1Completer;

      // Turn 0 finishes -> drains '排队 1'.
      repo.emitSse('data: {"type":"session.idle","properties":{}}\n\n');
      await tester.pump();
      await tester.pump();
      expect(repo.sentCommands, ['排队 1']);

      // '排队 1' finishes via SSE before its HTTP response even returns.
      repo.emitSse('data: {"type":"session.idle","properties":{}}\n\n');
      await tester.pump();
      repo.pendingSendCompleter = null;
      p1Completer.complete(repo.buildSendResult('排队 1'));
      await tester.pump();
      await tester.pump();
      await tester.pump();

      // '排队 2' must not be stranded after _drainingQueue resets.
      expect(repo.sentCommands, ['排队 1', '排队 2']);
      expect(
        container.read(aiWorkspaceControllerProvider).queuedPrompts,
        isEmpty,
      );
      container.dispose();
    },
  );

  testWidgets(
    'failed sendMessage and failed retryLatestInstruction restore currentStage so retry does not queue',
    (tester) async {
      final repo = FakeAiWorkspaceQueueRepository();
      final container = ProviderContainer(
        overrides: [aiWorkspaceRepositoryProvider.overrideWithValue(repo)],
      );
      addTearDown(() {
        container.dispose();
        unawaited(repo.stream.close());
      });

      container.read(aiWorkspaceControllerProvider);
      await tester.pump();
      await tester.pump();

      // 1. sendMessage throws.
      repo.failNextSend = true;
      final firstSent = await container
          .read(aiWorkspaceControllerProvider.notifier)
          .sendMessage('会失败的消息');
      expect(firstSent, isFalse);
      await tester.pump();

      final afterFail = container.read(aiWorkspaceControllerProvider);
      expect(afterFail.isSending, isFalse);
      expect(afterFail.isStreaming, isFalse);
      expect(afterFail.currentStage, 'idle');
      expect(afterFail.currentSession?.currentStage, 'idle');
      expect(afterFail.isAiThinking, isFalse);
      expect(
        afterFail.chatHistory.where((m) => m.isUser),
        isEmpty,
        reason: 'failed send must remove its optimistic local user bubble',
      );

      // Sending again must dispatch immediately rather than queuing.
      final secondSent = await container
          .read(aiWorkspaceControllerProvider.notifier)
          .sendMessage('重新发送');
      expect(secondSent, isTrue);
      expect(repo.sentCommands, ['会失败的消息', '重新发送']);
      expect(
        container.read(aiWorkspaceControllerProvider).queuedPrompts,
        isEmpty,
      );

      // Finish turn and test retryLatestInstruction failure rollback.
      repo
        ..currentStage = 'failed'
        ..sessionStatus = 'failed'
        ..latestInstruction = AiInstruction(
          id: 500,
          sessionId: 7,
          taskId: 7,
          content: '重新发送',
          status: 'failed',
          allowCode: true,
          autoPreview: false,
          requireApproval: false,
          analysisOnly: false,
          createdAt: DateTime.now(),
        );
      repo.emitSse('data: {"type":"session.idle","properties":{}}\n\n');
      await tester.pump();
      await tester.pump();

      repo.failNextRetry = true;
      final retried = await container
          .read(aiWorkspaceControllerProvider.notifier)
          .retryLatestInstruction();
      expect(retried, isFalse);
      final afterRetryFail = container.read(aiWorkspaceControllerProvider);
      expect(afterRetryFail.isStreaming, isFalse);
      expect(afterRetryFail.isAiThinking, isFalse);
      expect(afterRetryFail.currentStage, 'failed');
    },
  );

  testWidgets(
    'image-only send keeps optimistic user text empty instead of [@Skill: ] and rolls back on upload failure',
    (tester) async {
      final repo = FakeAiWorkspaceQueueRepository();
      final sendCompleter = Completer<AiInstructionSendResult>();
      repo.pendingSendCompleter = sendCompleter;

      final container = ProviderContainer(
        overrides: [aiWorkspaceRepositoryProvider.overrideWithValue(repo)],
      );
      addTearDown(() {
        container.dispose();
        unawaited(repo.stream.close());
      });

      container.read(aiWorkspaceControllerProvider);
      await tester.pump();
      await tester.pump();

      const sampleImage = 'data:image/jpeg;base64,AAAA';
      final sendFuture = container
          .read(aiWorkspaceControllerProvider.notifier)
          .sendMessage('', imageDataUrl: sampleImage);
      await tester.pump();

      final inFlightUsers = container
          .read(aiWorkspaceControllerProvider)
          .chatHistory
          .where((m) => m.isUser)
          .toList();
      expect(inFlightUsers, hasLength(1));
      expect(inFlightUsers.single.text, isEmpty);
      expect(inFlightUsers.single.imageDataUrl, sampleImage);

      repo.pendingSendCompleter = null;
      sendCompleter.complete(repo.buildSendResult(''));
      expect(await sendFuture, isTrue);

      repo
        ..currentStage = 'idle'
        ..sessionStatus = 'active';
      repo.emitSse('data: {"type":"session.idle","properties":{}}\n\n');
      await tester.pump();
      await tester.pump();

      // Upload failure must also remove the optimistic local image bubble.
      repo.failNextUpload = true;
      final failedUpload = await container
          .read(aiWorkspaceControllerProvider.notifier)
          .sendMessage('', imageDataUrl: sampleImage);
      expect(failedUpload, isFalse);
      await tester.pump();

      final afterUploadFail = container.read(aiWorkspaceControllerProvider);
      expect(afterUploadFail.errorMessage, contains('指令发送失败'));
      expect(
        afterUploadFail.chatHistory.where((m) => m.id.startsWith('local-')),
        hasLength(1),
        reason: 'only the earlier succeeded message remains; failed upload bubble is removed',
      );
    },
  );
}
