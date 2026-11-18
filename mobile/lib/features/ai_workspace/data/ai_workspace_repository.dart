import 'dart:async';
import 'dart:convert';

import 'package:dio/dio.dart';

import '../../../core/network/api_client.dart';
import '../../../core/storage/storage_service.dart';
import '../models/ai_dev_session.dart';
import '../models/ai_session_state_info.dart';
import '../models/code_convoy_plan.dart';
import '../models/code_model_catalog.dart';
import '../models/code_task.dart';
import '../models/code_workspace_file.dart';
import '../models/code_delivery_job.dart';
import '../models/code_execution_run_detail.dart';
import '../models/code_git_review.dart';
import '../models/code_session_flow.dart';
import '../models/code_project_terminal_session.dart';
import '../models/code_session_recovery.dart';
import '../models/code_quality_check.dart';
import '../models/code_memory_entry.dart';
import '../../../core/models/node_skill.dart';
import 'task_ai_utils.dart';
import 'hoxi_workspace_adapter.dart';

/// 与服务端 codeQualityTimeout（10 分钟）对齐，多留 30 秒给结果返回，和桌面端一致
const _qualityCheckReceiveTimeout = Duration(seconds: 630);

/// AI 工作区仓库
/// 负责读取服务器目录、执行远程 AI 任务等 API 交互
/// 只有隔离 Worktree 会话才能交付；创建入口已按隔离能力把关，默认与桌面端一致使用隔离。
Map<String, dynamic> codeSessionCreateBody({
  required int projectId,
  required String executorId,
  required String approvalPolicy,
  String title = '',
  bool isolated = true,
  String model = '',
}) {
  return {
    'title': title,
    'workDir': '',
    'projectId': projectId,
    'executorId': executorId,
    'approvalPolicy': approvalPolicy,
    'isolated': isolated,
    if (model.trim().isNotEmpty) 'model': model.trim(),
  };
}

/// 服务端只接受与评审一致的交付，reviewRevision 来自交付前的结果评审。
Map<String, dynamic> codeDeliveryMergeBody(String reviewRevision) {
  return {'confirm': true, 'reviewRevision': reviewRevision};
}

class AiWorkspaceRepository {
  final ApiClient _apiClient;
  late final HoxiWorkspaceAdapter _hoxiAdapter;

  AiWorkspaceRepository(this._apiClient, {HoxiWorkspaceAdapter? hoxiAdapter}) {
    _hoxiAdapter = hoxiAdapter ?? HoxiWorkspaceAdapter(_apiClient);
  }

  bool get _isHoxi => StorageService.isActiveHoxi;
  bool get _isBusinessService => StorageService.isActiveBusinessService;

  Future<List<CodeProject>> getProjects() async {
    if (_isBusinessService) return const [];
    if (_isHoxi) return _hoxiAdapter.getProjects();
    const limit = 100;
    final projects = <CodeProject>[];
    for (var page = 1; ; page++) {
      final response = await _apiClient.get<Map<String, dynamic>>(
        '/api/code/projects',
        queryParameters: {'page': page, 'limit': limit},
      );
      final items = _items(response.data, CodeProject.fromJson);
      projects.addAll(items);
      final total = (response.data?['total'] as num?)?.toInt() ?? 0;
      if (items.isEmpty || projects.length >= total) return projects;
    }
  }

  Future<List<CodeExecutor>> getExecutors() async {
    if (_isBusinessService) return const [];
    if (_isHoxi) return _hoxiAdapter.getExecutors();
    final response = await _apiClient.get<List<dynamic>>('/api/code/executors');
    return (response.data ?? const [])
        .whereType<Map>()
        .map((item) => CodeExecutor.fromJson(item.cast<String, dynamic>()))
        .toList();
  }

  Future<CodeWorktreeCapability> getWorktreeCapability(int projectId) async {
    if (_isHoxi) return _hoxiAdapter.getWorktreeCapability(projectId);
    final response = await _apiClient.get<Map<String, dynamic>>(
      '/api/code/projects/$projectId/worktree-capability',
    );
    return CodeWorktreeCapability.fromJson(
      response.data ?? const <String, dynamic>{},
    );
  }

