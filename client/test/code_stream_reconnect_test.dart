import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:hoxi/core/network/api_client.dart';
import 'package:hoxi/features/ai_workspace/data/ai_workspace_repository.dart';
import 'package:hoxi/features/ai_workspace/models/ai_dev_session.dart';
import 'package:hoxi/features/ai_workspace/models/ai_session_state_info.dart';
import 'package:hoxi/features/ai_workspace/models/code_delivery_job.dart';
import 'package:hoxi/features/ai_workspace/models/code_session_recovery.dart';
import 'package:hoxi/features/ai_workspace/models/code_task.dart';
import 'package:hoxi/features/ai_workspace/presentation/controllers/ai_workspace_controller.dart';

class _FlakyStreamRepository extends AiWorkspaceRepository {
  _FlakyStreamRepository() : super(ApiClient());

  bool serverDown = true;
  var opens = 0;
  final stream = StreamController<Uint8List>();

  static const _session = {'id': 7, 'projectId': 1, 'agentName': 'codex'};

  @override
  Future<List<CodeProject>> getProjects() async => [
    CodeProject.fromJson({'id': 1, 'name': 'shop'}),
  ];

  @override
  Future<List<CodeExecutor>> getExecutors() async => const [];

  @override
  Future<List<AiDevSession>> getSessions({int? projectId}) async => [
    AiDevSession.fromJson(_session),
  ];

  @override
  Future<List<CodeTask>> getTasks({int? projectId}) async => const [];

  @override
  Future<AiSessionStateInfo> getSessionState(int sessionId) async =>
      AiSessionStateInfo.fromJson({'session': _session});

  @override
  Future<CodeSessionHistory> getSessionHistory(
    int sessionId, {
    int page = 1,
    int limit = 20,
  }) async => CodeSessionHistory.fromJson({'session': _session});

  @override
  Future<CodeDeliveryJob?> getDelivery(int sessionId) async => null;

  @override
  Future<ResponseBody> openConversationStream(
    int sessionId, {
    CancelToken? cancelToken,
  }) async {
    opens++;
    if (serverDown) throw Exception('connection refused');
    return ResponseBody(stream.stream, 200);
  }
}

void main() {
  test('重连间隔 2、4、8、16 秒递增，封顶 30 秒', () {
    expect(
      [
        for (var attempt = 0; attempt < 7; attempt++)
          codeStreamReconnectDelay(attempt).inSeconds,
      ],
      [2, 4, 8, 16, 30, 30, 30],
    );
  });

  testWidgets('断线期间逐步放慢重连并提示，恢复后提示消失、间隔复位', (tester) async {
    final repository = _FlakyStreamRepository();
    final container = ProviderContainer(
      overrides: [aiWorkspaceRepositoryProvider.overrideWithValue(repository)],
    );
    addTearDown(() => unawaited(repository.stream.close()));
    container.read(aiWorkspaceControllerProvider);
    await tester.pump();
    await tester.pump();
    expect(repository.opens, 1);
    expect(
      container.read(aiWorkspaceControllerProvider).isStreamReconnecting,
      isTrue,
    );

    // 每次推进恰好一个退避间隔，确认第 N 次重连在预期的时刻发生
    for (final (seconds, expectedOpens) in [(2, 2), (4, 3), (8, 4)]) {
      await tester.pump(Duration(seconds: seconds - 1));
      await tester.pump();
      expect(repository.opens, expectedOpens - 1);
      await tester.pump(const Duration(seconds: 1));
      await tester.pump();
      await tester.pump();
      expect(repository.opens, expectedOpens);
    }

    repository.serverDown = false;
    await tester.pump(const Duration(seconds: 16));
    await tester.pump();
    await tester.pump();
    expect(repository.opens, 5);
    repository.stream.add(
      Uint8List.fromList(utf8.encode('event: snapshot\ndata: {}\n\n')),
    );
    await tester.pump();
    await tester.pump();
    expect(
      container.read(aiWorkspaceControllerProvider).isStreamReconnecting,
      isFalse,
    );

    // 恢复后再断开：间隔已复位，2 秒后就重连，而不是沿用 30 秒
    repository.serverDown = true;
    unawaited(repository.stream.close());
    await tester.pump();
    expect(
      container.read(aiWorkspaceControllerProvider).isStreamReconnecting,
      isTrue,
    );
    await tester.pump(const Duration(seconds: 2));
    await tester.pump();
    await tester.pump();
    expect(repository.opens, 6);
    // 释放控制器会取消仍在排队的重连定时器
    container.dispose();
  });
}
