import 'dart:async';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:gopanel/core/network/api_client.dart';
import 'package:gopanel/features/ai_workspace/data/ai_workspace_repository.dart';
import 'package:gopanel/features/ai_workspace/models/ai_dev_session.dart';
import 'package:gopanel/features/ai_workspace/models/ai_session_state_info.dart';
import 'package:gopanel/features/ai_workspace/models/code_delivery_job.dart';
import 'package:gopanel/features/ai_workspace/models/code_session_recovery.dart';
import 'package:gopanel/features/ai_workspace/models/code_task.dart';
import 'package:gopanel/features/ai_workspace/presentation/controllers/ai_workspace_controller.dart';

class _FakeDeliveryRepository extends AiWorkspaceRepository {
  _FakeDeliveryRepository() : super(ApiClient());

  String deliveryStatus = 'running';
  String sessionStatus = 'delivering';
  int deliveryCalls = 0;
  int stateCalls = 0;

  Map<String, dynamic> get _session => {
    'id': 7,
    'projectId': 1,
    'agentName': 'codex',
    'status': sessionStatus,
  };

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
  Future<AiSessionStateInfo> getSessionState(int sessionId) async {
    stateCalls++;
    return AiSessionStateInfo.fromJson({'session': _session});
  }

  @override
  Future<CodeSessionHistory> getSessionHistory(
    int sessionId, {
    int page = 1,
    int limit = 20,
  }) async => CodeSessionHistory.fromJson({'session': _session});

  @override
  Future<CodeDeliveryJob?> getDelivery(int sessionId) async {
    deliveryCalls++;
    return CodeDeliveryJob.fromJson({
      'id': 3,
      'sessionId': sessionId,
      'status': deliveryStatus,
    });
  }

  @override
  Future<ResponseBody> openConversationStream(
    int sessionId, {
    CancelToken? cancelToken,
  }) => Completer<ResponseBody>().future;
}

void main() {
  testWidgets('交付进行中定时刷新进度，完成后刷新会话并停止轮询', (tester) async {
    final repository = _FakeDeliveryRepository();
    final container = ProviderContainer(
      overrides: [aiWorkspaceRepositoryProvider.overrideWithValue(repository)],
    );
    addTearDown(container.dispose);
    container.read(aiWorkspaceControllerProvider);
    await tester.pump();
    await tester.pump();

    final state = container.read(aiWorkspaceControllerProvider);
    expect(state.currentSession?.id, 7);
    expect(state.delivery?.isActive, isTrue);
    final initialDeliveryCalls = repository.deliveryCalls;

    await tester.pump(const Duration(seconds: 3));
    await tester.pump();
    expect(repository.deliveryCalls, initialDeliveryCalls + 1);

    repository
      ..deliveryStatus = 'completed'
      ..sessionStatus = 'delivered';
    final stateCallsBeforeCompletion = repository.stateCalls;
    await tester.pump(const Duration(seconds: 3));
    await tester.pump();
    await tester.pump();

    final finished = container.read(aiWorkspaceControllerProvider);
    expect(finished.delivery?.isCompleted, isTrue);
    expect(finished.currentSession?.status, 'delivered');
    expect(repository.stateCalls, greaterThan(stateCallsBeforeCompletion));

    final settledCalls = repository.deliveryCalls;
    await tester.pump(const Duration(seconds: 30));
    expect(repository.deliveryCalls, settledCalls);
  });
}