  Future<CodeProjectTerminalSession> openProjectTerminal(int projectId) async {
    if (_isHoxi) return _hoxiAdapter.openProjectTerminal(projectId);
    final response = await _apiClient.post<Map<String, dynamic>>(
      '/api/code/projects/$projectId/terminal',
      data: const <String, dynamic>{},
    );
    return CodeProjectTerminalSession.fromJson(
      response.data ?? const <String, dynamic>{},
    );
  }

  Future<List<AiDevSession>> getSessions({int? projectId}) async {
    if (_isBusinessService) return const [];
    if (_isHoxi) return _hoxiAdapter.getSessions(projectId: projectId);
    return _getAllPagedItems(
      path: '/api/code/sessions',
      queryParameters: {if (projectId != null && projectId > 0) 'projectId': projectId},
      fromJson: AiDevSession.fromJson,
    );
  }

  Future<List<AiDevSession>> getRecentActiveSessions({
    int? projectId,
    Duration window = defaultRecentTaskWindow,
  }) async {
    if (runtimeType != AiWorkspaceRepository) {
      return getSessions(projectId: projectId);
    }
    if (_isBusinessService) return const [];
    if (_isHoxi) return _hoxiAdapter.getRecentActiveSessions(projectId: projectId, window: window);
    final res = await _apiClient.get<Map<String, dynamic>>(
      '/api/code/sessions',
      queryParameters: {
        'page': 1, 'limit': 20,
        if (projectId != null && projectId > 0) 'projectId': projectId,
      },
    );
    final now = DateTime.now();
    return _items(res.data, AiDevSession.fromJson)
        .where((s) => isHotAiDevSession(s, now: now, window: window))
        .toList();
  }

  Future<List<CodeTask>> getTasks({int? projectId}) async {
    if (_isBusinessService) return const [];
    if (_isHoxi) return _hoxiAdapter.getTasks(projectId: projectId);
    return _getAllPagedItems(
      path: '/api/code/tasks',
      queryParameters: {
        'includeGit': false,
        if (projectId != null && projectId > 0) 'projectId': projectId,
      },
      fromJson: CodeTask.fromJson,
    );
  }

  Future<List<CodeTask>> getRecentActiveTasks({
    int? projectId,
    Duration window = defaultRecentTaskWindow,
  }) async {
    if (runtimeType != AiWorkspaceRepository) {
      return getTasks(projectId: projectId);
    }
    if (_isBusinessService) return const [];
    if (_isHoxi) return _hoxiAdapter.getRecentActiveTasks(projectId: projectId, window: window);
    final res = await _apiClient.get<Map<String, dynamic>>(
      '/api/code/tasks',
      queryParameters: {
        'page': 1, 'limit': 20, 'includeGit': false,
        if (projectId != null && projectId > 0) 'projectId': projectId,
      },
    );
    final now = DateTime.now();
    return _items(res.data, CodeTask.fromJson)
        .where((t) => isHotCodeTask(t, now: now, window: window))
        .toList();
  }

  Future<AiDevSession> createSession({
    required int projectId,
    required String executorId,
    required String approvalPolicy,
    String title = '',
    bool isolated = true,
    String model = '',
    String? clientRequestId,
  }) async {
    if (_isHoxi) {
      return _hoxiAdapter.createSession(
        projectId: projectId, executorId: executorId,
        approvalPolicy: approvalPolicy, title: title,
        isolated: isolated, model: model, clientRequestId: clientRequestId,
      );
    }
    final reqId = clientRequestId?.trim() ?? '';
    final trimmedModel = model.trim();
    if (trimmedModel.isNotEmpty) {
      unawaited(
        CodeModelCatalog.saveLastSelection(
          model: trimmedModel,
          executorId: executorId,
        ),
      );
    }
    final response = await _apiClient.post<Map<String, dynamic>>(
      '/api/code/sessions',
      data: codeSessionCreateBody(
        projectId: projectId, executorId: executorId,
        approvalPolicy: approvalPolicy, title: title,
        isolated: isolated, model: trimmedModel,
      ),
      options: reqId.isEmpty ? null : Options(headers: {'X-Idempotency-Key': reqId}),
    );

    final created = AiDevSession.fromJson(response.data ?? const <String, dynamic>{});
    if (trimmedModel.isNotEmpty &&
        created.id > 0 &&
        created.providerModel.trim() != trimmedModel) {
      try {
        return await updateSessionModel(created.id, model: trimmedModel);
      } catch (_) {
        return created.copyWith(providerModel: trimmedModel);
      }
    }
    return created;
  }

