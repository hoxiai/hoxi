import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:hoxi/core/models/node_skill.dart';
import 'package:hoxi/core/network/api_client.dart';
import 'package:hoxi/features/ai_workspace/data/ai_workspace_repository.dart';
import 'package:hoxi/features/ai_workspace/models/ai_dev_session.dart';
import 'package:hoxi/features/ai_workspace/models/ai_session_state_info.dart';
import 'package:hoxi/features/ai_workspace/models/code_delivery_job.dart';
import 'package:hoxi/features/ai_workspace/models/code_session_recovery.dart';
import 'package:hoxi/features/ai_workspace/models/code_task.dart';

class FakeAiWorkspaceQueueRepository extends AiWorkspaceRepository {
  FakeAiWorkspaceQueueRepository() : super(ApiClient());

  String currentStage = 'idle';
  String sessionStatus = 'active';
  AiInstruction? latestInstruction;
  bool failNextSend = false;
  bool failNextRetry = false;
  bool failNextRefresh = false;
  bool busyGraceActive = false;
  int clearBusyGraceCalls = 0;

  Completer<AiInstructionSendResult>? pendingSendCompleter;
  Completer<AiSessionStateInfo>? pendingStateCompleter;
  final List<String> sentCommands = <String>[];
  StreamController<Uint8List> stream = StreamController<Uint8List>.broadcast();

  Map<String, dynamic> get _sessionJson => <String, dynamic>{
    'id': 7,
    'projectId': 1,
    'title': 'Session 7',
    'agentName': 'codex',
    'status': busyGraceActive ? 'running' : sessionStatus,
    'currentStage': busyGraceActive ? 'running' : currentStage,
  };

  @override
  void clearSessionBusyGrace(int sessionId) {
    clearBusyGraceCalls++;
    busyGraceActive = false;
  }

  @override
  void markSessionStage(int sessionId, String stage) {
    final normalized = stage.trim().toLowerCase();
    if (normalized == 'idle' ||
        normalized == 'completed' ||
        normalized == 'failed' ||
        normalized == 'stopped' ||
        normalized == 'cancelled' ||
        normalized == 'awaiting_approval') {
      busyGraceActive = false;
    }
  }

  @override
  Future<List<CodeProject>> getProjects() async => [
    CodeProject.fromJson(const {'id': 1, 'name': 'shop'}),
  ];

  @override
  Future<List<CodeExecutor>> getExecutors() async => const [];

  @override
  Future<List<AiDevSession>> getSessions({int? projectId}) async => [
    AiDevSession.fromJson(_sessionJson),
  ];

  @override
  Future<List<CodeTask>> getTasks({int? projectId}) async => [
    CodeTask.fromJson({
      'id': 7,
      'sessionId': 7,
      'projectId': 1,
      'title': 'Session 7',
      'agentName': 'codex',
      'status': sessionStatus,
    }),
  ];

  @override
  Future<AiSessionStateInfo> getSessionState(int sessionId) async {
    if (failNextRefresh) {
      failNextRefresh = false;
      throw Exception('temporary refresh network error');
    }
    if (pendingStateCompleter != null) {
      return pendingStateCompleter!.future;
    }
    final effectiveStage = busyGraceActive ? 'running' : currentStage;
    return AiSessionStateInfo(
      session: AiDevSession.fromJson(_sessionJson),
      currentTask: AiTaskSummary(
        id: 7,
        title: 'Session 7',
        status: effectiveStage == 'running' ? 'running' : 'completed',
        workDir: '/work',
        agentName: 'codex',
      ),
      latestInstruction: latestInstruction,
      currentStage: effectiveStage,
      recentOutput: '',
      recentMessages: const [],
      previews: const [],
      timelineEvents: const [],
      errorSummary: '',
      changedFiles: const [],
      pendingApproval: null,
    );
  }

  @override
  Future<CodeSessionHistory> getSessionHistory(
    int sessionId, {
    int page = 1,
    int limit = 20,
  }) async => CodeSessionHistory.fromJson({'session': _sessionJson});

  @override
  Future<CodeDeliveryJob?> getDelivery(int sessionId) async => null;

  @override
  Future<ResponseBody> openConversationStream(
    int sessionId, {
    int? afterSeq,
    CancelToken? cancelToken,
  }) async => ResponseBody(stream.stream, 200);

  @override
  Future<AiInstructionSendResult> sendAiCommand({
    required int sessionId,
    required String command,
    bool autoPreview = true,
    bool requireApproval = false,
    List<Map<String, dynamic>>? attachments,
    NodeSkillSelection? skillSelection,
    String? clientRequestId,
  }) async {
    sentCommands.add(command);
    if (failNextSend) {
      failNextSend = false;
      throw Exception('send failed');
    }
    if (pendingSendCompleter != null) {
      final result = await pendingSendCompleter!.future;
      busyGraceActive = true;
      return result;
    }
    busyGraceActive = true;
    return buildSendResult(command);
  }

  @override
  Future<AiInstruction> retryInstruction(int instructionId) async {
    if (failNextRetry) {
      failNextRetry = false;
      throw Exception('retry failed');
    }
    return AiInstruction(
      id: instructionId,
      sessionId: 7,
      taskId: 7,
      content: 'retry prompt',
      status: 'running',
      allowCode: true,
      autoPreview: false,
      requireApproval: false,
      analysisOnly: false,
      createdAt: DateTime.now(),
    );
  }

  AiInstructionSendResult buildSendResult(String command) {
    final now = DateTime.now();
    return AiInstructionSendResult(
      session: AiDevSession.fromJson({
        'id': 7,
        'projectId': 1,
        'title': 'Session 7',
        'agentName': 'codex',
        'status': 'running',
        'currentStage': 'running',
      }),
      instruction: AiInstruction(
        id: 101 + sentCommands.length,
        sessionId: 7,
        taskId: 7,
        content: command,
        status: 'running',
        allowCode: true,
        autoPreview: false,
        requireApproval: false,
        analysisOnly: false,
        createdAt: now,
      ),
      task: const AiTaskSummary(
        id: 7,
        title: 'Session 7',
        status: 'running',
        workDir: '/work',
        agentName: 'codex',
      ),
      approval: null,
    );
  }

  void emitSse(String frame) {
    stream.add(Uint8List.fromList(utf8.encode(frame)));
  }

  Future<void> resetStream() async {
    final old = stream;
    stream = StreamController<Uint8List>.broadcast();
    await old.close();
  }
}