  Future<AiSessionStateInfo> getSessionState(int sessionId) async {
    if (_isHoxi) return _hoxiAdapter.getSessionState(sessionId);
    final response = await _apiClient.get<Map<String, dynamic>>('/api/code/sessions/$sessionId/state');
    return AiSessionStateInfo.fromJson(response.data ?? const <String, dynamic>{});
  }

  void markSessionStage(int sessionId, String stage) {
    if (_isHoxi) _hoxiAdapter.markSessionStage(sessionId, stage);
  }

  Future<ResponseBody> openConversationStream(
    int sessionId, {
    int? afterSeq,
    CancelToken? cancelToken,
  }) {
    if (_isHoxi) {
      return _hoxiAdapter.openConversationStream(
        sessionId,
        afterSeq: afterSeq,
        cancelToken: cancelToken,
      );
    }
    return _apiClient.openSseStream(
      '/api/code/sessions/$sessionId/conversation/stream',
      cancelToken: cancelToken,
    );
  }

  Future<AiDevSession> updateSessionTitle(int sessionId, String title) async {
    if (_isHoxi) return _hoxiAdapter.updateSessionTitle(sessionId, title);
    final trimmed = title.trim();
    final response = await _apiClient.put<Map<String, dynamic>>(
      '/api/code/sessions/$sessionId/title', data: {'title': trimmed},
    );
    final parsed = AiDevSession.fromJson(response.data ?? const {});
    return parsed.copyWith(
      id: parsed.id > 0 ? parsed.id : sessionId,
      title: parsed.title.trim().isNotEmpty ? parsed.title.trim() : trimmed,
      currentTaskTitle: trimmed,
    );
  }

  Future<AiDevSession> updateApprovalPolicy(int sessionId, String approvalPolicy) async {
    if (_isHoxi) return _hoxiAdapter.updateApprovalPolicy(sessionId, approvalPolicy);
    final response = await _apiClient.put<Map<String, dynamic>>(
      '/api/code/sessions/$sessionId/approval-policy',
      data: {'approvalPolicy': approvalPolicy},
    );
    return AiDevSession.fromJson(response.data ?? const {});
  }

  Future<AiDevSession> updateSessionModel(
    int sessionId, {String? model, String? executorId, String? reasoningEffort}
  ) async {
    if (model != null && model.trim().isNotEmpty) {
      unawaited(
        CodeModelCatalog.saveLastSelection(
          model: model.trim(),
          executorId: executorId,
        ),
      );
    }
    if (_isHoxi) {
      return _hoxiAdapter.updateSessionModel(
        sessionId, model: model, executorId: executorId, reasoningEffort: reasoningEffort,
      );
    }
    final response = await _apiClient.put<Map<String, dynamic>>(
      '/api/code/sessions/$sessionId/model',
      data: {
        if (model != null) 'model': model.trim(),
        if (reasoningEffort != null) 'reasoningEffort': reasoningEffort.trim(),
      },
    );
    return AiDevSession.fromJson(response.data ?? const {});
  }

  Future<CodeSessionInitialization> abortSessionInitialization(int sessionId) async {
    if (_isHoxi) return _hoxiAdapter.getSessionInitialization(sessionId);
    final response = await _apiClient.post<Map<String, dynamic>>(
      '/api/code/sessions/$sessionId/initialization/abort', data: const {},
    );
    return CodeSessionInitialization.fromJson(response.data ?? const <String, dynamic>{});
  }

  Future<CodeSessionHistory> getSessionHistory(int sessionId, {int page = 1, int limit = 20}) async {
    if (_isHoxi) return _hoxiAdapter.getSessionHistory(sessionId, page: page, limit: limit);
    final response = await _apiClient.get<Map<String, dynamic>>(
      '/api/code/sessions/$sessionId/history', queryParameters: {'page': page, 'limit': limit},
    );
    return CodeSessionHistory.fromJson(response.data ?? const <String, dynamic>{});
  }

  Future<AiInstruction> retryInstruction(int instructionId) async {
    if (_isHoxi) return _hoxiAdapter.retryInstruction(instructionId);
    final response = await _apiClient.post<Map<String, dynamic>>(
      '/api/code/instructions/$instructionId/retry', data: const <String, dynamic>{},
    );
    return AiInstruction.fromJson(response.data ?? const <String, dynamic>{});
  }

  Future<CodeConvoyPlan> startConvoyPlan(int sessionId, CodeConvoyPlan draft) async {
    if (_isHoxi) return _hoxiAdapter.startConvoyPlan(sessionId, draft);
    throw UnsupportedError('当前节点类型暂不支持多 Agent 编队接口');
  }

  Future<CodeConvoyPlan> cancelConvoyPlan(int sessionId, String planId) async {
    if (_isHoxi) return _hoxiAdapter.cancelConvoyPlan(sessionId, planId);
    throw UnsupportedError('当前节点类型暂不支持取消编队');
  }

  Future<CodeConvoyPlan> retryConvoyPlan(int sessionId, String planId) async {
    if (_isHoxi) return _hoxiAdapter.retryConvoyPlan(sessionId, planId);
    throw UnsupportedError('当前节点类型暂不支持重试编队');
  }

  Future<CodeConvoyPlan> mergeConvoyPlan(int sessionId, String planId) async {
    if (_isHoxi) return _hoxiAdapter.mergeConvoyPlan(sessionId, planId);
    throw UnsupportedError('当前节点类型暂不支持合并编队分支');
  }

  Future<CodeExecutionRunDetail> getExecutionRun(int runId) async {
    if (_isHoxi) return _hoxiAdapter.getExecutionRun(runId);
    final response = await _apiClient.get<Map<String, dynamic>>('/api/code/runs/$runId');
    return CodeExecutionRunDetail.fromJson(response.data ?? const <String, dynamic>{});
  }

  Future<CodeGitStatus> getGitStatus(int sessionId) async {
    if (_isHoxi) return _hoxiAdapter.getGitStatus(sessionId);
    final response = await _apiClient.get<Map<String, dynamic>>('/api/code/sessions/$sessionId/git/status');
    return CodeGitStatus.fromJson(response.data ?? const <String, dynamic>{});
  }

  Future<CodeGitDiff> getGitDiff({
    required int sessionId, required String repositoryId, required String path, required String kind,
  }) async {
    if (_isHoxi) return _hoxiAdapter.getGitDiff(sessionId: sessionId, path: path);
    final response = await _apiClient.get<Map<String, dynamic>>(
      '/api/code/sessions/$sessionId/git/diff',
      queryParameters: {'repositoryId': repositoryId, 'path': path, 'kind': kind},
    );
    return CodeGitDiff.fromJson(response.data ?? const <String, dynamic>{});
  }

  Future<CodeGitSaveResult> saveGitChanges(int sessionId, String message, {String? clientRequestId}) async {
    if (_isHoxi) return _hoxiAdapter.saveGitChanges(sessionId, message, clientRequestId: clientRequestId);
    final reqId = clientRequestId?.trim() ?? '';
    final response = await _apiClient.post<Map<String, dynamic>>(
      '/api/code/sessions/$sessionId/git/save',
      data: {'message': message.trim()},
      options: reqId.isEmpty ? null : Options(headers: {'X-Idempotency-Key': reqId}),
    );
    return CodeGitSaveResult.fromJson(response.data ?? const <String, dynamic>{});
  }

  Future<CodeSessionInitialization> getSessionInitialization(int sessionId) async {
    if (_isHoxi) return _hoxiAdapter.getSessionInitialization(sessionId);
    final response = await _apiClient.get<Map<String, dynamic>>('/api/code/sessions/$sessionId/initialization');
    return CodeSessionInitialization.fromJson(response.data ?? const <String, dynamic>{});
  }

  Future<CodeSessionInitialization> retrySessionInitialization(int sessionId) async {
    if (_isHoxi) return _hoxiAdapter.getSessionInitialization(sessionId);
    final response = await _apiClient.post<Map<String, dynamic>>(
      '/api/code/sessions/$sessionId/initialization/retry', data: const <String, dynamic>{},
    );
    return CodeSessionInitialization.fromJson(response.data ?? const <String, dynamic>{});
  }

  Future<CodeStructureResult> getSessionStructure(int sessionId, {String path = ''}) async {
    if (_isHoxi) return _hoxiAdapter.getSessionStructure(sessionId, path: path);
    final response = await _apiClient.get<Map<String, dynamic>>(
      '/api/code/sessions/$sessionId/structure', queryParameters: {'path': path},
    );
    return CodeStructureResult.fromJson(response.data ?? const <String, dynamic>{});
  }

  Future<CodeSessionFile> getSessionFile(int sessionId, String path) async {
    if (_isHoxi) return _hoxiAdapter.getSessionFile(sessionId, path);
    final response = await _apiClient.get<Map<String, dynamic>>(
      '/api/code/sessions/$sessionId/file', queryParameters: {'path': path},
    );
    return CodeSessionFile.fromJson(response.data ?? const <String, dynamic>{});
  }

  Future<CodeSessionFile> saveSessionFile({
    required int sessionId, required CodeSessionFile file, required String content,
  }) async {
    if (_isHoxi) return _hoxiAdapter.saveSessionFile(sessionId: sessionId, file: file, content: content);
    final response = await _apiClient.put<Map<String, dynamic>>(
      '/api/code/sessions/$sessionId/file',
      data: {'path': file.path, 'content': content, 'baseVersion': file.version},
    );
    final data = response.data ?? const <String, dynamic>{};
    return CodeSessionFile(
      path: (data['path'] ?? file.path).toString(),
      content: content, extension: file.extension,
      size: (data['size'] as num?)?.toInt() ?? utf8.encode(content).length,
      version: (data['version'] ?? '').toString(),
    );
  }

  Future<List<AiPreview>> getSessionPreviews(int sessionId) async {
    if (_isHoxi) return const [];
    final response = await _apiClient.get<Map<String, dynamic>>('/api/code/sessions/$sessionId/previews');
    final data = response.data;
    if (data != null && data['items'] is List) {
      return (data['items'] as List)
          .whereType<Map>()
          .map((item) => AiPreview.fromJson(item.cast<String, dynamic>()))
          .toList();
    }
    return [];
  }

  Future<List<AiApproval>> getApprovals({String status = 'pending', int limit = 50}) async {
    if (_isHoxi) return _hoxiAdapter.getApprovals();
    final response = await _apiClient.get<Map<String, dynamic>>(
      '/api/code/approvals', queryParameters: {'status': status, 'limit': limit},
    );
    final data = response.data;
    if (data != null && data['items'] is List) {
      return (data['items'] as List)
          .whereType<Map>()
          .map((item) => AiApproval.fromJson(item.cast<String, dynamic>()))
          .toList();
    }
    return [];
  }

  Future<AiApproval> approveApproval(int approvalId, {String? reason, String? clientRequestId}) async {
    if (_isHoxi) {
      return _hoxiAdapter.replyApproval(approvalId, approve: true, reason: reason, clientRequestId: clientRequestId);
    }
    final reqId = clientRequestId?.trim() ?? '';
    final response = await _apiClient.post<Map<String, dynamic>>(
      '/api/code/approvals/$approvalId/approve',
      data: {'reason': reason ?? ''},
      options: reqId.isEmpty ? null : Options(headers: {'X-Idempotency-Key': reqId}),
    );
    final data = response.data ?? const <String, dynamic>{};
    return AiApproval.fromJson((data['approval'] as Map? ?? const {}).cast<String, dynamic>());
  }

  Future<AiApproval> rejectApproval(int approvalId, {String? reason, String? clientRequestId}) async {
    if (_isHoxi) {
      return _hoxiAdapter.replyApproval(approvalId, approve: false, reason: reason, clientRequestId: clientRequestId);
    }
    final reqId = clientRequestId?.trim() ?? '';
    final response = await _apiClient.post<Map<String, dynamic>>(
      '/api/code/approvals/$approvalId/reject',
      data: {'reason': reason ?? ''},
      options: reqId.isEmpty ? null : Options(headers: {'X-Idempotency-Key': reqId}),
    );
    final data = response.data ?? const <String, dynamic>{};
    return AiApproval.fromJson((data['approval'] as Map? ?? const {}).cast<String, dynamic>());
  }

  Future<Map<String, dynamic>> uploadAttachment({
    required int sessionId, required String filename, required String mime, required String dataUrl,
  }) async {
    if (_isHoxi) {
      return _hoxiAdapter.uploadAttachment(
        sessionId: sessionId, filename: filename, mime: mime, dataUrl: dataUrl,
      );
    }
    return {'filename': filename, 'mime': mime, 'url': dataUrl};
  }

  Future<AiInstructionSendResult> sendAiCommand({
    required int sessionId, required String command,
    bool autoPreview = true, bool requireApproval = false,
    List<Map<String, dynamic>>? attachments,
    NodeSkillSelection? skillSelection, String? clientRequestId,
  }) async {
    if (_isHoxi) {
      return _hoxiAdapter.sendAiCommand(
        sessionId: sessionId, command: command, attachments: attachments,
        skillSelection: skillSelection, clientRequestId: clientRequestId,
      );
    }
    final reqId = clientRequestId?.trim() ?? '';
    final response = await _apiClient.post<Map<String, dynamic>>(
      '/api/code/sessions/$sessionId/instructions',
      data: buildCodeInstructionRequest(
        command: command, autoPreview: autoPreview,
        requireApproval: requireApproval, skillSelection: skillSelection,
        clientRequestId: reqId,
      ),
      options: reqId.isEmpty ? null : Options(headers: {'X-Idempotency-Key': reqId}),
    );
    return AiInstructionSendResult.fromJson(response.data ?? const <String, dynamic>{});
  }

  Future<void> stopSession(int sessionId, {String? clientRequestId}) async {
    if (_isHoxi) return _hoxiAdapter.stopSession(sessionId, clientRequestId: clientRequestId);
    final reqId = clientRequestId?.trim() ?? '';
    await _apiClient.post<Map<String, dynamic>>(
      '/api/code/sessions/$sessionId/stop',
      data: const <String, dynamic>{},
      options: reqId.isEmpty ? null : Options(headers: {'X-Idempotency-Key': reqId}),
    );
  }

  Future<CodeDeliveryJob?> getDelivery(int sessionId) async {
    if (_isHoxi) return _hoxiAdapter.getDelivery(sessionId);
    final response = await _apiClient.get<Map<String, dynamic>>('/api/code/sessions/$sessionId/delivery');
    final data = response.data;
    return data == null ? null : CodeDeliveryJob.fromJson(data);
  }

  Future<CodeDeliveryReview> getDeliveryReview(int sessionId) async {
    if (_isHoxi) return _hoxiAdapter.getDeliveryReview(sessionId);
    final response = await _apiClient.get<Map<String, dynamic>>(
      '/api/code/sessions/$sessionId/git/status', queryParameters: const {'scope': 'result'},
    );
    return CodeDeliveryReview.fromJson(response.data ?? const <String, dynamic>{});
  }

  Future<CodeDeliveryJob> startDelivery(int sessionId, {required String reviewRevision}) async {
    if (_isHoxi) return _hoxiAdapter.startDelivery(sessionId);
    final response = await _apiClient.post<Map<String, dynamic>>(
      '/api/code/sessions/$sessionId/worktree/merge', data: codeDeliveryMergeBody(reviewRevision),
    );
    return CodeDeliveryJob.fromJson(response.data ?? const <String, dynamic>{});
  }

  Future<CodeSessionFlow> getSessionFlow(int sessionId) async {
    if (_isHoxi) return _hoxiAdapter.getSessionFlow(sessionId);
    final response = await _apiClient.get<Map<String, dynamic>>('/api/code/sessions/$sessionId/flow');
    return CodeSessionFlow.fromJson(response.data ?? const <String, dynamic>{});
  }

  Future<void> startSessionFlowPreview(int sessionId) async {
    if (_isHoxi) return;
    await _apiClient.post<Map<String, dynamic>>('/api/code/sessions/$sessionId/flow/preview');
  }

  Future<void> deployFlowRun(int runId, int environmentId) async {
    if (_isHoxi) return;
    await _apiClient.post<Map<String, dynamic>>(
      '/api/flow/runs/$runId/deploy', data: {'environmentId': environmentId},
    );
  }

  Future<List<CodeQualityCheck>> getQualityChecks(int sessionId) async {
    if (_isHoxi) return _hoxiAdapter.getQualityChecks(sessionId);
    final response = await _apiClient.get<Map<String, dynamic>>('/api/code/sessions/$sessionId/quality-checks');
    return _items(response.data, CodeQualityCheck.fromJson);
  }

  Future<CodeQualityCheckResult> runQualityCheck(int sessionId, String checkId) async {
    if (_isHoxi) {
      return _hoxiAdapter.runQualityCheck(sessionId, checkId, receiveTimeout: _qualityCheckReceiveTimeout);
    }
    final response = await _apiClient.post<Map<String, dynamic>>(
      '/api/code/sessions/$sessionId/quality-checks/run',
      data: {'checkId': checkId},
      options: Options(receiveTimeout: _qualityCheckReceiveTimeout),
    );
    return CodeQualityCheckResult.fromJson(response.data ?? const <String, dynamic>{});
  }

  Future<CodeMemoryExtractionStatus> getSessionMemoryStatus(int sessionId) async {
    if (_isHoxi) return _hoxiAdapter.getSessionMemoryStatus(sessionId);
    final response = await _apiClient.get<Map<String, dynamic>>('/api/code/sessions/$sessionId/memory/status');
    return CodeMemoryExtractionStatus.fromJson(response.data ?? const <String, dynamic>{});
  }

  Future<CodeMemoryExtractionStatus> extractSessionMemory(int sessionId) async {
    if (_isHoxi) return _hoxiAdapter.extractSessionMemory(sessionId);
    final response = await _apiClient.post<Map<String, dynamic>>('/api/code/sessions/$sessionId/memory/extract');
    final statusData = response.data?['status'] is Map<String, dynamic>
        ? response.data!['status'] as Map<String, dynamic>
        : response.data ?? const <String, dynamic>{};
    return CodeMemoryExtractionStatus.fromJson(statusData);
  }

  Future<List<CodeMemoryEntry>> getMemories({int? projectId}) async {
    if (_isHoxi) return _hoxiAdapter.getMemories(projectId: projectId);
    final response = await _apiClient.get<Map<String, dynamic>>(
      '/api/code/memories',
      queryParameters: {if (projectId != null && projectId > 0) 'projectId': projectId},
    );
    return _items(response.data, CodeMemoryEntry.fromJson);
  }

  List<T> _items<T>(Map<String, dynamic>? data, T Function(Map<String, dynamic>) fromJson) =>
      (data?['items'] as List<dynamic>? ?? const [])
          .whereType<Map>()
          .map((item) => fromJson(item.cast<String, dynamic>()))
          .toList();

  Future<List<T>> _getAllPagedItems<T>({
    required String path,
    required Map<String, dynamic> queryParameters,
    required T Function(Map<String, dynamic>) fromJson,
  }) async {
    const pageSize = 100;
    final items = <T>[];
    for (var page = 1; ; page++) {
      final response = await _apiClient.get<Map<String, dynamic>>(
        path, queryParameters: {...queryParameters, 'page': page, 'limit': pageSize},
      );
      final pageItems = _items(response.data, fromJson);
      items.addAll(pageItems);
      final total = (response.data?['total'] as num?)?.toInt() ?? items.length;
      if (pageItems.length < pageSize || items.length >= total) break;
    }
    return items;
  }
}

Map<String, dynamic> buildCodeInstructionRequest({
  required String command,
  required bool autoPreview,
  required bool requireApproval,
  NodeSkillSelection? skillSelection,
  String? clientRequestId,
}) {
  final reqId = clientRequestId?.trim() ?? '';
  return {
    'content': command, 'allowCode': true,
    'autoPreview': autoPreview, 'requireApproval': requireApproval,
    'analysisOnly': false,
    if (skillSelection != null) 'skillSelection': skillSelection.toJson(),
    if (reqId.isNotEmpty) 'clientRequestId': reqId,
  };
}
